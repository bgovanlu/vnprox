// SPDX-License-Identifier: Apache-2.0

package collect_test

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestOutageRecovery is T-104's acceptance criterion 2: a PVE outage (mock
// stopped mid-run) degrades gracefully — staleness is reported, nothing
// crashes — and the collector recovers on its own once the mock restarts,
// without the daemon/collector process itself being restarted.
func TestOutageRecovery(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)
	mock := newRestartableMock(t, srv)

	graph := inventory.NewGraph()
	cfg := collect.Config{
		PVE:          newTicketClient(t, mock.URL()),
		Host:         newFixtureHostReader(srv),
		Graph:        graph,
		PVEInterval:  50 * time.Millisecond,
		HostInterval: 50 * time.Millisecond,
		LLDPInterval: 50 * time.Millisecond,
	}
	c, err := collect.New(cfg)
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.RunPVELoop(ctx) }()
	go func() { _ = c.RunHostLoop(ctx) }()
	go func() { _ = c.RunLLDPLoop(ctx) }()

	// T-3103: +2 for the two vnet-scope firewall rulesets (vnet100/vnet200)
	// pollFirewall now polls alongside the cluster ruleset. T-3104: +1 for
	// the fixture's one configured ipam plugin instance ("pve").
	const wantTotal = 39
	waitFor(t, 3*time.Second, "graph to converge before the outage", func() bool {
		return graph.Snapshot().Len() == wantTotal
	})

	pveStatus := func() collect.SourceStatus {
		for _, s := range c.Status().Sources {
			if s.Name == "pve" {
				return s
			}
		}
		t.Fatal("no \"pve\" source in Status()")
		return collect.SourceStatus{}
	}

	beforeOutage := pveStatus()
	if beforeOutage.LastSuccess.IsZero() {
		t.Fatalf("expected a recorded pve success before the outage, got %+v", beforeOutage)
	}

	// --- stop the mock: the collector must degrade gracefully, not crash,
	// and must report growing staleness (rising ConsecutiveFailures,
	// LastSuccess no longer advancing). ---
	mock.Stop()

	waitFor(t, 3*time.Second, "pve source to report at least 2 consecutive failures", func() bool {
		return pveStatus().ConsecutiveFailures >= 2
	})

	// Baseline the outage only once it is fully established. mock.Stop() is
	// not an instantaneous boundary: a poll already in flight when it fires
	// (50ms intervals, three concurrent loops) can still complete one last
	// success — advancing LastSuccess and finishing its graph delta — which
	// is graceful degradation, not a bug. The real invariants are that,
	// *while polls are consistently failing*, LastSuccess does not advance
	// and the graph is not clobbered; so freeze the reference here (after the
	// outage is established) and prove it holds across further failures,
	// rather than comparing against the pre-stop value (a transition-window
	// race — the graph may briefly reflect a partial straddling poll).
	frozen := pveStatus()
	if frozen.LastError == "" {
		t.Errorf("expected a non-empty LastError while the mock is down")
	}
	frozenLen := graph.Snapshot().Len()
	if frozenLen == 0 {
		t.Errorf("graph was emptied by the outage (len=0) — failures must not clobber last-known-good state")
	}

	waitFor(t, 3*time.Second, "pve source to report further failures (sustained outage)", func() bool {
		return pveStatus().ConsecutiveFailures >= 4
	})
	if got := pveStatus(); !got.LastSuccess.Equal(frozen.LastSuccess) {
		t.Errorf("LastSuccess advanced while polls were still failing: frozen=%v got=%v", frozen.LastSuccess, got.LastSuccess)
	}
	if got := graph.Snapshot().Len(); got != frozenLen {
		t.Errorf("entity count changed while polls were failing: %d -> %d (failures must not alter last-known-good state)", frozenLen, got)
	}

	// --- restart the mock: the collector must recover on its own. ---
	mock.Start(t)

	waitFor(t, 3*time.Second, "pve source to recover after the mock restarts", func() bool {
		s := pveStatus()
		return s.ConsecutiveFailures == 0 && s.LastSuccess.After(beforeOutage.LastSuccess)
	})

	if got := graph.Snapshot().Len(); got != wantTotal {
		t.Errorf("entity count after recovery = %d, want %d", got, wantTotal)
	}
}
