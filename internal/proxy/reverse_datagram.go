package proxy

import (
	"bytes"
	"encoding/binary"

	"github.com/apernet/hysteria/core/v2/extension"
)

// A valid ordinary Hysteria UDP fragment has FragCount >= 1 at byte 7.
// The zero at byte 7 reserves this prefix without taking ordinary session IDs.
var reverseDatagramMagic = []byte{'U', 'B', 'R', 'E', 'V', '1', 0, 0}

func reversePrefix(index uint16) []byte {
	b := make([]byte, 10)
	copy(b, reverseDatagramMagic)
	binary.BigEndian.PutUint16(b[8:], index)
	return b
}
func parseReverseDatagram(b []byte) (uint16, *extension.UDPMessage, bool) {
	if !bytes.HasPrefix(b, reverseDatagramMagic) {
		return 0, nil, false
	}
	if len(b) < 10 {
		return 0, nil, true
	}
	index := binary.BigEndian.Uint16(b[8:10])
	message, err := extension.ParseUDPMessage(b[10:])
	if err != nil {
		return index, nil, true
	}
	if message.FragCount == 0 || message.FragID >= message.FragCount {
		return index, nil, true
	}
	return index, message, true
}
