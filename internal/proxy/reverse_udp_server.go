package proxy

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apernet/hysteria/core/v2/extension"
	"github.com/apernet/quic-go"
)

// Each mapping owns its routing lock and UDP writer. Traffic to a slow local
// socket cannot block the connection-wide DATAGRAM reader or another mapping.
type reverseBinding struct {
	peer    *reversePeer
	socket  *net.UDPConn
	index   uint16
	timeout time.Duration
	mu      sync.Mutex
	closed  bool
	nextID  uint32
	sources map[netip.AddrPort]*reverseRoute
	routes  map[uint32]*reverseRoute
	queue   *udpQueue
}
type reverseRoute struct {
	id          uint32
	source      netip.AddrPort
	address     *net.UDPAddr
	addressText string
	last        int64 // guarded by the mapping's mutex
	active      atomic.Bool
	defrag      extension.Defragger // only the mapping's writer goroutine uses it
}

func newReverseBinding(peer *reversePeer, socket *net.UDPConn, index uint16, timeout time.Duration) *reverseBinding {
	return &reverseBinding{peer: peer, socket: socket, index: index, timeout: timeout, nextID: 1, sources: make(map[netip.AddrPort]*reverseRoute), routes: make(map[uint32]*reverseRoute), queue: newUDPQueue(&peer.budget)}
}
func (b *reverseBinding) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	for _, route := range b.routes {
		route.active.Store(false)
	}
	b.peer.budget.sessions.Add(-int64(len(b.routes)))
	b.routes = nil
	b.sources = nil
	b.mu.Unlock()
	_ = b.socket.Close()
	b.queue.close()
}
func (b *reverseBinding) route(source netip.AddrPort) *reverseRoute {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	route := b.sources[source]
	now := udpNow()
	if route == nil {
		if !b.peer.budget.newSession() {
			return nil
		}
		id := b.nextID
		for id == 0 || b.routes[id] != nil {
			id++
		}
		b.nextID = id + 1
		route = &reverseRoute{id: id, source: source, address: net.UDPAddrFromAddrPort(source), addressText: source.String()}
		route.active.Store(true)
		b.sources[source] = route
		b.routes[id] = route
		b.peer.expiry.add(now+int64(b.timeout), func(now int64) int64 { return b.expire(route, now) })
	}
	route.last = now
	return route
}
func (b *reverseBinding) expire(route *reverseRoute, now int64) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.routes[route.id] != route {
		return 0
	}
	next := route.last + int64(b.timeout)
	if next > now {
		return next
	}
	delete(b.routes, route.id)
	delete(b.sources, route.source)
	route.active.Store(false)
	b.peer.budget.sessions.Add(-1)
	return 0
}
func (b *reverseBinding) serve() {
	// If a mapping's socket fails, close registration so the client reconnects
	// and binds the complete listener set again.
	defer b.peer.Close()
	go b.writeReplies()
	reader := newUDPReader(b.socket, b.peer.budget.options.BatchSize)
	sender := extension.UDPSender{Conn: b.peer.conn, Prefix: reversePrefix(b.index)}
	for {
		packets, err := reader.read()
		for _, packet := range packets {
			route := b.route(packet.source)
			if route == nil {
				continue
			}
			message := extension.UDPMessage{SessionID: route.id, FragCount: 1, Addr: route.addressText, Data: packet.data}
			if err := sender.Send(&message); err != nil && b.peer.ctx.Err() != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
func (s *reverseServer) HandleDatagram(conn *quic.Conn, data []byte) bool {
	index, message, handled := parseReverseDatagram(data)
	if !handled || message == nil {
		return handled
	}
	s.mu.RLock()
	peer := s.peers[conn]
	s.mu.RUnlock()
	if peer == nil {
		return true
	}
	peer.mu.RLock()
	if int(index) >= len(peer.udp) {
		peer.mu.RUnlock()
		return true
	}
	b := peer.udp[index]
	peer.mu.RUnlock()
	b.mu.Lock()
	route := b.routes[message.SessionID]
	if route != nil {
		route.last = udpNow()
	}
	b.mu.Unlock()
	if route != nil {
		b.queue.push(udpQueuedPacket{message: message, route: route})
	}
	return true
}
func (b *reverseBinding) writeReplies() {
	batchSize := b.peer.budget.options.BatchSize
	writer := newUDPWriter(b.socket, batchSize)
	writes := make([]udpWritePacket, 0, batchSize)
	consume := func(p udpQueuedPacket) {
		if !p.route.active.Load() {
			return
		}
		message := p.route.defrag.Feed(p.message)
		if message != nil && len(message.Data) <= extension.MaxUDPSize {
			writes = append(writes, udpWritePacket{data: message.Data, address: p.route.address})
		}
	}
	for receiveUDPBatch(b.peer.ctx, b.queue, batchSize, consume) {
		if len(writes) > 0 {
			_, _ = writer.write(writes)
		}
		clear(writes)
		writes = writes[:0]
	}
}
