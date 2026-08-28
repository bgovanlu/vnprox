// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"strings"
	"testing"
)

func testKnown() knownGuests {
	return knownGuests{
		vmids: map[int]bool{300: true, 301: true, 302: true},
		macs:  map[string]bool{"aa:bb:cc:dd:ee:01": true, "aa:bb:cc:dd:ee:02": true, "aa:bb:cc:dd:ee:03": true},
	}
}

// Acceptance criterion 1: one of each confidence label (allocated, observed,
// both, conflict) from a single merge — plus the gateway/reserved/free
// states the grid also renders — asserted against a fixed, hand-built
// golden cell-state map (mirrors the ipam-lab.yaml fixture's scenario
// exactly; service_test.go exercises the same scenario end-to-end through
// the real PVE client/mock).
func TestMergeSubnet_GoldenCellStateMap(t *testing.T) {
	allocs := []Allocation{
		{IP: "10.50.0.1", Gateway: true},
		{IP: "10.50.0.10", MAC: "AA:BB:CC:DD:EE:01", Hostname: "web1", VMID: 300},
		{IP: "10.50.0.11", MAC: "CC:CC:CC:CC:CC:CC", Hostname: "ghost", VMID: 999},
		{IP: "10.50.0.20", MAC: "AA:BB:CC:DD:EE:02", Hostname: "web2", VMID: 301},
		{IP: "10.50.0.30", Hostname: "reserved-for-later"}, // no VMID: a plain reservation
	}
	obs := []Observation{
		{IP: "10.50.0.10", MAC: "AA:BB:CC:DD:EE:01", Hostname: "web1", Source: "guest-agent"},
		{IP: "10.50.0.77", MAC: "AA:BB:CC:DD:EE:02", Hostname: "web2", Source: "guest-agent"},
		{IP: "10.50.0.77", MAC: "AA:BB:CC:DD:EE:03", Hostname: "web3", Source: "guest-agent"},
		{IP: "10.50.0.88", MAC: "AA:BB:CC:DD:EE:03", Hostname: "web3", Source: "guest-agent"},
	}

	cells, conflicts := mergeSubnet(allocs, obs, testKnown(), "10.50.0.1")

	golden := map[string]struct {
		state CellState
		conf  Confidence
	}{
		"10.50.0.1":  {CellGateway, ConfidenceAllocated},
		"10.50.0.10": {CellAllocated, ConfidenceBoth},
		"10.50.0.11": {CellConflict, ConfidenceAllocated}, // allocated_dark
		"10.50.0.20": {CellAllocated, ConfidenceAllocated},
		"10.50.0.30": {CellReserved, ConfidenceAllocated},
		"10.50.0.77": {CellConflict, ConfidenceConflict}, // duplicate_ip
		"10.50.0.88": {CellObserved, ConfidenceObserved},
		"10.50.0.99": {CellFree, ""},
	}

	for ip, want := range golden {
		got, ok := cells[ip]
		if ip == "10.50.0.99" {
			if ok {
				t.Errorf("%s: expected no cell entry (free address), got %+v", ip, got)
			}
			continue
		}
		if !ok {
			t.Fatalf("%s: missing from merged cell map", ip)
		}
		if got.State != want.state {
			t.Errorf("%s: state = %q, want %q", ip, got.State, want.state)
		}
		if got.Confidence != want.conf {
			t.Errorf("%s: confidence = %q, want %q", ip, got.Confidence, want.conf)
		}
	}

	// Every one of the four confidence labels must appear at least once
	// (T-405 acceptance criterion 1).
	seen := map[Confidence]bool{}
	for _, c := range cells {
		if c.Confidence != "" {
			seen[c.Confidence] = true
		}
	}
	for _, want := range []Confidence{ConfidenceAllocated, ConfidenceObserved, ConfidenceBoth, ConfidenceConflict} {
		if !seen[want] {
			t.Errorf("confidence label %q never appears in the merged map", want)
		}
	}

	// Acceptance criterion 2: all three conflict types detected.
	types := map[string]Conflict{}
	for _, c := range conflicts {
		types[c.Type] = c
	}
	for _, want := range []string{"duplicate_ip", "allocated_dark", "observed_unallocated"} {
		c, ok := types[want]
		if !ok {
			t.Errorf("conflict type %q not detected", want)
			continue
		}
		if c.Suggestion == "" {
			t.Errorf("conflict type %q has no suggested resolution", want)
		}
		if c.Severity == "" {
			t.Errorf("conflict type %q has no severity", want)
		}
	}
}

func TestMergeSubnet_DuplicateIP_SuggestionNamesBothGuests(t *testing.T) {
	obs := []Observation{
		{IP: "10.0.0.5", MAC: "aa:aa:aa:aa:aa:01", Hostname: "vmA", Source: "guest-agent"},
		{IP: "10.0.0.5", MAC: "aa:aa:aa:aa:aa:02", Hostname: "vmB", Source: "guest-agent"},
	}
	_, conflicts := mergeSubnet(nil, obs, knownGuests{}, "")
	if len(conflicts) != 1 || conflicts[0].Type != "duplicate_ip" {
		t.Fatalf("conflicts = %+v, want exactly one duplicate_ip", conflicts)
	}
	msg := conflicts[0].Message + conflicts[0].Suggestion
	if !strings.Contains(msg, "vmA") || !strings.Contains(msg, "vmB") {
		t.Errorf("duplicate_ip finding does not name both guests: %+v", conflicts[0])
	}
}

func TestMergeSubnet_AllocatedDark_KnownMACSuppressesFinding(t *testing.T) {
	// A allocation whose VMID doesn't exist but whose MAC *does* match a
	// known guest NIC (e.g. the guest was renumbered) should not be flagged
	// dark — the address is still traceable to a real guest.
	allocs := []Allocation{{IP: "10.0.0.9", MAC: "aa:bb:cc:dd:ee:01", VMID: 999}}
	known := knownGuests{vmids: map[int]bool{}, macs: map[string]bool{"aa:bb:cc:dd:ee:01": true}}
	cells, conflicts := mergeSubnet(allocs, nil, known, "")
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none (MAC is known)", conflicts)
	}
	if cells["10.0.0.9"].State != CellAllocated {
		t.Errorf("state = %q, want allocated", cells["10.0.0.9"].State)
	}
}

func TestMergeSubnet_ObservedUnallocated_NoAllocation(t *testing.T) {
	obs := []Observation{{IP: "10.0.0.50", MAC: "aa:bb:cc:00:00:01", Hostname: "squatter", Source: "guest-agent"}}
	cells, conflicts := mergeSubnet(nil, obs, knownGuests{}, "")
	cell := cells["10.0.0.50"]
	if cell.State != CellObserved || cell.Confidence != ConfidenceObserved {
		t.Fatalf("cell = %+v, want observed/observed", cell)
	}
	if len(conflicts) != 1 || conflicts[0].Type != "observed_unallocated" {
		t.Fatalf("conflicts = %+v, want one observed_unallocated", conflicts)
	}
}

func TestMergeSubnet_AllocObsMismatch_IsDuplicateIP(t *testing.T) {
	allocs := []Allocation{{IP: "10.0.0.60", MAC: "aa:bb:cc:00:00:01", Hostname: "recorded-owner", VMID: 5}}
	obs := []Observation{{IP: "10.0.0.60", MAC: "aa:bb:cc:00:00:02", Hostname: "actual-owner", Source: "guest-agent"}}
	known := knownGuests{vmids: map[int]bool{5: true}}
	cells, conflicts := mergeSubnet(allocs, obs, known, "")
	cell := cells["10.0.0.60"]
	if cell.State != CellConflict || cell.Confidence != ConfidenceConflict {
		t.Fatalf("cell = %+v, want conflict/conflict", cell)
	}
	if len(conflicts) != 1 || conflicts[0].Type != "duplicate_ip" {
		t.Fatalf("conflicts = %+v, want one duplicate_ip", conflicts)
	}
}

func TestMergeSubnet_FreeAddressHasNoCellEntry(t *testing.T) {
	cells, conflicts := mergeSubnet(nil, nil, knownGuests{}, "")
	if len(cells) != 0 || len(conflicts) != 0 {
		t.Fatalf("expected empty merge result, got cells=%v conflicts=%v", cells, conflicts)
	}
}
