package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

type Options struct {
	Auth                    string
	SendBPS, ReceiveBPS     uint64
	DisableLossCompensation bool
	IdleTimeout             time.Duration
	HandshakeTimeout        time.Duration
	MaxSessions             int
}

func (o Options) defaults() Options {
	if o.SendBPS == 0 {
		o.SendBPS = 12500000
	}
	if o.IdleTimeout == 0 {
		o.IdleTimeout = 30 * time.Second
	}
	if o.HandshakeTimeout == 0 {
		o.HandshakeTimeout = 5 * time.Second
	}
	if o.MaxSessions == 0 {
		o.MaxSessions = 128
	}
	return o
}

type negotiation struct {
	Auth    string `json:"auth,omitempty"`
	Send    uint64 `json:"tx"`
	Receive uint64 `json:"rx"`
}

func limited(a, b uint64) uint64 {
	if b == 0 {
		return a
	}
	return min(a, b)
}
func sessionKey(id uint64, addr net.Addr) string { return fmt.Sprintf("%d/%s", id, addr.String()) }

type Listener struct {
	socket   *net.UDPConn
	options  Options
	mu       sync.Mutex
	sessions map[string]*Conn
	accepted chan net.Conn
	done     chan struct{}
	once     sync.Once
}

var _ net.Listener = (*Listener)(nil)

func Listen(addr string, options Options) (*Listener, error) {
	a, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	socket, err := net.ListenUDP("udp", a)
	if err != nil {
		return nil, err
	}
	_ = socket.SetReadBuffer(4 << 20)
	_ = socket.SetWriteBuffer(4 << 20)
	l := &Listener{socket: socket, options: options.defaults(), sessions: make(map[string]*Conn), accepted: make(chan net.Conn, 128), done: make(chan struct{})}
	go l.readLoop()
	return l, nil
}
func (l *Listener) readLoop() {
	buf := make([]byte, 65536)
	for {
		n, addr, err := l.socket.ReadFromUDP(buf)
		if err != nil {
			_ = l.Close()
			return
		}
		p, err := decode(buf[:n])
		if err != nil {
			continue
		}
		key := sessionKey(p.session, addr)
		l.mu.Lock()
		c := l.sessions[key]
		l.mu.Unlock()
		if p.kind == hello {
			var request negotiation
			if json.Unmarshal(p.payload, &request) != nil || request.Send == 0 || request.Send > 1<<40 || request.Receive > 1<<40 {
				continue
			}
			if request.Auth != l.options.Auth {
				_, _ = l.socket.WriteToUDP(encode(packet{kind: reject, session: p.session, payload: []byte("authentication failed")}), addr)
				continue
			}
			options := l.options
			options.SendBPS = limited(options.SendBPS, request.Receive)
			reply, _ := json.Marshal(negotiation{Send: limited(request.Send, options.ReceiveBPS), Receive: options.SendBPS})
			if c == nil {
				l.mu.Lock()
				if len(l.sessions) >= options.MaxSessions {
					l.mu.Unlock()
					continue
				}
				c = newConn(p.session, l.socket.LocalAddr(), addr, func(b []byte) error { _, err := l.socket.WriteToUDP(b, addr); return err }, options, func() { l.mu.Lock(); delete(l.sessions, key); l.mu.Unlock() })
				l.sessions[key] = c
				l.mu.Unlock()
				select {
				case l.accepted <- c:
				default:
					_ = c.Close()
					continue
				}
			}
			_, _ = l.socket.WriteToUDP(encode(packet{kind: welcome, session: p.session, next: 1, payload: reply}), addr)
		} else if c != nil {
			c.input(p)
		}
	}
}
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	default:
	}
	select {
	case c := <-l.accepted:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *Listener) Addr() net.Addr { return l.socket.LocalAddr() }
func (l *Listener) Close() error {
	l.once.Do(func() {
		close(l.done)
		_ = l.socket.Close()
		l.mu.Lock()
		sessions := make([]*Conn, 0, len(l.sessions))
		for _, c := range l.sessions {
			sessions = append(sessions, c)
		}
		l.mu.Unlock()
		for _, c := range sessions {
			c.fail(net.ErrClosed)
		}
	})
	return nil
}

func Dial(ctx context.Context, addr string, options Options) (*Conn, error) {
	options = options.defaults()
	ctx, cancel := context.WithTimeout(ctx, options.HandshakeTimeout)
	defer cancel()
	a, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	socket, err := net.DialUDP("udp", nil, a)
	if err != nil {
		return nil, err
	}
	_ = socket.SetReadBuffer(4 << 20)
	_ = socket.SetWriteBuffer(4 << 20)
	id := rand.Uint64() // Identifier only. It is deliberately not a security credential.
	request, _ := json.Marshal(negotiation{Auth: options.Auth, Send: options.SendBPS, Receive: options.ReceiveBPS})
	if len(request) > payloadSize {
		_ = socket.Close()
		return nil, errors.New("authentication string too long")
	}
	done := make(chan struct{})
	watcherExited := make(chan struct{})
	go func() {
		defer close(watcherExited)
		select {
		case <-ctx.Done():
			_ = socket.SetReadDeadline(time.Now())
		case <-done:
		}
	}()
	buf := make([]byte, 65536)
	for {
		if ctx.Err() != nil {
			close(done)
			_ = socket.Close()
			return nil, ctx.Err()
		}
		_, err = socket.Write(encode(packet{kind: hello, session: id, next: 1, payload: request}))
		if err != nil {
			close(done)
			_ = socket.Close()
			return nil, err
		}
		_ = socket.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, err := socket.Read(buf)
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				continue
			}
			close(done)
			_ = socket.Close()
			return nil, err
		}
		p, err := decode(buf[:n])
		if err != nil || p.session != id {
			continue
		}
		if p.kind == reject {
			close(done)
			_ = socket.Close()
			return nil, errors.New(string(p.payload))
		}
		if p.kind != welcome {
			continue
		}
		var reply negotiation
		if json.Unmarshal(p.payload, &reply) != nil || reply.Send == 0 || reply.Send > options.SendBPS {
			continue
		}
		options.SendBPS = reply.Send
		close(done)
		<-watcherExited
		_ = socket.SetReadDeadline(time.Time{})
		c := newConn(id, socket.LocalAddr(), socket.RemoteAddr(), func(b []byte) error { _, err := socket.Write(b); return err }, options, func() { _ = socket.Close() })
		go func() {
			b := make([]byte, 65536)
			for {
				n, err := socket.Read(b)
				if err != nil {
					c.fail(err)
					return
				}
				p, err := decode(b[:n])
				if err == nil && p.session == id {
					c.input(p)
				}
			}
		}()
		return c, nil
	}
}
