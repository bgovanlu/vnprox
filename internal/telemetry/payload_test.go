// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/verify"
)

// TestReduceLeavesEveryIdentifierBehind is the leak test for the reduction
// itself, and it has controls.
//
// The report fixture carries node names, an endpoint address, interface
// names, a MAC, a guest name and evidence bodies. The payload must contain
// none of them — and the four control assertions at the end must find the
// things that ARE supposed to survive, so that a reduction which emitted an
// empty payload (or a scan looking in the wrong place) fails loudly instead
// of producing green subtests that assert nothing.
func TestReduceLeavesEveryIdentifierBehind(t *testing.T) {
	rep := sampleReport()
	raw, err := marshalPayload(Reduce(rep, sampleInstallID))
	if err != nil {
		t.Fatalf("marshalPayload: %v", err)
	}
	got := string(raw)

	mustNotContain := []struct {
		what  string
		value string
	}{
		{"a node name", "node-alpha"},
		{"the other node name", "node-beta"},
		{"the PVE endpoint", "192.0.2.10"},
		{"the endpoint URL", "https://192.0.2.10:8006"},
		{"an interface name", "enp3s0"},
		{"a bridge/guest interface name", "bond0"},
		{"a MAC from the evidence", "aa:bb:cc:dd:ee:ff"},
		{"a guest name from a detail line", "web-prod-01"},
		{"an evidence body", "Partner Mac Address"},
		{"a check's free-text detail", "matches its staged config"},
		{"a skip reason", "only one node online"},
		{"the modalias string", "pci:v00008086"},
	}
	for _, tc := range mustNotContain {
		if strings.Contains(got, tc.value) {
			t.Errorf("the payload contains %s (%q):\n%s", tc.what, tc.value, got)
		}
	}

	// Controls. Each of these MUST be present: without them, every
	// assertion above would pass for a reduction that produced `{}`.
	mustContain := []struct {
		what  string
		value string
	}{
		{"the PVE version", "pve-manager/9.2.4"},
		{"the kernel", "6.8.12-4-pve"},
		{"a NIC's PCI id", "0x8086:0x1521"},
		{"a check id", "drift.config_vs_live"},
		{"a verdict", "fail"},
	}
	for _, tc := range mustContain {
		if !strings.Contains(got, tc.value) {
			t.Errorf("the payload does not contain %s (%q) — this scan is not looking at a real payload:\n%s", tc.what, tc.value, got)
		}
	}

	p := Reduce(rep, sampleInstallID)
	if p.NodeCount != 2 {
		t.Errorf("NodeCount = %d, want 2 (the count survives; the names do not)", p.NodeCount)
	}
	if len(p.Checks) != len(rep.Results) {
		t.Errorf("got %d verdicts, want %d — a reduction that dropped checks would pass every leak assertion above", len(p.Checks), len(rep.Results))
	}
}

func TestReduceSuite(t *testing.T) {
	// Strings first, the slice last: govet's fieldalignment, which wants the
	// pointer-bearing prefix short and a slice's len/cap tail is the
	// cheapest thing to leave at the end.
	cases := []struct {
		name      string
		suite     verify.Suite
		want      string
		selection []string
	}{
		{name: "a hardware suite run", suite: verify.SuiteHardware, want: "hardware"},
		{name: "a destructive suite run", suite: verify.SuiteDestructive, want: "destructive"},
		{name: "an explicit --only selection", selection: []string{"drift.config_vs_live"}, want: SelectionSuite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := sampleReport()
			rep.Suite = tc.suite
			rep.Selection = tc.selection
			if got := Reduce(rep, sampleInstallID).Suite; got != tc.want {
				t.Errorf("Suite = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPCIIDsKeepOnlyTheHardwareID is the reason nicPciIds cannot carry a
// name: everything that is not a vendor:device pair is dropped here, before
// the guard ever sees it.
func TestPCIIDsKeepOnlyTheHardwareID(t *testing.T) {
	cases := []struct {
		name  string
		want  []string
		lines []string
	}{
		{
			name:  "a real line keeps only the ids",
			lines: []string{"enp3s0 0x8086:0x1521 pci:v00008086d00001521sv00008086sd00000002bc02sc00i00"},
			want:  []string{"0x8086:0x1521"},
		},
		{
			name:  "duplicates collapse and the result is sorted",
			lines: []string{"enp4s0 0x15b3:0x1017 pci:x", "enp3s0 0x8086:0x1521 pci:y", "enp5s0 0x8086:0x1521 pci:z"},
			want:  []string{"0x15b3:0x1017", "0x8086:0x1521"},
		},
		{
			name:  "a guest tap interface contributes only its hardware id",
			lines: []string{"tap101i0 0x1af4:0x0001 pci:virtio"},
			want:  []string{"0x1af4:0x0001"},
		},
		{
			name:  "a line with no id at all contributes nothing",
			lines: []string{"vmbr0 node-alpha.example.com"},
			want:  nil,
		},
		{
			name:  "a MAC is not a PCI id",
			lines: []string{"enp3s0 aa:bb:cc:dd:ee:ff pci:v1"},
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pciIDs(tc.lines)
			if len(got) != len(tc.want) {
				t.Fatalf("pciIDs(%q) = %q, want %q", tc.lines, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("pciIDs(%q) = %q, want %q", tc.lines, got, tc.want)
				}
			}
		})
	}
}

// TestBuildRefusesAMockReport: a run against internal/pvemock is not
// compatibility evidence, and a matrix polluted with mock runs would look
// larger than it is, which is worse than being small.
func TestBuildRefusesAMockReport(t *testing.T) {
	rep := sampleReport()
	rep.Environment.Mock = true
	rep.Environment.MockReason = "the endpoint identified itself as internal/pvemock"

	snap, err := Build(rep, sampleInstallID)
	if err == nil {
		t.Fatalf("Build accepted a mock report and produced %d bytes", len(snap.Bytes()))
	}
	if !strings.Contains(err.Error(), "mock") {
		t.Errorf("the refusal does not mention the mock: %v", err)
	}
}

// TestBuildRefusesAnInvalidReport: reducing a malformed report would produce
// verdict counts nobody can reproduce from the artifact they came from.
func TestBuildRefusesAnInvalidReport(t *testing.T) {
	rep := sampleReport()
	rep.Summary.Passed += 7 // a summary its own results do not support

	if _, err := Build(rep, sampleInstallID); err == nil {
		t.Fatal("Build accepted a report whose summary contradicts its results")
	}
}
