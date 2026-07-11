package drift_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/drift"
)

// TestFindings_DeterministicAcrossRepeatedCycles is T-305 acceptance
// criterion 5: "no finding flapping ... stable-key dedup verified across
// repeated cycles on unchanged state" — calling Findings() repeatedly
// against an unchanged graph must return byte-identical results (same IDs,
// same order, same content) every time, not just the same count.
func TestFindings_DeterministicAcrossRepeatedCycles(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	pveBridge(g, "pve1", "vmbr0", 1500, true, nil, nil)
	pveBridge(g, "pve2", "vmbr0", 9000, true, nil, nil)
	pveBridge(g, "pve3", "vmbr0", 1500, false, nil, nil)
	pveSDNZone(g, "zone-a", "vmbr-missing", []string{"pve1"})

	svc := drift.New(drift.Config{Graph: g})
	first := svc.Findings()
	if len(first) == 0 {
		t.Fatal("expected at least one finding to make this test meaningful")
	}
	for i := 0; i < 10; i++ {
		got := svc.Findings()
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("cycle %d: findings changed on an unchanged graph.\nfirst: %+v\ngot:   %+v", i, first, got)
		}
	}
}

// TestRunLoop_NoChangeNoFire: OnChange must not fire on cycles after the
// first when the finding set is unchanged (the loop's hysteresis/dedup
// mechanism), and must fire exactly once for the initial cycle.
func TestRunLoop_NoChangeNoFire(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2")
	pveBridge(g, "pve1", "vmbr0", 1500, true, nil, nil)
	pveBridge(g, "pve2", "vmbr0", 9000, true, nil, nil)

	var mu sync.Mutex
	var fireCounts []int
	svc := drift.New(drift.Config{
		Graph:    g,
		Interval: 5 * time.Millisecond,
		OnChange: func(count int) {
			mu.Lock()
			fireCounts = append(fireCounts, count)
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if err := svc.RunLoop(ctx); err != nil {
		t.Fatalf("RunLoop: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(fireCounts) != 1 {
		t.Fatalf("OnChange fired %d times over an unchanged graph across ~12 cycles, want exactly 1 (the initial cycle): %v", len(fireCounts), fireCounts)
	}
	if fireCounts[0] != 1 {
		t.Errorf("initial fire count = %d, want 1", fireCounts[0])
	}
}

// TestRunLoop_FiresOnChange: OnChange fires again once the underlying
// graph changes the finding set.
func TestRunLoop_FiresOnChange(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2")
	pveBridge(g, "pve1", "vmbr0", 1500, true, nil, nil)
	pveBridge(g, "pve2", "vmbr0", 1500, true, nil, nil) // clean initially

	var mu sync.Mutex
	var fireCounts []int
	svc := drift.New(drift.Config{
		Graph:    g,
		Interval: 5 * time.Millisecond,
		OnChange: func(count int) {
			mu.Lock()
			fireCounts = append(fireCounts, count)
			mu.Unlock()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.RunLoop(ctx) }()

	time.Sleep(20 * time.Millisecond)
	pveBridge(g, "pve2", "vmbr0", 9000, true, nil, nil) // introduce drift
	time.Sleep(40 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunLoop: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(fireCounts) < 2 {
		t.Fatalf("OnChange fired %d times, want >= 2 (initial clean cycle + the cycle that observed the introduced drift): %v", len(fireCounts), fireCounts)
	}
	if fireCounts[0] != 0 {
		t.Errorf("initial fire count = %d, want 0 (clean cluster)", fireCounts[0])
	}
	last := fireCounts[len(fireCounts)-1]
	if last == 0 {
		t.Errorf("final fire count = 0, want > 0 after introducing drift")
	}
}

// TestFixOps_UnknownID: an unrecognized finding ID reports not-found.
func TestFixOps_UnknownID(t *testing.T) {
	g := newGraphWithNodes("pve1")
	svc := drift.New(drift.Config{Graph: g})
	if _, _, ok := svc.FixOps("no-such-finding"); ok {
		t.Error("FixOps for an unknown ID returned ok=true")
	}
}
