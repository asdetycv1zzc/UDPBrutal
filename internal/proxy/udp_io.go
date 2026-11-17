package proxy

import (
	"context"
	"net"
	"net/netip"

	"github.com/apernet/hysteria/core/v2/extension"
)

type udpReadPacket struct {
	data   []byte
	source netip.AddrPort
}
type udpWritePacket struct {
	data    []byte
	address *net.UDPAddr // nil for a connected client target socket
}
type udpReader interface {
	read() ([]udpReadPacket, error)
}
type udpWriter interface {
	write([]udpWritePacket) (int, error)
}

type singleUDPReader struct {
	conn   *net.UDPConn
	buffer [extension.MaxUDPSize + 1]byte
	packet [1]udpReadPacket
}

func (r *singleUDPReader) read() ([]udpReadPacket, error) {
	n, source, err := r.conn.ReadFromUDPAddrPort(r.buffer[:])
	if err != nil {
		return nil, err
	}
	if n > extension.MaxUDPSize {
		return r.packet[:0], nil
	}
	source = netip.AddrPortFrom(source.Addr().Unmap(), source.Port())
	r.packet[0] = udpReadPacket{data: r.buffer[:n], source: source}
	return r.packet[:], nil
}

type singleUDPWriter struct{ conn *net.UDPConn }

func (w *singleUDPWriter) write(packets []udpWritePacket) (int, error) {
	for i, p := range packets {
		var err error
		if p.address == nil {
			_, err = w.conn.Write(p.data)
		} else {
			_, err = w.conn.WriteToUDP(p.data, p.address)
		}
		if err != nil {
			return i, err
		}
	}
	return len(packets), nil
}

// Wait for the first packet, then take only packets already available. Batching
// never waits for a full batch and preserves ordering within each queue.
func receiveUDPBatch(ctx context.Context, q *udpQueue, batchSize int, consume func(udpQueuedPacket)) bool {
	var first udpQueuedPacket
	select {
	case <-ctx.Done():
		return false
	case p, ok := <-q.packets:
		if !ok {
			return false
		}
		first = p
	}
	q.release(first)
	consume(first)
	for i := 1; i < batchSize; i++ {
		select {
		case p, ok := <-q.packets:
			if !ok {
				return true
			}
			q.release(p)
			consume(p)
		default:
			return true
		}
	}
	return true
}
