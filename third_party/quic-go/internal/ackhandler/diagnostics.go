package ackhandler

import "github.com/apernet/quic-go/internal/monotime"

// RecoveryDiagnosticState is read only by the connection's owner goroutine.
// It exposes the current controller, rather than the cached public cwnd gauge
// that a replacement congestion controller might not update.
type RecoveryDiagnosticState struct {
	BytesInFlight    int64
	CongestionWindow int64
	PTOCount         uint32
	TrackedPackets   int
	SendMode         string
}

func (h *sentPacketHandler) DiagnosticState(now monotime.Time) RecoveryDiagnosticState {
	return RecoveryDiagnosticState{
		BytesInFlight:    int64(h.bytesInFlight),
		CongestionWindow: int64(h.getCongestionControl().GetCongestionWindow()),
		PTOCount:         h.ptoCount,
		TrackedPackets:   h.hist.Len(),
		SendMode:         h.SendMode(now).String(),
	}
}
