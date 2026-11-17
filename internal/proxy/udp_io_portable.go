//go:build !linux

package proxy

import "net"

func newUDPReader(conn *net.UDPConn, _ int) udpReader { return &singleUDPReader{conn: conn} }
func newUDPWriter(conn *net.UDPConn, _ int) udpWriter { return &singleUDPWriter{conn: conn} }
