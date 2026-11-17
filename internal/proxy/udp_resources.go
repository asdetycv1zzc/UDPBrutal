package proxy

import (
	"container/heap"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apernet/hysteria/core/v2/extension"
	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
)

var udpEpoch = time.Now()

func udpNow() int64 { return time.Since(udpEpoch).Nanoseconds() }

// Budgets are per registered peer on the server, and per client on the client.
// Queued-byte accounting follows each entry until it leaves a local queue.
type udpBudget struct {
	options     config.ReverseUDPOptions
	sessions    atomic.Int64
	queuedBytes atomic.Int64
}

func reserve(counter *atomic.Int64, n, limit int64) bool {
	for {
		old := counter.Load()
		if n > limit-old {
			return false
		}
		if counter.CompareAndSwap(old, old+n) {
			return true
		}
	}
}

func (b *udpBudget) newSession() bool {
	return reserve(&b.sessions, 1, int64(b.options.MaxSessions))
}

type udpQueuedPacket struct {
	message *extension.UDPMessage
	route   *reverseRoute // server return path only
	size    int64
}

type udpQueue struct {
	mu      sync.Mutex
	closed  bool
	packets chan udpQueuedPacket
	budget  *udpBudget
}

func newUDPQueue(budget *udpBudget) *udpQueue {
	return &udpQueue{packets: make(chan udpQueuedPacket, budget.options.QueuePackets), budget: budget}
}

// Full queues drop new datagrams. The connection's sole DATAGRAM reader must
// never wait on a local UDP socket or a slow reverse destination.
func (q *udpQueue) push(p udpQueuedPacket) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || len(q.packets) == cap(q.packets) {
		return false
	}
	p.size = int64(p.message.Size() + 10)
	if !reserve(&q.budget.queuedBytes, p.size, int64(q.budget.options.MaxQueuedBytes)) {
		return false
	}
	q.packets <- p
	return true
}

func (q *udpQueue) release(p udpQueuedPacket) {
	q.budget.queuedBytes.Add(-p.size)
}

func (q *udpQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.packets)
	for p := range q.packets {
		q.release(p)
	}
}

// One monotonic deadline heap and timer replace periodic map scans and the
// per-session timers. Traffic only updates last activity; heap entries move
// when their old deadline is reached, rather than on every packet.
type udpExpiryEntry struct {
	deadline int64
	expire   func(int64) int64 // next deadline, or zero after removal
	index    int
	canceled bool
}
type udpExpiryHeap []*udpExpiryEntry

func (h udpExpiryHeap) Len() int           { return len(h) }
func (h udpExpiryHeap) Less(i, j int) bool { return h[i].deadline < h[j].deadline }
func (h udpExpiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *udpExpiryHeap) Push(v any) {
	entry := v.(*udpExpiryEntry)
	entry.index = len(*h)
	*h = append(*h, entry)
}
func (h *udpExpiryHeap) Pop() any {
	a := *h
	i := len(a) - 1
	v := a[i]
	v.index = -1
	a[i] = nil
	*h = a[:i]
	return v
}

type udpExpiry struct {
	ctx  context.Context
	mu   sync.Mutex
	heap udpExpiryHeap
	wake chan struct{}
}

func newUDPExpiry(ctx context.Context) *udpExpiry {
	e := &udpExpiry{ctx: ctx, wake: make(chan struct{}, 1)}
	go e.run(ctx)
	return e
}
func (e *udpExpiry) add(deadline int64, expire func(int64) int64) *udpExpiryEntry {
	entry := &udpExpiryEntry{deadline: deadline, expire: expire, index: -1}
	e.put(entry)
	return entry
}
func (e *udpExpiry) remove(entry *udpExpiryEntry) {
	if entry == nil {
		return
	}
	e.mu.Lock()
	entry.canceled = true
	if entry.index >= 0 {
		heap.Remove(&e.heap, entry.index)
	}
	e.mu.Unlock()
}
func (e *udpExpiry) put(entry *udpExpiryEntry) {
	e.mu.Lock()
	if entry.canceled || e.ctx.Err() != nil {
		e.mu.Unlock()
		return
	}
	heap.Push(&e.heap, entry)
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}
func (e *udpExpiry) run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	defer func() {
		e.mu.Lock()
		for _, entry := range e.heap {
			entry.index = -1
			entry.canceled = true
		}
		e.heap = nil
		e.mu.Unlock()
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		now := udpNow()
		wait := time.Hour
		var due *udpExpiryEntry
		e.mu.Lock()
		if len(e.heap) > 0 {
			if e.heap[0].deadline <= now {
				due = heap.Pop(&e.heap).(*udpExpiryEntry)
			} else {
				wait = time.Duration(e.heap[0].deadline - now)
			}
		}
		e.mu.Unlock()
		if due != nil {
			if next := due.expire(now); next > 0 {
				due.deadline = next
				e.put(due)
			}
			continue
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return
		case <-e.wake:
		case <-timer.C:
		}
	}
}
