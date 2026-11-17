//go:build !linux

package quic

func newPacketBatchWriter(sendConn) packetBatchWriter { return nil }
