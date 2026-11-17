//go:build linux

package quic

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type procSocketCounters struct{ drops, receiveQueue, sendQueue uint64 }

// One scan per second per process, shared by all diagnostic connections. Socket
// inode matching avoids confusing a port reused on another address or process.
var udpProcDiagnostics struct {
	sync.Mutex
	at       time.Time
	entries  map[uint64]procSocketCounters
	complete bool
}

func newKernelSocketObserver(conn rawConn) func() kernelSocketSnapshot {
	provider, ok := conn.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return kernelSocketUnavailable("unsupported_socket")
	}
	raw, err := provider.SyscallConn()
	if err != nil {
		return kernelSocketUnavailable("fd_unavailable")
	}
	var inode uint64
	var readBuffer, writeBuffer int
	if err := raw.Control(func(fd uintptr) {
		var st unix.Stat_t
		if unix.Fstat(int(fd), &st) == nil {
			inode = uint64(st.Ino)
		}
		readBuffer, _ = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF)
		writeBuffer, _ = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_SNDBUF)
	}); err != nil || inode == 0 {
		return kernelSocketUnavailable("fd_unavailable")
	}
	return func() kernelSocketSnapshot {
		c, age, status := readUDPProcCounters(inode)
		return kernelSocketSnapshot{status: status, age: age, drops: c.drops,
			receiveQueue: c.receiveQueue, sendQueue: c.sendQueue,
			readBuffer: readBuffer, writeBuffer: writeBuffer}
	}
}

func kernelSocketUnavailable(status string) func() kernelSocketSnapshot {
	return func() kernelSocketSnapshot { return kernelSocketSnapshot{status: status, age: -time.Millisecond} }
}

func readUDPProcCounters(inode uint64) (procSocketCounters, time.Duration, string) {
	udpProcDiagnostics.Lock()
	defer udpProcDiagnostics.Unlock()
	if udpProcDiagnostics.at.IsZero() || time.Since(udpProcDiagnostics.at) >= time.Second {
		entries := make(map[uint64]procSocketCounters)
		complete := true
		tables := 0
		for _, path := range []string{"/proc/net/udp", "/proc/net/udp6"} {
			f, err := os.Open(path)
			if err != nil {
				if !os.IsNotExist(err) {
					complete = false
				}
				continue
			}
			tables++
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				fields := strings.Fields(scanner.Text())
				// tx_queue:rx_queue and timer columns each occupy one field;
				// inode is field 9, and socket drops is the final field.
				if len(fields) < 13 {
					continue
				}
				id, err := strconv.ParseUint(fields[9], 10, 64)
				if err != nil || id == 0 {
					continue
				}
				queues := strings.SplitN(fields[4], ":", 2)
				if len(queues) != 2 {
					continue
				}
				tx, e1 := strconv.ParseUint(queues[0], 16, 64)
				rx, e2 := strconv.ParseUint(queues[1], 16, 64)
				drops, e3 := strconv.ParseUint(fields[len(fields)-1], 10, 64)
				if e1 != nil || e2 != nil || e3 != nil {
					continue
				}
				entries[id] = procSocketCounters{drops: drops, receiveQueue: rx, sendQueue: tx}
			}
			if scanner.Err() != nil {
				complete = false
			}
			_ = f.Close()
		}
		udpProcDiagnostics.entries, udpProcDiagnostics.complete = entries, complete && tables > 0
		udpProcDiagnostics.at = time.Now()
	}
	age := time.Since(udpProcDiagnostics.at)
	if c, ok := udpProcDiagnostics.entries[inode]; ok {
		return c, age, "ok"
	}
	status := "socket_not_found"
	if !udpProcDiagnostics.complete {
		status = "proc_unavailable"
	}
	return procSocketCounters{}, age, status
}
