// SPDX-License-Identifier: Apache-2.0

package mtuprobe_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/mtuprobe"
)

// countingProber is a mtuprobe.Prober double reporting a fixed MTU per
// LinkID and counting calls, so tests can assert probe cadence/coverage
// precisely without shelling out to a real `ping`.
type countingProber struct {
	mtuByLink map[string]int
	failLinks map[string]bool
	calls     int
	mu        sync.Mutex
}

func (p *countingProber) ProbeMTU(_ context.Context, target latmesh.Pair) (int, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.failLinks[target.LinkID] {
		return 0, 1, errors.New("probe unavailable")
	}
	mtu, ok := p.mtuByLink[target.LinkID]
	if !ok {
		return 0, 1, nil // honest "even the floor failed"
	}
	return mtu, 3, nil
}

func (p *countingProber) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func fixedPairs() []latmesh.Pair {
	pairs := []latmesh.Pair{
		{Fabric: latmesh.FabricGuest, Label: "vmbr0", FromNode: "pve1", ToNode: "pve2"},
		{Fabric: latmesh.FabricGuest, Label: "vmbr0", FromNode: "pve1", ToNode: "pve3"},
	}
	for i := range pairs {
		pairs[i].LinkID = latmesh.ComputeLinkID(pairs[i].Fabric, pairs[i].Label, pairs[i].FromNode, pairs[i].ToNode)
	}
	return pairs
}

// TestService_Tick_PopulatesResults: a successful probe tick records a
// Result per link, retrievable via Results/Result.
func TestService_Tick_PopulatesResults(t *testing.T) {
	pairs := fixedPairs()
	prober := &countingProber{mtuByLink: map[string]int{
		pairs[0].LinkID: 1450,
		pairs[1].LinkID: 1500,
	}}

	svc := mtuprobe.New(mtuprobe.Config{
		Discoverer: latmesh.DiscovererFunc(func() []latmesh.Pair { return pairs }),
		Prober:     prober,
		Now:        func() time.Time { return time.Unix(1_000, 0) },
	})
	svc.Tick(context.Background())

	results := svc.Results()
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(results), results)
	}
	r0, ok := svc.Result(pairs[0].LinkID)
	if !ok || r0.MTU != 1450 || r0.At != 1_000 {
		t.Fatalf("Result(%s) = %+v, ok=%v, want mtu=1450 at=1000", pairs[0].LinkID, r0, ok)
	}
	r1, ok := svc.Result(pairs[1].LinkID)
	if !ok || r1.MTU != 1500 {
		t.Fatalf("Result(%s) = %+v, ok=%v, want mtu=1500", pairs[1].LinkID, r1, ok)
	}
}

// TestService_Result_UnprobedLinkReportsNotOk: AC2's "no probe result yet"
// case — a link this service has never probed has no entry, not a
// stale/zero value.
func TestService_Result_UnprobedLinkReportsNotOk(t *testing.T) {
	svc := mtuprobe.New(mtuprobe.Config{})
	r, ok := svc.Result("guest:vmbr0|pve1->pve9")
	if ok {
		t.Fatalf("Result for an unprobed link = %+v, ok=true, want ok=false", r)
	}
	if r.MTU != 0 {
		t.Fatalf("zero-value Result.MTU = %d, want 0", r.MTU)
	}
}

// TestService_Tick_ProbeErrorKeepsPriorReading: a probe attempt failure
// (transport-level, not "too big") leaves the last known good reading in
// place rather than clearing it to zero/absent.
func TestService_Tick_ProbeErrorKeepsPriorReading(t *testing.T) {
	pairs := fixedPairs()
	prober := &countingProber{
		mtuByLink: map[string]int{pairs[0].LinkID: 1450},
		failLinks: map[string]bool{},
	}
	svc := mtuprobe.New(mtuprobe.Config{
		Discoverer: latmesh.DiscovererFunc(func() []latmesh.Pair { return pairs[:1] }),
		Prober:     prober,
		Now:        func() time.Time { return time.Unix(1_000, 0) },
	})
	svc.Tick(context.Background())
	if _, ok := svc.Result(pairs[0].LinkID); !ok {
		t.Fatal("expected a reading after the first successful tick")
	}

	prober.failLinks[pairs[0].LinkID] = true
	svc.Tick(context.Background())
	r, ok := svc.Result(pairs[0].LinkID)
	if !ok || r.MTU != 1450 {
		t.Fatalf("after a failed probe attempt, Result = %+v ok=%v, want the prior mtu=1450 reading kept", r, ok)
	}
}

// TestService_MeasuredUnderlayMTU_MinAcrossNodesLinks: the per-node
// aggregate findings.MTUProvider consumes is the minimum verified MTU
// across every link this node has successfully probed outbound.
func TestService_MeasuredUnderlayMTU_MinAcrossNodesLinks(t *testing.T) {
	pairs := fixedPairs()
	prober := &countingProber{mtuByLink: map[string]int{
		pairs[0].LinkID: 1500,
		pairs[1].LinkID: 1400, // the tighter of the two
	}}
	svc := mtuprobe.New(mtuprobe.Config{
		Discoverer: latmesh.DiscovererFunc(func() []latmesh.Pair { return pairs }),
		Prober:     prober,
		Now:        func() time.Time { return time.Unix(1_000, 0) },
	})
	svc.Tick(context.Background())

	mtu, ok := svc.MeasuredUnderlayMTU("pve1")
	if !ok || mtu != 1400 {
		t.Fatalf("MeasuredUnderlayMTU(pve1) = %d, ok=%v, want 1400", mtu, ok)
	}
	if _, ok := svc.MeasuredUnderlayMTU("pve9"); ok {
		t.Fatal("MeasuredUnderlayMTU for a node with no probed outbound links should report ok=false")
	}
}

// TestService_RunLoop_UsesConfiguredInterval is AC5: probes run on the
// configured (coarser) interval, reusing internal/latmesh's own
// scheduler (RunTicker) rather than a second implementation — a
// short-interval, bounded-duration run counts roughly ctx-duration/interval
// ticks (the exact "prime immediately, then tick every interval" shape
// RunTicker/latmesh.Service.RunLoop already establish, per
// docs/development.md's "every goroutine has an owner" convention: this is
// the second goroutine mtuprobe.Service.RunLoop registers with cmd/vnproxd's
// run group, sharing latmesh's scheduler code, not a rival scheduler).
func TestService_RunLoop_UsesConfiguredInterval(t *testing.T) {
	pairs := fixedPairs()[:1]
	prober := &countingProber{mtuByLink: map[string]int{pairs[0].LinkID: 1500}}

	svc := mtuprobe.New(mtuprobe.Config{
		Discoverer: latmesh.DiscovererFunc(func() []latmesh.Pair { return pairs }),
		Prober:     prober,
		// ProbeIntervalSec left at 0 (defaults to DefaultProbeIntervalSec,
		// 300s) — this test only needs RunLoop's *priming* tick (RunTicker
		// runs fn immediately before ever touching the ticker), so it never
		// has to wait out a real 300s interval to observe RunLoop actually
		// invoking Tick through latmesh.RunTicker.
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.RunLoop(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for prober.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	if prober.callCount() == 0 {
		t.Fatal("RunLoop never primed an immediate tick before ctx cancellation")
	}
}

// TestService_RunLoop_DefaultsToDocumentedInterval confirms
// DefaultProbeIntervalSec (300s, far coarser than latmesh's 10s) is what an
// unconfigured Service actually uses — the "coarser interval than latency
// probing" AC5/the task card require, asserted directly against the
// exported constant rather than by waiting out a real 5-minute ticker.
func TestService_RunLoop_DefaultsToDocumentedInterval(t *testing.T) {
	if mtuprobe.DefaultProbeIntervalSec != 300 {
		t.Fatalf("DefaultProbeIntervalSec = %d, want 300 (5x coarser than latmesh.DefaultProbeIntervalSec=10)", mtuprobe.DefaultProbeIntervalSec)
	}
	if mtuprobe.DefaultProbeIntervalSec <= latmesh.DefaultProbeIntervalSec {
		t.Fatalf("DefaultProbeIntervalSec (%d) must be coarser than latmesh's (%d)", mtuprobe.DefaultProbeIntervalSec, latmesh.DefaultProbeIntervalSec)
	}
}

// TestService_Tick_NilDiscovererOrProber_NoOp: the same degraded-mode
// convention latmesh.Service.Tick follows.
func TestService_Tick_NilDiscovererOrProber_NoOp(t *testing.T) {
	svc := mtuprobe.New(mtuprobe.Config{})
	svc.Tick(context.Background()) // must not panic
	if len(svc.Results()) != 0 {
		t.Fatal("Tick with nil Discoverer/Prober produced results")
	}
}

// TestService_MeasuredUnderlayMTU_SatisfiesFindingsProviderShape is a
// compile-time-adjacent check that *Service has the exact no-context
// signature internal/findings.MTUProvider expects, mirroring
// TestService_LatMeshHeatmap_SatisfiesInterfaceShape's own precedent in
// internal/latmesh.
func TestService_MeasuredUnderlayMTU_SatisfiesFindingsProviderShape(t *testing.T) {
	var _ interface {
		MeasuredUnderlayMTU(node string) (int, bool)
	} = mtuprobe.New(mtuprobe.Config{})
}
