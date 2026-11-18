package config

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"time"
)

// FixedBandwidthBytes excludes switching between fixed rates and automatic
// congestion control. Existing rate parsing and units remain unchanged.
func FixedBandwidthBytes(b Bandwidth) (uint64, uint64, error) {
	up, down, err := BandwidthBytes(b)
	if err != nil {
		return 0, 0, err
	}
	if up == 0 || down == 0 {
		return 0, 0, errors.New("bandwidth hot reload requires explicit positive bandwidth.up and bandwidth.down")
	}
	return up, down, nil
}

func WatchClientBandwidth(ctx context.Context, path string, initial Client, apply func(Bandwidth) error) {
	watchBandwidth(ctx, path, initial.Bandwidth, "client", func(data []byte) (Bandwidth, error) {
		var next Client
		if err := decode(data, &next); err != nil {
			return Bandwidth{}, err
		}
		if err := next.Validate(); err != nil {
			return Bandwidth{}, err
		}
		bandwidth := next.Bandwidth
		next.Bandwidth = initial.Bandwidth
		if !reflect.DeepEqual(next, initial) {
			return Bandwidth{}, errors.New("changes outside bandwidth require a restart")
		}
		return bandwidth, nil
	}, apply)
}

func WatchServerBandwidth(ctx context.Context, path string, initial Server, apply func(Bandwidth) error) {
	if initial.IgnoreClientBandwidth {
		slog.Info("bandwidth hot reload disabled", "role", "server", "reason", "ignoreClientBandwidth is enabled")
		return
	}
	watchBandwidth(ctx, path, initial.Bandwidth, "server", func(data []byte) (Bandwidth, error) {
		var next Server
		if err := decode(data, &next); err != nil {
			return Bandwidth{}, err
		}
		if err := next.Validate(); err != nil {
			return Bandwidth{}, err
		}
		bandwidth := next.Bandwidth
		next.Bandwidth = initial.Bandwidth
		if !reflect.DeepEqual(next, initial) {
			return Bandwidth{}, errors.New("changes outside bandwidth require a restart")
		}
		return bandwidth, nil
	}, apply)
}

func watchBandwidth(ctx context.Context, path string, initial Bandwidth, role string, inspect func([]byte) (Bandwidth, error), apply func(Bandwidth) error) {
	if _, _, err := FixedBandwidthBytes(initial); err != nil {
		slog.Info("bandwidth hot reload disabled", "role", role, "reason", err)
		return
	}
	slog.Info("bandwidth hot reload watching", "role", role, "config", path)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	current := initial
	var candidate, processed []byte
	var haveCandidate, haveProcessed bool
	var lastReadError string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		data, err := os.ReadFile(path)
		if err != nil {
			haveCandidate = false
			if err.Error() != lastReadError {
				slog.Warn("bandwidth reload rejected; keeping current settings", "role", role, "error", err)
				lastReadError = err.Error()
			}
			continue
		}
		lastReadError = ""
		// Read twice with a quiet interval so an editor's intermediate write is
		// not applied. Reopening the path also notices atomic file replacement.
		if !haveCandidate || !bytes.Equal(data, candidate) {
			candidate = bytes.Clone(data)
			haveCandidate = true
			continue
		}
		if haveProcessed && bytes.Equal(data, processed) {
			continue
		}
		processed = bytes.Clone(data)
		haveProcessed = true
		next, err := inspect(data)
		if err == nil {
			_, _, err = FixedBandwidthBytes(next)
		}
		if err == nil && next != current {
			err = apply(next)
			if err == nil {
				current = next
				slog.Info("bandwidth config reloaded", "role", role, "up", next.Up, "down", next.Down, "disableLossCompensation", next.DisableLossCompensation)
			}
		}
		if err != nil {
			slog.Warn("bandwidth reload rejected; keeping current settings", "role", role, "error", err)
		}
	}
}
