package quic

import (
	"errors"

	"github.com/apernet/quic-go/internal/monotime"
)

// A batch submits distinct, already-packed UDP datagrams. It never concatenates
// payloads, requests UDP_SEGMENT, waits for more data or makes pacing decisions.
type packetBatchWriter interface {
	Write([]queueEntry) (int, error)
	Unsupported(error) bool
}

func batchEligible(entries []queueEntry) bool {
	for _, e := range entries {
		// A queued GSO buffer must still use the upstream segmentation and
		// fallback path, including after GSO has failed for this connection.
		if e.gsoSize != 0 {
			return false
		}
	}
	return true
}

func (h *sendQueue) writeQueued(entries []queueEntry, batch *packetBatchWriter) error {
	defer func() {
		for i := range entries {
			entries[i].buf.Release()
			entries[i] = queueEntry{}
		}
	}()

	remaining := entries
	if *batch != nil && len(remaining) > 1 && batchEligible(remaining) {
		for len(remaining) > 1 {
			n, err := h.writeBatch(*batch, remaining)
			// An accepted prefix must never be submitted again, even if a
			// subsequent datagram failed or the batch was only partially sent.
			remaining = remaining[n:]
			if err != nil || n == 0 {
				if d := h.diagnostics.Load(); d != nil {
					d.batchFallbacks.Add(1)
				}
				if n == 0 && err == nil || (*batch).Unsupported(err) {
					*batch = nil
				}
				// Retry only the unaccepted suffix through ordinary writes.
				// This also preserves per-datagram MTU error handling.
				break
			}
		}
	}
	for _, e := range remaining {
		if err := h.writePacket(e); err != nil {
			// Preserve upstream's nonfatal oversized-datagram handling, so
			// PMTU probes and later loss detection retain their semantics.
			var tooLarge *DatagramTooLargeError
			if !isSendMsgSizeErr(err) && !errors.As(err, &tooLarge) {
				return err
			}
		}
	}
	return nil
}

func (h *sendQueue) writePacket(e queueEntry) error {
	d := h.diagnostics.Load()
	started := beginSocketWrite(d)
	err := h.conn.Write(e.buf.Data, e.gsoSize, e.ecn)
	bytes := 0
	if err == nil {
		bytes = len(e.buf.Data)
	}
	finishSocketWrite(d, started, bytes, err)
	return err
}

func (h *sendQueue) writeBatch(w packetBatchWriter, entries []queueEntry) (int, error) {
	d := h.diagnostics.Load()
	started := beginSocketWrite(d)
	if d != nil {
		d.batchCalls.Add(1)
	}
	n, err := w.Write(entries)
	bytes := 0
	for _, e := range entries[:n] {
		bytes += len(e.buf.Data)
	}
	if d != nil {
		d.batchPackets.Add(uint64(n))
	}
	finishSocketWrite(d, started, bytes, err)
	return n, err
}

func beginSocketWrite(d *socketWriteDiagnostics) monotime.Time {
	if d == nil {
		return 0
	}
	now := monotime.Now()
	d.calls.Add(1)
	d.started.Store(int64(now))
	return now
}

func finishSocketWrite(d *socketWriteDiagnostics, started monotime.Time, bytes int, err error) {
	if d == nil {
		return
	}
	done := monotime.Now()
	d.lastDuration.Store(int64(done.Sub(started)))
	d.completed.Store(int64(done))
	d.bytes.Add(uint64(bytes))
	if err != nil {
		d.errors.Add(1)
	}
	d.started.Store(0)
}
