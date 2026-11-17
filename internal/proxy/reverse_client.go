package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/apernet/hysteria/core/v2/extension"
	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/quicvarint"
	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
)

type reverseClient struct {
	ctx      context.Context
	tcp, udp []config.Forward
	mu       sync.Mutex
	sessions map[reverseSessionKey]*reverseUDPSession
	closed   bool
}
type reverseSessionKey struct {
	conn  *quic.Conn
	id    uint32
	index uint16
}
type reverseUDPSession struct {
	owner    *reverseClient
	key      reverseSessionKey
	incoming chan *extension.UDPMessage
	ctx      context.Context
	cancel   context.CancelFunc
}

func newReverseClient(ctx context.Context, tcp, udp []config.Forward) *reverseClient {
	return &reverseClient{ctx: ctx, tcp: tcp, udp: udp, sessions: make(map[reverseSessionKey]*reverseUDPSession)}
}
func (r *reverseClient) Register(conn *quic.Conn) (registrationReply, error) {
	ctx, cancel := context.WithTimeout(conn.Context(), 10*time.Second)
	defer cancel()
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return registrationReply{}, err
	}
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	request := registration{TCP: make([]config.Forward, len(r.tcp)), UDP: make([]config.Forward, len(r.udp))}
	for i, f := range r.tcp {
		request.TCP[i].Listen = f.Listen
	}
	for i, f := range r.udp {
		request.UDP[i].Listen = f.Listen
		request.UDP[i].Timeout = f.Timeout
	}
	fail := func(err error) (registrationReply, error) {
		stream.CancelRead(0)
		_ = stream.Close()
		return registrationReply{}, err
	}
	if err := writeFrameType(stream, reverseControlType); err != nil {
		return fail(err)
	}
	if err := writeMessage(stream, request); err != nil {
		return fail(err)
	}
	var reply registrationReply
	if err := readMessage(stream, &reply); err != nil {
		return fail(err)
	}
	if !reply.OK {
		return fail(errors.New(reply.Error))
	}
	_ = stream.SetDeadline(time.Time{})
	go func() {
		_, _ = io.Copy(io.Discard, stream)
		stream.CancelRead(0)
		_ = stream.Close()
		// Losing registration while the transport is alive must restore mappings.
		_ = conn.CloseWithError(0x100, "reverse registration closed")
	}()
	return reply, nil
}
func (r *reverseClient) AcceptStreams(conn *quic.Conn) {
	for {
		stream, err := conn.AcceptStream(conn.Context())
		if err != nil {
			return
		}
		go r.handleTCP(conn, stream)
	}
}
func (r *reverseClient) handleTCP(conn *quic.Conn, stream *quic.Stream) {
	tunnel := &streamConn{stream: stream, conn: conn}
	defer tunnel.Close()
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	kind, err := readFrameType(stream)
	if err != nil || kind != reverseTCPType {
		return
	}
	index, err := quicvarint.Read(quicvarint.NewReader(stream))
	if err != nil || index >= uint64(len(r.tcp)) {
		_ = extension.WriteTCPResponse(stream, false, "unknown reverse mapping")
		return
	}
	ctx, cancel := context.WithTimeout(conn.Context(), 10*time.Second)
	defer cancel()
	remote, err := (&net.Dialer{}).DialContext(ctx, "tcp", r.tcp[index].Remote)
	if err != nil {
		_ = extension.WriteTCPResponse(stream, false, err.Error())
		return
	}
	defer remote.Close()
	if err := extension.WriteTCPResponse(stream, true, "Connected"); err != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	relay(tunnel, remote)
}
func (r *reverseClient) HandleDatagram(conn *quic.Conn, b []byte) bool {
	index, message, handled := parseReverseDatagram(b)
	if !handled || message == nil {
		return handled
	}
	if int(index) >= len(r.udp) {
		return true
	}
	key := reverseSessionKey{conn: conn, id: message.SessionID, index: index}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return true
	}
	session := r.sessions[key]
	if session == nil {
		ctx, cancel := context.WithCancel(conn.Context())
		session = &reverseUDPSession{owner: r, key: key, incoming: make(chan *extension.UDPMessage, 1024), ctx: ctx, cancel: cancel}
		r.sessions[key] = session
		go session.run()
	}
	select {
	case session.incoming <- message:
	default:
	}
	r.mu.Unlock()
	return true
}
func (r *reverseClient) Close() {
	r.mu.Lock()
	r.closed = true
	for _, s := range r.sessions {
		s.cancel()
	}
	r.mu.Unlock()
}
func (s *reverseUDPSession) run() {
	defer s.cancel()
	defer func() {
		s.owner.mu.Lock()
		if s.owner.sessions[s.key] == s {
			delete(s.owner.sessions, s.key)
		}
		s.owner.mu.Unlock()
	}()
	cfg := s.owner.udp[s.key.index]
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", cfg.Remote)
	cancel()
	if err != nil {
		return
	}
	udp := conn.(*net.UDPConn)
	defer udp.Close()
	done := make(chan struct{})
	activity := make(chan struct{}, 1)
	go func() {
		defer close(done)
		buf := make([]byte, extension.MaxUDPSize)
		for {
			n, err := udp.Read(buf)
			if err != nil {
				return
			}
			message := &extension.UDPMessage{SessionID: s.key.id, FragCount: 1, Addr: udp.RemoteAddr().String(), Data: buf[:n]}
			_ = extension.SendUDP(s.key.conn, reversePrefix(s.key.index), message)
			select {
			case activity <- struct{}{}:
			default:
			}
		}
	}()
	defer func() { udp.Close(); <-done }()
	idle := udpTimeout(cfg.Timeout)
	timer := time.NewTimer(idle)
	defer timer.Stop()
	reset := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(idle)
	}
	var defrag extension.Defragger
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.owner.ctx.Done():
			return
		case <-done:
			return
		case <-timer.C:
			return
		case <-activity:
			reset()
		case message := <-s.incoming:
			reset()
			packet := defrag.Feed(message)
			if packet == nil {
				continue
			}
			if _, err := udp.Write(packet.Data); err != nil {
				return
			}
		}
	}
}
