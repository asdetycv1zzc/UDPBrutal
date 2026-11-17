// Package extension exports the pinned Hysteria framing and fragmentation for
// UDPBrutal's reverse extension. Forward proxy messages use the original core.
package extension

import (
	"errors"
	"io"
	"math/rand"

	"github.com/apernet/hysteria/core/v2/internal/congestion"
	"github.com/apernet/hysteria/core/v2/internal/frag"
	"github.com/apernet/hysteria/core/v2/internal/protocol"
	"github.com/apernet/quic-go"
)

type UDPMessage = protocol.UDPMessage
type Defragger = frag.Defragger

const MaxUDPSize = protocol.MaxUDPSize

func ValidateCongestion(kind, profile string) error {
	normalized, err := congestion.NormalizeType(kind)
	if err != nil {
		return err
	}
	if normalized == congestion.TypeBBR {
		_, err = congestion.NormalizeBBRProfile(profile)
	}
	return err
}

func ParseUDPMessage(b []byte) (*UDPMessage, error) { return protocol.ParseUDPMessage(b) }
func WriteTCPResponse(w io.Writer, ok bool, message string) error {
	return protocol.WriteTCPResponse(w, ok, message)
}
func ReadTCPResponse(r io.Reader) (bool, string, error) { return protocol.ReadTCPResponse(r) }

// SendUDP follows core/client/udp.go: first send without fragmentation, then
// split only after the native connection reports DatagramTooLargeError. Prefix
// bytes belong solely to the reverse extension, never ordinary HY2 messages.
func SendUDP(conn *quic.Conn, prefix []byte, message *UDPMessage) error {
	send := func(m *UDPMessage) error {
		b := make([]byte, len(prefix)+m.Size())
		copy(b, prefix)
		if m.Serialize(b[len(prefix):]) < 0 {
			return io.ErrShortBuffer
		}
		return conn.SendDatagram(b)
	}
	err := send(message)
	var tooLarge *quic.DatagramTooLargeError
	if !errors.As(err, &tooLarge) {
		return err
	}
	size := int(tooLarge.MaxDatagramPayloadSize) - len(prefix)
	if size <= message.HeaderSize() {
		return err
	}
	copyMessage := *message
	copyMessage.PacketID = uint16(rand.Intn(0xFFFF)) + 1
	for _, part := range frag.FragUDPMessage(&copyMessage, size) {
		if err := send(&part); err != nil {
			return err
		}
	}
	return nil
}
