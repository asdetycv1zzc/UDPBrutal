//go:build !linux

package quic

import "time"

func newKernelSocketObserver(rawConn) func() kernelSocketSnapshot {
	return func() kernelSocketSnapshot {
		return kernelSocketSnapshot{status: "unsupported_platform", age: -time.Millisecond}
	}
}
