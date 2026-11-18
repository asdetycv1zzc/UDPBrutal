package proxy

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net"
	"sync"
	"time"

	"github.com/apernet/hysteria/core/v2/extension"
	"github.com/apernet/quic-go"
	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
)

const bandwidthControlType uint64 = 0x554203

type bandwidthLimits struct {
	up, down                uint64 // bytes/s, before loss compensation
	disableLossCompensation bool
	revision                uint64
}

// Each endpoint owns its own revision. An ACK refers only to the other
// endpoint's revision on this stream, so concurrent edits cannot overwrite it.
type bandwidthMessage struct {
	Version  uint8  `json:"version"`
	Revision uint64 `json:"revision"`
	Ack      uint64 `json:"ack"`
	Up       uint64 `json:"up"`
	Down     uint64 `json:"down"`
}

type bandwidthManager struct {
	mu      sync.RWMutex
	local   bandwidthLimits
	role    string
	enabled bool
	closed  bool
	peers   map[*quic.Conn]*bandwidthPeer
}

func newBandwidthManager(b config.Bandwidth, role string, allow bool) (*bandwidthManager, error) {
	up, down, err := config.BandwidthBytes(b)
	if err != nil {
		return nil, err
	}
	return &bandwidthManager{
		local: bandwidthLimits{up: up, down: down, disableLossCompensation: b.DisableLossCompensation, revision: 1},
		role:  role, enabled: allow && up > 0 && down > 0,
		peers: make(map[*quic.Conn]*bandwidthPeer),
	}, nil
}

func (m *bandwidthManager) snapshot() bandwidthLimits {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.local
}

func (m *bandwidthManager) reload(b config.Bandwidth) error {
	up, down, err := config.FixedBandwidthBytes(b)
	if err != nil {
		return err
	}
	if m.role == "server" && (up < 65536 || down < 65536) {
		return errors.New("server bandwidth must be at least 65536 bytes/s")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return net.ErrClosed
	}
	if !m.enabled {
		return errors.New("bandwidth hot reload requires fixed bandwidth on both endpoints and ignoreClientBandwidth disabled")
	}
	if m.local.up == up && m.local.down == down && m.local.disableLossCompensation == b.DisableLossCompensation {
		return nil
	}
	m.local = bandwidthLimits{up: up, down: down, disableLossCompensation: b.DisableLossCompensation, revision: m.local.revision + 1}
	for _, p := range m.peers {
		p.wake()
	}
	slog.Info("bandwidth update queued", "role", m.role, "revision", m.local.revision, "active_peers", len(m.peers))
	return nil
}

func (m *bandwidthManager) add(p *bandwidthPeer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return net.ErrClosed
	}
	if m.peers[p.conn] != nil {
		return errors.New("bandwidth control stream already exists")
	}
	m.peers[p.conn] = p
	return nil
}

func (m *bandwidthManager) remove(p *bandwidthPeer) {
	m.mu.Lock()
	if m.peers[p.conn] == p {
		delete(m.peers, p.conn)
	}
	m.mu.Unlock()
}

func (m *bandwidthManager) isClosed() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closed
}

func (m *bandwidthManager) close() {
	m.mu.Lock()
	m.closed = true
	peers := make([]*bandwidthPeer, 0, len(m.peers))
	for _, p := range m.peers {
		peers = append(peers, p)
	}
	m.mu.Unlock()
	for _, p := range peers {
		p.close(net.ErrClosed)
	}
}

func (m *bandwidthManager) HandleStream(conn *quic.Conn, kind uint64, stream *quic.Stream) (bool, error) {
	if kind != bandwidthControlType {
		return false, nil
	}
	if !m.enabled {
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return true, nil
	}
	go m.serve(conn, stream)
	return true, nil
}

type bandwidthPeer struct {
	manager *bandwidthManager
	conn    *quic.Conn
	stream  *quic.Stream
	ctx     context.Context
	cancel  context.CancelCauseFunc
	once    sync.Once
	notify  chan struct{}
	mu      sync.Mutex
	remote  bandwidthMessage
	acked   uint64

	// Only the stream writer accesses the applied/sent state.
	applied                     bool
	tx                          uint64
	disabled                    bool
	appliedLocal, appliedRemote uint64
	sentRevision, sentAck       uint64
}

func (m *bandwidthManager) newPeer(conn *quic.Conn, stream *quic.Stream) (*bandwidthPeer, error) {
	ctx, cancel := context.WithCancelCause(conn.Context())
	p := &bandwidthPeer{manager: m, conn: conn, stream: stream, ctx: ctx, cancel: cancel, notify: make(chan struct{}, 1)}
	if err := m.add(p); err != nil {
		p.close(err)
		return nil, err
	}
	return p, nil
}

func (p *bandwidthPeer) close(err error) {
	p.once.Do(func() {
		p.cancel(err)
		p.stream.CancelRead(0)
		p.stream.CancelWrite(0)
	})
}

func (p *bandwidthPeer) wake() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (p *bandwidthPeer) receive(message bandwidthMessage) error {
	if message.Version != 1 || message.Revision == 0 || message.Up == 0 || message.Down == 0 || message.Up > math.MaxInt64 || message.Down > math.MaxInt64 {
		return errors.New("invalid fixed-bandwidth control message")
	}
	if message.Ack > p.manager.snapshot().revision {
		return errors.New("bandwidth acknowledgement refers to a future revision")
	}
	p.mu.Lock()
	previous := p.remote
	if message.Revision < previous.Revision || (message.Revision == previous.Revision && (message.Up != previous.Up || message.Down != previous.Down)) || message.Ack < p.acked {
		p.mu.Unlock()
		return errors.New("inconsistent bandwidth control revision")
	}
	changed := message.Revision > previous.Revision
	p.remote = message
	newAck := message.Ack > p.acked
	p.acked = message.Ack
	p.mu.Unlock()
	if newAck && message.Ack > 1 {
		slog.Info("bandwidth update acknowledged", "role", p.manager.role, "peer", p.conn.RemoteAddr(), "revision", message.Ack)
	}
	if changed {
		p.wake()
	}
	return nil
}

func bandwidthAdvertisement(local bandwidthLimits, ack uint64) bandwidthMessage {
	return bandwidthMessage{Version: 1, Revision: local.revision, Ack: ack, Up: local.up, Down: local.down}
}

func (p *bandwidthPeer) applyAndSend() error {
	local := p.manager.snapshot()
	p.mu.Lock()
	remote := p.remote
	p.mu.Unlock()
	if remote.Revision == 0 {
		return errors.New("missing peer bandwidth limits")
	}
	tx, rx := min(local.up, remote.Down), min(local.down, remote.Up)
	if !p.applied || p.tx != tx || p.disabled != local.disableLossCompensation {
		if err := extension.UpdateBrutalBandwidth(p.conn, tx, local.disableLossCompensation); err != nil {
			return err
		}
		p.tx, p.disabled = tx, local.disableLossCompensation
	}
	if !p.applied || p.appliedLocal != local.revision || p.appliedRemote != remote.Revision {
		p.applied, p.appliedLocal, p.appliedRemote = true, local.revision, remote.Revision
		slog.Info("bandwidth applied", "role", p.manager.role, "peer", p.conn.RemoteAddr(), "local_revision", local.revision, "peer_revision", remote.Revision,
			"tx_mbps", float64(tx)*8/1e6, "rx_target_mbps", float64(rx)*8/1e6, "disableLossCompensation", local.disableLossCompensation)
	}
	if p.sentRevision == local.revision && p.sentAck == remote.Revision {
		return nil
	}
	_ = p.stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := writeMessage(p.stream, bandwidthAdvertisement(local, remote.Revision))
	_ = p.stream.SetWriteDeadline(time.Time{})
	if err == nil {
		p.sentRevision, p.sentAck = local.revision, remote.Revision
	}
	return err
}

func (p *bandwidthPeer) run() error {
	defer p.manager.remove(p)
	defer p.close(net.ErrClosed)
	go func() {
		for {
			var message bandwidthMessage
			if err := readMessage(p.stream, &message); err != nil {
				p.close(err)
				return
			}
			if err := p.receive(message); err != nil {
				p.close(err)
				return
			}
		}
	}()
	p.wake() // include edits that happened during the initial exchange
	for {
		select {
		case <-p.ctx.Done():
			return context.Cause(p.ctx)
		case <-p.notify:
			if err := p.applyAndSend(); err != nil {
				return err
			}
		}
	}
}

func (m *bandwidthManager) serve(conn *quic.Conn, stream *quic.Stream) {
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	kind, err := readFrameType(stream)
	if err != nil || kind != bandwidthControlType {
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return
	}
	p, err := m.newPeer(conn, stream)
	if err != nil {
		return
	}
	defer m.remove(p)
	defer p.close(net.ErrClosed)
	var message bandwidthMessage
	if err = readMessage(stream, &message); err == nil {
		err = p.receive(message)
	}
	if err == nil {
		err = p.applyAndSend()
	}
	if err == nil {
		_ = stream.SetDeadline(time.Time{})
		err = p.run()
	}
	if err != nil && conn.Context().Err() == nil && !m.isClosed() {
		slog.Warn("bandwidth control stream closed; waiting for client retry", "peer", conn.RemoteAddr(), "error", err)
	}
}

func (m *bandwidthManager) openClient(conn *quic.Conn) (*bandwidthPeer, error) {
	ctx, cancel := context.WithTimeout(conn.Context(), 10*time.Second)
	defer cancel()
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	p, err := m.newPeer(conn, stream)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			p.close(net.ErrClosed)
			m.remove(p)
		}
	}()
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	if err := writeFrameType(stream, bandwidthControlType); err != nil {
		return nil, err
	}
	local := m.snapshot()
	if err := writeMessage(stream, bandwidthAdvertisement(local, 0)); err != nil {
		return nil, err
	}
	p.sentRevision = local.revision
	var reply bandwidthMessage
	if err := readMessage(stream, &reply); err != nil {
		return nil, err
	}
	if err := p.receive(reply); err != nil {
		return nil, err
	}
	if err := p.applyAndSend(); err != nil {
		return nil, err
	}
	_ = stream.SetDeadline(time.Time{})
	success = true
	return p, nil
}

func (m *bandwidthManager) maintainClient(conn *quic.Conn, first *bandwidthPeer) {
	p := first
	for conn.Context().Err() == nil && !m.isClosed() {
		if p != nil {
			err := p.run()
			if conn.Context().Err() != nil || m.isClosed() {
				return
			}
			slog.Warn("bandwidth control stream interrupted; retrying without reconnecting the tunnel", "error", err)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-conn.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		var err error
		p, err = m.openClient(conn)
		if err != nil {
			slog.Debug("bandwidth control retry failed", "error", err)
		}
	}
}
