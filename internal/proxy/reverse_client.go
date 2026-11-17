package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apernet/hysteria/core/v2/extension"
	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/quicvarint"
	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
)

type reverseClient struct {
	ctx      context.Context
	cancel   context.CancelFunc
	tcp, udp []config.Forward
	shards   [32]reverseClientShard
	budget   udpBudget
	expiry   *udpExpiry
	closed   atomic.Bool
}
type reverseClientShard struct {
	mu       sync.Mutex
	sessions map[reverseSessionKey]*reverseUDPSession
}
type reverseSessionKey struct {
	conn  *quic.Conn
	id    uint32
	index uint16
}
type reverseUDPSession struct {
	owner  *reverseClient
	key    reverseSessionKey
	queue  *udpQueue
	ctx    context.Context
	cancel context.CancelFunc
	last   atomic.Int64
	expiry *udpExpiryEntry
}

func newReverseClient(parent context.Context, tcp, udp []config.Forward, options config.ReverseUDPOptions) *reverseClient {
	ctx, cancel := context.WithCancel(parent)
	r := &reverseClient{ctx: ctx, cancel: cancel, tcp: tcp, udp: udp, budget: udpBudget{options: options}}
	for i := range r.shards {
		r.shards[i].sessions = make(map[reverseSessionKey]*reverseUDPSession)
	}
	r.expiry = newUDPExpiry(ctx)
	return r
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
func (r *reverseClient) shard(key reverseSessionKey) *reverseClientShard {
	return &r.shards[(key.id^uint32(key.index))%uint32(len(r.shards))]
}
func (r *reverseClient) HandleDatagram(conn *quic.Conn, data []byte) bool {
	index, message, handled := parseReverseDatagram(data)
	if !handled || message == nil {
		return handled
	}
	if int(index) >= len(r.udp) {
		return true
	}
	key := reverseSessionKey{conn: conn, id: message.SessionID, index: index}
	shard := r.shard(key)
	shard.mu.Lock()
	if r.closed.Load() || r.ctx.Err() != nil || conn.Context().Err() != nil {
		shard.mu.Unlock()
		return true
	}
	session := shard.sessions[key]
	if session == nil {
		if !r.budget.newSession() {
			shard.mu.Unlock()
			return true
		}
		ctx, cancel := context.WithCancel(conn.Context())
		session = &reverseUDPSession{owner: r, key: key, queue: newUDPQueue(&r.budget), ctx: ctx, cancel: cancel}
		session.last.Store(udpNow())
		shard.sessions[key] = session
		session.expiry = r.expiry.add(session.last.Load()+int64(udpTimeout(r.udp[index].Timeout)), session.expire)
		go session.run()
	}
	if session.ctx.Err() != nil {
		shard.mu.Unlock()
		return true
	}
	session.last.Store(udpNow())
	session.queue.push(udpQueuedPacket{message: message})
	shard.mu.Unlock()
	return true
}
func (r *reverseClient) Close() {
	if r.closed.Swap(true) {
		return
	}
	r.cancel()
	for i := range r.shards {
		shard := &r.shards[i]
		shard.mu.Lock()
		for key, s := range shard.sessions {
			delete(shard.sessions, key)
			r.budget.sessions.Add(-1)
			s.cancel()
			s.queue.close()
			r.expiry.remove(s.expiry)
		}
		shard.mu.Unlock()
	}
}
func (s *reverseUDPSession) expire(now int64) int64 {
	shard := s.owner.shard(s.key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if shard.sessions[s.key] != s {
		return 0
	}
	next := s.last.Load() + int64(udpTimeout(s.owner.udp[s.key.index].Timeout))
	if next > now {
		return next
	}
	// Keep the slot until both IO goroutines have stopped. A native
	// SendDatagram may be waiting for congestion/backpressure to clear; freeing
	// its slot early would permit unbounded replacement goroutines.
	s.cancel()
	s.queue.close()
	return 0
}
func (s *reverseUDPSession) run() {
	defer s.cancel()
	defer s.queue.close()
	defer func() {
		shard := s.owner.shard(s.key)
		shard.mu.Lock()
		if shard.sessions[s.key] == s {
			delete(shard.sessions, s.key)
			s.owner.budget.sessions.Add(-1)
		}
		shard.mu.Unlock()
		s.owner.expiry.remove(s.expiry)
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
	stop := context.AfterFunc(s.ctx, func() { udp.Close() })
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sender := extension.UDPSender{Conn: s.key.conn, Prefix: reversePrefix(s.key.index)}
		address := udp.RemoteAddr().String()
		buffer := make([]byte, extension.MaxUDPSize+1)
		for {
			n, err := udp.Read(buffer)
			if err != nil {
				s.cancel()
				return
			}
			if n > extension.MaxUDPSize {
				continue
			}
			s.last.Store(udpNow())
			message := extension.UDPMessage{SessionID: s.key.id, FragCount: 1, Addr: address, Data: buffer[:n]}
			if err := sender.Send(&message); err != nil && s.key.conn.Context().Err() != nil {
				s.cancel()
				return
			}
		}
	}()
	defer func() { udp.Close(); <-done }()
	batchSize := s.owner.budget.options.BatchSize
	writer := newUDPWriter(udp, batchSize)
	writes := make([]udpWritePacket, 0, batchSize)
	var defrag extension.Defragger
	consume := func(p udpQueuedPacket) {
		message := defrag.Feed(p.message)
		if message != nil && len(message.Data) <= extension.MaxUDPSize {
			writes = append(writes, udpWritePacket{data: message.Data})
		}
	}
	for receiveUDPBatch(s.ctx, s.queue, batchSize, consume) {
		if len(writes) > 0 {
			if _, err := writer.write(writes); err != nil {
				return
			}
		}
		clear(writes)
		writes = writes[:0]
	}
}
