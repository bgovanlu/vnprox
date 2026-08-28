// SPDX-License-Identifier: Apache-2.0

package telemetrycollector

// retention.go is T-3710 AC4: "retention actually expires data. Demonstrate
// it, with a shortened window if necessary." RunOnce is the mechanism;
// cmd/vnproxtelemetryd wires it into a background loop for the daemon and
// exposes it as a one-shot `retention-run` subcommand precisely so the
// shortened-window demonstration does not require waiting for a real
// window to elapse.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// DefaultRetentionWindow is how long a submission is kept before it is
// deleted. 180 days: long enough to see a PVE point-release's adoption
// curve, short enough that this collector is not quietly becoming a
// permanent archive of every install that ever opted in.
const DefaultRetentionWindow = 180 * 24 * time.Hour

// RunOnce deletes every submission received before now-window and returns
// how many rows were removed.
func RunOnce(ctx context.Context, store *Store, now time.Time, window time.Duration) (int64, error) {
	if window <= 0 {
		window = DefaultRetentionWindow
	}
	cutoff := now.Add(-window)
	n, err := store.Prune(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("telemetrycollector: retention pass: %w", err)
	}
	return n, nil
}

// RunLoop runs RunOnce every interval until ctx is cancelled, logging
// failures via logger (nil discards them) rather than stopping the loop —
// the same non-fatal-background-loop contract internal/store's
// RunSnapshotRetentionLoop uses.
func RunLoop(ctx context.Context, store *Store, interval, window time.Duration, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			n, err := RunOnce(ctx, store, now, window)
			switch {
			case err == nil:
				if n > 0 {
					logger.Info("telemetrycollector: retention pass complete", "deleted", n, "window", window.String())
				}
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				// Shutting down; not a failure worth logging.
			default:
				logger.Warn("telemetrycollector: retention pass failed", "error", err.Error())
			}
		}
	}
}
