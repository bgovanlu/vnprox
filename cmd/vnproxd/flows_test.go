package main

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// observableGraph wraps an *inventory.Graph so a test can tell when the
// refresh loop has actually read it. Snapshot() is the only thing the loop
// calls, so one signal per Snapshot is one signal per refresh.
type observableGraph struct {
	inner     *inventory.Graph
	refreshed chan struct{}
}

func (o *observableGraph) Snapshot() inventory.Snapshot {
	s := o.inner.Snapshot()
	select {
	case o.refreshed <- struct{}{}:
	default:
	}
	return s
}

// TestFlowResolverRefreshLoopColdStart pins the startup behaviour that
// T-2108 found the hard way: a NetFlow record ingested between daemon start
// and the inventory graph's first successful poll got no srcRef/dstRef, and
// — since resolution happens once, at ingest, and is never retried — stayed
// unattributed forever. With a single 15s cadence that window was up to a
// full 15 seconds wide.
//
// The loop must therefore re-check on the cold-start cadence while its index
// is empty. The assertion is deliberately about *when the index becomes
// usable*, not about how many times the loop ticked: the latter would pass
// just as happily with the bug present, since the buggy loop also ticks.
func TestFlowResolverRefreshLoopColdStart(t *testing.T) {
	t.Parallel()

	graph := inventory.NewGraph()
	observed := &observableGraph{inner: graph, refreshed: make(chan struct{}, 16)}
	resolver := flow.NewGraphResolver()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		// A steady-state interval far longer than this test's patience: if
		// the loop ever falls back to it while still un-indexed, the test
		// times out instead of passing slowly.
		done <- runFlowResolverRefreshLoop(ctx, resolver, observed, time.Hour, 10*time.Millisecond)
	}()

	// Wait for the loop's own priming refresh to have happened against the
	// still-empty graph BEFORE populating it. Without this the test races
	// the goroutine: if priming ran after the poll below, the resolver would
	// be populated by the prime and the test would pass with or without the
	// cold-start cadence — which is exactly how an earlier draft of this
	// test passed against a deliberately broken loop.
	select {
	case <-observed.refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh loop never primed")
	}

	// Control: the daemon really does start with nothing to resolve
	// against, which is the precondition the whole fix is about.
	if got := resolver.Indexed(); got != 0 {
		t.Fatalf("resolver indexed %d CIDRs before any poll; the cold-start window this test covers does not exist", got)
	}
	if _, ok := resolver.Resolve("10.10.0.5"); ok {
		t.Fatal("resolver answered before any inventory poll")
	}

	// The graph converges, exactly as internal/collect's first poll would
	// make it.
	graph.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Addresses: []string{"10.10.0.11/24"}},
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if ref, ok := resolver.Resolve("10.10.0.5"); ok {
			if ref != "bridge:pve1:vmbr0" {
				t.Fatalf("Resolve = %q, want bridge:pve1:vmbr0", ref)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resolver never picked up the converged graph: the loop is not using the cold-start cadence while its index is empty")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("loop did not return after ctx cancellation")
	}
}

// TestFlowResolverRefreshLoopSettlesToSteadyInterval is the other half: once
// there IS an index, the loop must stop polling every second. Without this,
// "fix the cold start" could quietly become "poll the inventory graph once a
// second forever", which is the coupling flowResolverRefreshInterval's doc
// comment deliberately avoids.
func TestFlowResolverRefreshLoopSettlesToSteadyInterval(t *testing.T) {
	t.Parallel()

	graph := inventory.NewGraph()
	graph.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Addresses: []string{"10.10.0.11/24"}},
	})
	resolver := flow.NewGraphResolver()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runFlowResolverRefreshLoop(ctx, resolver, graph, time.Hour, 10*time.Millisecond) }()

	// Primed synchronously on entry, so this is immediate.
	deadline := time.Now().Add(time.Second)
	for resolver.Indexed() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("resolver never indexed the pre-populated graph")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Now remove the bridge. A loop still running on the 10ms cold-start
	// cadence would notice within milliseconds; one that has correctly
	// settled onto the 1h steady interval will not.
	graph.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: "pve1"}, nil)
	time.Sleep(150 * time.Millisecond)
	if resolver.Indexed() == 0 {
		t.Fatal("resolver re-indexed on the cold-start cadence after it already had an index; the loop never settles to the steady interval")
	}
}
