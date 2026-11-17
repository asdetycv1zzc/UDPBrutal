package handshake

import (
	"errors"

	"github.com/apernet/quic-go/internal/monotime"
	"github.com/apernet/quic-go/internal/protocol"
)

// Payload and header adapters preserve the engine interfaces without protecting
// bytes. Packet-number reconstruction remains the upstream implementation.
type plaintextLong struct{ largest protocol.PacketNumber }

func (p *plaintextLong) DecodePacketNumber(n protocol.PacketNumber, l protocol.PacketNumberLen) protocol.PacketNumber {
	return protocol.DecodePacketNumber(l, p.largest, n)
}
func (p *plaintextLong) Open(dst, src []byte, n protocol.PacketNumber, _ []byte) ([]byte, error) {
	p.largest = max(p.largest, n)
	return append(dst, src...), nil
}
func (*plaintextLong) Seal(dst, src []byte, _ protocol.PacketNumber, _ []byte) []byte {
	return append(dst, src...)
}
func (*plaintextLong) EncryptHeader([]byte, *byte, []byte) {}
func (*plaintextLong) DecryptHeader([]byte, *byte, []byte) {}
func (*plaintextLong) Overhead() int                       { return 0 }

type plaintextShort struct{ largest protocol.PacketNumber }

func (p *plaintextShort) DecodePacketNumber(n protocol.PacketNumber, l protocol.PacketNumberLen) protocol.PacketNumber {
	return protocol.DecodePacketNumber(l, p.largest, n)
}
func (p *plaintextShort) Open(dst, src []byte, _ monotime.Time, n protocol.PacketNumber, kp protocol.KeyPhaseBit, _ []byte) ([]byte, error) {
	if kp != protocol.KeyPhaseZero {
		return nil, errors.New("key phase must remain zero in plaintext profile")
	}
	p.largest = max(p.largest, n)
	return append(dst, src...), nil
}
func (*plaintextShort) Seal(dst, src []byte, _ protocol.PacketNumber, _ []byte) []byte {
	return append(dst, src...)
}
func (*plaintextShort) EncryptHeader([]byte, *byte, []byte) {}
func (*plaintextShort) DecryptHeader([]byte, *byte, []byte) {}
func (*plaintextShort) Overhead() int                       { return 0 }
func (*plaintextShort) KeyPhase() protocol.KeyPhaseBit      { return protocol.KeyPhaseZero }
func NewInitialAEAD(_ protocol.ConnectionID, _ protocol.Perspective, _ protocol.Version) (LongHeaderSealer, LongHeaderOpener) {
	p := &plaintextLong{largest: protocol.InvalidPacketNumber}
	return p, p
}

// Retry integrity fields are zero padding; source-address tokens are literal
// ASN.1 records. They make no authenticity or ownership guarantee.
func GetRetryIntegrityTag([]byte, protocol.ConnectionID, protocol.Version) *[16]byte {
	return new([16]byte)
}

type TokenProtectorKey [32]byte
type tokenProtector struct{}

func newTokenProtector(TokenProtectorKey) *tokenProtector    { return &tokenProtector{} }
func (*tokenProtector) NewToken(b []byte) ([]byte, error)    { return append([]byte(nil), b...), nil }
func (*tokenProtector) DecodeToken(b []byte) ([]byte, error) { return append([]byte(nil), b...), nil }
