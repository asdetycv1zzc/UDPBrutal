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

	"github.com/apernet/hysteria/core/v2/extension"
	"gopkg.in/yaml.v3"
)

type Bandwidth struct {
	Up                      string `yaml:"up"`
	Down                    string `yaml:"down"`
	DisableLossCompensation bool   `yaml:"disableLossCompensation"`
}
type Congestion struct {
	Type       string `yaml:"type"`
	BBRProfile string `yaml:"bbrProfile"`
}
type Transport struct {
	Type string `yaml:"type"`
}
type QUIC struct {
	InitStreamReceiveWindow uint64        `yaml:"initStreamReceiveWindow"`
	MaxStreamReceiveWindow  uint64        `yaml:"maxStreamReceiveWindow"`
	InitConnReceiveWindow   uint64        `yaml:"initConnReceiveWindow"`
	MaxConnReceiveWindow    uint64        `yaml:"maxConnReceiveWindow"`
	MaxIdleTimeout          time.Duration `yaml:"maxIdleTimeout"`
	KeepAlivePeriod         time.Duration `yaml:"keepAlivePeriod"`
	MaxIncomingStreams      int64         `yaml:"maxIncomingStreams"`
	DisablePathMTUDiscovery bool          `yaml:"disablePathMTUDiscovery"`
	DisableGSO              bool          `yaml:"disableGSO"`
	DisableStatelessReset   bool          `yaml:"disableStatelessReset"`
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

// ReverseUDPOptions controls local resources used by the reverse extension.
// The same options are accepted by client and server; they are not wire fields.
type ReverseUDPOptions struct {
	MaxSessions    int `yaml:"maxSessions"`
	QueuePackets   int `yaml:"queuePackets"`
	MaxQueuedBytes int `yaml:"maxQueuedBytes"`
	BatchSize      int `yaml:"batchSize"`
}

func (o *ReverseUDPOptions) FillAndValidate() error {
	if o.MaxSessions == 0 {
		o.MaxSessions = 4096
	}
	if o.QueuePackets == 0 {
		o.QueuePackets = 64
	}
	if o.MaxQueuedBytes == 0 {
		o.MaxQueuedBytes = 16 << 20
	}
	if o.BatchSize == 0 {
		o.BatchSize = 32
	}
	if o.MaxSessions < 1 || o.MaxSessions > 1<<20 {
		return errors.New("reverseUDPOptions.maxSessions must be in 1..1048576")
	}
	if o.QueuePackets < 1 || o.QueuePackets > 65536 {
		return errors.New("reverseUDPOptions.queuePackets must be in 1..65536")
	}
	if o.MaxQueuedBytes < 4096 || o.MaxQueuedBytes > 1<<30 {
		return errors.New("reverseUDPOptions.maxQueuedBytes must be in 4096..1073741824")
	}
	if o.BatchSize < 1 || o.BatchSize > 64 {
		return errors.New("reverseUDPOptions.batchSize must be in 1..64")
	}
	return nil
}

type Client struct {
	Server            string            `yaml:"server"`
	Auth              string            `yaml:"auth"`
	Bandwidth         Bandwidth         `yaml:"bandwidth"`
	Congestion        Congestion        `yaml:"congestion"`
	Transport         Transport         `yaml:"transport"`
	QUIC              QUIC              `yaml:"quic"`
	FastOpen          bool              `yaml:"fastOpen"`
	SOCKS5            SOCKS5            `yaml:"socks5"`
	TCPForwarding     []Forward         `yaml:"tcpForwarding"`
	UDPForwarding     []Forward         `yaml:"udpForwarding"`
	ReverseTCP        []Forward         `yaml:"reverseTCP"`
	ReverseUDP        []Forward         `yaml:"reverseUDP"`
	ReverseUDPOptions ReverseUDPOptions `yaml:"reverseUDPOptions"`
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
	Listen                string            `yaml:"listen"`
	Auth                  Auth              `yaml:"auth"`
	Bandwidth             Bandwidth         `yaml:"bandwidth"`
	Congestion            Congestion        `yaml:"congestion"`
	Transport             Transport         `yaml:"transport"`
	QUIC                  QUIC              `yaml:"quic"`
	Reverse               Reverse           `yaml:"reverse"`
	IgnoreClientBandwidth bool              `yaml:"ignoreClientBandwidth"`
	DisableUDP            bool              `yaml:"disableUDP"`
	UDPIdleTimeout        time.Duration     `yaml:"udpIdleTimeout"`
	ReverseUDPOptions     ReverseUDPOptions `yaml:"reverseUDPOptions"`
}

func ParseBandwidth(s string) (uint64, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	for _, unit := range []struct {
		suffix string
		factor float64
	}{{"tbps", 1e12}, {"gbps", 1e9}, {"mbps", 1e6}, {"kbps", 1e3}, {"bps", 1}} {
		if strings.HasSuffix(s, unit.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(s, unit.suffix), 64)
			bps := n * unit.factor / 8
			if err != nil || math.IsNaN(bps) || math.IsInf(bps, 0) || bps < 1 || bps >= float64(math.MaxInt64) {
				return 0, fmt.Errorf("invalid bandwidth %q", s)
			}
			return uint64(bps), nil
		}
	}
	return 0, fmt.Errorf("bandwidth %q requires bps/kbps/mbps/gbps/tbps", s)
}
func BandwidthBytes(b Bandwidth) (uint64, uint64, error) {
	up, err := ParseBandwidth(b.Up)
	if err != nil {
		return 0, 0, err
	}
	down, err := ParseBandwidth(b.Down)
	return up, down, err
}
func validateCommon(b Bandwidth, t Transport, q QUIC, c Congestion) error {
	if t.Type != "" && t.Type != "udp" {
		return errors.New("transport.type must be udp")
	}
	if _, _, err := BandwidthBytes(b); err != nil {
		return err
	}
	if q.MaxIdleTimeout != 0 && (q.MaxIdleTimeout < 4*time.Second || q.MaxIdleTimeout > 120*time.Second) {
		return errors.New("quic.maxIdleTimeout must be between 4s and 120s")
	}
	if q.KeepAlivePeriod != 0 && (q.KeepAlivePeriod < 2*time.Second || q.KeepAlivePeriod > 60*time.Second) {
		return errors.New("quic.keepAlivePeriod must be between 2s and 60s")
	}
	for _, n := range []uint64{q.InitStreamReceiveWindow, q.MaxStreamReceiveWindow, q.InitConnReceiveWindow, q.MaxConnReceiveWindow} {
		if n != 0 && n < 16384 {
			return errors.New("QUIC receive windows must be at least 16384")
		}
	}
	if q.MaxIncomingStreams != 0 && q.MaxIncomingStreams < 8 {
		return errors.New("quic.maxIncomingStreams must be at least 8")
	}
	return extension.ValidateCongestion(c.Type, c.BBRProfile)
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
	n, _ := strconv.Atoi(port)
	if host == "" || n == 0 {
		return errors.New("target requires a host and nonzero port")
	}
	return nil
}
func (c *Client) Validate() error {
	if err := c.ReverseUDPOptions.FillAndValidate(); err != nil {
		return err
	}
	if c.Server == "" {
		return errors.New("server is required")
	}
	if _, _, err := net.SplitHostPort(c.Server); err != nil {
		if strings.Contains(c.Server, ":") && net.ParseIP(strings.Trim(c.Server, "[]")) == nil {
			return fmt.Errorf("server: %w", err)
		}
		c.Server = net.JoinHostPort(strings.Trim(c.Server, "[]"), "443")
	}
	if err := checkTarget(c.Server); err != nil {
		return err
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
		return errors.New("SOCKS5 username and password must be supplied together")
	}
	if len(c.SOCKS5.Username) > 255 || len(c.SOCKS5.Password) > 255 {
		return errors.New("SOCKS5 credentials exceed 255 bytes")
	}
	if len(c.ReverseTCP)+len(c.ReverseUDP) > 64 {
		return errors.New("at most 64 reverse listeners")
	}
	groups := [][]Forward{c.TCPForwarding, c.UDPForwarding, c.ReverseTCP, c.ReverseUDP}
	for _, group := range groups {
		for _, f := range group {
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
	}
	return validateCommon(c.Bandwidth, c.Transport, c.QUIC, c.Congestion)
}
func (s *Server) Validate() error {
	if err := s.ReverseUDPOptions.FillAndValidate(); err != nil {
		return err
	}
	if s.Listen == "" {
		s.Listen = ":443"
	}
	if err := checkAddress(s.Listen); err != nil {
		return err
	}
	switch s.Auth.Type {
	case "", "password", "none":
	default:
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
	if s.UDPIdleTimeout < 2*time.Second || s.UDPIdleTimeout > 600*time.Second {
		return errors.New("udpIdleTimeout must be between 2s and 600s")
	}
	up, down, err := BandwidthBytes(s.Bandwidth)
	if err != nil {
		return err
	}
	if (up != 0 && up < 65536) || (down != 0 && down < 65536) {
		return errors.New("server bandwidth must be at least 65536 bytes/s when set")
	}
	return validateCommon(s.Bandwidth, s.Transport, s.QUIC, s.Congestion)
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
