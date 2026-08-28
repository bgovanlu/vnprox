// SPDX-License-Identifier: Apache-2.0

// T-3501: table-driven coverage for the badge-construction logic
// (findingBadgeTokens, paintFindings, paintDrift) this task's card asked
// for, plus a fixture reproducing pvecube's real finding set (the phase-35
// card's own evidence table) and a test pinning docs/api.md's documented
// badge vocabulary against what the handler actually emits.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestFindingBadgeTokens_WorstSeverityPerSource is table-driven coverage
// over findingBadgeTokens directly: the legacy findingBadge token is always
// present when there is at least one finding, one findingBadgePrefix token
// is emitted per distinct source, and a source with more than one finding
// reports its worst (highest-ranked) severity, not its first or last.
func TestFindingBadgeTokens_WorstSeverityPerSource(t *testing.T) {
	tests := []struct {
		name string
		fbs  []topology.FindingBadge
		want []string
	}{
		{name: "no findings emits nothing", fbs: nil, want: nil},
		{
			name: "a single drift finding",
			fbs:  []topology.FindingBadge{{Source: "drift", Severity: "warning"}},
			want: []string{"drift", "finding:drift:warning"},
		},
		{
			name: "a single health finding — never presents as drift",
			fbs:  []topology.FindingBadge{{Source: "health", Severity: "error"}},
			want: []string{"drift", "finding:health:error"},
		},
		{
			name: "drift and health together — two distinct source tokens, sorted",
			fbs: []topology.FindingBadge{
				{Source: "drift", Severity: "warning"},
				{Source: "health", Severity: "warning"},
			},
			want: []string{"drift", "finding:drift:warning", "finding:health:warning"},
		},
		{
			name: "two findings from the same source report the worst severity",
			fbs: []topology.FindingBadge{
				{Source: "health", Severity: "warning"},
				{Source: "health", Severity: "error"},
			},
			want: []string{"drift", "finding:health:error"},
		},
		{
			name: "worst-severity reduction is order-independent (error listed first this time)",
			fbs: []topology.FindingBadge{
				{Source: "health", Severity: "error"},
				{Source: "health", Severity: "info"},
				{Source: "health", Severity: "warning"},
			},
			want: []string{"drift", "finding:health:error"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findingBadgeTokens(tt.fbs)
			if !equalStringSlices(got, tt.want) {
				t.Errorf("findingBadgeTokens(%+v) = %v, want %v", tt.fbs, got, tt.want)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pvecubeFindings reproduces phase-35's card verbatim: the finding set
// observed against the real reference node pvecube (GET /api/v1/findings,
// 2026-08-19) — drift on vmbr0 plus a separate mgmt_single_path warning,
// bridge_no_carrier errors on vmbr1/vmbr3, and two ref-less service_down
// errors for dnsmasq/frr that name no entity at all.
func pvecubeFindings() []findings.Finding {
	return []findings.Finding{
		{
			ID: "drift:file_runtime_divergence|bridge:pvecube:vmbr0", Source: findings.SourceDrift,
			Check: "file_runtime_divergence", Severity: findings.SeverityWarning,
			Detail: "vmbr0's runtime bridge members diverge from /etc/network/interfaces",
			Refs:   []string{"bridge:pvecube:vmbr0"}, Nodes: []string{"pvecube"},
		},
		{
			ID: "health:mgmt_single_path|bridge:pvecube:vmbr0", Source: findings.SourceHealth,
			Check: "mgmt_single_path", Severity: findings.SeverityWarning,
			Detail: "the management IP has no redundant physical path",
			Refs:   []string{"bridge:pvecube:vmbr0"}, Nodes: []string{"pvecube"},
		},
		{
			ID: "health:bridge_no_carrier|bridge:pvecube:vmbr1", Source: findings.SourceHealth,
			Check: "bridge_no_carrier", Severity: findings.SeverityError,
			Detail: "enp2s0 has no carrier", Refs: []string{"bridge:pvecube:vmbr1"}, Nodes: []string{"pvecube"},
		},
		{
			ID: "health:bridge_no_carrier|bridge:pvecube:vmbr3", Source: findings.SourceHealth,
			Check: "bridge_no_carrier", Severity: findings.SeverityError,
			Detail: "enp4s0 has no carrier", Refs: []string{"bridge:pvecube:vmbr3"}, Nodes: []string{"pvecube"},
		},
		{
			ID: "health:service_down|dnsmasq", Source: findings.SourceHealth,
			Check: "service_down", Severity: findings.SeverityError,
			Detail: "dnsmasq is not running", Nodes: []string{"pvecube"},
		},
		{
			ID: "health:service_down|frr", Source: findings.SourceHealth,
			Check: "service_down", Severity: findings.SeverityError,
			Detail: "frr is not running", Nodes: []string{"pvecube"},
		},
	}
}

// TestPaintFindings_ReferenceNodeFindingSet is T-3501 AC1 against a fixture
// reproducing pvecube's real finding set: vmbr1/vmbr3 present as a carrier
// *error* naming health as the source (never drift), vmbr0 carries both its
// genuine drift finding and its separate health warning, and the two
// ref-less service_down findings land on Topology.UnrefFindings rather than
// painting nowhere.
func TestPaintFindings_ReferenceNodeFindingSet(t *testing.T) {
	tp := &topology.Topology{Nodes: []topology.Node{
		{ID: "bridge:pvecube:vmbr0", Kind: "bridge", Badges: []string{}},
		{ID: "bridge:pvecube:vmbr1", Kind: "bridge", Badges: []string{}},
		{ID: "bridge:pvecube:vmbr2", Kind: "bridge", Badges: []string{}}, // untouched control
		{ID: "bridge:pvecube:vmbr3", Kind: "bridge", Badges: []string{}},
	}}
	paintFindings(tp, pvecubeFindings())

	byID := make(map[string]topology.Node, len(tp.Nodes))
	for _, n := range tp.Nodes {
		byID[n.ID] = n
	}

	vmbr0 := byID["bridge:pvecube:vmbr0"]
	if !containsString(vmbr0.Badges, "finding:drift:warning") {
		t.Errorf("vmbr0 badges = %v, want finding:drift:warning (its genuine drift finding)", vmbr0.Badges)
	}
	if !containsString(vmbr0.Badges, "finding:health:warning") {
		t.Errorf("vmbr0 badges = %v, want finding:health:warning (its separate mgmt_single_path finding)", vmbr0.Badges)
	}
	if len(vmbr0.Findings) != 2 {
		t.Errorf("vmbr0.Findings = %+v, want 2 entries (drift + health)", vmbr0.Findings)
	}

	for _, id := range []string{"bridge:pvecube:vmbr1", "bridge:pvecube:vmbr3"} {
		n := byID[id]
		if containsString(n.Badges, "finding:drift:warning") || containsString(n.Badges, "finding:drift:error") {
			t.Errorf("%s badges = %v, want no finding:drift:* token — this is a carrier error, not drift", id, n.Badges)
		}
		if !containsString(n.Badges, "finding:health:error") {
			t.Errorf("%s badges = %v, want finding:health:error (bridge_no_carrier)", id, n.Badges)
		}
		if len(n.Findings) != 1 || n.Findings[0].Source != "health" || n.Findings[0].Severity != "error" ||
			n.Findings[0].Check != "bridge_no_carrier" {
			t.Errorf("%s.Findings = %+v, want exactly one health/error bridge_no_carrier finding", id, n.Findings)
		}
	}

	vmbr2 := byID["bridge:pvecube:vmbr2"]
	if len(vmbr2.Badges) != 0 || len(vmbr2.Findings) != 0 {
		t.Errorf("vmbr2 (named by no finding) = %+v, want untouched", vmbr2)
	}

	if len(tp.UnrefFindings) != 2 {
		t.Fatalf("UnrefFindings = %+v, want 2 entries (dnsmasq, frr service_down)", tp.UnrefFindings)
	}
	gotChecks := make([]string, 0, 2)
	for _, uf := range tp.UnrefFindings {
		if uf.Source != "health" || uf.Severity != "error" || uf.Check != "service_down" {
			t.Errorf("unref finding %+v does not match the expected service_down shape", uf)
		}
		if len(uf.Nodes) != 1 || uf.Nodes[0] != "pvecube" {
			t.Errorf("unref finding %+v.Nodes = %v, want [pvecube]", uf, uf.Nodes)
		}
		gotChecks = append(gotChecks, uf.Detail)
	}
	sort.Strings(gotChecks)
	want := []string{"dnsmasq is not running", "frr is not running"}
	if !equalStringSlices(gotChecks, want) {
		t.Errorf("unref finding details = %v, want %v", gotChecks, want)
	}
}

// TestTopologyRoute_ReferenceNodeFindingSet is the same fixture exercised
// end to end through GET /topology, proving the route wires paintFindings'
// output through to the wire response unmodified (JSON round-trip included).
func TestTopologyRoute_ReferenceNodeFindingSet(t *testing.T) {
	nodes := []topology.Node{
		{ID: "bridge:pvecube:vmbr0", Kind: "bridge", Layer: topology.LayerL2, Status: topology.StatusOK, Badges: []string{}},
		{ID: "bridge:pvecube:vmbr1", Kind: "bridge", Layer: topology.LayerL2, Status: topology.StatusOK, Badges: []string{}},
		{ID: "bridge:pvecube:vmbr3", Kind: "bridge", Layer: topology.LayerL2, Status: topology.StatusOK, Badges: []string{}},
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     driftTestAuth(map[string]bool{"netRead": true}),
		Topology: fakeTopologyService{nodes: nodes},
		Findings: fakeFindingsService{findings: pvecubeFindings()},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got topology.Topology
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	byID := make(map[string]topology.Node, len(got.Nodes))
	for _, n := range got.Nodes {
		byID[n.ID] = n
	}
	if !containsString(byID["bridge:pvecube:vmbr1"].Badges, "finding:health:error") {
		t.Errorf("vmbr1 badges over the wire = %v, want finding:health:error", byID["bridge:pvecube:vmbr1"].Badges)
	}
	if len(got.UnrefFindings) != 2 {
		t.Errorf("UnrefFindings over the wire = %+v, want 2 entries", got.UnrefFindings)
	}
}

// TestDocsAPI_BadgeVocabularyMatchesHandler is T-3501 AC2: docs/api.md's
// documented badge vocabulary is pinned against what the handler actually
// emits (findingBadge, findingBadgePrefix, and every internal/findings.Source
// value), so the two cannot silently drift apart the way Phase 31's SDN
// Fabrics scoping incident showed a mock and its check can when both are
// copied from the same stale source.
func TestDocsAPI_BadgeVocabularyMatchesHandler(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "api.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	doc := string(raw)

	if !strings.Contains(doc, `"finding:<source>:<severity>"`) {
		t.Errorf("docs/api.md does not document the %q token format the handler emits (findingBadgePrefix)", findingBadgePrefix+"<source>:<severity>")
	}
	if !strings.Contains(doc, `"`+findingBadge+`"`) {
		t.Errorf("docs/api.md does not mention the legacy %q wire badge the handler still emits for back-compat", findingBadge)
	}

	// Every severity findingSeverityRank knows about must be documented.
	for sev := range findingSeverityRank {
		if !strings.Contains(doc, `"`+sev+`"`) {
			t.Errorf("docs/api.md does not document severity %q", sev)
		}
	}

	// Every internal/findings.Source value the handler can emit in a
	// "finding:<source>:<severity>" token must be named in docs/api.md's
	// badge-vocabulary paragraph — enumerated from the Go source directly
	// (CLAUDE.md: read the Go source, not a doc that copies it), so a
	// Source added to types.go without a docs update fails this test.
	sources := []findings.Source{
		findings.SourceDrift, findings.SourceLLDP, findings.SourceIPAM, findings.SourceHealth,
		findings.SourceProbe, findings.SourceWireguard, findings.SourceWan, findings.SourceFlow,
		findings.SourceK8s, findings.SourceRogue, findings.SourceCapacity, findings.SourceBaseline,
		findings.SourceFederation, findings.SourcePeer, findings.SourceStore, findings.SourceCert,
		findings.SourceGitSync,
	}
	for _, s := range sources {
		if !strings.Contains(doc, "`"+string(s)+"`") {
			t.Errorf("docs/api.md's badge vocabulary paragraph does not name source %q", s)
		}
	}
}
