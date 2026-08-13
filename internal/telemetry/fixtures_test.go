package telemetry

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/verify"
)

// sampleInstallID is a syntactically real ULID, written out rather than
// generated so every test's bytes are reproducible.
const sampleInstallID = "01HZY0Z1QW8V9N7M3K5R2T4B6D"

// sampleReport is a report that is realistic in the one way that matters
// here: it is FULL of the things the payload must not contain. Node names,
// an endpoint with an address in it, evidence quoting a hostname and a MAC,
// interface names next to the NIC ids, a guest name in a detail line.
//
// A reduction fixture built from a sanitised report would pass every leak
// assertion in this file without the reduction doing anything at all.
func sampleReport() verify.Report {
	results := []verify.Result{
		{
			ID:           "drift.config_vs_live",
			MatrixRow:    21,
			Area:         "Drift detection (config-vs-live, node-vs-node)",
			Suite:        verify.SuiteHardware,
			Precondition: "a real PVE node",
			Status:       verify.StatusPass,
			Detail:       "node-alpha matches its staged config",
			Evidence: []verify.Evidence{
				verify.NewEvidence(verify.SourceCommand, "ssh node-alpha ip -j link",
					"enp3s0 link/ether aa:bb:cc:dd:ee:ff, address 192.0.2.10/24"),
			},
			DurationMS: 412,
		},
		{
			ID:           "iface.lacp_partner_observed",
			MatrixRow:    6,
			Area:         "Bridges, bonds, VLANs, interfaces",
			Suite:        verify.SuiteHardware,
			Precondition: "a real 802.3ad bond",
			Status:       verify.StatusFail,
			Detail:       "bond0 on node-beta reports no LACP partner; guest web-prod-01 is on it",
			Evidence: []verify.Evidence{
				verify.NewEvidence(verify.SourceFile, "/proc/net/bonding/bond0", "Partner Mac Address: 00:00:00:00:00:00"),
			},
			DurationMS: 1203,
		},
		{
			ID:           "ha.standby_promotes",
			MatrixRow:    30,
			Area:         "Daemon HA",
			Suite:        verify.SuiteHardware,
			Precondition: "two daemons in an active/standby pair",
			Status:       verify.StatusSkip,
			Detail:       "only one node online (node-alpha)",
			SkipReason:   "only one node online (node-alpha)",
			DurationMS:   3,
		},
	}
	return verify.Report{
		ReportVersion: verify.CurrentReportVersion,
		GeneratedAt:   time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		Suite:         verify.SuiteHardware,
		Environment: verify.Environment{
			VnproxVersion: "3.0.3",
			PVEVersion:    "pve-manager/9.2.4",
			Kernel:        "6.8.12-4-pve",
			NICModels: []string{
				"enp3s0 0x8086:0x1521 pci:v00008086d00001521sv00008086sd00000002bc02sc00i00",
				"enp4s0 0x15b3:0x1017 pci:v000015B3d00001017sv000015B3sd00000001bc02sc00i00",
			},
			Nodes:       []string{"node-alpha", "node-beta"},
			PVEEndpoint: "https://192.0.2.10:8006",
		},
		Results: results,
		Summary: verify.Summarize(results),
	}
}

// mustBuild builds a snapshot from the sample report, failing the test if
// the guard refuses it — which for the clean fixture would itself be a bug
// worth knowing about.
func mustBuild(t *testing.T) *Snapshot {
	t.Helper()
	snap, err := Build(sampleReport(), sampleInstallID)
	if err != nil {
		t.Fatalf("Build on the clean fixture: %v", err)
	}
	return snap
}

// mutatedPayload reduces the sample report and then lets a test plant
// something in the result, so the guard is exercised against a payload that
// is otherwise exactly what production builds.
func mutatedPayload(t *testing.T, plant func(p *Payload)) []byte {
	t.Helper()
	p := Reduce(sampleReport(), sampleInstallID)
	plant(&p)
	raw, err := marshalPayload(p)
	if err != nil {
		t.Fatalf("marshalPayload: %v", err)
	}
	return raw
}
