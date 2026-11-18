package proxy

import (
	"context"
	"errors"
	"net"
	"sync"

	hyserver "github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/quic-go"
	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
)

type Server struct {
	socket    net.PacketConn
	native    hyserver.Server
	reverse   *reverseServer
	bandwidth *bandwidthManager
	once      sync.Once
	closeErr  error
}
type passwordAuth struct{ config config.Auth }

func (a passwordAuth) Authenticate(_ net.Addr, provided string, _ uint64) (bool, string) {
	if a.config.Type == "none" || (a.config.Type == "" && a.config.Password == "") {
		return true, "anonymous"
	}
	return provided == a.config.Password, "password"
}
func NewServer(cfg config.Server) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	bandwidth, err := newBandwidthManager(cfg.Bandwidth, "server", !cfg.IgnoreClientBandwidth)
	if err != nil {
		return nil, err
	}
	socket, err := net.ListenPacket("udp", cfg.Listen)
	if err != nil {
		return nil, err
	}
	reverse := newReverseServer(cfg.Reverse, cfg.ReverseUDPOptions)
	up, down, err := config.BandwidthBytes(cfg.Bandwidth)
	if err != nil {
		socket.Close()
		return nil, err
	}
	q := cfg.QUIC
	native, err := hyserver.NewServer(&hyserver.Config{
		Conn: socket, Authenticator: passwordAuth{cfg.Auth},
		QUICConfig: hyserver.QUICConfig{
			InitialStreamReceiveWindow: q.InitStreamReceiveWindow, MaxStreamReceiveWindow: q.MaxStreamReceiveWindow,
			InitialConnectionReceiveWindow: q.InitConnReceiveWindow, MaxConnectionReceiveWindow: q.MaxConnReceiveWindow,
			MaxIdleTimeout: q.MaxIdleTimeout, MaxIncomingStreams: q.MaxIncomingStreams,
			DisablePathMTUDiscovery: q.DisablePathMTUDiscovery, DisableGSO: q.DisableGSO, DisableStatelessReset: q.DisableStatelessReset,
		},
		BandwidthConfig: hyserver.BandwidthConfig{MaxTx: up, MaxRx: down, DisableLossCompensation: cfg.Bandwidth.DisableLossCompensation},
		BandwidthConfigFunc: func() hyserver.BandwidthConfig {
			current := bandwidth.snapshot()
			return hyserver.BandwidthConfig{MaxTx: current.up, MaxRx: current.down, DisableLossCompensation: current.disableLossCompensation}
		},
		BandwidthReload:       bandwidth.enabled,
		CongestionConfig:      hyserver.CongestionConfig{Type: cfg.Congestion.Type, BBRProfile: cfg.Congestion.BBRProfile},
		IgnoreClientBandwidth: cfg.IgnoreClientBandwidth, DisableUDP: cfg.DisableUDP, UDPIdleTimeout: cfg.UDPIdleTimeout,
		StreamHandler: func(conn *quic.Conn, kind uint64, stream *quic.Stream) (bool, error) {
			if handled, err := bandwidth.HandleStream(conn, kind, stream); handled || err != nil {
				return handled, err
			}
			return reverse.HandleStream(conn, kind, stream)
		},
		DatagramHandler: reverse.HandleDatagram,
	})
	if err != nil {
		socket.Close()
		reverse.Close()
		return nil, err
	}
	return &Server{socket: socket, native: native, reverse: reverse, bandwidth: bandwidth}, nil
}
func (s *Server) ReloadBandwidth(b config.Bandwidth) error { return s.bandwidth.reload(b) }
func (s *Server) Addr() net.Addr                           { return s.socket.LocalAddr() }
func (s *Server) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer stop()
	err := s.native.Serve()
	if ctx.Err() != nil || errors.Is(err, net.ErrClosed) || errors.Is(err, quic.ErrServerClosed) {
		return nil
	}
	return err
}
func (s *Server) Close() error {
	s.once.Do(func() { s.bandwidth.close(); s.reverse.Close(); s.closeErr = s.native.Close() })
	return s.closeErr
}
