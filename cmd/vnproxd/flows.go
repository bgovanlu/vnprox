package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// flowResolverRefreshInterval is how often flow.GraphResolver re-indexes
// itself from the live inventory graph — decoupled from topology's own
// delta-push event stream (docs/architecture.md §3) since flow record
// resolution is explicitly best-effort/never-guessed, not required to be
// instantaneously fresh: a bridge/subnet that was added moments ago simply
// resolves starting from the next refresh tick, the same "poll loop, not
// wired to every single delta" tradeoff internal/collect's own poll
// intervals already make throughout this codebase.
const flowResolverRefreshInterval = 15 * time.Second

// flowResolverColdStartInterval is the cadence used before the resolver has
// indexed anything at all.
//
// The 15s steady-state tick above is a deliberate decoupling, and the
// tradeoff it documents — "a bridge added moments ago resolves from the next
// tick" — is fine. Daemon start is not that case: the very first
// RefreshFromGraph runs before internal/collect has polled anything, so the
// index is EMPTY, and resolution happens once at ingest and is never
// retried. Every flow record arriving in that window is therefore
// permanently unattributed, silently, with no way for an operator to tell it
// apart from an address that genuinely matches nothing. Polling a second
// apart until there is an index to resolve against closes that hole without
// touching the steady-state behaviour.
const flowResolverColdStartInterval = time.Second

// setupFlows builds T-1002's flow.Service — resolving srcRef/dstRef against
// graph, persisting to the bounded flow_samples ring (store.FlowSampleRepo),
// and pushing docs/api.md's `flow.batch` WS event — plus a supervised actor
// per enabled protocol listener (off by default; see config.FlowsConfig's
// doc comment and internal/flow's package doc comment for the enforced
// ring bound). Returns the Service (wired into api.Options.Flows via the
// local FlowLocalSource seam — actually store.FlowSampleRepo itself
// satisfies that seam directly, see server.go), the repo (for
// peer.ServerOptions.Flows via flowPeerAdapter), and the list of actors to
// register with the daemon's run group (the listeners plus the resolver
// refresh/prune loops) — cmd/vnproxd itself decides how those compose with
// every other supervised subsystem.
func setupFlows(cfg *config.Config, db *store.DB, graph *inventory.Graph, ws flow.Broadcaster, localNode func() string, logger *slog.Logger) (*flow.Service, *store.FlowSampleRepo, []func(context.Context) error) {
	flowRepo := store.NewFlowSampleRepo(db)
	resolver := flow.NewGraphResolver()

	// T-3706: seed flow_samples from a static dev fixture corpus before
	// anything else touches it, so it is present for the very first
	// microseg/baseline/flows read this daemon serves. See
	// flows_fixture.go's doc comment for why this is additive, not a
	// replacement for the real listeners/samplers below.
	if cfg.Flows.DevFixtureDir != "" {
		if _, err := loadFlowFixtures(context.Background(), flowRepo, cfg.Flows.DevFixtureDir, time.Now(), logger); err != nil {
			logger.Error("flows: failed to seed dev fixture corpus; flow_samples starts empty", "dir", cfg.Flows.DevFixtureDir, "error", err)
		}
	}

	svc := flow.New(flow.Config{
		Store:            flowRepo,
		Resolver:         resolver,
		WS:               ws,
		Logger:           logger,
		RetentionMinutes: cfg.Flows.RetentionMinutes,
		MaxRows:          cfg.Flows.MaxRows,
	})

	var actors []func(context.Context) error

	// The bounded ring's own prune tick (retention window AND hard row
	// cap — internal/flow's package doc comment).
	actors = append(actors, func(ctx context.Context) error {
		return svc.RunPruneLoop(ctx, flow.DefaultPruneInterval)
	})

	// Keeps the resolver's subnet/vnet index current without coupling flow
	// ingestion to topology's own delta push.
	actors = append(actors, func(ctx context.Context) error {
		return runFlowResolverRefreshLoop(ctx, resolver, graph, flowResolverRefreshInterval, flowResolverColdStartInterval)
	})

	ingest := func(ctx context.Context, records []flow.Record) {
		node := localNode()
		for i := range records {
			records[i].Node = node
		}
		svc.Ingest(ctx, records)
	}

	if cfg.Flows.SFlowEnabled {
		l := flow.NewSFlowListener(fmt.Sprintf("0.0.0.0:%d", cfg.Flows.SFlowPort), "")
		l.Logger = logger
		actors = append(actors, flowListenerActor(l, ingest, logger, "sflow"))
	}
	if cfg.Flows.NetFlowEnabled {
		// NewNetFlowListener sniffs each datagram's own version field to
		// dispatch between v5 and v9 decoding — see its doc comment for
		// why this is one listener/port, not two.
		l := flow.NewNetFlowListener(fmt.Sprintf("0.0.0.0:%d", cfg.Flows.NetFlowPort), "", flow.NewTemplateCache(nil))
		l.Logger = logger
		actors = append(actors, flowListenerActor(l, ingest, logger, "netflow"))
	}
	if cfg.Flows.IPFIXEnabled {
		l := flow.NewIPFIXListener(fmt.Sprintf("0.0.0.0:%d", cfg.Flows.IPFIXPort), "", flow.NewTemplateCache(nil))
		l.Logger = logger
		actors = append(actors, flowListenerActor(l, ingest, logger, "ipfix"))
	}

	return svc, flowRepo, actors
}

// flowListenerActor wraps l.Run as a runGroup actor: a bind failure (the
// only error Run ever returns once past a successful bind — see its doc
// comment) is logged and swallowed, not propagated, so a misconfigured or
// firewalled opt-in listener degrades this one feature rather than taking
// down the whole daemon (runGroup.run treats any non-nil actor return as
// fatal to every other actor too — see that type's doc comment).
func flowListenerActor(l *flow.Listener, ingest func(context.Context, []flow.Record), logger *slog.Logger, proto string) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := l.Run(ctx, ingest); err != nil {
			logger.Error("flow: listener failed to start; this protocol's flow ingestion is unavailable", "protocol", proto, "addr", l.Addr, "error", err)
		}
		return nil
	}
}

// runFlowResolverRefreshLoop re-indexes resolver from graph's live snapshot
// until ctx is cancelled, priming immediately so the first datagrams
// ingested after startup already have a populated index to resolve against.
//
// The cadence is coldStart until the resolver has indexed something, and
// interval from then on — see flowResolverColdStartInterval for why the
// startup window is treated differently from the steady state.
// graph is taken as flow.InventoryGraph rather than *inventory.Graph so a
// test can observe when a refresh has actually happened — the priming
// refresh on an empty graph leaves the index empty, which is otherwise
// indistinguishable from "the loop has not started yet", and a cold-start
// test that cannot tell those apart silently passes with the bug present.
func runFlowResolverRefreshLoop(ctx context.Context, resolver *flow.GraphResolver, graph flow.InventoryGraph, interval, coldStart time.Duration) error {
	resolver.RefreshFromGraph(graph)
	for {
		wait := interval
		if resolver.Indexed() == 0 {
			wait = coldStart
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
			resolver.RefreshFromGraph(graph)
		}
	}
}
