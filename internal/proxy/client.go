package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	hyclient "github.com/apernet/hysteria/core/v2/client"
	"github.com/apernet/quic-go"
	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
	"github.com/asdetycv1zzc/UDPBrutal/internal/forwarding"
	"github.com/asdetycv1zzc/UDPBrutal/internal/socks5"
)

type Client struct {
	cfg                    config.Client
	ctx                    context.Context
	cancel                 context.CancelFunc
	mu                     sync.RWMutex
	native                 hyclient.Client
	conn                   *quic.Conn
	reverse                *reverseClient
	reverseTCP, reverseUDP []string
	listeners              []net.Listener
	udpListeners           []net.PacketConn
	closed                 bool
	done                   chan struct{}
}

var _ hyclient.Client = (*Client)(nil)

type packetFactory struct{ socket net.PacketConn }

func (f packetFactory) New(net.Addr) (net.PacketConn, error) { return f.socket, nil }

func StartClient(parent context.Context, cfg config.Client) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	c := &Client{cfg: cfg, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	c.reverse = newReverseClient(ctx, cfg.ReverseTCP, cfg.ReverseUDP, cfg.ReverseUDPOptions)
	if err := c.connect(); err != nil {
		cancel()
		c.reverse.Close()
		return nil, err
	}
	if cfg.SOCKS5.Listen != "" {
		l, err := net.Listen("tcp", cfg.SOCKS5.Listen)
		if err != nil {
			c.Close()
			return nil, err
		}
		c.listeners = append(c.listeners, l)
		server := &socks5.Server{HyClient: c, DisableUDP: cfg.SOCKS5.DisableUDP}
		if cfg.SOCKS5.Username != "" {
			server.AuthFunc = func(u, p string) bool { return u == cfg.SOCKS5.Username && p == cfg.SOCKS5.Password }
		}
		go server.Serve(l)
		slog.Info("SOCKS5 listening", "listen", l.Addr())
	}
	for _, f := range cfg.TCPForwarding {
		l, err := net.Listen("tcp", f.Listen)
		if err != nil {
			c.Close()
			return nil, err
		}
		c.listeners = append(c.listeners, l)
		tunnel := &forwarding.TCPTunnel{HyClient: c, Remote: f.Remote}
		go tunnel.Serve(l)
		slog.Info("TCP forwarding listening", "listen", l.Addr(), "remote", f.Remote)
	}
	for _, f := range cfg.UDPForwarding {
		socket, err := net.ListenPacket("udp", f.Listen)
		if err != nil {
			c.Close()
			return nil, err
		}
		c.udpListeners = append(c.udpListeners, socket)
		tunnel := &forwarding.UDPTunnel{HyClient: c, Remote: f.Remote, Timeout: udpTimeout(f.Timeout)}
		go tunnel.Serve(socket)
		slog.Info("UDP forwarding listening", "listen", socket.LocalAddr(), "remote", f.Remote)
	}
	go c.maintain()
	return c, nil
}
func (c *Client) connect() error {
	addr, err := net.ResolveUDPAddr("udp", c.cfg.Server)
	if err != nil {
		return err
	}
	socket, err := net.ListenUDP("udp", nil)
	if err != nil {
		return err
	}
	stopCancel := context.AfterFunc(c.ctx, func() { socket.Close() })
	defer stopCancel()
	up, down, err := config.BandwidthBytes(c.cfg.Bandwidth)
	if err != nil {
		socket.Close()
		return err
	}
	q := c.cfg.QUIC
	incomingStreams := q.MaxIncomingStreams
	if len(c.cfg.ReverseTCP) > 0 && incomingStreams == 0 {
		incomingStreams = 1024
	}
	native, _, err := hyclient.NewClient(&hyclient.Config{
		ConnFactory: packetFactory{socket}, ServerAddr: addr, Auth: c.cfg.Auth, FastOpen: c.cfg.FastOpen,
		QUICConfig: hyclient.QUICConfig{
			MaxIncomingStreams:         incomingStreams,
			InitialStreamReceiveWindow: q.InitStreamReceiveWindow, MaxStreamReceiveWindow: q.MaxStreamReceiveWindow,
			InitialConnectionReceiveWindow: q.InitConnReceiveWindow, MaxConnectionReceiveWindow: q.MaxConnReceiveWindow,
			MaxIdleTimeout: q.MaxIdleTimeout, KeepAlivePeriod: q.KeepAlivePeriod,
			DisablePathMTUDiscovery: q.DisablePathMTUDiscovery, DisableGSO: q.DisableGSO, DisableChromeParrot: true,
		},
		CongestionConfig: hyclient.CongestionConfig{Type: c.cfg.Congestion.Type, BBRProfile: c.cfg.Congestion.BBRProfile},
		BandwidthConfig:  hyclient.BandwidthConfig{MaxTx: up, MaxRx: down, DisableLossCompensation: c.cfg.Bandwidth.DisableLossCompensation},
		DatagramHandler:  c.reverse.HandleDatagram,
	})
	if err != nil {
		socket.Close()
		return err
	}
	owner, ok := native.(interface{ Connection() *quic.Conn })
	if !ok {
		native.Close()
		return errors.New("Hysteria native connection hook is unavailable")
	}
	conn := owner.Connection()
	startConnectionObserver(conn, "client")
	go c.reverse.AcceptStreams(conn)
	var reply registrationReply
	if len(c.cfg.ReverseTCP)+len(c.cfg.ReverseUDP) > 0 {
		reply, err = c.reverse.Register(conn)
		if err != nil {
			native.Close()
			return err
		}
	}
	c.mu.Lock()
	if c.closed || c.ctx.Err() != nil {
		c.mu.Unlock()
		native.Close()
		return net.ErrClosed
	}
	c.native = native
	c.conn = conn
	c.reverseTCP = reply.TCP
	c.reverseUDP = reply.UDP
	c.mu.Unlock()
	slog.Info("connected to server", "server", c.cfg.Server, "transport", "Hysteria plaintext profile", "reverseTCP", reply.TCP, "reverseUDP", reply.UDP)
	return nil
}
func (c *Client) maintain() {
	defer close(c.done)
	defer c.Close()
	for {
		c.mu.RLock()
		conn := c.conn
		native := c.native
		c.mu.RUnlock()
		select {
		case <-c.ctx.Done():
			return
		case <-conn.Context().Done():
		}
		_ = native.Close()
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
func (c *Client) TCP(address string) (net.Conn, error) {
	c.mu.RLock()
	native := c.native
	closed := c.closed
	c.mu.RUnlock()
	if closed || native == nil {
		return nil, net.ErrClosed
	}
	return native.TCP(address)
}
func (c *Client) DialTCP(address string) (net.Conn, error) { return c.TCP(address) }
func (c *Client) UDP() (hyclient.HyUDPConn, error) {
	c.mu.RLock()
	native := c.native
	closed := c.closed
	c.mu.RUnlock()
	if closed || native == nil {
		return nil, net.ErrClosed
	}
	return native.UDP()
}
func (c *Client) Close() error {
	c.cancel()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	native := c.native
	listeners := append([]net.Listener(nil), c.listeners...)
	udp := append([]net.PacketConn(nil), c.udpListeners...)
	c.mu.Unlock()
	for _, l := range listeners {
		l.Close()
	}
	for _, l := range udp {
		l.Close()
	}
	c.reverse.Close()
	if native != nil {
		return native.Close()
	}
	return nil
}
func (c *Client) Wait() { <-c.done }
func (c *Client) LocalAddresses() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var a []string
	for _, l := range c.listeners {
		a = append(a, l.Addr().String())
	}
	return a
}
func (c *Client) LocalUDPAddresses() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var a []string
	for _, l := range c.udpListeners {
		a = append(a, l.LocalAddr().String())
	}
	return a
}
func (c *Client) ReverseAddresses() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.reverseTCP...)
}
func (c *Client) ReverseUDPAddresses() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.reverseUDP...)
}
