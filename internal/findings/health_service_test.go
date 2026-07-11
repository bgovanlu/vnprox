package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
)

// TestServiceDown: a node reporting frr inactive fires CheckServiceDown
// after hysteresis clears (AC1).
func TestServiceDown(t *testing.T) {
	g := newGraphWithNodes("pve1")
	eng := findings.New(findings.Config{Graph: g})

	eng.IngestServices("pve1", map[string]bool{"dnsmasq": true, "frr": false})
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckServiceDown)
	if len(found) != 1 {
		t.Fatalf("got %d service_down findings, want 1", len(found))
	}
	f := found[0]
	if f.Fixable {
		t.Error("service_down should not be fixable")
	}
	if f.DocsLink == "" {
		t.Error("service_down must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "frr") || !strings.Contains(f.Detail, "pve1") {
		t.Errorf("detail = %q, want mention of frr/pve1", f.Detail)
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Errorf("Nodes = %v, want [pve1]", f.Nodes)
	}
}

// TestServiceDown_TwoServicesSameNode_DistinctFindings: dnsmasq and frr both
// down on the same node produce two distinct findings, not a collision onto
// one ID.
func TestServiceDown_TwoServicesSameNode_DistinctFindings(t *testing.T) {
	g := newGraphWithNodes("pve1")
	eng := findings.New(findings.Config{Graph: g})

	eng.IngestServices("pve1", map[string]bool{"dnsmasq": false, "frr": false})
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckServiceDown)
	if len(found) != 2 {
		t.Fatalf("got %d service_down findings for two down services, want 2: %+v", len(found), found)
	}
	if found[0].ID == found[1].ID {
		t.Errorf("both findings share ID %q, want distinct IDs", found[0].ID)
	}
}

// TestServiceUp_NeverFires: a node reporting every watched service active
// never produces a finding.
func TestServiceUp_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	eng := findings.New(findings.Config{Graph: g})

	eng.IngestServices("pve1", map[string]bool{"dnsmasq": true, "frr": true})
	for i := 0; i < 3; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckServiceDown); len(found) != 0 {
			t.Fatalf("cycle %d: healthy services produced a finding: %+v", i, found)
		}
	}
}

// TestService_NeverReported_NeverFlagged: a node that never reports a given
// service at all (not installed) is never flagged for it.
func TestService_NeverReported_NeverFlagged(t *testing.T) {
	g := newGraphWithNodes("pve1")
	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 3; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckServiceDown); len(found) != 0 {
			t.Fatalf("cycle %d: no service data ingested yet produced a finding: %+v", i, found)
		}
	}
}
