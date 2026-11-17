// Package transport implements a plaintext reliable ordered byte stream over UDP.
// Brutal controls every original and retransmitted DATA datagram.
package transport

import (
	"encoding/binary"
	"errors"
)

const (
	mtu         = 1200
	headerSize  = 37
	payloadSize = mtu - headerSize
	window      = 512
)

const (
	hello byte = iota + 1
	welcome
	data
	ack
	ping
	pong
	goodbye
	reject
)

type packet struct {
	kind                            byte
	session, sequence, number, next uint64
	payload                         []byte
}

func encode(p packet) []byte {
	b := make([]byte, headerSize+len(p.payload))
	copy(b, "UB01")
	b[4] = p.kind
	binary.BigEndian.PutUint64(b[5:13], p.session)
	binary.BigEndian.PutUint64(b[13:21], p.sequence)
	binary.BigEndian.PutUint64(b[21:29], p.number)
	binary.BigEndian.PutUint64(b[29:37], p.next)
	copy(b[headerSize:], p.payload)
	return b
}

func decode(b []byte) (packet, error) {
	if len(b) < headerSize || len(b) > mtu || string(b[:4]) != "UB01" {
		return packet{}, errors.New("invalid UDPBrutal packet")
	}
	return packet{b[4], binary.BigEndian.Uint64(b[5:13]), binary.BigEndian.Uint64(b[13:21]), binary.BigEndian.Uint64(b[21:29]), binary.BigEndian.Uint64(b[29:37]), b[headerSize:]}, nil
}
