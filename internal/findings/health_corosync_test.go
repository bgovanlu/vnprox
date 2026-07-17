package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
)

// fakeCorosyncProvider is a minimal findings.CorosyncProvider stand-in.
type fakeCorosyncProvider struct {
	err    error
	status map[string][]host.RingStatus
}

func (f fakeCorosyncProvider) CorosyncStatus() (map[string][]host.RingStatus, error) {
	return f.status, f.err
}

// TestCorosyncLinkDegraded_Fires: a node's ring reporting faulty fires after
// hysteresis clears (AC1's firing case; AC4 "no computable fix").
func TestCorosyncLinkDegraded_Fires(t *testing.T) {
	status := map[string][]host.RingStatus{
		"pve3": {
			{RingID: 0, Addr: "10.10.0.13", StatusText: "ring 0 active with no faults"},
			{RingID: 1, Addr: "10.10.1.13", StatusText: "Marking ringid 1 interface 10.10.1.13 FAULTY", Faulty: true},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve3"), Corosync: fakeCorosyncProvider{status: status}})

	first := findByCheck(t, eng.Findings(), findings.CheckCorosyncLinkDegraded)
	if len(first) != 0 {
		t.Fatalf("corosync_link_degraded fired on the very first observation (no debounce), got %+v", first)
	}
	found := findByCheck(t, eng.Findings(), findings.CheckCorosyncLinkDegraded)
	if len(found) != 1 {
		t.Fatalf("got %d corosync_link_degraded findings after 2 cycles, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Severity != findings.SeverityWarning {
		t.Errorf("severity = %q, want warning", f.Severity)
	}
	if f.Fixable {
		t.Errorf("corosync_link_degraded should never be fixable, got Fixable=true")
	}
	if f.DocsLink == "" {
		t.Error("corosync_link_degraded must carry a DocsLink")
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve3" {
		t.Errorf("Nodes = %v, want [pve3]", f.Nodes)
	}
	if !strings.Contains(f.Detail, "pve3") || !strings.Contains(f.Detail, "FAULTY") {
		t.Errorf("detail = %q, want mention of pve3 and the raw FAULTY status text", f.Detail)
	}

	if _, _, ok := eng.FixOps(f.ID); ok {
		t.Error("FixOps succeeded for a corosync_link_degraded finding, want ok=false")
	}
}

// TestCorosyncLinkDegraded_Healthy_NoFinding: every ring reporting healthy
// never fires.
func TestCorosyncLinkDegraded_Healthy_NoFinding(t *testing.T) {
	status := map[string][]host.RingStatus{
		"pve1": {
			{RingID: 0, Addr: "10.10.0.11", StatusText: "ring 0 active with no faults"},
			{RingID: 1, Addr: "10.10.1.11", StatusText: "ring 1 active with no faults"},
		},
	}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Corosync: fakeCorosyncProvider{status: status}})
	for i := 0; i < 5; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckCorosyncLinkDegraded); len(found) != 0 {
			t.Fatalf("cycle %d: healthy rings produced a finding: %+v", i, found)
		}
	}
}

// TestCorosyncLinkDegraded_NilProvider_NoFindings: not wired -> quietly
// absent, matching every other optional Config field.
func TestCorosyncLinkDegraded_NilProvider_NoFindings(t *testing.T) {
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1")})
	if found := findByCheck(t, eng.Findings(), findings.CheckCorosyncLinkDegraded); len(found) != 0 {
		t.Fatalf("nil Corosync provider produced findings: %+v", found)
	}
}

// TestCorosyncLinkDegraded_ProviderError_NoFindings: a computation error
// degrades to no findings rather than erroring the whole stream.
func TestCorosyncLinkDegraded_ProviderError_NoFindings(t *testing.T) {
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Corosync: fakeCorosyncProvider{err: errBoom}})
	if found := findByCheck(t, eng.Findings(), findings.CheckCorosyncLinkDegraded); len(found) != 0 {
		t.Fatalf("provider error produced findings: %+v", found)
	}
}

// TestCorosyncLinkDegraded_ClearsAfterRecovery: a ring that recovers for
// enough consecutive cycles clears the finding (the hysteresis fall side).
func TestCorosyncLinkDegraded_ClearsAfterRecovery(t *testing.T) {
	faulty := map[string][]host.RingStatus{
		"pve1": {{RingID: 0, Addr: "10.10.0.11", StatusText: "FAULTY", Faulty: true}},
	}
	prov := &mutableCorosyncProvider{status: faulty}
	eng := findings.New(findings.Config{Graph: newGraphWithNodes("pve1"), Corosync: prov})
	eng.Findings()
	if found := findByCheck(t, eng.Findings(), findings.CheckCorosyncLinkDegraded); len(found) != 1 {
		t.Fatalf("setup: expected the finding active before testing recovery, got %d", len(found))
	}

	prov.status = map[string][]host.RingStatus{
		"pve1": {{RingID: 0, Addr: "10.10.0.11", StatusText: "ring 0 active with no faults"}},
	}
	stillActive := findByCheck(t, eng.Findings(), findings.CheckCorosyncLinkDegraded)
	if len(stillActive) != 1 {
		t.Fatalf("finding cleared after a single recovered sample, want it to persist one more cycle: %+v", stillActive)
	}
	cleared := findByCheck(t, eng.Findings(), findings.CheckCorosyncLinkDegraded)
	if len(cleared) != 0 {
		t.Fatalf("finding did not clear after 2 consecutive recovered samples: %+v", cleared)
	}
}

// mutableCorosyncProvider lets a test flip the reported status between
// Findings() calls, to exercise the fall-hysteresis path.
type mutableCorosyncProvider struct {
	status map[string][]host.RingStatus
}

func (p *mutableCorosyncProvider) CorosyncStatus() (map[string][]host.RingStatus, error) {
	return p.status, nil
}
