//go:build linux

package quic

import (
	"errors"
	"net"

	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"

	"github.com/apernet/quic-go/internal/protocol"
)

type batchPacketConn interface {
	WriteBatch([]ipv4.Message, int) (int, error)
}

type linuxPacketBatchWriter struct {
	conn     *sconn
	raw      *oobConn
	batch    batchPacketConn
	messages [sendQueueCapacity]ipv4.Message
	buffers  [sendQueueCapacity][1][]byte
	oob      [sendQueueCapacity][oobBufferSize]byte
}

func newPacketBatchWriter(conn sendConn) packetBatchWriter {
	c, ok := conn.(*sconn)
	if !ok {
		return nil
	}
	raw, ok := c.rawConn.(*oobConn)
	if !ok {
		return nil
	}
	bc, ok := raw.batchConn.(batchPacketConn)
	if !ok {
		return nil
	}
	w := &linuxPacketBatchWriter{conn: c, raw: raw, batch: bc}
	for i := range w.messages {
		w.messages[i].Buffers = w.buffers[i][:]
		w.messages[i].OOB = w.oob[i][:0]
	}
	return w
}

func (w *linuxPacketBatchWriter) Write(entries []queueEntry) (int, error) {
	ai := w.conn.remoteAddrInfo.Load()
	addr := ai.addr.(*net.UDPAddr)
	for i, e := range entries {
		m := &w.messages[i]
		m.Buffers[0] = e.buf.Data
		m.Addr = addr
		// Each message owns its control-message storage. Reusing ai.oob's
		// spare capacity for ECN would overwrite earlier batch entries.
		m.OOB = append(m.OOB[:0], ai.oob...)
		if e.ecn != protocol.ECNUnsupported {
			if !w.raw.capabilities().ECN {
				panic("tried to send an ECN-marked packet although ECN is disabled")
			}
			if addr.IP.To4() != nil {
				m.OOB = appendIPv4ECNMsg(m.OOB, e.ecn)
			} else {
				m.OOB = appendIPv6ECNMsg(m.OOB, e.ecn)
			}
		}
	}
	n, err := w.batch.WriteBatch(w.messages[:len(entries)], 0)
	for i := range entries {
		w.messages[i].Buffers[0] = nil
		w.messages[i].Addr = nil
	}
	// x/net can expose the syscall's -1 result on Linux errors. No
	// datagrams were accepted in that case; callers need a prefix length.
	if n < 0 {
		n = 0
	}
	if n > 0 {
		w.conn.wroteFirstPacket = true
	}
	return n, err
}

func (*linuxPacketBatchWriter) Unsupported(err error) bool {
	return errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES)
}
