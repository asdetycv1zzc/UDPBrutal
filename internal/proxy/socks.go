// SOCKS5 negotiation and CONNECT/UDP ASSOCIATE dispatch follow Hysteria2's
// app/internal/socks5/server.go; the outbound is the plaintext tunnel.
package proxy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"time"
)

func (c *Client) serveSOCKS(l net.Listener) {
	slots := make(chan struct{}, 256)
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			go func() { defer func() { <-slots }(); c.handleSOCKS(conn) }()
		default:
			_ = conn.Close()
		}
	}
}
func readN(r io.Reader, n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}
func readSOCKSAddress(r io.Reader, kind byte) (string, error) {
	var host string
	switch kind {
	case 1:
		b, err := readN(r, 4)
		if err != nil {
			return "", err
		}
		host = net.IP(b).String()
	case 4:
		b, err := readN(r, 16)
		if err != nil {
			return "", err
		}
		host = net.IP(b).String()
	case 3:
		n, err := readN(r, 1)
		if err != nil {
			return "", err
		}
		if n[0] == 0 {
			return "", errors.New("empty SOCKS5 domain")
		}
		b, err := readN(r, int(n[0]))
		if err != nil {
			return "", err
		}
		host = string(b)
	default:
		return "", errors.New("unsupported SOCKS5 address")
	}
	port, err := readN(r, 2)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))), nil
}
func socksAddress(address string) ([]byte, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return nil, errors.New("invalid port")
	}
	var b []byte
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			b = append([]byte{1}, v4...)
		} else {
			b = append([]byte{4}, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, errors.New("invalid hostname")
		}
		b = append([]byte{3, byte(len(host))}, []byte(host)...)
	}
	return append(b, byte(n>>8), byte(n)), nil
}
func socksReply(w io.Writer, status byte, address string) error {
	b, err := socksAddress(address)
	if err != nil {
		return err
	}
	return writeFull(w, append([]byte{5, status, 0}, b...))
}
func (c *Client) negotiateSOCKS(conn net.Conn) error {
	h, err := readN(conn, 2)
	if err != nil {
		return err
	}
	if h[0] != 5 || h[1] == 0 {
		return errors.New("invalid SOCKS5 negotiation")
	}
	methods, err := readN(conn, int(h[1]))
	if err != nil {
		return err
	}
	method := byte(0)
	if c.cfg.SOCKS5.Username != "" {
		method = 2
	}
	if !bytes.Contains(methods, []byte{method}) {
		_ = writeFull(conn, []byte{5, 255})
		return errors.New("no supported SOCKS5 method")
	}
	if err = writeFull(conn, []byte{5, method}); err != nil {
		return err
	}
	if method == 2 {
		h, err = readN(conn, 2)
		if err != nil {
			return err
		}
		if h[0] != 1 || h[1] == 0 {
			return errors.New("invalid SOCKS5 auth")
		}
		user, err := readN(conn, int(h[1]))
		if err != nil {
			return err
		}
		size, err := readN(conn, 1)
		if err != nil {
			return err
		}
		password, err := readN(conn, int(size[0]))
		if err != nil {
			return err
		}
		if string(user) != c.cfg.SOCKS5.Username || string(password) != c.cfg.SOCKS5.Password {
			_ = writeFull(conn, []byte{1, 1})
			return errors.New("SOCKS5 auth failed")
		}
		return writeFull(conn, []byte{1, 0})
	}
	return nil
}
func (c *Client) handleSOCKS(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if c.negotiateSOCKS(conn) != nil {
		return
	}
	h, err := readN(conn, 4)
	if err != nil || h[0] != 5 || h[2] != 0 {
		return
	}
	address, err := readSOCKSAddress(conn, h[3])
	if err != nil {
		_ = socksReply(conn, 8, "0.0.0.0:0")
		return
	}
	switch h[1] {
	case 1:
		stream, err := c.DialTCP(address)
		if err != nil {
			_ = socksReply(conn, 4, "0.0.0.0:0")
			return
		}
		defer stream.Close()
		if socksReply(conn, 0, "0.0.0.0:0") != nil {
			return
		}
		_ = conn.SetDeadline(time.Time{})
		relay(conn, stream)
	case 3:
		if c.cfg.SOCKS5.DisableUDP {
			_ = socksReply(conn, 7, "0.0.0.0:0")
			return
		}
		c.socksUDP(conn)
	default:
		_ = socksReply(conn, 7, "0.0.0.0:0")
	}
}
func (c *Client) socksUDP(conn net.Conn) {
	host, _, _ := net.SplitHostPort(conn.LocalAddr().String())
	bind, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, "0"))
	if err != nil {
		return
	}
	udp, err := net.ListenUDP("udp", bind)
	if err != nil {
		_ = socksReply(conn, 1, "0.0.0.0:0")
		return
	}
	defer udp.Close()
	stream, err := c.open(request{Command: "udp"})
	if err != nil {
		_ = socksReply(conn, 1, "0.0.0.0:0")
		return
	}
	defer stream.Close()
	if socksReply(conn, 0, udp.LocalAddr().String()) != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(io.Discard, conn); done <- struct{}{} }()
	go func() { c.socksUDPLoop(conn, udp, stream); done <- struct{}{} }()
	<-done
	_ = conn.Close()
	_ = udp.Close()
	_ = stream.SetDeadline(time.Now())
	_ = stream.Close()
	<-done
}
func (c *Client) socksUDPLoop(control net.Conn, udp *net.UDPConn, stream net.Conn) {
	host, _, _ := net.SplitHostPort(control.RemoteAddr().String())
	peerIP := net.ParseIP(host)
	var peer *net.UDPAddr
	var repliesDone chan struct{}
	defer func() {
		_ = udp.Close()
		_ = stream.SetDeadline(time.Now())
		if repliesDone != nil {
			<-repliesDone
		}
	}()
	b := make([]byte, 65535)
	for {
		_ = udp.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, from, err := udp.ReadFromUDP(b)
		if err != nil {
			return
		}
		if !peerIP.Equal(from.IP) || (peer != nil && (peer.Port != from.Port || !peer.IP.Equal(from.IP))) {
			continue
		}
		if n < 4 || b[0] != 0 || b[1] != 0 || b[2] != 0 {
			continue
		} // SOCKS fragmentation is unsupported, like Hysteria2.
		reader := bytes.NewReader(b[4:n])
		address, err := readSOCKSAddress(reader, b[3])
		if err != nil {
			continue
		}
		payload := b[n-reader.Len() : n]
		if peer == nil {
			peer = from
			repliesDone = make(chan struct{})
			go func() {
				defer close(repliesDone)
				defer udp.Close()
				for {
					_ = stream.SetReadDeadline(time.Now().Add(60 * time.Second))
					address, payload, err := readDatagram(stream)
					if err != nil {
						return
					}
					header, err := socksAddress(address)
					if err != nil {
						continue
					}
					packet := append(append([]byte{0, 0, 0}, header...), payload...)
					if _, err = udp.WriteToUDP(packet, peer); err != nil {
						return
					}
				}
			}()
		}
		_ = stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err = writeDatagram(stream, address, payload); err != nil {
			return
		}
	}
}
