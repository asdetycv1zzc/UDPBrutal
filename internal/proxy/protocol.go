package proxy

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
	"github.com/hashicorp/yamux"
)

type request struct {
	Command    string           `json:"command"`
	Address    string           `json:"address,omitempty"`
	Index      int              `json:"index,omitempty"`
	Reverse    []config.Forward `json:"reverse,omitempty"`
	ReverseUDP []config.Forward `json:"reverseUDP,omitempty"`
}
type response struct {
	OK           bool     `json:"ok"`
	Error        string   `json:"error,omitempty"`
	Addresses    []string `json:"addresses,omitempty"`
	UDPAddresses []string `json:"udpAddresses,omitempty"`
}

func muxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard
	c.ConnectionWriteTimeout = 15 * time.Second
	c.StreamOpenTimeout = 10 * time.Second
	c.StreamCloseTimeout = 5 * time.Minute
	c.MaxStreamWindowSize = 1 << 20
	return c
}
func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
func writeMessage(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > 16384 {
		return errors.New("control message too large")
	}
	h := make([]byte, 4)
	binary.BigEndian.PutUint32(h, uint32(len(b)))
	return writeFull(w, append(h, b...))
}
func readMessage(r io.Reader, v any) error {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > 16384 {
		return errors.New("invalid control message length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func open(session *yamux.Session, req request) (*yamux.Stream, error) {
	s, err := session.OpenStream()
	if err != nil {
		return nil, err
	}
	_ = s.SetDeadline(time.Now().Add(10 * time.Second))
	if err = writeMessage(s, req); err != nil {
		_ = s.Close()
		return nil, err
	}
	var reply response
	if err = readMessage(s, &reply); err != nil {
		_ = s.Close()
		return nil, err
	}
	if !reply.OK {
		_ = s.Close()
		return nil, errors.New(reply.Error)
	}
	_ = s.SetDeadline(time.Time{})
	return s, nil
}
func closeWrite(c net.Conn) {
	if t, ok := c.(interface{ CloseWrite() error }); ok {
		_ = t.CloseWrite()
	} else {
		_ = c.Close()
	}
}

// relay preserves TCP half-close: EOF on a request must not truncate its reply.
func relay(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan error, 2)
	copyOne := func(dst, src net.Conn) { _, err := io.Copy(dst, src); closeWrite(dst); done <- err }
	go copyOne(a, b)
	go copyOne(b, a)
	if err := <-done; err != nil {
		_ = a.SetDeadline(time.Now())
		_ = b.SetDeadline(time.Now())
		_ = a.Close()
		_ = b.Close()
	}
	<-done
}
func writeDatagram(w io.Writer, address string, payload []byte) error {
	if len(address) > 512 || len(payload) > 65507 {
		return errors.New("UDP datagram too large")
	}
	b := make([]byte, 4+len(address)+len(payload))
	binary.BigEndian.PutUint16(b, uint16(len(address)))
	binary.BigEndian.PutUint16(b[2:], uint16(len(payload)))
	copy(b[4:], address)
	copy(b[4+len(address):], payload)
	return writeFull(w, b)
}
func readDatagram(r io.Reader) (string, []byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return "", nil, err
	}
	al, pl := int(binary.BigEndian.Uint16(h[:2])), int(binary.BigEndian.Uint16(h[2:]))
	if al == 0 || al > 512 || pl > 65507 {
		return "", nil, errors.New("invalid UDP datagram length")
	}
	b := make([]byte, al+pl)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", nil, err
	}
	return string(b[:al]), b[al:], nil
}
