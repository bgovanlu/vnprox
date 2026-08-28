// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/findings"
)

// TestStalePendingInterfaces: a NIC that has been continuously pending for
// over an hour fires CheckStalePendingInterfaces (AC1, docs/features/
// monitoring.md §5's literal ">1h" threshold).
func TestStalePendingInterfaces(t *testing.T) {
	g := newGraphWithNodes("pve1")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pvePendingPhysNic(g, "pve1", "eno1", "changed")

	eng := findings.New(findings.Config{Graph: g, Now: func() time.Time { return now }})

	// Freshly pending: not yet stale.
	if found := findByCheck(t, eng.Findings(), findings.CheckStalePendingInterfaces); len(found) != 0 {
		t.Fatalf("freshly-pending interface already flagged stale: %+v", found)
	}

	now = now.Add(90 * time.Minute)
	found := findByCheck(t, eng.Findings(), findings.CheckStalePendingInterfaces)
	if len(found) != 1 {
		t.Fatalf("got %d stale_pending_interfaces findings after 90m pending, want 1", len(found))
	}
	f := found[0]
	if f.Fixable {
		t.Error("stale_pending_interfaces should not be fixable")
	}
	if f.DocsLink == "" {
		t.Error("stale_pending_interfaces must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "eno1") || !strings.Contains(f.Detail, "pve1") {
		t.Errorf("detail = %q, want mention of eno1/pve1", f.Detail)
	}
}

// TestStalePendingInterfaces_ClearsOnReload: once the staged edit is applied
// (Pending clears), the finding disappears immediately — no lingering
// hysteresis on the clearing side, since a reload is a discrete, trusted
// event (unlike a noisy metric sample).
func TestStalePendingInterfaces_ClearsOnReload(t *testing.T) {
	g := newGraphWithNodes("pve1")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pvePendingPhysNic(g, "pve1", "eno1", "changed")
	eng := findings.New(findings.Config{Graph: g, Now: func() time.Time { return now }})
	eng.Findings() // seed firstSeen at the original `now`

	now = now.Add(2 * time.Hour)
	if found := findByCheck(t, eng.Findings(), findings.CheckStalePendingInterfaces); len(found) != 1 {
		t.Fatalf("setup: expected the finding active before testing clear, got %d", len(found))
	}

	clearPending(g, "pve1", "eno1")
	if found := findByCheck(t, eng.Findings(), findings.CheckStalePendingInterfaces); len(found) != 0 {
		t.Fatalf("finding persisted after the staged edit was applied: %+v", found)
	}
}
