package quic

import (
	"sync/atomic"
	"time"

	"github.com/apernet/quic-go/internal/ackhandler"
	"github.com/apernet/quic-go/internal/monotime"
)

// These optional observers never schedule sends, wake the connection loop,
// extend a deadline or change congestion/flow-control decisions.
type connectionDiagnostics struct {
	lastCapture  monotime.Time // owner: connection run loop
	lastAck      monotime.Time // owner: connection run loop
	loop         atomic.Pointer[connectionDiagnosticSample]
	socket       socketWriteDiagnostics
	rxQueueDrops atomic.Uint64
}
type connectionDiagnosticSample struct {
	at             monotime.Time
	lastAck        monotime.Time
	lastPacket     monotime.Time
	lossDeadline   monotime.Time
	pacingDeadline monotime.Time
	recovery       ackhandler.RecoveryDiagnosticState
	queueLen       int
	gso            bool
}
type socketWriteDiagnostics struct {
	started      atomic.Int64
	completed    atomic.Int64
	lastDuration atomic.Int64
	calls        atomic.Uint64
	bytes        atomic.Uint64
	errors       atomic.Uint64
}

type DiagnosticSnapshot struct {
	Ready                   bool
	LoopAge                 time.Duration
	AckAge                  time.Duration
	PacketAge               time.Duration
	LossTimerRemaining      time.Duration
	LossTimerSet            bool
	PacingTimerRemaining    time.Duration
	PacingTimerSet          bool
	BytesInFlight           int64
	CongestionWindow        int64
	PTOCount                uint32
	TrackedPackets          int
	SendMode                string
	SendQueueLen            int
	ReceiveQueueDrops       uint64
	GSO                     bool
	SocketWritePending      time.Duration
	SocketWriteLastDuration time.Duration
	SocketWriteAge          time.Duration
	SocketWriteCalls        uint64
	SocketWriteBytes        uint64
	SocketWriteErrors       uint64
}

// EnableDiagnostics enables passive 50 ms owner-loop snapshots. Nothing wakes
// the loop to take a sample: stale samples are part of the diagnostic evidence.
func (c *Conn) EnableDiagnostics() {
	if c.diagnostics.Load() == nil {
		c.diagnostics.CompareAndSwap(nil, &connectionDiagnostics{})
	}
}
func (c *Conn) captureDiagnostics() {
	d := c.diagnostics.Load()
	if d == nil {
		return
	}
	now := monotime.Now()
	if !d.lastCapture.IsZero() && now.Sub(d.lastCapture) < 50*time.Millisecond {
		return
	}
	d.lastCapture = now
	provider, ok := c.sentPacketHandler.(interface {
		DiagnosticState(monotime.Time) ackhandler.RecoveryDiagnosticState
	})
	if !ok {
		return
	}
	queueLen := 0
	if q, ok := c.sendQueue.(*sendQueue); ok {
		q.diagnostics.Store(&d.socket)
		queueLen = len(q.queue)
	}
	d.loop.Store(&connectionDiagnosticSample{
		at: now, lastAck: d.lastAck, lastPacket: c.lastPacketReceivedTime,
		lossDeadline: c.sentPacketHandler.GetLossDetectionTimeout(), pacingDeadline: c.pacingDeadline,
		recovery: provider.DiagnosticState(now), queueLen: queueLen, gso: c.conn.capabilities().GSO,
	})
}
func (c *Conn) noteDiagnosticAck(t monotime.Time) {
	if d := c.diagnostics.Load(); d != nil {
		d.lastAck = t
	}
}
func (c *Conn) noteDiagnosticQueueDrop() {
	if d := c.diagnostics.Load(); d != nil {
		d.rxQueueDrops.Add(1)
	}
}

// DiagnosticSnapshot can be called concurrently. Recovery fields are a passive
// owner-loop sample; LoopAge must be considered before interpreting them.
func (c *Conn) DiagnosticSnapshot() DiagnosticSnapshot {
	d := c.diagnostics.Load()
	if d == nil {
		return DiagnosticSnapshot{}
	}
	sample := d.loop.Load()
	started := monotime.Time(d.socket.started.Load())
	completed := monotime.Time(d.socket.completed.Load())
	now := monotime.Now()
	out := DiagnosticSnapshot{AckAge: -time.Millisecond, PacketAge: -time.Millisecond, SocketWriteAge: -time.Millisecond,
		ReceiveQueueDrops: d.rxQueueDrops.Load(),
		SocketWriteCalls:  d.socket.calls.Load(), SocketWriteBytes: d.socket.bytes.Load(), SocketWriteErrors: d.socket.errors.Load(),
		SocketWriteLastDuration: time.Duration(d.socket.lastDuration.Load()),
	}
	if !started.IsZero() {
		out.SocketWritePending = now.Sub(started)
	}
	if !completed.IsZero() {
		out.SocketWriteAge = now.Sub(completed)
	}
	if s := sample; s != nil {
		out.Ready = true
		out.LoopAge = now.Sub(s.at)
		if !s.lastAck.IsZero() {
			out.AckAge = now.Sub(s.lastAck)
		}
		if !s.lastPacket.IsZero() {
			out.PacketAge = now.Sub(s.lastPacket)
		}
		out.LossTimerSet = !s.lossDeadline.IsZero()
		if out.LossTimerSet {
			out.LossTimerRemaining = s.lossDeadline.Sub(now)
		}
		out.PacingTimerSet = !s.pacingDeadline.IsZero() && s.pacingDeadline != deadlineSendImmediately
		if out.PacingTimerSet {
			out.PacingTimerRemaining = s.pacingDeadline.Sub(now)
		}
		out.BytesInFlight = s.recovery.BytesInFlight
		out.CongestionWindow = s.recovery.CongestionWindow
		out.PTOCount = s.recovery.PTOCount
		out.TrackedPackets = s.recovery.TrackedPackets
		out.SendMode = s.recovery.SendMode
		out.SendQueueLen = s.queueLen
		out.GSO = s.gso
	}
	return out
}

type StreamDiagnosticSnapshot struct {
	SendReady             bool
	ReceiveReady          bool
	ConnectionCreditReady bool
	SendOffset            int64
	BufferedBytes         int
	RetransmissionFrames  int
	StreamCredit          int64
	ConnectionCredit      int64
	ReceiveOffset         int64
	ReceiveHighest        int64
}

// StreamDiagnosticSnapshot uses nonblocking locks so the reporter never waits
// behind the IO path. A false Ready flag means unavailable, not zero credit.
func (s *Stream) StreamDiagnosticSnapshot() StreamDiagnosticSnapshot {
	var out StreamDiagnosticSnapshot
	if w := s.sendStr; w.mutex.TryLock() {
		out.SendReady = true
		out.SendOffset = int64(w.writeOffset)
		out.BufferedBytes = len(w.dataForWriting)
		if w.nextFrame != nil {
			out.BufferedBytes += len(w.nextFrame.Data)
		}
		out.RetransmissionFrames = len(w.retransmissionQueue)
		out.StreamCredit = int64(w.flowController.sendWindow - w.flowController.bytesSent)
		fc := w.flowController.connection
		if fc.sendMutex.TryLock() {
			out.ConnectionCreditReady = true
			out.ConnectionCredit = int64(fc.sendWindow - fc.bytesSent)
			fc.sendMutex.Unlock()
		}
		w.mutex.Unlock()
	}
	if r := s.receiveStr; r.mutex.TryLock() {
		out.ReceiveOffset = int64(r.readPos)
		if r.flowController.mutex.TryLock() {
			out.ReceiveReady = true
			out.ReceiveHighest = int64(r.flowController.highestReceived)
			r.flowController.mutex.Unlock()
		}
		r.mutex.Unlock()
	}
	return out
}
