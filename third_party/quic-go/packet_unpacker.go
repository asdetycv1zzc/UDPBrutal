package quic

import (
	"fmt"

	"github.com/apernet/quic-go/internal/handshake"
	"github.com/apernet/quic-go/internal/monotime"
	"github.com/apernet/quic-go/internal/protocol"
	"github.com/apernet/quic-go/internal/qerr"
	"github.com/apernet/quic-go/internal/wire"
)

type headerDecryptor interface {
	DecryptHeader(sample []byte, firstByte *byte, pnBytes []byte)
}

type headerParseError struct {
	err error
}

func (e *headerParseError) Unwrap() error {
	return e.err
}

func (e *headerParseError) Error() string {
	return e.err.Error()
}

type unpackedPacket struct {
	hdr             *wire.ExtendedHeader
	encryptionLevel protocol.EncryptionLevel
	data            []byte
}

// The packetUnpacker unpacks QUIC packets.
type packetUnpacker struct {
	cs handshake.CryptoSetup

	shortHdrConnIDLen int
}

var _ unpacker = &packetUnpacker{}

func newPacketUnpacker(cs handshake.CryptoSetup, shortHdrConnIDLen int) *packetUnpacker {
	return &packetUnpacker{
		cs:                cs,
		shortHdrConnIDLen: shortHdrConnIDLen,
	}
}

// UnpackLongHeader unpacks a Long Header packet.
// If the reserved bits are invalid, the error is wire.ErrInvalidReservedBits.
// If any other error occurred when parsing the header, the error is of type headerParseError.
// If decrypting the payload fails for any reason, the error is the error returned by the AEAD.
func (u *packetUnpacker) UnpackLongHeader(hdr *wire.Header, data []byte) (*unpackedPacket, error) {
	var encLevel protocol.EncryptionLevel
	var extHdr *wire.ExtendedHeader
	var decrypted []byte
	//nolint:exhaustive // Retry packets can't be unpacked.
	switch hdr.Type {
	case protocol.PacketTypeInitial:
		encLevel = protocol.EncryptionInitial
		opener, err := u.cs.GetInitialOpener()
		if err != nil {
			return nil, err
		}
		extHdr, decrypted, err = u.unpackLongHeaderPacket(opener, hdr, data)
		if err != nil {
			return nil, err
		}
	case protocol.PacketTypeHandshake:
		encLevel = protocol.EncryptionHandshake
		opener, err := u.cs.GetHandshakeOpener()
		if err != nil {
			return nil, err
		}
		extHdr, decrypted, err = u.unpackLongHeaderPacket(opener, hdr, data)
		if err != nil {
			return nil, err
		}
	case protocol.PacketType0RTT:
		encLevel = protocol.Encryption0RTT
		opener, err := u.cs.Get0RTTOpener()
		if err != nil {
			return nil, err
		}
		extHdr, decrypted, err = u.unpackLongHeaderPacket(opener, hdr, data)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown packet type: %s", hdr.Type)
	}

	if len(decrypted) == 0 {
		return nil, &qerr.TransportError{
			ErrorCode:    qerr.ProtocolViolation,
			ErrorMessage: "empty packet",
		}
	}

	return &unpackedPacket{
		hdr:             extHdr,
		encryptionLevel: encLevel,
		data:            decrypted,
	}, nil
}

func (u *packetUnpacker) UnpackShortHeader(rcvTime monotime.Time, data []byte) (protocol.PacketNumber, protocol.PacketNumberLen, protocol.KeyPhaseBit, []byte, error) {
	opener, err := u.cs.Get1RTTOpener()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	pn, pnLen, kp, decrypted, err := u.unpackShortHeaderPacket(opener, rcvTime, data)
	if err != nil {
		return 0, 0, 0, nil, err
	}
	if len(decrypted) == 0 {
		return 0, 0, 0, nil, &qerr.TransportError{
			ErrorCode:    qerr.ProtocolViolation,
			ErrorMessage: "empty packet",
		}
	}
	return pn, pnLen, kp, decrypted, nil
}

func (u *packetUnpacker) unpackLongHeaderPacket(opener handshake.LongHeaderOpener, hdr *wire.Header, data []byte) (*wire.ExtendedHeader, []byte, error) {
	extHdr, parseErr := u.unpackLongHeader(opener, hdr, data)
	// If the reserved bits are set incorrectly, we still need to continue unpacking.
	// This avoids a timing side-channel, which otherwise might allow an attacker
	// to gain information about the header encryption.
	if parseErr != nil && parseErr != wire.ErrInvalidReservedBits {
		return nil, nil, parseErr
	}
	extHdrLen := extHdr.ParsedLen()
	extHdr.PacketNumber = opener.DecodePacketNumber(extHdr.PacketNumber, extHdr.PacketNumberLen)
	decrypted, err := opener.Open(data[extHdrLen:extHdrLen], data[extHdrLen:], extHdr.PacketNumber, data[:extHdrLen])
	if err != nil {
		return nil, nil, err
	}
	if parseErr != nil {
		return nil, nil, parseErr
	}
	return extHdr, decrypted, nil
}

func (u *packetUnpacker) unpackShortHeaderPacket(opener handshake.ShortHeaderOpener, rcvTime monotime.Time, data []byte) (protocol.PacketNumber, protocol.PacketNumberLen, protocol.KeyPhaseBit, []byte, error) {
	l, pn, pnLen, kp, parseErr := u.unpackShortHeader(opener, data)
	// If the reserved bits are set incorrectly, we still need to continue unpacking.
	// This avoids a timing side-channel, which otherwise might allow an attacker
	// to gain information about the header encryption.
	if parseErr != nil && parseErr != wire.ErrInvalidReservedBits {
		return 0, 0, 0, nil, &headerParseError{parseErr}
	}
	pn = opener.DecodePacketNumber(pn, pnLen)
	decrypted, err := opener.Open(data[l:l], data[l:], rcvTime, pn, kp, data[:l])
	if err != nil {
		return 0, 0, 0, nil, err
	}
	return pn, pnLen, kp, decrypted, parseErr
}

func (u *packetUnpacker) unpackShortHeader(_ headerDecryptor, data []byte) (int, protocol.PacketNumber, protocol.PacketNumberLen, protocol.KeyPhaseBit, error) {
	hdrLen := 1 /* first header byte */ + u.shortHdrConnIDLen
	if len(data) < hdrLen+4 {
		return 0, 0, 0, 0, fmt.Errorf("plaintext packet too small, expected at least 4 bytes after the header, got %d", len(data)-hdrLen)
	}
	// Packet-number length and header bytes are transmitted literally. There
	// is no protection sample to read, or overwritten payload to restore.
	return wire.ParseShortHeader(data, u.shortHdrConnIDLen)
}

// The error is either nil, a wire.ErrInvalidReservedBits or of type headerParseError.
func (u *packetUnpacker) unpackLongHeader(hd headerDecryptor, hdr *wire.Header, data []byte) (*wire.ExtendedHeader, error) {
	extHdr, err := unpackLongHeader(hd, hdr, data)
	if err != nil && err != wire.ErrInvalidReservedBits {
		return nil, &headerParseError{err: err}
	}
	return extHdr, err
}

func unpackLongHeader(_ headerDecryptor, hdr *wire.Header, data []byte) (*wire.ExtendedHeader, error) {
	hdrLen := hdr.ParsedLen()
	if protocol.ByteCount(len(data)) < hdrLen+4 {
		return nil, fmt.Errorf("plaintext packet too small, expected at least 4 bytes after the header, got %d", protocol.ByteCount(len(data))-hdrLen)
	}
	return hdr.ParseExtended(data)
}
