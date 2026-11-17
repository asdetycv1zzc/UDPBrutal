package client

import "github.com/apernet/quic-go"

// Connection exposes the native connection for server-initiated reverse streams.
// HTTP/3's client receives unidirectional control streams; the extension owns
// server-initiated bidirectional streams exclusively.
func (c *clientImpl) Connection() *quic.Conn { return c.conn }
