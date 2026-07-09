package main

import "context"

// actor is one supervised subsystem: it must run until ctx is cancelled (or
// it fails on its own), then return promptly.
type actor func(ctx context.Context) error

// runGroup runs a fixed set of actors concurrently and stops all of them as
// soon as any one returns (mirroring the "run group"/oklog-run actor
// pattern), or as soon as the parent context is cancelled. This is the
// supervision point cmd/vnproxd wires every long-lived subsystem into: the
// HTTPS server and the TLS cert watcher today; T-003's store-maintenance
// jobs (metric pruning) and T-101's PVE/host collectors are expected to
// register here too via additional calls to add(), each as a small closure
// that owns its own shutdown path.
//
// Every actor MUST return once ctx is cancelled — run() will otherwise
// block forever waiting for it, which is exactly the graceful-shutdown
// contract acceptance criterion 3 depends on.
type runGroup struct {
	actors []actor
}

func (g *runGroup) add(fn actor) {
	g.actors = append(g.actors, fn)
}

// run starts every registered actor, waits for all of them to return, and
// reports the first non-nil error (if any). It cancels the shared
// sub-context as soon as one actor returns (successfully or not) so the
// rest wind down too, and also honors cancellation of the parent ctx (e.g.
// on SIGTERM).
func (g *runGroup) run(ctx context.Context) error {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, len(g.actors))
	for _, fn := range g.actors {
		fn := fn
		go func() {
			errs <- fn(subCtx)
		}()
	}

	var firstErr error
	for range g.actors {
		if err := <-errs; err != nil && firstErr == nil {
			firstErr = err
		}
		cancel() // as soon as one actor returns, tell the rest to stop
	}
	return firstErr
}
