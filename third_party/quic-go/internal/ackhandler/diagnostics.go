package ackhandler

import "github.com/apernet/quic-go/internal/monotime"

// These counters are enabled and accessed only by the connection's owner loop.
// The public loss counters belong to the built-in CUBIC controller and are not
// updated by external controllers such as Brutal. Observe recovery decisions
// directly instead. PTO requeueing is distinct from threshold-based loss.
type recoveryDiagnosticCounters struct {
	lostPackets        uint64
	lostBytes          uint64
	spuriousLosses     uint64
	ptoRequeuedPackets uint64
}

// RecoveryDiagnosticState exposes the actual controller's state and recovery
// counters. Counters start at the first diagnostic snapshot, not at connection
// creation. All fields are sampled by the connection's owner goroutine.
type RecoveryDiagnosticState struct {
	BytesInFlight       int64
	CongestionWindow    int64
	PTOCount            uint32
	TrackedPackets      int
	SendMode            string
	LossDeclaredPackets uint64
	LossDeclaredBytes   uint64
	SpuriousLossPackets uint64
	PTORequeuedPackets  uint64
}

func (h *sentPacketHandler) DiagnosticState(now monotime.Time) RecoveryDiagnosticState {
	if h.diagnostics == nil {
		h.diagnostics = &recoveryDiagnosticCounters{}
	}
	return RecoveryDiagnosticState{
		BytesInFlight:       int64(h.bytesInFlight),
		CongestionWindow:    int64(h.getCongestionControl().GetCongestionWindow()),
		PTOCount:            h.ptoCount,
		TrackedPackets:      h.hist.Len(),
		SendMode:            h.SendMode(now).String(),
		LossDeclaredPackets: h.diagnostics.lostPackets,
		LossDeclaredBytes:   h.diagnostics.lostBytes,
		SpuriousLossPackets: h.diagnostics.spuriousLosses,
		PTORequeuedPackets:  h.diagnostics.ptoRequeuedPackets,
	}
}
