// UDPBrutal plaintext adaptation of the pinned Hysteria quic-go handshake.
// CRYPTO frames carry transport parameters and completion records, with no TLS,
// keys, ciphers, authentication tags, certificate operations, or key updates.
package handshake

import (
	"bytes"
	"context"
	"crypto/tls" // Public API compatibility types only; no TLS connection is created.
	"encoding/binary"
	"fmt"
	"net"

	"github.com/apernet/quic-go/internal/protocol"
	"github.com/apernet/quic-go/internal/qerr"
	"github.com/apernet/quic-go/internal/utils"
	"github.com/apernet/quic-go/internal/wire"
	"github.com/apernet/quic-go/qlogwriter"
)

const plaintextMagic = "UDPBrutal-HY2-P1"

type quicVersionContextKey struct{}

var QUICVersionContextKey = &quicVersionContextKey{}

const maxPlaintextHandshake = 65536
const (
	clientHello byte = iota + 1
	serverHello
	serverFinished
	clientFinished
)

type plaintextSetup struct {
	perspective                      protocol.Perspective
	params                           *wire.TransportParameters
	events                           []Event
	initial, handshake               plaintextLong
	application                      plaintextShort
	received                         [4][]byte
	stage                            byte
	handshakeReady, applicationReady bool
	initialDropped, handshakeDropped bool
}

var _ CryptoSetup = (*plaintextSetup)(nil)

func newPlaintextSetup(p protocol.Perspective, tp *wire.TransportParameters) *plaintextSetup {
	return &plaintextSetup{perspective: p, params: tp,
		initial:     plaintextLong{largest: protocol.InvalidPacketNumber},
		handshake:   plaintextLong{largest: protocol.InvalidPacketNumber},
		application: plaintextShort{largest: protocol.InvalidPacketNumber}}
}
func NewCryptoSetupClient(_ protocol.ConnectionID, tp *wire.TransportParameters, _ *tls.Config, _ bool, _ bool, _ *utils.RTTStats, _ qlogwriter.Recorder, _ utils.Logger, _ protocol.Version) (CryptoSetup, error) {
	return newPlaintextSetup(protocol.PerspectiveClient, tp), nil
}
func NewCryptoSetupServer(_ protocol.ConnectionID, _, _ net.Addr, tp *wire.TransportParameters, _ *tls.Config, _ bool, _ *utils.RTTStats, _ qlogwriter.Recorder, _ utils.Logger, _ protocol.Version) CryptoSetup {
	return newPlaintextSetup(protocol.PerspectiveServer, tp)
}
func (h *plaintextSetup) emit(kind EventKind, data []byte) {
	h.events = append(h.events, Event{Kind: kind, Data: data})
}
func (h *plaintextSetup) record(kind byte, params []byte) []byte {
	payload := append(append([]byte(plaintextMagic), kind), params...)
	b := make([]byte, 4, 4+len(payload))
	binary.BigEndian.PutUint32(b, uint32(len(payload)))
	return append(b, payload...)
}
func (h *plaintextSetup) StartHandshake(context.Context) error {
	if h.perspective == protocol.PerspectiveClient {
		h.emit(EventWriteInitialData, h.record(clientHello, h.params.Marshal(h.perspective)))
	}
	return nil
}
func (h *plaintextSetup) HandleMessage(data []byte, level protocol.EncryptionLevel) error {
	if level != protocol.EncryptionInitial && level != protocol.EncryptionHandshake {
		return h.protocolError("unexpected handshake level")
	}
	index := int(level)
	if index < 0 || index >= len(h.received) {
		return h.protocolError("invalid handshake level")
	}
	b := append(h.received[index], data...)
	if len(b) > maxPlaintextHandshake+4 {
		return h.protocolError("handshake record too large")
	}
	for len(b) >= 4 {
		size := int(binary.BigEndian.Uint32(b[:4]))
		if size < len(plaintextMagic)+1 || size > maxPlaintextHandshake {
			return h.protocolError("invalid handshake record length")
		}
		if len(b) < 4+size {
			break
		}
		body := b[4 : 4+size]
		if !bytes.Equal(body[:len(plaintextMagic)], []byte(plaintextMagic)) {
			return h.protocolError("not a UDPBrutal plaintext peer")
		}
		if err := h.handleRecord(body[len(plaintextMagic)], body[len(plaintextMagic)+1:], level); err != nil {
			return err
		}
		b = b[4+size:]
	}
	h.received[index] = b
	return nil
}
func (h *plaintextSetup) peerParameters(b []byte) error {
	var tp wire.TransportParameters
	if err := tp.Unmarshal(b, h.perspective.Opposite()); err != nil {
		return err
	}
	h.events = append(h.events, Event{Kind: EventReceivedTransportParameters, TransportParameters: &tp})
	return nil
}
func (h *plaintextSetup) handleRecord(kind byte, b []byte, level protocol.EncryptionLevel) error {
	switch {
	case h.perspective == protocol.PerspectiveServer && h.stage == 0 && kind == clientHello && level == protocol.EncryptionInitial:
		if err := h.peerParameters(b); err != nil {
			return err
		}
		h.stage = 1
		h.handshakeReady = true
		h.emit(EventReceivedHandshakeReadKeys, nil)
		h.emit(EventWriteInitialData, h.record(serverHello, h.params.Marshal(h.perspective)))
		h.emit(EventWriteHandshakeData, h.record(serverFinished, nil))
	case h.perspective == protocol.PerspectiveClient && h.stage == 0 && kind == serverHello && level == protocol.EncryptionInitial:
		if err := h.peerParameters(b); err != nil {
			return err
		}
		h.stage = 1
		h.handshakeReady = true
		h.emit(EventReceivedHandshakeReadKeys, nil)
	case h.perspective == protocol.PerspectiveClient && h.stage == 1 && kind == serverFinished && len(b) == 0 && level == protocol.EncryptionHandshake:
		// The server may discard its Handshake retransmissions after receiving this
		// record, so it must only be emitted after serverFinished was received.
		h.emit(EventWriteHandshakeData, h.record(clientFinished, nil))
		h.complete()
	case h.perspective == protocol.PerspectiveServer && h.stage == 1 && kind == clientFinished && len(b) == 0 && level == protocol.EncryptionHandshake:
		h.complete()
	default:
		return h.protocolError(fmt.Sprintf("unexpected plaintext handshake record %d", kind))
	}
	return nil
}
func (h *plaintextSetup) complete() {
	h.stage = 2
	h.applicationReady = true
	h.emit(EventReceived1RTTReadKeys, nil)
	h.emit(EventHandshakeComplete, nil)
}
func (h *plaintextSetup) protocolError(s string) error {
	return &qerr.TransportError{ErrorCode: qerr.ProtocolViolation, ErrorMessage: s}
}
func (h *plaintextSetup) NextEvent() Event {
	if len(h.events) == 0 {
		return Event{Kind: EventNoEvent}
	}
	v := h.events[0]
	h.events = h.events[1:]
	return v
}
func (*plaintextSetup) Close() error                                    { return nil }
func (*plaintextSetup) ChangeConnectionID(protocol.ConnectionID)        {}
func (*plaintextSetup) SetLargest1RTTAcked(protocol.PacketNumber) error { return nil }
func (*plaintextSetup) GetSessionTicket() ([]byte, error)               { return nil, nil }
func (h *plaintextSetup) DiscardInitialKeys()                           { h.initialDropped = true }
func (h *plaintextSetup) SetHandshakeConfirmed()                        { h.handshakeDropped = true }
func (*plaintextSetup) ConnectionState() ConnectionState {
	// ALPN is API metadata for HTTP/3 framing. This does not report a TLS handshake.
	return ConnectionState{ConnectionState: tls.ConnectionState{NegotiatedProtocol: "h3"}}
}
func (h *plaintextSetup) GetInitialOpener() (LongHeaderOpener, error) {
	if h.initialDropped {
		return nil, ErrKeysDropped
	}
	return &h.initial, nil
}
func (h *plaintextSetup) GetInitialSealer() (LongHeaderSealer, error) {
	if h.initialDropped {
		return nil, ErrKeysDropped
	}
	return &h.initial, nil
}
func (h *plaintextSetup) GetHandshakeOpener() (LongHeaderOpener, error) {
	if h.handshakeDropped {
		return nil, ErrKeysDropped
	}
	if !h.handshakeReady {
		return nil, ErrKeysNotYetAvailable
	}
	return &h.handshake, nil
}
func (h *plaintextSetup) GetHandshakeSealer() (LongHeaderSealer, error) {
	if h.handshakeDropped {
		return nil, ErrKeysDropped
	}
	if !h.handshakeReady {
		return nil, ErrKeysNotYetAvailable
	}
	return &h.handshake, nil
}
func (*plaintextSetup) Get0RTTOpener() (LongHeaderOpener, error) { return nil, ErrKeysDropped }
func (*plaintextSetup) Get0RTTSealer() (LongHeaderSealer, error) { return nil, ErrKeysDropped }
func (h *plaintextSetup) Get1RTTOpener() (ShortHeaderOpener, error) {
	if !h.applicationReady {
		return nil, ErrKeysNotYetAvailable
	}
	return &h.application, nil
}
func (h *plaintextSetup) Get1RTTSealer() (ShortHeaderSealer, error) {
	if !h.applicationReady {
		return nil, ErrKeysNotYetAvailable
	}
	return &h.application, nil
}
