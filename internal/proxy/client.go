package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
	"github.com/asdetycv1zzc/UDPBrutal/internal/transport"
	"github.com/hashicorp/yamux"
)

type Client struct {
	cfg                 config.Client
	ctx                 context.Context
	cancel              context.CancelFunc
	mu                  sync.RWMutex
	session             *yamux.Session
	reverseAddresses    []string
	reverseUDPAddresses []string
	listeners           []net.Listener
	udpListeners        []*net.UDPConn
	closed              bool
	done                chan struct{}
}

func StartClient(parent context.Context, cfg config.Client) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	c := &Client{cfg: cfg, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if err := c.connect(); err != nil {
		cancel()
		return nil, err
	}
	if cfg.SOCKS5.Listen != "" {
		l, err := net.Listen("tcp", cfg.SOCKS5.Listen)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		c.listeners = append(c.listeners, l)
		go c.serveSOCKS(l)
		slog.Info("SOCKS5 listening", "listen", l.Addr())
	}
	for _, f := range cfg.TCPForwarding {
		l, err := net.Listen("tcp", f.Listen)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		c.listeners = append(c.listeners, l)
		go c.serveTCP(l, f.Remote)
		slog.Info("TCP forwarding listening", "listen", l.Addr(), "remote", f.Remote)
	}
	for _, f := range cfg.UDPForwarding {
		addr, err := net.ResolveUDPAddr("udp", f.Listen)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		l, err := net.ListenUDP("udp", addr)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		c.udpListeners = append(c.udpListeners, l)
		go serveUDPMapping(ctx, l, func() (*yamux.Stream, error) { return c.open(request{Command: "udpFixed", Address: f.Remote}) }, udpTimeout(f.Timeout))
		slog.Info("UDP forwarding listening", "listen", l.LocalAddr(), "remote", f.Remote)
	}
	go c.maintain()
	return c, nil
}
func (c *Client) connect() error {
	options, err := config.Options(c.cfg.Bandwidth, c.cfg.Transport, c.cfg.Auth)
	if err != nil {
		return err
	}
	conn, err := transport.Dial(c.ctx, c.cfg.Server, options)
	if err != nil {
		return err
	}
	session, err := yamux.Client(conn, muxConfig())
	if err != nil {
		_ = conn.Close()
		return err
	}
	control, err := session.OpenStream()
	if err != nil {
		_ = session.Close()
		return err
	}
	_ = control.SetDeadline(time.Now().Add(10 * time.Second))
	// The server needs the bind addresses only; local destinations stay on client.
	mappings := make([]config.Forward, len(c.cfg.ReverseTCP))
	for i, f := range c.cfg.ReverseTCP {
		mappings[i].Listen = f.Listen
	}
	udpMappings := make([]config.Forward, len(c.cfg.ReverseUDP))
	for i, f := range c.cfg.ReverseUDP {
		udpMappings[i].Listen = f.Listen
		udpMappings[i].Timeout = f.Timeout
	}
	if err = writeMessage(control, request{Command: "control", Reverse: mappings, ReverseUDP: udpMappings}); err != nil {
		_ = session.Close()
		return err
	}
	var reply response
	if err = readMessage(control, &reply); err != nil {
		_ = session.Close()
		return err
	}
	if !reply.OK {
		_ = session.Close()
		return errors.New(reply.Error)
	}
	_ = control.SetDeadline(time.Time{})
	c.mu.Lock()
	if c.closed || c.ctx.Err() != nil {
		c.mu.Unlock()
		_ = session.Close()
		return net.ErrClosed
	}
	c.session = session
	c.reverseAddresses = reply.Addresses
	c.reverseUDPAddresses = reply.UDPAddresses
	c.mu.Unlock()
	go c.acceptReverse(session)
	slog.Info("connected to server", "server", c.cfg.Server, "reverseTCP", reply.Addresses, "reverseUDP", reply.UDPAddresses)
	return nil
}
func (c *Client) maintain() {
	defer close(c.done)
	defer c.Close()
	for {
		c.mu.RLock()
		session := c.session
		c.mu.RUnlock()
		select {
		case <-c.ctx.Done():
			return
		case <-session.CloseChan():
		}
		slog.Warn("server disconnected; reconnecting")
		for {
			timer := time.NewTimer(time.Second)
			select {
			case <-c.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if err := c.connect(); err == nil {
				break
			} else {
				slog.Debug("reconnect failed", "error", err)
			}
		}
	}
}
func (c *Client) Wait() { <-c.done }
func (c *Client) Close() error {
	c.cancel()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	session := c.session
	listeners := append([]net.Listener(nil), c.listeners...)
	udpListeners := append([]*net.UDPConn(nil), c.udpListeners...)
	c.mu.Unlock()
	for _, l := range listeners {
		_ = l.Close()
	}
	for _, l := range udpListeners {
		_ = l.Close()
	}
	if session != nil {
		_ = session.Close()
	}
	return nil
}
func (c *Client) LocalAddresses() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.listeners))
	for i, l := range c.listeners {
		out[i] = l.Addr().String()
	}
	return out
}
func (c *Client) ReverseAddresses() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.reverseAddresses...)
}
func (c *Client) ReverseUDPAddresses() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.reverseUDPAddresses...)
}
func (c *Client) LocalUDPAddresses() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.udpListeners))
	for i, l := range c.udpListeners {
		out[i] = l.LocalAddr().String()
	}
	return out
}
func (c *Client) open(req request) (*yamux.Stream, error) {
	c.mu.RLock()
	session := c.session
	closed := c.closed
	c.mu.RUnlock()
	if closed || session == nil || session.IsClosed() {
		return nil, errors.New("server is disconnected")
	}
	return open(session, req)
}
func (c *Client) DialTCP(address string) (net.Conn, error) {
	return c.open(request{Command: "tcp", Address: address})
}
func (c *Client) serveTCP(l net.Listener, remote string) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			stream, err := c.DialTCP(remote)
			if err != nil {
				_ = conn.Close()
				return
			}
			relay(conn, stream)
		}()
	}
}
func (c *Client) acceptReverse(session *yamux.Session) {
	slots := make(chan struct{}, 256)
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			go func() { defer func() { <-slots }(); c.handleReverse(stream) }()
		default:
			_ = stream.Close()
		}
	}
}
func (c *Client) handleReverse(stream *yamux.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	var req request
	if readMessage(stream, &req) != nil {
		return
	}
	if req.Command == "reverseUDP" {
		if req.Index < 0 || req.Index >= len(c.cfg.ReverseUDP) {
			_ = writeMessage(stream, response{Error: "unknown reverse UDP mapping"})
			return
		}
		f := c.cfg.ReverseUDP[req.Index]
		handleFixedUDP(c.ctx, stream, f.Remote, udpTimeout(f.Timeout))
		return
	}
	if req.Command != "reverse" || req.Index < 0 || req.Index >= len(c.cfg.ReverseTCP) {
		_ = writeMessage(stream, response{Error: "unknown reverse mapping"})
		return
	}
	remote := c.cfg.ReverseTCP[req.Index].Remote
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(c.ctx, "tcp", remote)
	if err != nil {
		_ = writeMessage(stream, response{Error: fmt.Sprintf("local target: %v", err)})
		return
	}
	defer conn.Close()
	if writeMessage(stream, response{OK: true}) != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	relay(stream, conn)
}
