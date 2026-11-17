package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/asdetycv1zzc/UDPBrutal/internal/transport"
	"gopkg.in/yaml.v3"
)

type Bandwidth struct {
	Up                      string `yaml:"up"`
	Down                    string `yaml:"down"`
	DisableLossCompensation bool   `yaml:"disableLossCompensation"`
}
type UDP struct {
	IdleTimeout      time.Duration `yaml:"idleTimeout"`
	HandshakeTimeout time.Duration `yaml:"handshakeTimeout"`
	MaxSessions      int           `yaml:"maxSessions"`
}
type Transport struct {
	Type string `yaml:"type"`
	UDP  UDP    `yaml:"udp"`
}
type SOCKS5 struct {
	Listen     string `yaml:"listen"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	DisableUDP bool   `yaml:"disableUDP"`
}
type Forward struct {
	Listen  string        `yaml:"listen" json:"listen"`
	Remote  string        `yaml:"remote" json:"remote,omitempty"`
	Timeout time.Duration `yaml:"timeout" json:"timeout,omitempty"`
}
type Client struct {
	Server        string    `yaml:"server"`
	Auth          string    `yaml:"auth"`
	Bandwidth     Bandwidth `yaml:"bandwidth"`
	Transport     Transport `yaml:"transport"`
	SOCKS5        SOCKS5    `yaml:"socks5"`
	TCPForwarding []Forward `yaml:"tcpForwarding"`
	UDPForwarding []Forward `yaml:"udpForwarding"`
	ReverseTCP    []Forward `yaml:"reverseTCP"`
	ReverseUDP    []Forward `yaml:"reverseUDP"`
}
type Auth struct {
	Type     string `yaml:"type"`
	Password string `yaml:"password"`
}
type Reverse struct {
	Enabled      bool `yaml:"enabled"`
	MaxListeners int  `yaml:"maxListeners"`
}
type Server struct {
	Listen         string        `yaml:"listen"`
	Auth           Auth          `yaml:"auth"`
	Bandwidth      Bandwidth     `yaml:"bandwidth"`
	Transport      Transport     `yaml:"transport"`
	Reverse        Reverse       `yaml:"reverse"`
	DisableUDP     bool          `yaml:"disableUDP"`
	UDPIdleTimeout time.Duration `yaml:"udpIdleTimeout"`
}

// ParseBandwidth uses the same decimal bit/s convention as Hysteria2.
func ParseBandwidth(s string) (uint64, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	multipliers := []struct {
		suffix     string
		multiplier float64
	}{{"tbps", 1e12}, {"gbps", 1e9}, {"mbps", 1e6}, {"kbps", 1e3}, {"bps", 1}}
	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(s, m.suffix), 64)
			bps := n * m.multiplier / 8
			if err != nil || math.IsNaN(bps) || math.IsInf(bps, 0) || bps < 1 || bps > 1<<40 {
				return 0, fmt.Errorf("invalid bandwidth %q", s)
			}
			return uint64(bps), nil
		}
	}
	return 0, fmt.Errorf("bandwidth %q needs bps/kbps/mbps/gbps/tbps", s)
}

func Options(b Bandwidth, t Transport, auth string) (transport.Options, error) {
	if t.Type != "" && t.Type != "udp" {
		return transport.Options{}, errors.New("transport.type must be udp")
	}
	up, err := ParseBandwidth(b.Up)
	if err != nil {
		return transport.Options{}, err
	}
	if up == 0 {
		up = 12500000
	}
	down, err := ParseBandwidth(b.Down)
	if err != nil {
		return transport.Options{}, err
	}
	if len(auth) > 512 {
		return transport.Options{}, errors.New("auth must be at most 512 bytes")
	}
	if t.UDP.IdleTimeout != 0 && t.UDP.IdleTimeout < 5*time.Second {
		return transport.Options{}, errors.New("idleTimeout must be at least 5s")
	}
	if t.UDP.HandshakeTimeout < 0 {
		return transport.Options{}, errors.New("handshakeTimeout must be positive")
	}
	if t.UDP.MaxSessions < 0 || t.UDP.MaxSessions > 4096 {
		return transport.Options{}, errors.New("maxSessions must be in 1..4096")
	}
	return transport.Options{Auth: auth, SendBPS: up, ReceiveBPS: down, DisableLossCompensation: b.DisableLossCompensation, IdleTimeout: t.UDP.IdleTimeout, HandshakeTimeout: t.UDP.HandshakeTimeout, MaxSessions: t.UDP.MaxSessions}, nil
}
func checkAddress(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return errors.New("port must be in 0..65535")
	}
	return nil
}
func checkTarget(addr string) error {
	if err := checkAddress(addr); err != nil {
		return err
	}
	host, port, _ := net.SplitHostPort(addr)
	if host == "" || port == "0" {
		return errors.New("target requires a host and a nonzero port")
	}
	return nil
}
func (c *Client) Validate() error {
	if c.Server == "" {
		return errors.New("server is required")
	}
	if _, _, err := net.SplitHostPort(c.Server); err != nil {
		if strings.Contains(c.Server, ":") && net.ParseIP(c.Server) == nil {
			return fmt.Errorf("invalid server: %w", err)
		}
		c.Server = net.JoinHostPort(strings.Trim(c.Server, "[]"), "443")
	}
	if err := checkTarget(c.Server); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if c.SOCKS5.Listen == "" && len(c.TCPForwarding)+len(c.UDPForwarding)+len(c.ReverseTCP)+len(c.ReverseUDP) == 0 {
		return errors.New("configure socks5 or a forwarding mode")
	}
	if c.SOCKS5.Listen != "" {
		if err := checkAddress(c.SOCKS5.Listen); err != nil {
			return err
		}
	}
	if (c.SOCKS5.Username == "") != (c.SOCKS5.Password == "") {
		return errors.New("socks5 username and password must be supplied together")
	}
	if len(c.SOCKS5.Username) > 255 || len(c.SOCKS5.Password) > 255 {
		return errors.New("SOCKS5 credentials exceed 255 bytes")
	}
	if len(c.ReverseTCP)+len(c.ReverseUDP) > 64 {
		return errors.New("at most 64 reverse listeners")
	}
	forwards := append(append(append(append([]Forward{}, c.TCPForwarding...), c.UDPForwarding...), c.ReverseTCP...), c.ReverseUDP...)
	for _, f := range forwards {
		if err := checkAddress(f.Listen); err != nil {
			return fmt.Errorf("listen %q: %w", f.Listen, err)
		}
		if err := checkTarget(f.Remote); err != nil {
			return fmt.Errorf("remote %q: %w", f.Remote, err)
		}
		if f.Timeout < 0 || f.Timeout > 24*time.Hour {
			return errors.New("forwarding timeout must be in 0..24h")
		}
	}
	_, err := Options(c.Bandwidth, c.Transport, c.Auth)
	return err
}
func (s *Server) Validate() error {
	if s.Listen == "" {
		s.Listen = ":443"
	}
	if err := checkAddress(s.Listen); err != nil {
		return err
	}
	if s.Auth.Type != "" && s.Auth.Type != "password" && s.Auth.Type != "none" {
		return errors.New("auth.type must be password or none")
	}
	if s.Auth.Type == "password" && s.Auth.Password == "" {
		return errors.New("auth.password is required")
	}
	if s.Auth.Type == "none" && s.Auth.Password != "" {
		return errors.New("auth.type none cannot have a password")
	}
	if s.Reverse.MaxListeners == 0 {
		s.Reverse.MaxListeners = 16
	}
	if s.Reverse.MaxListeners < 1 || s.Reverse.MaxListeners > 64 {
		return errors.New("reverse.maxListeners must be in 1..64")
	}
	if s.UDPIdleTimeout == 0 {
		s.UDPIdleTimeout = 60 * time.Second
	}
	if s.UDPIdleTimeout < time.Second {
		return errors.New("udpIdleTimeout must be at least 1s")
	}
	_, err := Options(s.Bandwidth, s.Transport, s.Auth.Password)
	return err
}

func Find(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{".", filepath.Join(home, ".udpbrutal"), "/etc/udpbrutal"} {
		for _, name := range []string{"config.yaml", "config.yml", "config.json"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", errors.New("no config found; use -c config.yaml")
}
func Load(path string, out any) error {
	p, err := Find(path)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err = dec.Decode(out); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return errors.New("config must contain one YAML document")
	}
	return nil
}
