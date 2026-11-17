package transport

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/asdetycv1zzc/UDPBrutal/internal/congestion"
	"github.com/asdetycv1zzc/UDPBrutal/internal/congestion/brutal"
	"github.com/asdetycv1zzc/UDPBrutal/internal/monotime"
)

type rttStats struct{ smoothed, variance time.Duration }

func (r *rttStats) SmoothedRTT() time.Duration { return r.smoothed }
func (r *rttStats) observe(d time.Duration) {
	if r.smoothed == 0 {
		r.smoothed = d
		r.variance = d / 2
		return
	}
	delta := r.smoothed - d
	if delta < 0 {
		delta = -delta
	}
	r.variance = (3*r.variance + delta) / 4
	r.smoothed = (7*r.smoothed + d) / 8
}
func (r *rttStats) timeout() time.Duration {
	if r.smoothed == 0 {
		return 200 * time.Millisecond
	}
	return min(2*time.Second, max(80*time.Millisecond, r.smoothed+4*r.variance))
}

type pending struct {
	payload  []byte
	number   uint64
	sent     monotime.Time
	attempts int
}

// Conn is a flow-controlled, bounded net.Conn. All congestion state is protected
// by mu; the imported Brutal controller itself needs no additional locks.
type Conn struct {
	mu                                          sync.Mutex
	writeMu                                     sync.Mutex
	session                                     uint64
	local, remote                               net.Addr
	writePacket                                 func([]byte) error
	cleanup                                     func()
	cc                                          *brutal.BrutalSender
	rtt                                         rttStats
	outbound                                    map[uint64]*pending
	inbound                                     map[uint64][]byte
	nextWrite, nextRead, peerNext, packetNumber uint64
	readOffset                                  int
	inFlight                                    congestion.ByteCount
	lastRecv, lastPing                          monotime.Time
	idle                                        time.Duration
	readDeadline, writeDeadline                 time.Time
	changed                                     chan struct{}
	wake                                        chan struct{}
	done                                        chan struct{}
	err                                         error
}

var _ net.Conn = (*Conn)(nil)

func newConn(id uint64, local, remote net.Addr, write func([]byte) error, options Options, cleanup func()) *Conn {
	c := &Conn{session: id, local: local, remote: remote, writePacket: write, cleanup: cleanup,
		outbound: make(map[uint64]*pending), inbound: make(map[uint64][]byte),
		nextWrite: 1, nextRead: 1, peerNext: 1, lastRecv: monotime.Now(), idle: options.IdleTimeout,
		changed: make(chan struct{}), wake: make(chan struct{}, 1), done: make(chan struct{})}
	c.cc = brutal.NewBrutalSender(options.SendBPS, options.DisableLossCompensation)
	c.cc.SetRTTStatsProvider(&c.rtt)
	c.cc.SetMaxDatagramSize(mtu)
	go c.run()
	return c
}

func (c *Conn) notify() {
	close(c.changed)
	c.changed = make(chan struct{})
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
func (c *Conn) control(kind byte, seq, number uint64) error {
	return c.writePacket(encode(packet{kind: kind, session: c.session, sequence: seq, number: number, next: c.nextRead}))
}

func (c *Conn) input(p packet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	now := monotime.Now()
	c.lastRecv = now
	if p.next > c.peerNext && p.next <= c.nextWrite {
		c.peerNext = p.next
		c.notify()
	}
	switch p.kind {
	case data:
		if p.sequence == 0 || len(p.payload) == 0 {
			return
		}
		if p.sequence >= c.nextRead+window {
			return
		} // Receiver backpressure: do not ACK dropped data.
		if p.sequence >= c.nextRead {
			if _, ok := c.inbound[p.sequence]; !ok {
				c.inbound[p.sequence] = append([]byte(nil), p.payload...)
				c.notify()
			}
		}
		_ = c.control(ack, p.sequence, p.number)
	case ack:
		if q, ok := c.outbound[p.sequence]; ok && q.number == p.number && !q.sent.IsZero() {
			prior := c.inFlight
			c.inFlight -= congestion.ByteCount(headerSize + len(q.payload))
			if q.attempts == 1 {
				c.rtt.observe(now.Sub(q.sent))
			} // Karn's algorithm.
			delete(c.outbound, p.sequence)
			c.cc.OnCongestionEventEx(prior, now, []congestion.AckedPacketInfo{{PacketNumber: congestion.PacketNumber(p.number)}}, nil)
			c.notify()
		}
	case ping:
		_ = c.control(pong, 0, 0)
	case goodbye:
		go c.fail(io.EOF)
	}
}

func (c *Conn) run() {
	timer := time.NewTicker(time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-timer.C:
		case <-c.wake:
		}
		c.mu.Lock()
		if c.err != nil {
			c.mu.Unlock()
			return
		}
		now := monotime.Now()
		if now.Sub(c.lastRecv) > c.idle {
			c.mu.Unlock()
			c.fail(errors.New("UDP session idle timeout"))
			return
		}
		if now.Sub(c.lastPing) >= 2*time.Second {
			_ = c.control(ping, 0, 0)
			c.lastPing = now
		}
		// Iterate in sequence order: retransmissions and new bytes share the pacer.
		start := c.nextWrite
		for seq := range c.outbound {
			start = min(start, seq)
		}
		var sendErr error
		for seq := start; seq < c.nextWrite; seq++ {
			q := c.outbound[seq]
			if q == nil {
				continue
			}
			if q.attempts > 0 && !q.sent.IsZero() {
				timeout := c.rtt.timeout() * time.Duration(1<<min(q.attempts-1, 4))
				if now.Sub(q.sent) < min(timeout, 2*time.Second) {
					continue
				}
				prior := c.inFlight
				c.inFlight -= congestion.ByteCount(headerSize + len(q.payload))
				c.cc.OnCongestionEventEx(prior, now, nil, []congestion.LostPacketInfo{{PacketNumber: congestion.PacketNumber(q.number)}})
				q.sent = 0
			}
			if q.attempts >= 30 {
				sendErr = errors.New("UDP retransmission limit exceeded")
				break
			}
			if seq >= c.peerNext+window || !c.cc.CanSend(c.inFlight) || !c.cc.HasPacingBudget(now) {
				continue
			}
			c.packetNumber++
			q.number = c.packetNumber
			b := encode(packet{kind: data, session: c.session, sequence: seq, number: q.number, next: c.nextRead, payload: q.payload})
			if err := c.writePacket(b); err != nil {
				sendErr = err
				break
			}
			q.attempts++
			q.sent = now
			size := congestion.ByteCount(len(b))
			c.cc.OnPacketSent(now, c.inFlight, congestion.PacketNumber(q.number), size, true)
			c.inFlight += size
		}
		c.mu.Unlock()
		if sendErr != nil {
			c.fail(sendErr)
			return
		}
	}
}

func waitChange(ch, done <-chan struct{}, deadline time.Time) error {
	if deadline.IsZero() {
		select {
		case <-ch:
			return nil
		case <-done:
			return net.ErrClosed
		}
	}
	delay := time.Until(deadline)
	if delay <= 0 {
		return os.ErrDeadlineExceeded
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ch:
		return nil
	case <-done:
		return net.ErrClosed
	case <-timer.C:
		return os.ErrDeadlineExceeded
	}
}

func (c *Conn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	for {
		c.mu.Lock()
		if !c.readDeadline.IsZero() && !time.Now().Before(c.readDeadline) {
			c.mu.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		if p, ok := c.inbound[c.nextRead]; ok {
			n := copy(b, p[c.readOffset:])
			c.readOffset += n
			if c.readOffset == len(p) {
				delete(c.inbound, c.nextRead)
				c.nextRead++
				c.readOffset = 0
				_ = c.control(ack, 0, 0)
			}
			c.mu.Unlock()
			return n, nil
		}
		if c.err != nil {
			err := c.err
			c.mu.Unlock()
			return 0, err
		}
		ch, deadline := c.changed, c.readDeadline
		c.mu.Unlock()
		if err := waitChange(ch, c.done, deadline); err != nil {
			return 0, err
		}
	}
}

func (c *Conn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	total := 0
	for len(b) > 0 {
		c.mu.Lock()
		if c.err != nil {
			err := c.err
			c.mu.Unlock()
			return total, err
		}
		if !c.writeDeadline.IsZero() && !time.Now().Before(c.writeDeadline) {
			c.mu.Unlock()
			return total, os.ErrDeadlineExceeded
		}
		if len(c.outbound) >= window || c.nextWrite >= c.peerNext+window {
			ch, deadline := c.changed, c.writeDeadline
			c.mu.Unlock()
			if err := waitChange(ch, c.done, deadline); err != nil {
				return total, err
			}
			continue
		}
		n := min(len(b), payloadSize)
		c.outbound[c.nextWrite] = &pending{payload: append([]byte(nil), b[:n]...)}
		c.nextWrite++
		total += n
		b = b[n:]
		c.notify()
		c.mu.Unlock()
	}
	return total, nil
}

func (c *Conn) fail(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	close(c.done)
	c.notify()
	cleanup := c.cleanup
	c.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.err == nil {
		_ = c.control(goodbye, 0, 0)
	}
	c.mu.Unlock()
	c.fail(net.ErrClosed)
	return nil
}
func (c *Conn) LocalAddr() net.Addr  { return c.local }
func (c *Conn) RemoteAddr() net.Addr { return c.remote }
func (c *Conn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.writeDeadline = t
	c.notify()
	c.mu.Unlock()
	return nil
}
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.notify()
	c.mu.Unlock()
	return nil
}
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.notify()
	c.mu.Unlock()
	return nil
}
