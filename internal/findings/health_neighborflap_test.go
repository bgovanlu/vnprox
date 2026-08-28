// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"errors"
	"strings"
	"testing"
)

type staticNeighborFlap struct {
	err      error
	readings []NeighborFlapReading
}

func (s staticNeighborFlap) NeighborFlaps() ([]NeighborFlapReading, error) { return s.readings, s.err }

func TestNeighborFlapFindings_NilProviderIsSilent(t *testing.T) {
	if got := neighborFlapFindings(nil); len(got) != 0 {
		t.Fatalf("nil provider produced %d findings, want 0", len(got))
	}
}

func TestNeighborFlapFindings_ProviderErrorIsSilent(t *testing.T) {
	prov := staticNeighborFlap{err: errors.New("db unavailable")}
	if got := neighborFlapFindings(prov); len(got) != 0 {
		t.Fatalf("provider error produced %d findings, want 0", len(got))
	}
}

func TestNeighborFlapFindings_NoReadingsIsSilent(t *testing.T) {
	if got := neighborFlapFindings(staticNeighborFlap{}); len(got) != 0 {
		t.Fatalf("empty reading set produced %d findings, want 0", len(got))
	}
}

func TestNeighborFlapFindings_IPChurn(t *testing.T) {
	prov := staticNeighborFlap{readings: []NeighborFlapReading{
		{Node: "pve1", Kind: NeighborFlapIPChurn, IP: "10.0.0.1", Count: 4},
	}}
	got := neighborFlapFindings(prov)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	f := got[0]
	if f.Check != CheckNeighborBindingFlap {
		t.Fatalf("Check = %q, want %q", f.Check, CheckNeighborBindingFlap)
	}
	if f.Source != SourceHealth {
		t.Fatalf("Source = %q, want %q", f.Source, SourceHealth)
	}
	if f.Severity != SeverityWarning {
		t.Fatalf("Severity = %q, want %q (a flap is an operational signal, not arp_spoof_suspected's confirmed security alarm)", f.Severity, SeverityWarning)
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Fatalf("Nodes = %v, want [pve1]", f.Nodes)
	}
	if len(f.Refs) != 1 || f.Refs[0] != "10.0.0.1" {
		t.Fatalf("Refs = %v, want [10.0.0.1]", f.Refs)
	}
	if !strings.Contains(f.Detail, "10.0.0.1") || !strings.Contains(f.Detail, "4 different MACs") {
		t.Fatalf("Detail = %q, missing IP or count", f.Detail)
	}
	if f.Fixable {
		t.Fatalf("neighbor_binding_flap must never be Fixable (detection-only)")
	}
}

func TestNeighborFlapFindings_MACClaim(t *testing.T) {
	prov := staticNeighborFlap{readings: []NeighborFlapReading{
		{Node: "pve1", Kind: NeighborFlapMACClaim, MAC: "aa:bb:cc:dd:ee:ff", Count: 6, IPs: []string{"10.0.0.1", "10.0.0.2"}},
	}}
	got := neighborFlapFindings(prov)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	f := got[0]
	if !strings.Contains(f.Detail, "aa:bb:cc:dd:ee:ff") || !strings.Contains(f.Detail, "6 different IPs") {
		t.Fatalf("Detail = %q, missing MAC or count", f.Detail)
	}
	wantRefs := []string{"10.0.0.1", "10.0.0.2", "aa:bb:cc:dd:ee:ff"}
	if len(f.Refs) != len(wantRefs) {
		t.Fatalf("Refs = %v, want %v", f.Refs, wantRefs)
	}
	for _, want := range wantRefs {
		found := false
		for _, got := range f.Refs {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("Refs %v missing %q", f.Refs, want)
		}
	}
}

func TestNeighborFlapFindings_MultipleReadingsAreDeterministicallyOrdered(t *testing.T) {
	prov := staticNeighborFlap{readings: []NeighborFlapReading{
		{Node: "pve2", Kind: NeighborFlapIPChurn, IP: "10.0.0.9", Count: 3},
		{Node: "pve1", Kind: NeighborFlapIPChurn, IP: "10.0.0.1", Count: 3},
	}}
	got1 := neighborFlapFindings(prov)
	got2 := neighborFlapFindings(prov)
	if len(got1) != 2 || len(got2) != 2 {
		t.Fatalf("got %d/%d findings, want 2/2", len(got1), len(got2))
	}
	if got1[0].ID != got2[0].ID || got1[1].ID != got2[1].ID {
		t.Fatalf("finding order/IDs not stable across calls: %v vs %v", got1, got2)
	}
}

func TestNeighborFlapFindings_IsInCatalog(t *testing.T) {
	found := false
	for _, name := range AllCheckNames() {
		if name == CheckNeighborBindingFlap {
			found = true
		}
	}
	if !found {
		t.Fatalf("%q is missing from catalog.go's allCheckNames", CheckNeighborBindingFlap)
	}
}
