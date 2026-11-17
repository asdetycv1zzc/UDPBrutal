package proxy

import (
	"log/slog"
	"time"

	"github.com/apernet/quic-go"
)

const diagnosticWriteWait = 500 * time.Millisecond

// Reporter-owned state. Idle reads never trigger a write-wait report. An IO
// operation's packed identity distinguishes a resumed write from the next one.
type writeWaitObserver struct {
	operation    uint64
	reported     bool
	firstSeen    time.Time
	writeStarted time.Time
	baseline     quic.DiagnosticSnapshot
}

func diagnosticDelta(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func receiveDiagnosticArgs(x, previous quic.SocketReceiveDiagnosticSnapshot) []any {
	return []any{
		"socket_rx_ready", x.Ready, "socket_rx_scope", "initial_transport",
		"socket_rx_packets", diagnosticDelta(x.Packets, previous.Packets),
		"socket_rx_bytes", diagnosticDelta(x.Bytes, previous.Bytes),
		"socket_dispatch_packets", diagnosticDelta(x.DispatchedPackets, previous.DispatchedPackets),
		"socket_unmatched_cid", diagnosticDelta(x.UnmatchedCID, previous.UnmatchedCID),
		"socket_header_drops", diagnosticDelta(x.HeaderDrops, previous.HeaderDrops),
		"socket_non_quic", diagnosticDelta(x.NonQUICPackets, previous.NonQUICPackets),
		"socket_empty_packets", diagnosticDelta(x.EmptyPackets, previous.EmptyPackets),
		"socket_read_errors", diagnosticDelta(x.ReadErrors, previous.ReadErrors),
		"socket_rx_age_ms", diagnosticMS(x.PacketAge),
		"socket_rx_stage", x.Stage, "socket_rx_stage_ms", diagnosticMS(x.StageWait),
		"kernel_socket_status", x.KernelStatus, "kernel_socket_age_ms", diagnosticMS(x.KernelAge),
		"kernel_rx_drops", x.KernelDrops, "kernel_rx_queue", x.KernelReceiveQueue,
		"kernel_tx_queue", x.KernelSendQueue, "kernel_rcvbuf", x.KernelReadBuffer,
		"kernel_sndbuf", x.KernelWriteBuffer,
	}
}

func (w *writeWaitObserver) observe(d *connectionObserver, stream any, direction, stage string, wait float64,
	operation uint64, s quic.StreamDiagnosticSnapshot, x, previous quic.DiagnosticSnapshot, now time.Time, elapsed time.Duration,
) {
	if w.operation != 0 && (stage != "write" || operation != w.operation) {
		if w.reported {
			w.report(d, stream, direction, "reverse TCP write resumed", "progress_observed",
				diagnosticMS(now.Sub(w.writeStarted)), s, x, now)
		}
		*w = writeWaitObserver{}
	}
	if stage != "write" {
		return
	}
	if w.operation == 0 {
		*w = writeWaitObserver{operation: operation, firstSeen: now.Add(-elapsed),
			writeStarted: now.Add(-time.Duration(wait * float64(time.Millisecond))), baseline: previous}
		// Counter deltas start with the preceding sample. The observed IO
		// wait is measured independently; neither claims an exact packet trace.
	}
	if w.reported || wait < diagnosticMS(diagnosticWriteWait) {
		return
	}
	w.reported = true
	hint := "write_wait"
	switch {
	case direction == "to_local":
		hint = "local_tcp_write_wait"
	case x.SocketWritePending >= diagnosticWriteWait:
		hint = "socket_write_wait"
	case s.SendReady && (s.StreamCredit <= 0 || s.ConnectionCreditReady && s.ConnectionCredit <= 0):
		hint = "flow_control_wait"
	case x.Ready && x.AckAge >= diagnosticWriteWait:
		hint = "ack_progress_stopped"
	}
	w.report(d, stream, direction, "reverse TCP write wait", hint, wait, s, x, now)
}

func (w *writeWaitObserver) report(d *connectionObserver, stream any, direction, message, hint string,
	wait float64, s quic.StreamDiagnosticSnapshot, x quic.DiagnosticSnapshot, now time.Time,
) {
	args := []any{"diagnostic_conn", d.id, "role", d.role, "stream", stream,
		"direction", direction, "hint", hint, "observed_wait_ms", wait,
		"counter_window_ms", diagnosticMS(now.Sub(w.firstSeen)),
		"snapshot_ready", x.Ready,
		"ack_age_ms", diagnosticMS(x.AckAge), "loop_age_ms", diagnosticMS(x.LoopAge),
		"pto_count", x.PTOCount, "inflight", x.BytesInFlight, "cwnd", x.CongestionWindow,
		"send_mode", x.SendMode, "send_queue", x.SendQueueLen,
		"socket_write_pending_ms", diagnosticMS(x.SocketWritePending),
		"socket_tx_bytes", diagnosticDelta(x.SocketWriteBytes, w.baseline.SocketWriteBytes),
		"socket_write_errors", diagnosticDelta(x.SocketWriteErrors, w.baseline.SocketWriteErrors),
		"rx_queue_drops", diagnosticDelta(x.ReceiveQueueDrops, w.baseline.ReceiveQueueDrops),
		"send_ready", s.SendReady, "stream_credit", s.StreamCredit,
		"conn_credit_ready", s.ConnectionCreditReady, "conn_credit", s.ConnectionCredit,
	}
	args = append(args, receiveDiagnosticArgs(x.SocketReceive, w.baseline.SocketReceive)...)
	if message == "reverse TCP write wait" {
		slog.Warn(message, args...)
	} else {
		slog.Info(message, args...)
	}
}
