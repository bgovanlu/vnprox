package main

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/ipam"
)

func TestIpamConflictToFinding(t *testing.T) {
	f := ipamConflictToFinding(ipam.SubnetConflict{
		CIDR: "10.50.0.0/24",
		Conflict: ipam.Conflict{
			Type:       "duplicate_ip",
			Severity:   findings.SeverityError,
			Message:    "two guests claim 10.50.0.5",
			Suggestion: "release one of them",
			IPs:        []string{"10.50.0.5"},
		},
	})

	if f.Source != findings.SourceIPAM {
		t.Errorf("source = %q, want %q", f.Source, findings.SourceIPAM)
	}
	if f.Check != "duplicate_ip" {
		t.Errorf("check = %q", f.Check)
	}
	if f.Severity != findings.SeverityError {
		t.Errorf("severity = %q", f.Severity)
	}
	// Stable, content-derived id: source, type, subnet, sorted addresses.
	if want := "ipam:duplicate_ip|10.50.0.0/24|10.50.0.5"; f.ID != want {
		t.Errorf("id = %q, want %q", f.ID, want)
	}
	if f.Fixable {
		t.Error("IPAM conflicts carry no computed fix op — Fixable must be false")
	}
	if f.DocsLink == "" {
		t.Error("a non-fixable finding must carry a docs link")
	}
	if !strings.Contains(f.Detail, "release one of them") {
		t.Errorf("detail should fold in the suggestion, got %q", f.Detail)
	}
}

func TestIpamFindingsAdapter_NilServiceIsSafe(t *testing.T) {
	a := ipamFindingsAdapter{ipam: nil, logger: testLogger()}
	if got := a.Findings(); got != nil {
		t.Errorf("a nil ipam service must contribute no findings, got %v", got)
	}
}
