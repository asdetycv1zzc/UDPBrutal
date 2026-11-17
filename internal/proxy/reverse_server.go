package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/apernet/hysteria/core/v2/extension"
	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/quicvarint"
	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
)

type reverseServer struct {
	cfg     config.Reverse
	options config.ReverseUDPOptions
	mu      sync.RWMutex
	peers   map[*quic.Conn]*reversePeer
	closed  bool
}
type reversePeer struct {
	owner   *reverseServer
	conn    *quic.Conn
	control *quic.Stream
	ctx     context.Context
	cancel  context.CancelFunc
	tcp     []net.Listener
	udp     []*reverseBinding
	mu      sync.RWMutex
	budget  udpBudget
	expiry  *udpExpiry
	done    chan struct{}
	once    sync.Once
	closed  bool
}

func newReverseServer(cfg config.Reverse, options config.ReverseUDPOptions) *reverseServer {
	return &reverseServer{cfg: cfg, options: options, peers: make(map[*quic.Conn]*reversePeer)}
}
func (s *reverseServer) HandleStream(conn *quic.Conn, kind uint64, stream *quic.Stream) (bool, error) {
	if kind != reverseControlType {
		return false, nil
	}
	// Registration owns the stream for its lifetime; return to the dispatcher.
	go s.register(conn, stream)
	return true, nil
}
func (s *reverseServer) register(conn *quic.Conn, stream *quic.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	kind, err := readFrameType(stream)
	if err != nil || kind != reverseControlType {
		return
	}
	var request registration
	if err := readMessage(stream, &request); err != nil {
		return
	}
	reject := func(err error) { _ = writeMessage(stream, registrationReply{Error: err.Error()}); stream.CancelRead(0) }
	count := len(request.TCP) + len(request.UDP)
	if count > 0 && !s.cfg.Enabled {
		reject(fmt.Errorf("server reverse forwarding is disabled"))
		return
	}
	if count > s.cfg.MaxListeners {
		reject(fmt.Errorf("too many reverse listeners"))
		return
	}
	peerContext, peerCancel := context.WithCancel(conn.Context())
	p := &reversePeer{owner: s, conn: conn, control: stream, ctx: peerContext, cancel: peerCancel, budget: udpBudget{options: s.options}, done: make(chan struct{})}
	p.expiry = newUDPExpiry(peerContext)
	s.mu.Lock()
	if s.closed || s.peers[conn] != nil {
		s.mu.Unlock()
		peerCancel()
		reject(fmt.Errorf("reverse registration already exists or server closed"))
		return
	}
	s.peers[conn] = p
	s.mu.Unlock()
	defer p.Close()
	go func() {
		select {
		case <-conn.Context().Done():
			p.Close()
		case <-p.done:
		}
	}()
	reply := registrationReply{OK: true}
	for _, f := range request.TCP {
		l, err := net.Listen("tcp", f.Listen)
		if err != nil {
			reject(err)
			return
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			l.Close()
			return
		}
		p.tcp = append(p.tcp, l)
		p.mu.Unlock()
		reply.TCP = append(reply.TCP, l.Addr().String())
	}
	for i, f := range request.UDP {
		if f.Timeout < 0 || f.Timeout > 24*time.Hour {
			reject(fmt.Errorf("reverse UDP timeout must be in 0..24h"))
			return
		}
		addr, err := net.ResolveUDPAddr("udp", f.Listen)
		if err != nil {
			reject(err)
			return
		}
		socket, err := net.ListenUDP("udp", addr)
		if err != nil {
			reject(err)
			return
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			socket.Close()
			return
		}
		p.udp = append(p.udp, newReverseBinding(p, socket, uint16(i), udpTimeout(f.Timeout)))
		p.mu.Unlock()
		reply.UDP = append(reply.UDP, socket.LocalAddr().String())
	}
	if err := writeMessage(stream, reply); err != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	p.mu.Lock()
	tcpListeners := append([]net.Listener(nil), p.tcp...)
	udpListeners := append([]*reverseBinding(nil), p.udp...)
	p.mu.Unlock()
	for i, l := range tcpListeners {
		go p.serveTCP(l, uint64(i))
		slog.Info("reverse TCP listening", "listen", l.Addr())
	}
	for _, b := range udpListeners {
		go b.serve()
		slog.Info("reverse UDP listening", "listen", b.socket.LocalAddr())
	}
	_, _ = io.Copy(io.Discard, stream)
}
func (p *reversePeer) Close() {
	p.once.Do(func() {
		p.cancel()
		close(p.done)
		p.mu.Lock()
		p.closed = true
		tcp := append([]net.Listener(nil), p.tcp...)
		udp := append([]*reverseBinding(nil), p.udp...)
		p.mu.Unlock()
		for _, l := range tcp {
			l.Close()
		}
		for _, b := range udp {
			b.close()
		}
		p.control.CancelRead(0)
		_ = p.control.Close()
		p.owner.mu.Lock()
		if p.owner.peers[p.conn] == p {
			delete(p.owner.peers, p.conn)
		}
		p.owner.mu.Unlock()
	})
}
func (s *reverseServer) Close() {
	s.mu.Lock()
	s.closed = true
	var peers []*reversePeer
	for _, p := range s.peers {
		peers = append(peers, p)
	}
	s.mu.Unlock()
	for _, p := range peers {
		p.Close()
	}
}
func (p *reversePeer) serveTCP(l net.Listener, index uint64) {
	for {
		incoming, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer incoming.Close()
			stopIncoming := context.AfterFunc(p.ctx, func() { incoming.Close() })
			defer stopIncoming()
			ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
			defer cancel()
			stream, err := p.conn.OpenStreamSync(ctx)
			if err != nil {
				return
			}
			tunnel := &streamConn{stream: stream, conn: p.conn}
			defer tunnel.Close()
			stopStream := context.AfterFunc(p.ctx, func() { stream.CancelRead(0); stream.CancelWrite(0) })
			defer stopStream()
			_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
			if err = writeFrameType(stream, reverseTCPType); err != nil {
				return
			}
			if err = writeFull(stream, quicvarint.Append(nil, index)); err != nil {
				return
			}
			ok, _, err := extension.ReadTCPResponse(stream)
			if err != nil || !ok {
				return
			}
			_ = stream.SetDeadline(time.Time{})
			relay(incoming, tunnel)
		}()
	}
}
