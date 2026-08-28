// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/neighbor"
)

// neighborFlapProvider is the seam neighborFlapAdapter needs from
// *neighbor.HistoryRecorder — a stand-alone interface (rather than
// depending on *neighbor.HistoryRecorder directly) purely so tests can
// substitute a fake, the same seam-per-adapter convention
// storeCapacityAdapter/rogueScanAdapter establish.
type neighborFlapProvider interface {
	Flaps(ctx context.Context, now time.Time) ([]neighbor.FlapEvent, error)
}

// neighborFlapAdapter (T-3905) adapts internal/neighbor.HistoryRecorder's
// persisted flap detector into findings.NeighborFlapProvider — see
// internal/findings/health_neighborflap.go's doc comment for how this
// check relates to arp_spoof_suspected. A nil recorder makes NeighborFlaps
// return (nil, nil), the same nil-safe degradation every other findings
// adapter in this package uses.
type neighborFlapAdapter struct {
	baseCtx  context.Context //nolint:containedctx // daemon shutdown ctx — see ipamFindingsAdapter.baseCtx
	recorder neighborFlapProvider
	now      func() time.Time
	logger   *slog.Logger
}

func (a neighborFlapAdapter) NeighborFlaps() ([]findings.NeighborFlapReading, error) {
	if a.recorder == nil {
		return nil, nil
	}
	ctx, cancel := findingsAdapterCtx(a.baseCtx)
	defer cancel()
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	events, err := a.recorder.Flaps(ctx, now())
	if err != nil {
		a.logger.Warn("neighbor: reading binding flap report", "error", err)
		return nil, err
	}
	out := make([]findings.NeighborFlapReading, 0, len(events))
	for _, e := range events {
		out = append(out, findings.NeighborFlapReading{
			Node: e.Node, Kind: findings.NeighborFlapKind(e.Kind),
			IP: e.IP, MAC: e.MAC, Count: e.Count, IPs: e.IPs,
		})
	}
	return out, nil
}
