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

	const wantTotal = 35
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
	// LastSuccess stuck at its pre-outage value). ---
	mock.Stop()

	waitFor(t, 3*time.Second, "pve source to report at least 2 consecutive failures", func() bool {
		return pveStatus().ConsecutiveFailures >= 2
	})
	afterOutage := pveStatus()
	if !afterOutage.LastSuccess.Equal(beforeOutage.LastSuccess) {
		t.Errorf("LastSuccess advanced during the outage: before=%v after=%v", beforeOutage.LastSuccess, afterOutage.LastSuccess)
	}
	if afterOutage.LastError == "" {
		t.Errorf("expected a non-empty LastError while the mock is down")
	}

	// The graph itself must not have been clobbered by the outage (no
	// entities removed just because polling failed).
	if got := graph.Snapshot().Len(); got != wantTotal {
		t.Errorf("entity count during outage = %d, want unchanged %d", got, wantTotal)
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
