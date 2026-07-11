package findings_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/findings"
)

// TestSTPTopologyBurst: a bridge whose forwarding port set churns 3 times
// within the burst window fires CheckSTPTopologyBurst (AC1).
func TestSTPTopologyBurst(t *testing.T) {
	g := newGraphWithNodes("pve1")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	eng := findings.New(findings.Config{Graph: g, Now: func() time.Time { return now }})

	ports := [][]string{
		{"eno1"}, {"eno1", "eno2"}, {"eno1"}, {"eno1", "eno2"},
	}
	for _, p := range ports {
		netlinkBridgeWithPorts(g, "pve1", "vmbr0", p)
		eng.Findings()
		now = now.Add(time.Minute)
	}

	found := findByCheck(t, eng.Findings(), findings.CheckSTPTopologyBurst)
	if len(found) != 1 {
		t.Fatalf("got %d stp_topology_burst findings after 3 churns, want 1", len(found))
	}
	f := found[0]
	if f.DocsLink == "" {
		t.Error("stp_topology_burst must carry a DocsLink (no computable fix)")
	}
	if f.Fixable {
		t.Error("stp_topology_burst should not be fixable")
	}
	if !strings.Contains(f.Detail, "vmbr0") || !strings.Contains(f.Detail, "pve1") {
		t.Errorf("detail = %q, want mention of vmbr0/pve1", f.Detail)
	}
}

// TestSTPTopologyBurst_BelowThreshold_NoFinding: a single port-set change
// (planned maintenance, one NIC swap) never fires on its own — AC3's "don't
// flap on noisy fixtures" applied to this check's own burst threshold.
func TestSTPTopologyBurst_BelowThreshold_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	eng := findings.New(findings.Config{Graph: g, Now: func() time.Time { return now }})

	netlinkBridgeWithPorts(g, "pve1", "vmbr0", []string{"eno1"})
	eng.Findings()
	now = now.Add(time.Minute)
	netlinkBridgeWithPorts(g, "pve1", "vmbr0", []string{"eno1", "eno2"})
	found := findByCheck(t, eng.Findings(), findings.CheckSTPTopologyBurst)
	if len(found) != 0 {
		t.Fatalf("a single port-set change fired stp_topology_burst, want no finding below threshold: %+v", found)
	}
}

// TestSTPTopologyBurst_OutsideWindow_NoFinding: churns spread out beyond the
// burst window never accumulate into a burst.
func TestSTPTopologyBurst_OutsideWindow_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	eng := findings.New(findings.Config{Graph: g, Now: func() time.Time { return now }})

	ports := [][]string{{"eno1"}, {"eno1", "eno2"}, {"eno1"}, {"eno1", "eno2"}}
	for _, p := range ports {
		netlinkBridgeWithPorts(g, "pve1", "vmbr0", p)
		eng.Findings()
		now = now.Add(20 * time.Minute) // well beyond the 10-minute window
	}

	found := findByCheck(t, eng.Findings(), findings.CheckSTPTopologyBurst)
	if len(found) != 0 {
		t.Fatalf("churns spread beyond the window fired stp_topology_burst, want none: %+v", found)
	}
}
