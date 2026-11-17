package proxy

import (
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apernet/quic-go"
)

var diagnosticConnections sync.Map // *quic.Conn -> *connectionObserver
var diagnosticIDs atomic.Uint64
var diagnosticEpoch = time.Now()
var diagnosticInterval = sync.OnceValue(func() time.Duration {
	value := strings.TrimSpace(os.Getenv("UDPBRUTAL_DIAGNOSTICS"))
	switch strings.ToLower(value) {
	case "", "0", "false", "off":
		return 0
	case "1", "true", "on":
		return 200 * time.Millisecond
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval < 100*time.Millisecond || interval > 60*time.Second {
		slog.Warn("invalid UDPBRUTAL_DIAGNOSTICS; use 100ms..60s, true, or off", "value", value)
		return 0
	}
	return interval
})

type connectionObserver struct {
	id      uint64
	role    string
	conn    *quic.Conn
	streams sync.Map // quic.StreamID -> *relayObserver
}
type relayObserver struct {
	stream   *quic.Stream
	toTunnel relayDirection
	toLocal  relayDirection
}
type relayDirection struct {
	read    atomic.Uint64
	written atomic.Uint64
	// One atomic word combines the monotonic timestamp and IO stage, so a
	// sample cannot pair an old stage with the next operation's timestamp.
	state atomic.Uint64
}

const (
	diagnosticReading uint64 = 1
	diagnosticWriting uint64 = 2
	diagnosticDone    uint64 = 3
)

func (d *relayDirection) stage(state uint64) {
	d.state.Store(uint64(time.Since(diagnosticEpoch).Nanoseconds())<<2 | state)
}
func (d *relayDirection) snapshot() (string, float64) {
	v := d.state.Load()
	switch v & 3 {
	case diagnosticReading:
		return "read", diagnosticMS(time.Since(diagnosticEpoch) - time.Duration(v>>2))
	case diagnosticWriting:
		return "write", diagnosticMS(time.Since(diagnosticEpoch) - time.Duration(v>>2))
	case diagnosticDone:
		return "done", 0
	default:
		return "running", 0
	}
}

type diagnosticReader struct {
	io.Reader
	direction *relayDirection
}

func (r diagnosticReader) Read(p []byte) (int, error) {
	r.direction.stage(diagnosticReading)
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.direction.read.Add(uint64(n))
	}
	r.direction.state.Store(0)
	return n, err
}

type diagnosticWriter struct {
	io.Writer
	direction *relayDirection
}

func (w diagnosticWriter) Write(p []byte) (int, error) {
	w.direction.stage(diagnosticWriting)
	n, err := w.Writer.Write(p)
	if n > 0 {
		w.direction.written.Add(uint64(n))
	}
	w.direction.state.Store(0)
	return n, err
}

func startConnectionObserver(conn *quic.Conn, role string) {
	interval := diagnosticInterval()
	if interval == 0 {
		return
	}
	d := &connectionObserver{id: diagnosticIDs.Add(1), role: role, conn: conn}
	if _, loaded := diagnosticConnections.LoadOrStore(conn, d); loaded {
		return
	}
	conn.EnableDiagnostics()
	slog.Info("transport diagnostics enabled", "diagnostic_conn", d.id, "role", role, "interval", interval, "local", conn.LocalAddr(), "remote", conn.RemoteAddr())
	go d.run(interval)
}
func observeRelay(a, b net.Conn) (*relayObserver, func()) {
	var tunnel *streamConn
	if t, ok := a.(*streamConn); ok {
		tunnel = t
	} else if t, ok := b.(*streamConn); ok {
		tunnel = t
	}
	if tunnel == nil {
		return nil, func() {}
	}
	entry, ok := diagnosticConnections.Load(tunnel.conn)
	if !ok {
		return nil, func() {}
	}
	d := entry.(*connectionObserver)
	r := &relayObserver{stream: tunnel.stream}
	id := tunnel.stream.StreamID()
	d.streams.Store(id, r)
	return r, func() {
		d.streams.Delete(id)
		slog.Info("reverse TCP diagnostics closed", "diagnostic_conn", d.id, "role", d.role, "stream", id,
			"to_tunnel_read", r.toTunnel.read.Load(), "to_tunnel_written", r.toTunnel.written.Load(),
			"to_local_read", r.toLocal.read.Load(), "to_local_written", r.toLocal.written.Load())
	}
}
func diagnosticMS(t time.Duration) float64 {
	return math.Round(float64(t)/float64(time.Millisecond)*100) / 100
}
func diagnosticMbps(n uint64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return math.Round(float64(n)*8/elapsed.Seconds()/1e6*100) / 100
}
func (d *connectionObserver) run(interval time.Duration) {
	defer diagnosticConnections.CompareAndDelete(d.conn, d)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	previous := d.conn.ConnectionStats()
	previousSocketBytes := uint64(0)
	var previousRecovery quic.DiagnosticSnapshot
	last := time.Now()
	for {
		select {
		case <-d.conn.Context().Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		elapsed := now.Sub(last)
		last = now
		stats := d.conn.ConnectionStats()
		x := d.conn.DiagnosticSnapshot()
		slog.Info("transport diagnostics", "diagnostic_conn", d.id, "role", d.role,
			"sample_ms", diagnosticMS(elapsed), "quic_tx_mbps", diagnosticMbps(stats.BytesSent-previous.BytesSent, elapsed),
			"quic_rx_mbps", diagnosticMbps(stats.BytesReceived-previous.BytesReceived, elapsed),
			"quic_tx_packets", stats.PacketsSent-previous.PacketsSent, "quic_rx_packets", stats.PacketsReceived-previous.PacketsReceived,
			"socket_tx_mbps", diagnosticMbps(x.SocketWriteBytes-previousSocketBytes, elapsed),
			"recovery_lost_delta", x.LossDeclaredPackets-previousRecovery.LossDeclaredPackets,
			"recovery_lost_bytes_delta", x.LossDeclaredBytes-previousRecovery.LossDeclaredBytes,
			"recovery_spurious_delta", x.SpuriousLossPackets-previousRecovery.SpuriousLossPackets,
			"pto_requeued_delta", x.PTORequeuedPackets-previousRecovery.PTORequeuedPackets,
			"srtt_ms", diagnosticMS(stats.SmoothedRTT), "latest_rtt_ms", diagnosticMS(stats.LatestRTT),
			"snapshot_ready", x.Ready, "loop_age_ms", diagnosticMS(x.LoopAge),
			"ack_age_ms", diagnosticMS(x.AckAge), "packet_age_ms", diagnosticMS(x.PacketAge),
			"send_mode", x.SendMode, "inflight", x.BytesInFlight, "cwnd", x.CongestionWindow,
			"pto_count", x.PTOCount, "tracked_packets", x.TrackedPackets,
			"loss_timer_set", x.LossTimerSet, "loss_timer_ms", diagnosticMS(x.LossTimerRemaining),
			"pacing_timer_set", x.PacingTimerSet, "pacing_timer_ms", diagnosticMS(x.PacingTimerRemaining),
			"send_queue", x.SendQueueLen, "rx_queue_drops", x.ReceiveQueueDrops, "gso", x.GSO,
			"socket_write_pending_ms", diagnosticMS(x.SocketWritePending),
			"socket_write_last_ms", diagnosticMS(x.SocketWriteLastDuration),
			"socket_write_age_ms", diagnosticMS(x.SocketWriteAge), "socket_write_calls", x.SocketWriteCalls, "socket_write_errors", x.SocketWriteErrors)
		previous = stats
		previousSocketBytes = x.SocketWriteBytes
		previousRecovery = x
		d.streams.Range(func(key, value any) bool {
			r := value.(*relayObserver)
			outStage, outWait := r.toTunnel.snapshot()
			inStage, inWait := r.toLocal.snapshot()
			s := r.stream.StreamDiagnosticSnapshot()
			slog.Info("reverse TCP diagnostics", "diagnostic_conn", d.id, "role", d.role, "stream", key,
				"to_tunnel_stage", outStage, "to_tunnel_wait_ms", outWait,
				"to_tunnel_read", r.toTunnel.read.Load(), "to_tunnel_written", r.toTunnel.written.Load(),
				"to_local_stage", inStage, "to_local_wait_ms", inWait,
				"to_local_read", r.toLocal.read.Load(), "to_local_written", r.toLocal.written.Load(),
				"send_ready", s.SendReady, "buffered", s.BufferedBytes, "send_offset", s.SendOffset,
				"stream_credit", s.StreamCredit, "conn_credit_ready", s.ConnectionCreditReady, "conn_credit", s.ConnectionCredit,
				"retransmission_frames", s.RetransmissionFrames,
				"receive_ready", s.ReceiveReady, "receive_offset", s.ReceiveOffset, "receive_highest", s.ReceiveHighest)
			return true
		})
	}
}
