package proxy

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

func udpTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 || timeout > 24*time.Hour {
		return 60 * time.Second
	}
	return timeout
}

// Each source address gets its own stream and connected target socket. Replies
// cannot cross between two callers that use the same UDP mapping.
func serveUDPMapping(ctx context.Context, l *net.UDPConn, openStream func() (*yamux.Stream, error), idle time.Duration) {
	type association struct {
		queue chan []byte
		done  chan struct{}
	}
	var mu sync.Mutex
	sessions := make(map[string]*association)
	finished := make(chan struct{})
	defer close(finished)
	defer l.Close()
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-finished:
		}
	}()
	run := func(peer *net.UDPAddr, key string, a *association) {
		defer func() {
			close(a.done)
			mu.Lock()
			if sessions[key] == a {
				delete(sessions, key)
			}
			mu.Unlock()
		}()
		stream, err := openStream()
		if err != nil {
			return
		}
		defer stream.Close()
		replies := make(chan struct{})
		activity := make(chan struct{}, 1)
		go func() {
			defer close(replies)
			for {
				_, payload, err := readDatagram(stream)
				if err != nil {
					return
				}
				if _, err = l.WriteToUDP(payload, peer); err != nil {
					return
				}
				select {
				case activity <- struct{}{}:
				default:
				}
			}
		}()
		timer := time.NewTimer(idle)
		defer timer.Stop()
		reset := func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idle)
		}
		defer func() { _ = stream.SetDeadline(time.Now()); _ = stream.Close(); <-replies }()
		for {
			select {
			case <-ctx.Done():
				return
			case <-finished:
				return
			case <-replies:
				return
			case <-timer.C:
				return
			case <-activity:
				reset()
			case payload := <-a.queue:
				_ = stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if writeDatagram(stream, peer.String(), payload) != nil {
					return
				}
				reset()
			}
		}
	}
	buf := make([]byte, 65535)
	for {
		n, peer, err := l.ReadFromUDP(buf)
		if err != nil {
			return
		}
		key := peer.String()
		mu.Lock()
		a := sessions[key]
		if a != nil {
			select {
			case <-a.done:
				a = nil
			default:
			}
		}
		if a == nil && len(sessions) < 256 {
			a = &association{queue: make(chan []byte, 32), done: make(chan struct{})}
			sessions[key] = a
			go run(peer, key, a)
		}
		mu.Unlock()
		if a != nil {
			select {
			case a.queue <- append([]byte(nil), buf[:n]...):
			default:
			}
		} // Bounded UDP queues drop on overload.
	}
}

func handleFixedUDP(ctx context.Context, stream *yamux.Stream, address string, idle time.Duration) {
	if _, _, err := net.SplitHostPort(address); err != nil {
		_ = writeMessage(stream, response{Error: "invalid UDP address"})
		return
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "udp", address)
	if err != nil {
		_ = writeMessage(stream, response{Error: err.Error()})
		return
	}
	udp, ok := conn.(*net.UDPConn)
	if !ok {
		_ = conn.Close()
		_ = writeMessage(stream, response{Error: "expected UDP socket"})
		return
	}
	defer udp.Close()
	if writeMessage(stream, response{OK: true}) != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	relayUDPSocket(ctx, stream, udp, address, idle)
}

// A single idle timer observes both directions. On shutdown deadlines are set
// once by this owner, so another goroutine cannot restore a future read deadline.
func relayUDPSocket(ctx context.Context, stream *yamux.Stream, udp *net.UDPConn, fixed string, idle time.Duration) {
	done := make(chan struct{}, 2)
	activity := make(chan struct{}, 1)
	touch := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65535)
		for {
			n, addr, err := udp.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_ = stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if writeDatagram(stream, addr.String(), buf[:n]) != nil {
				return
			}
			touch()
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			address, payload, err := readDatagram(stream)
			if err != nil {
				return
			}
			_ = udp.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if fixed != "" {
				_, err = udp.Write(payload)
			} else {
				var destination *net.UDPAddr
				destination, err = net.ResolveUDPAddr("udp", address)
				if err != nil {
					continue
				}
				_, err = udp.WriteToUDP(payload, destination)
			}
			if err != nil {
				return
			}
			touch()
		}
	}()
	timer := time.NewTimer(idle)
	defer timer.Stop()
	completed := 0
	running := true
	for running {
		select {
		case <-ctx.Done():
			running = false
		case <-done:
			completed++
			running = false
		case <-timer.C:
			running = false
		case <-activity:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idle)
		}
	}
	_ = udp.Close()
	_ = stream.SetDeadline(time.Now())
	_ = stream.Close()
	for completed < 2 {
		<-done
		completed++
	}
}
