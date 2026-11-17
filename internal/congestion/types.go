// Package congestion supplies the transport-neutral types used by the unmodified
// Hysteria Brutal algorithm. No QUIC or TLS implementation is linked.
package congestion

import (
	"github.com/asdetycv1zzc/UDPBrutal/internal/monotime"
	"time"
)

type ByteCount int64
type PacketNumber uint64

// Hysteria's pinned quic-go revision defines InitialPacketSize as 1280.
// The transport calls SetMaxDatagramSize(1200) after construction.
const InitialPacketSize ByteCount = 1280
const MinPacingDelay = time.Millisecond

type RTTStatsProvider interface{ SmoothedRTT() time.Duration }
type AckedPacketInfo struct{ PacketNumber PacketNumber }
type LostPacketInfo struct{ PacketNumber PacketNumber }
type CongestionControl interface {
	SetRTTStatsProvider(RTTStatsProvider)
	TimeUntilSend(ByteCount) monotime.Time
	HasPacingBudget(monotime.Time) bool
	CanSend(ByteCount) bool
	GetCongestionWindow() ByteCount
	OnPacketSent(monotime.Time, ByteCount, PacketNumber, ByteCount, bool)
	OnPacketAcked(PacketNumber, ByteCount, ByteCount, monotime.Time)
	OnCongestionEvent(PacketNumber, ByteCount, ByteCount)
	OnCongestionEventEx(ByteCount, monotime.Time, []AckedPacketInfo, []LostPacketInfo)
	SetMaxDatagramSize(ByteCount)
	InSlowStart() bool
	InRecovery() bool
	MaybeExitSlowStart()
	OnRetransmissionTimeout(bool)
}
