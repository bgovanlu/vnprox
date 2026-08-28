// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"strings"
	"testing"
)

// The five tests below are T-2503 AC3: "a payload containing any hostname,
// IP, MAC, guest name or cluster name fails a structural check before send.
// One test per field class."
//
// Two things they all do deliberately:
//
//   - They plant the offending value in a field that is SUPPOSED to be
//     there — `kernel`, `pveVersion`, `nicPciIds`, a check id. A guard that
//     only caught a new, obviously-wrong field would prove nothing: the risk
//     is a legitimate string field carrying something it should not.
//   - Each ends with a control leg: the same payload, unplanted, must pass.
//     Without it a guard that refused everything would score five green
//     tests.

// assertCaught runs the guard and requires it to fail with the given class.
func assertCaught(t *testing.T, raw []byte, known []Known, want Class) {
	t.Helper()
	err := Guard(raw, known)
	if err == nil {
		t.Fatalf("the guard passed a payload containing a %s:\n%s", want, raw)
	}
	ge, ok := AsGuardError(err)
	if !ok {
		t.Fatalf("the guard failed with %T, not a *GuardError: %v", err, err)
	}
	for _, c := range ge.Classes() {
		if c == want {
			return
		}
	}
	t.Fatalf("the guard caught %v but not %s:\n%v", ge.Classes(), want, ge)
}

// assertControlPasses is the leg that makes the four assertions above mean
// something: the unplanted payload must be sendable.
func assertControlPasses(t *testing.T, known []Known) {
	t.Helper()
	clean := mutatedPayload(t, func(*Payload) {})
	if err := Guard(clean, known); err != nil {
		t.Fatalf("the guard refused the CONTROL payload, so every refusal in this test proves nothing: %v", err)
	}
}

func TestGuardCatchesAHostname(t *testing.T) {
	known := KnownFromReport(sampleReport())

	// A dotted name, caught by shape alone — no knowledge of this cluster
	// needed. `kernel` is a `string` whose name is not "hostname", which is
	// exactly the point: a locally built kernel's release string can carry
	// the build host.
	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.Kernel = "6.8.12-4-pve-custom-buildhost.example.com"
	}), known, ClassHostname)

	// A bare node name has no shape at all, so it is caught by the
	// known-value rule fed from the report being reduced.
	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.PVEVersion = "pve-manager/9.2.4 on node-alpha"
	}), known, ClassHostname)

	// Including when it hides in a check id, the one field the FQDN rule
	// does not run on.
	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.Checks[0].ID = "drift.config_vs_live.node-alpha"
	}), known, ClassHostname)

	assertControlPasses(t, known)
}

func TestGuardCatchesAnIPAddress(t *testing.T) {
	known := KnownFromReport(sampleReport())

	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.PVEVersion = "pve-manager/9.2.4 (10.0.0.7)"
	}), known, ClassIP)

	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.Kernel = "6.8.12-4-pve 2001:db8::1"
	}), known, ClassIP)

	// The endpoint's own address, which the report carries and the payload
	// must not.
	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.VnproxVersion = "3.0.3 192.0.2.10"
	}), known, ClassIP)

	assertControlPasses(t, known)
}

func TestGuardCatchesAMAC(t *testing.T) {
	known := KnownFromReport(sampleReport())

	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.NICPCIIDs = append(p.NICPCIIDs, "aa:bb:cc:dd:ee:ff")
	}), known, ClassMAC)

	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.Kernel = "6.8.12-4-pve aa-bb-cc-dd-ee-ff"
	}), known, ClassMAC)

	assertControlPasses(t, known)
}

func TestGuardCatchesAGuestName(t *testing.T) {
	// A guest name has no shape whatsoever — `web-prod-01` is
	// indistinguishable from any other token — so the caller supplies it.
	// See KnownFromReport's doc comment for why a T-2501 report cannot.
	known := append(KnownFromReport(sampleReport()), Known{Value: "web-prod-01", Class: ClassGuest})

	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.Kernel = "6.8.12-4-pve web-prod-01"
	}), known, ClassGuest)

	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.Checks[1].ID = "guest.web-prod-01"
	}), known, ClassGuest)

	assertControlPasses(t, known)
}

func TestGuardCatchesAClusterName(t *testing.T) {
	known := append(KnownFromReport(sampleReport()), Known{Value: "office-cluster", Class: ClassCluster})

	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.VnproxVersion = "3.0.3 (office-cluster)"
	}), known, ClassCluster)

	assertCaught(t, mutatedPayload(t, func(p *Payload) {
		p.PVEVersion = "pve-manager/9.2.4 office-cluster"
	}), known, ClassCluster)

	assertControlPasses(t, known)
}

// TestGuardIsClosedNotOpen: the schema half. An undocumented key, a value
// that is not the shape its field is documented to hold, or a payload that
// is not an object at all, is refused — so a field added to Payload without
// being thought about cannot ship even if no shape rule happens to match it.
func TestGuardIsClosedNotOpen(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantSub string
	}{
		{
			name:    "an extra field nobody documented",
			raw:     `{"payloadVersion":1,"installId":"` + sampleInstallID + `","vnproxVersion":"3.0.3","pveVersion":"pve-manager/9.2.4","kernel":"6.8.12-4-pve","nicPciIds":[],"nodeCount":2,"suite":"hardware","checks":[],"clusterName":"office"}`,
			wantSub: "unknown-field",
		},
		{
			name:    "an install-id that is not a ULID",
			raw:     `{"payloadVersion":1,"installId":"node-alpha","vnproxVersion":"3.0.3","pveVersion":"pve-manager/9.2.4","kernel":"6.8.12-4-pve","nicPciIds":[],"nodeCount":2,"suite":"hardware","checks":[]}`,
			wantSub: "install-id-not-a-ulid",
		},
		{
			name:    "a nicPciIds entry that is not a PCI id",
			raw:     `{"payloadVersion":1,"installId":"` + sampleInstallID + `","vnproxVersion":"3.0.3","pveVersion":"pve-manager/9.2.4","kernel":"6.8.12-4-pve","nicPciIds":["enp3s0"],"nodeCount":2,"suite":"hardware","checks":[]}`,
			wantSub: "not-a-pci-id",
		},
		{
			name:    "a verdict that is not one of the three",
			raw:     `{"payloadVersion":1,"installId":"` + sampleInstallID + `","vnproxVersion":"3.0.3","pveVersion":"pve-manager/9.2.4","kernel":"6.8.12-4-pve","nicPciIds":[],"nodeCount":2,"suite":"hardware","checks":[{"id":"drift.config_vs_live","status":"probably fine","durationMs":1}]}`,
			wantSub: "unknown-status",
		},
		{
			name:    "a suite nobody runs",
			raw:     `{"payloadVersion":1,"installId":"` + sampleInstallID + `","vnproxVersion":"3.0.3","pveVersion":"pve-manager/9.2.4","kernel":"6.8.12-4-pve","nicPciIds":[],"nodeCount":2,"suite":"everything","checks":[]}`,
			wantSub: "unknown-suite",
		},
		{
			name:    "a missing field",
			raw:     `{"payloadVersion":1,"installId":"` + sampleInstallID + `","vnproxVersion":"3.0.3","pveVersion":"pve-manager/9.2.4","kernel":"6.8.12-4-pve","nicPciIds":[],"suite":"hardware","checks":[]}`,
			wantSub: "missing-field",
		},
		{
			name:    "a kernel string with a newline in it",
			raw:     `{"payloadVersion":1,"installId":"` + sampleInstallID + `","vnproxVersion":"3.0.3","pveVersion":"pve-manager/9.2.4","kernel":"6.8.12-4-pve\nnode-alpha","nicPciIds":[],"nodeCount":2,"suite":"hardware","checks":[]}`,
			wantSub: "not-printable-text",
		},
		{
			name:    "something that is not JSON at all",
			raw:     `not json`,
			wantSub: "unparsable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Guard([]byte(tc.raw), nil)
			if err == nil {
				t.Fatalf("the guard passed %s", tc.name)
			}
			ge, ok := AsGuardError(err)
			if !ok {
				t.Fatalf("want a *GuardError, got %T: %v", err, err)
			}
			found := false
			for _, v := range ge.Violations {
				if v.Rule == tc.wantSub {
					found = true
				}
			}
			if !found {
				t.Fatalf("want a %q violation, got %v", tc.wantSub, ge.Violations)
			}
		})
	}

	// Control: the shape those cases were mutations of is accepted.
	clean := `{"payloadVersion":1,"installId":"` + sampleInstallID + `","vnproxVersion":"3.0.3","pveVersion":"pve-manager/9.2.4","kernel":"6.8.12-4-pve","nicPciIds":["0x8086:0x1521"],"nodeCount":2,"suite":"hardware","checks":[{"id":"drift.config_vs_live","status":"pass","durationMs":1}]}`
	if err := Guard([]byte(clean), nil); err != nil {
		t.Fatalf("the guard refused the control document, so every case above proves nothing: %v", err)
	}
}

// TestGuardScansKeysNotOnlyValues: a map-shaped field added later would put
// text in key position, and a value-only scan would sail past it.
func TestGuardScansKeysNotOnlyValues(t *testing.T) {
	raw := `{"payloadVersion":1,"installId":"` + sampleInstallID + `","vnproxVersion":"3.0.3","pveVersion":"pve-manager/9.2.4","kernel":"6.8.12-4-pve","nicPciIds":[],"nodeCount":2,"suite":"hardware","checks":[{"id":"a.b","status":"pass","durationMs":1,"node-alpha":"x"}]}`
	err := Guard([]byte(raw), []Known{{Value: "node-alpha", Class: ClassHostname}})
	if err == nil {
		t.Fatal("a hostname in KEY position was not caught")
	}
	ge, _ := AsGuardError(err)
	sawHostname := false
	for _, v := range ge.Violations {
		if v.Class == ClassHostname && strings.Contains(v.Path, "key") {
			sawHostname = true
		}
	}
	if !sawHostname {
		t.Fatalf("the hostname was not reported against the key: %v", ge.Violations)
	}
}

// TestKnownFromReportHarvestsWhatTheReportActuallyCarries pins the honest
// half of the guard's coverage: node names and the endpoint host come from
// the report, so the hostname and IP classes are covered on real data
// without anyone remembering to pass anything in.
func TestKnownFromReportHarvestsWhatTheReportActuallyCarries(t *testing.T) {
	known := KnownFromReport(sampleReport())
	want := map[string]Class{
		"node-alpha": ClassHostname,
		"node-beta":  ClassHostname,
		"192.0.2.10": ClassIP,
	}
	got := map[string]Class{}
	for _, k := range known {
		got[k.Value] = k.Class
	}
	for value, class := range want {
		if got[value] != class {
			t.Errorf("KnownFromReport did not harvest %q as %s (got %q); the known-value rule would not catch it", value, class, got[value])
		}
	}
}

func TestEndpointHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{in: "https://192.0.2.10:8006", want: "192.0.2.10"},
		{in: "https://pve1.example.com:8006/api2/json", want: "pve1.example.com"},
		{in: "https://[2001:db8::1]:8006", want: "2001:db8::1"},
		{in: "pve1", want: "pve1"},
		{in: "", want: ""},
	}
	for _, tc := range cases {
		if got := endpointHost(tc.in); got != tc.want {
			t.Errorf("endpointHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
