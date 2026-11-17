package proxy

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/quicvarint"
	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
)

const reverseControlType uint64 = 0x554201
const reverseTCPType uint64 = 0x554202

type registration struct {
	TCP []config.Forward `json:"tcp,omitempty"`
	UDP []config.Forward `json:"udp,omitempty"`
}
type registrationReply struct {
	OK    bool     `json:"ok"`
	Error string   `json:"error,omitempty"`
	TCP   []string `json:"tcp,omitempty"`
	UDP   []string `json:"udp,omitempty"`
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
		return errors.New("reverse control message too large")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(b)))
	return writeFull(w, append(header[:], b...))
}
func readMessage(r io.Reader, v any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > 16384 {
		return errors.New("invalid reverse control message length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

type streamConn struct {
	stream *quic.Stream
	conn   *quic.Conn
}

func (c *streamConn) Read(b []byte) (int, error)         { return c.stream.Read(b) }
func (c *streamConn) Write(b []byte) (int, error)        { return c.stream.Write(b) }
func (c *streamConn) Close() error                       { c.stream.CancelRead(0); return c.stream.Close() }
func (c *streamConn) CloseWrite() error                  { return c.stream.Close() }
func (c *streamConn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *streamConn) RemoteAddr() net.Addr               { return c.conn.RemoteAddr() }
func (c *streamConn) SetDeadline(t time.Time) error      { return c.stream.SetDeadline(t) }
func (c *streamConn) SetReadDeadline(t time.Time) error  { return c.stream.SetReadDeadline(t) }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return c.stream.SetWriteDeadline(t) }
func closeWrite(c net.Conn) {
	if x, ok := c.(interface{ CloseWrite() error }); ok {
		_ = x.CloseWrite()
	} else {
		_ = c.Close()
	}
}
func relay(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	observer, detach := observeRelay(a, b)
	defer detach()
	done := make(chan error, 2)
	copyOne := func(dst, src net.Conn) {
		var err error
		if observer == nil {
			_, err = io.Copy(dst, src)
		} else {
			direction := &observer.toLocal
			if _, ok := dst.(*streamConn); ok {
				direction = &observer.toTunnel
			}
			_, err = io.Copy(diagnosticWriter{Writer: dst, direction: direction}, diagnosticReader{Reader: src, direction: direction})
			direction.stage(diagnosticDone)
		}
		closeWrite(dst)
		done <- err
	}
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
func writeFrameType(w io.Writer, kind uint64) error {
	return writeFull(w, quicvarint.Append(nil, kind))
}
func readFrameType(r io.Reader) (uint64, error) { return quicvarint.Read(quicvarint.NewReader(r)) }
func udpTimeout(t time.Duration) time.Duration {
	if t <= 0 {
		return 60 * time.Second
	}
	return t
}
