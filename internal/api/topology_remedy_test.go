// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// Phase 36: topology.FindingRemediation is a structural copy of
// findings.Remediation, forced by the import direction (internal/findings
// imports internal/topology, so the dependency cannot run the other way).
//
// A silently-diverged copy is precisely the failure this phase exists to
// prevent — a renderer keying off `action` would simply stop finding the
// field, the button would vanish, and the finding would still look fine.
// So the two are pinned by round-tripping one through the other's JSON: if
// a field is renamed, retyped or added on either side, this fails.
func TestFindingRemediation_MirrorsTheFindingsPackageShape(t *testing.T) {
	src := findings.Remediation{
		Action: findings.RemedyActionServiceStart,
		Kind:   findings.RemedyOperational,
		Label:  "Start dnsmasq",
		Params: map[string]string{"node": "pvecube", "service": "dnsmasq"},
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal findings.Remediation: %v", err)
	}

	// Decoding into the mirror must lose nothing...
	var mirror topology.FindingRemediation
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if decErr := dec.Decode(&mirror); decErr != nil {
		t.Fatalf("topology.FindingRemediation cannot decode findings.Remediation's JSON — the shapes have diverged: %v", decErr)
	}
	if mirror.Action != src.Action || mirror.Kind != string(src.Kind) || mirror.Label != src.Label {
		t.Errorf("mirror = %+v, want the source's values", mirror)
	}
	if mirror.Params["node"] != "pvecube" || mirror.Params["service"] != "dnsmasq" {
		t.Errorf("mirror params = %v", mirror.Params)
	}

	// ...and re-encoding must produce byte-identical JSON, which is what
	// actually matters: the SPA parses one shape for both surfaces.
	back, err := json.Marshal(mirror)
	if err != nil {
		t.Fatalf("marshal mirror: %v", err)
	}
	if string(back) != string(b) {
		t.Errorf("round-trip changed the wire bytes:\n  findings: %s\n  topology: %s", b, back)
	}
}

// The remedy actually reaches the map. Without this, a service_down
// finding would render in the topology banner with no button while the
// findings stream showed one — the two surfaces disagreeing, which is the
// thing Phase 36 is for.
func TestPaintFindings_CarriesTheRemedyOntoUnrefFindings(t *testing.T) {
	var top topology.Topology
	paintFindings(&top, []findings.Finding{{
		ID: "health:service_down|pvecube|dnsmasq", Source: findings.SourceHealth,
		Check: "service_down", Severity: findings.SeverityError,
		Detail: "dnsmasq is not running on node pvecube", Nodes: []string{"pvecube"},
		Remedy: &findings.Remediation{
			Action: findings.RemedyActionServiceStart, Kind: findings.RemedyOperational,
			Label: "Start dnsmasq", Params: map[string]string{"node": "pvecube", "service": "dnsmasq"},
		},
	}})
	if len(top.UnrefFindings) != 1 {
		t.Fatalf("got %d unref findings, want 1", len(top.UnrefFindings))
	}
	r := top.UnrefFindings[0].Remedy
	if r == nil {
		t.Fatal("remedy was dropped on the way to the map")
	}
	if r.Action != findings.RemedyActionServiceStart || r.Params["service"] != "dnsmasq" {
		t.Errorf("remedy = %+v", r)
	}
}

// A detection-only finding must carry no remedy key at all, so an older
// SPA sees a byte-identical payload.
func TestPaintFindings_DetectionOnlyCarriesNoRemedy(t *testing.T) {
	var top topology.Topology
	paintFindings(&top, []findings.Finding{{
		ID: "health:x|pve1", Source: findings.SourceHealth, Check: "x",
		Severity: findings.SeverityWarning, Detail: "d", Nodes: []string{"pve1"},
	}})
	if len(top.UnrefFindings) != 1 {
		t.Fatalf("got %d unref findings, want 1", len(top.UnrefFindings))
	}
	if top.UnrefFindings[0].Remedy != nil {
		t.Error("a detection-only finding gained a remedy")
	}
	b, err := json.Marshal(top.UnrefFindings[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytesContain(b, "remedy") {
		t.Errorf("json = %s, want no remedy key", b)
	}
}

func bytesContain(b []byte, sub string) bool {
	s := string(b)
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
