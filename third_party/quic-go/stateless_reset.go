package quic

import "github.com/apernet/quic-go/internal/protocol"

// Plaintext reset tokens contain the connection ID padded to 16 bytes.
// They are routing identifiers and make no authenticity guarantee.
type statelessResetter struct{}

func newStatelessResetter(*StatelessResetKey) *statelessResetter { return &statelessResetter{} }
func (*statelessResetter) GetStatelessResetToken(id protocol.ConnectionID) protocol.StatelessResetToken {
	var token protocol.StatelessResetToken
	copy(token[:], id.Bytes())
	return token
}
