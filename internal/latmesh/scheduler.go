package latmesh

import (
	"context"
	"time"
)

// RunTicker is this package's shared scheduler primitive: run fn
// immediately, then again every interval, until ctx is cancelled — the
// exact ticker-loop shape Service.RunLoop/RunPruneLoop below both need.
// Exported so a sibling package built directly on this task's probe
// infrastructure (internal/mtuprobe, T-1306, on its own coarser interval)
// reuses the identical scheduling mechanism rather than hand-rolling a
// second implementation of the same loop — docs/development.md's "every
// goroutine has an owner and a shutdown path" convention, expressed once
// instead of copy-pasted per package.
func RunTicker(ctx context.Context, interval time.Duration, fn func(context.Context)) error {
	fn(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			fn(ctx)
		}
	}
}
