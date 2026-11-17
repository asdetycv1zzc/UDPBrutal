package quic

import (
	"sync/atomic"
	"time"

	"github.com/apernet/quic-go/internal/monotime"
)

// SocketReceiveDiagnosticSnapshot describes the initial Transport's shared UDP
// socket, before connection-ID dispatch. It includes all peers using that socket.
// Counters are live atomics, independent of the connection loop's sample age.
type SocketReceiveDiagnosticSnapshot struct {
	Ready                                                               bool
	Packets, Bytes, DispatchedPackets                                   uint64
	UnmatchedCID, HeaderDrops, NonQUICPackets, EmptyPackets, ReadErrors uint64
	PacketAge                                                           time.Duration
	Stage                                                               string
	StageWait                                                           time.Duration
	KernelStatus                                                        string
	KernelAge                                                           time.Duration
	KernelDrops, KernelReceiveQueue, KernelSendQueue                    uint64
	KernelReadBuffer, KernelWriteBuffer                                 int
}

type kernelSocketSnapshot struct {
	status                         string
	age                            time.Duration
	drops, receiveQueue, sendQueue uint64
	readBuffer, writeBuffer        int
}

type socketReceiveDiagnostics struct {
	packets, bytes, dispatched                                          atomic.Uint64
	unmatchedCID, headerDrops, nonQUICPackets, emptyPackets, readErrors atomic.Uint64
	lastPacket                                                          atomic.Int64
	// Combine stage and its monotonic start in one word. Only the socket
	// receive goroutine writes it; readers never take a receive-path lock.
	stage  atomic.Uint64
	kernel func() kernelSocketSnapshot
}

func (c *Conn) attachDiagnosticTransport(runner connRunner) {
	if r, ok := runner.(*packetHandlerMap); ok {
		c.diagnosticTransport = (*Transport)(r)
	}
}

func (t *Transport) enableSocketDiagnostics() *socketReceiveDiagnostics {
	if d := t.receiveDiagnostics.Load(); d != nil {
		return d
	}
	d := &socketReceiveDiagnostics{kernel: newKernelSocketObserver(t.conn)}
	if t.receiveDiagnostics.CompareAndSwap(nil, d) {
		return d
	}
	return t.receiveDiagnostics.Load()
}

func (d *socketReceiveDiagnostics) beginRead() {
	d.stage.Store(uint64(monotime.Now())<<2 | 1)
}

func (d *socketReceiveDiagnostics) readResult(p receivedPacket, err error) {
	if err != nil {
		d.readErrors.Add(1)
		d.stage.Store(uint64(monotime.Now())<<2 | 3)
		return
	}
	at := p.rcvTime
	if at.IsZero() {
		at = monotime.Now()
	}
	d.packets.Add(1)
	d.bytes.Add(uint64(len(p.data)))
	d.lastPacket.Store(int64(at))
	d.stage.Store(uint64(at)<<2 | 2)
}

func (d *socketReceiveDiagnostics) snapshot() SocketReceiveDiagnosticSnapshot {
	k := d.kernel() // proc reads run on the snapshot caller, not the receive loop
	stage := d.stage.Load()
	last := monotime.Time(d.lastPacket.Load())
	now := monotime.Now()
	out := SocketReceiveDiagnosticSnapshot{
		Ready: true, Packets: d.packets.Load(), Bytes: d.bytes.Load(),
		DispatchedPackets: d.dispatched.Load(), UnmatchedCID: d.unmatchedCID.Load(),
		HeaderDrops: d.headerDrops.Load(), NonQUICPackets: d.nonQUICPackets.Load(),
		EmptyPackets: d.emptyPackets.Load(), ReadErrors: d.readErrors.Load(),
		PacketAge: -time.Millisecond, Stage: "unknown", StageWait: -time.Millisecond,
	}
	if !last.IsZero() {
		out.PacketAge = now.Sub(last)
	}
	if stage != 0 {
		out.StageWait = now.Sub(monotime.Time(stage >> 2))
		switch stage & 3 {
		case 1:
			out.Stage = "read"
		case 2:
			out.Stage = "dispatch"
		case 3:
			out.Stage = "read_error"
		}
	}
	out.KernelStatus, out.KernelAge = k.status, k.age
	out.KernelDrops, out.KernelReceiveQueue, out.KernelSendQueue = k.drops, k.receiveQueue, k.sendQueue
	out.KernelReadBuffer, out.KernelWriteBuffer = k.readBuffer, k.writeBuffer
	return out
}
