//go:build linux

package proxy

import (
	"errors"
	"io"
	"net"
	"net/netip"

	"github.com/apernet/hysteria/core/v2/extension"
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

// x/net's Message and batch socket APIs also handle dual-stack UDP sockets.
// The kernel returns currently available packets; no full-batch delay is added.
type batchUDPReader struct {
	conn          *ipv4.PacketConn
	fallback      singleUDPReader
	messages      []ipv4.Message
	packets       []udpReadPacket
	batchDisabled bool
}

func newUDPReader(conn *net.UDPConn, size int) udpReader {
	if size == 1 {
		return &singleUDPReader{conn: conn}
	}
	r := &batchUDPReader{conn: ipv4.NewPacketConn(conn), fallback: singleUDPReader{conn: conn}, messages: make([]ipv4.Message, size), packets: make([]udpReadPacket, size)}
	for i := range r.messages {
		r.messages[i].Buffers = [][]byte{make([]byte, extension.MaxUDPSize+1)}
	}
	return r
}
func batchUnsupported(err error) bool {
	return errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP)
}
func (r *batchUDPReader) read() ([]udpReadPacket, error) {
	if r.batchDisabled {
		return r.fallback.read()
	}
	n, err := r.conn.ReadBatch(r.messages, 0)
	if n == 0 && batchUnsupported(err) {
		r.batchDisabled = true
		return r.fallback.read()
	}
	out := r.packets[:0]
	for i := 0; i < n; i++ {
		m := &r.messages[i]
		addr, ok := m.Addr.(*net.UDPAddr)
		if !ok || m.N > extension.MaxUDPSize || m.Flags&unix.MSG_TRUNC != 0 {
			continue
		}
		source := addr.AddrPort()
		source = netip.AddrPortFrom(source.Addr().Unmap(), source.Port())
		out = append(out, udpReadPacket{data: m.Buffers[0][:m.N], source: source})
	}
	return out, err
}

type batchUDPWriter struct {
	conn          *ipv4.PacketConn
	fallback      singleUDPWriter
	messages      []ipv4.Message
	batchDisabled bool
}

func newUDPWriter(conn *net.UDPConn, size int) udpWriter {
	if size == 1 {
		return &singleUDPWriter{conn: conn}
	}
	w := &batchUDPWriter{conn: ipv4.NewPacketConn(conn), fallback: singleUDPWriter{conn: conn}, messages: make([]ipv4.Message, size)}
	for i := range w.messages {
		w.messages[i].Buffers = make([][]byte, 1)
	}
	return w
}
func (w *batchUDPWriter) write(packets []udpWritePacket) (int, error) {
	if w.batchDisabled || len(packets) == 1 {
		return w.fallback.write(packets)
	}
	// Empty UDP payloads are valid; use the ordinary socket API for batches
	// containing them, which does not require a nonempty scatter/gather buffer.
	for _, p := range packets {
		if len(p.data) == 0 {
			return w.fallback.write(packets)
		}
	}
	written := 0
	for written < len(packets) {
		size := min(len(w.messages), len(packets)-written)
		for i := 0; i < size; i++ {
			p := packets[written+i]
			w.messages[i].Buffers[0] = p.data
			// Converting a nil *net.UDPAddr to net.Addr yields a non-nil
			// interface. Connected sockets must receive an actual nil here.
			w.messages[i].Addr = nil
			if p.address != nil {
				w.messages[i].Addr = p.address
			}
		}
		n, err := w.conn.WriteBatch(w.messages[:size], 0)
		for i := 0; i < size; i++ {
			w.messages[i].Buffers[0] = nil
			w.messages[i].Addr = nil
		}
		written += n
		if batchUnsupported(err) {
			w.batchDisabled = true
			more, e := w.fallback.write(packets[written:])
			return written + more, e
		}
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrNoProgress
		}
	}
	return written, nil
}
