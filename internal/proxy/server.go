package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
	"github.com/asdetycv1zzc/UDPBrutal/internal/transport"
	"github.com/hashicorp/yamux"
)

type Server struct {
	config   config.Server
	listener *transport.Listener
	mu       sync.Mutex
	sessions map[*yamux.Session]struct{}
	closed   bool
}

func NewServer(cfg config.Server) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	options, err := config.Options(cfg.Bandwidth, cfg.Transport, cfg.Auth.Password)
	if err != nil {
		return nil, err
	}
	l, err := transport.Listen(cfg.Listen, options)
	if err != nil {
		return nil, err
	}
	return &Server{config: cfg, listener: l, sessions: make(map[*yamux.Session]struct{})}, nil
}
func (s *Server) Addr() net.Addr { return s.listener.Addr() }
func (s *Server) Serve(ctx context.Context) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-done:
		}
	}()
	for {
		c, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.serveSession(ctx, c)
	}
}
func (s *Server) Close() error {
	_ = s.listener.Close()
	s.mu.Lock()
	s.closed = true
	sessions := make([]*yamux.Session, 0, len(s.sessions))
	for m := range s.sessions {
		sessions = append(sessions, m)
	}
	s.mu.Unlock()
	for _, m := range sessions {
		_ = m.Close()
	}
	return nil
}
func (s *Server) serveSession(parent context.Context, c net.Conn) {
	defer c.Close()
	session, err := yamux.Server(c, muxConfig())
	if err != nil {
		return
	}
	defer session.Close()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.sessions[session] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.sessions, session); s.mu.Unlock() }()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() {
		select {
		case <-session.CloseChan():
			cancel()
		case <-ctx.Done():
		}
	}()
	// The first stream registers reverse listeners atomically before proxies start.
	initCtx, initCancel := context.WithTimeout(ctx, 10*time.Second)
	control, err := session.AcceptStreamWithContext(initCtx)
	initCancel()
	if err != nil {
		return
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(10 * time.Second))
	var req request
	if readMessage(control, &req) != nil || req.Command != "control" {
		return
	}
	listeners, udpListeners, addresses, udpAddresses, err := s.bindReverse(req.Reverse, req.ReverseUDP)
	if err != nil {
		_ = writeMessage(control, response{Error: err.Error()})
		return
	}
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
		for _, l := range udpListeners {
			_ = l.Close()
		}
	}()
	if writeMessage(control, response{OK: true, Addresses: addresses, UDPAddresses: udpAddresses}) != nil {
		return
	}
	_ = control.SetDeadline(time.Time{})
	// Control EOF tears down this session and releases its advertised ports.
	go func() { _, _ = io.Copy(io.Discard, control); _ = session.Close() }()
	for i, l := range listeners {
		go serveReverse(ctx, session, l, i)
	}
	for i, l := range udpListeners {
		go serveUDPMapping(ctx, l, func() (*yamux.Stream, error) { return open(session, request{Command: "reverseUDP", Index: i}) }, udpTimeout(req.ReverseUDP[i].Timeout))
	}
	slots := make(chan struct{}, 256)
	for {
		stream, err := session.AcceptStreamWithContext(ctx)
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			go func() { defer func() { <-slots }(); s.handleStream(ctx, stream) }()
		default:
			_ = stream.Close()
		}
	}
}
func (s *Server) bindReverse(forwards, udpForwards []config.Forward) ([]net.Listener, []*net.UDPConn, []string, []string, error) {
	if len(forwards)+len(udpForwards) > 0 && !s.config.Reverse.Enabled {
		return nil, nil, nil, nil, errors.New("server reverse forwarding is disabled")
	}
	if len(forwards)+len(udpForwards) > s.config.Reverse.MaxListeners {
		return nil, nil, nil, nil, errors.New("too many reverse listeners")
	}
	var listeners []net.Listener
	var udpListeners []*net.UDPConn
	var addresses, udpAddresses []string
	cleanup := func() {
		for _, l := range listeners {
			_ = l.Close()
		}
		for _, l := range udpListeners {
			_ = l.Close()
		}
	}
	for _, f := range forwards {
		l, err := net.Listen("tcp", f.Listen)
		if err != nil {
			cleanup()
			return nil, nil, nil, nil, err
		}
		listeners = append(listeners, l)
		addresses = append(addresses, l.Addr().String())
		slog.Info("reverse TCP listening", "listen", l.Addr())
	}
	for _, f := range udpForwards {
		addr, err := net.ResolveUDPAddr("udp", f.Listen)
		if err != nil {
			cleanup()
			return nil, nil, nil, nil, err
		}
		l, err := net.ListenUDP("udp", addr)
		if err != nil {
			cleanup()
			return nil, nil, nil, nil, err
		}
		udpListeners = append(udpListeners, l)
		udpAddresses = append(udpAddresses, l.LocalAddr().String())
		slog.Info("reverse UDP listening", "listen", l.LocalAddr())
	}
	return listeners, udpListeners, addresses, udpAddresses, nil
}
func serveReverse(ctx context.Context, session *yamux.Session, l net.Listener, index int) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-done:
		}
	}()
	slots := make(chan struct{}, 256)
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				stream, err := open(session, request{Command: "reverse", Index: index})
				if err != nil {
					_ = c.Close()
					return
				}
				relay(c, stream)
			}()
		default:
			_ = c.Close()
		}
	}
}
func (s *Server) handleStream(ctx context.Context, stream *yamux.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	var req request
	if err := readMessage(stream, &req); err != nil {
		return
	}
	switch req.Command {
	case "tcp":
		if _, _, err := net.SplitHostPort(req.Address); err != nil {
			_ = writeMessage(stream, response{Error: "invalid TCP address"})
			return
		}
		c, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", req.Address)
		if err != nil {
			_ = writeMessage(stream, response{Error: err.Error()})
			return
		}
		defer c.Close()
		if writeMessage(stream, response{OK: true}) != nil {
			return
		}
		_ = stream.SetDeadline(time.Time{})
		relay(stream, c)
	case "udp", "udpFixed":
		if s.config.DisableUDP {
			_ = writeMessage(stream, response{Error: "server UDP relay is disabled"})
			return
		}
		if req.Command == "udpFixed" {
			handleFixedUDP(ctx, stream, req.Address, s.config.UDPIdleTimeout)
			return
		}
		c, err := net.ListenUDP("udp", nil)
		if err != nil {
			_ = writeMessage(stream, response{Error: err.Error()})
			return
		}
		defer c.Close()
		if writeMessage(stream, response{OK: true}) != nil {
			return
		}
		_ = stream.SetDeadline(time.Time{})
		relayUDPSocket(ctx, stream, c, "", s.config.UDPIdleTimeout)
	default:
		_ = writeMessage(stream, response{Error: "unsupported command"})
	}
}
