package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// fakeFindingsService is a minimal FindingsService stand-in for router tests.
type fakeFindingsService struct {
	fixOps   map[string][]change.Op
	fixTitle map[string]string
	findings []findings.Finding
}

func (f fakeFindingsService) Findings() []findings.Finding { return f.findings }

func (f fakeFindingsService) FixOps(id string) ([]change.Op, string, bool) {
	ops, ok := f.fixOps[id]
	if !ok {
		return nil, "", false
	}
	return ops, f.fixTitle[id], true
}

func mixedSourceFindings() []findings.Finding {
	return []findings.Finding{
		{ID: "drift:1", Source: findings.SourceDrift, Check: "bridge_divergence", Severity: findings.SeverityWarning, Detail: "d1", Nodes: []string{"pve1"}},
		{ID: "lldp:1", Source: findings.SourceLLDP, Check: "vlan_cross_check_missing_on_switch", Severity: findings.SeverityWarning, Detail: "l1", Nodes: []string{"pve2"}},
		{ID: "health:1", Source: findings.SourceHealth, Check: "bond_slave_down", Severity: findings.SeverityError, Detail: "h1", Nodes: []string{"pve1"}},
		{ID: "ipam:1", Source: findings.SourceIPAM, Check: "subnet_conflict", Severity: findings.SeverityInfo, Detail: "i1", Nodes: []string{"pve3"}},
		// T-806: the new "probe" source (POST /simulate/verify's persisted
		// sim_divergence finding) — additive to the four above.
		{ID: "probe:1", Source: findings.SourceProbe, Check: "sim_divergence", Severity: findings.SeverityWarning, Detail: "p1", Refs: []string{"guest-nic:pve1:300/net0"}},
	}
}

func TestFindingsRoute_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{}, Findings: fakeFindingsService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/findings (unauthenticated) status = %d, want 401", rec.Code)
	}
}

// TestFindingsRoute_ReturnsAllSources: AC2's premise at the HTTP layer —
// every source's findings are present in one response.
func TestFindingsRoute_ReturnsAllSources(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: driftTestAuth(map[string]bool{"netRead": true}), Topology: fakeTopologyService{},
		Findings: fakeFindingsService{findings: mixedSourceFindings()},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []findings.Finding `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 5 {
		t.Fatalf("got %d items, want 5 (one per source)", len(body.Items))
	}
}

// TestFindingsRoute_SourceProbeFilter is T-806 AC5's golden test:
// GET /findings?source=probe filters correctly (additive — never a 400 for
// the new value), and every other source's own filtering behavior is
// unchanged (mixedSourceFindings' other four entries are exercised by
// TestFindingsRoute_FiltersBySourceSeverityNode above, unmodified).
func TestFindingsRoute_SourceProbeFilter(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: driftTestAuth(map[string]bool{"netRead": true}), Topology: fakeTopologyService{},
		Findings: fakeFindingsService{findings: mixedSourceFindings()},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/findings?source=probe", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("source=probe status = %d, want 200 (never a 400 for a recognized value), body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []findings.Finding `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ID != "probe:1" {
		t.Fatalf("source=probe items = %+v, want exactly [probe:1]", body.Items)
	}
	if body.Items[0].Source != findings.SourceProbe {
		t.Errorf("source = %q, want %q", body.Items[0].Source, findings.SourceProbe)
	}
}

// TestFindingsRoute_FiltersBySourceSeverityNode: AC2's filter contract,
// exercised uniformly across every source (no per-source special-casing).
func TestFindingsRoute_FiltersBySourceSeverityNode(t *testing.T) {
	base := Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: driftTestAuth(map[string]bool{"netRead": true}), Topology: fakeTopologyService{},
		Findings: fakeFindingsService{findings: mixedSourceFindings()},
	}

	cases := []struct {
		name    string
		query   string
		wantIDs []string
	}{
		{"by source", "?source=lldp", []string{"lldp:1"}},
		{"by severity", "?severity=error", []string{"health:1"}},
		{"by node", "?node=pve1", []string{"drift:1", "health:1"}},
		{"combined source+node", "?source=health&node=pve1", []string{"health:1"}},
		{"combined mismatched", "?source=drift&node=pve2", []string{}},
		{"no filter", "", []string{"drift:1", "lldp:1", "health:1", "ipam:1", "probe:1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRouter(base)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/findings"+tc.query, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Items []findings.Finding `json:"items"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding response: %v", err)
			}
			gotIDs := make([]string, 0, len(body.Items))
			for _, f := range body.Items {
				gotIDs = append(gotIDs, f.ID)
			}
			if len(gotIDs) != len(tc.wantIDs) {
				t.Fatalf("query %q: got IDs %v, want %v", tc.query, gotIDs, tc.wantIDs)
			}
			want := map[string]bool{}
			for _, id := range tc.wantIDs {
				want[id] = true
			}
			for _, id := range gotIDs {
				if !want[id] {
					t.Errorf("query %q: unexpected id %q in result %v", tc.query, id, gotIDs)
				}
			}
		})
	}
}

func TestFindingsFix_CreatesChangeset(t *testing.T) {
	svc := newChangesetTestService(t)
	fs := fakeFindingsService{
		fixOps:   map[string][]change.Op{"health:1": {{Type: change.OpBridgeUpdate}}},
		fixTitle: map[string]string{"health:1": "fix it"},
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     driftTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Topology: fakeTopologyService{}, Findings: fs, Changesets: svc,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/findings/health:1/fix", bytes.NewReader(nil))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

func TestFindingsFix_UnknownFinding404(t *testing.T) {
	svc := newChangesetTestService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     driftTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Topology: fakeTopologyService{}, Findings: fakeFindingsService{}, Changesets: svc,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/findings/no-such-finding/fix", bytes.NewReader(nil))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestTopologyRoute_FindingsSupersedesDrift: when both Drift and Findings
// are wired, the badge overlay comes from the unified stream (proven here
// by a finding that only exists in Findings, not in Drift, still painting).
func TestTopologyRoute_FindingsSupersedesDrift(t *testing.T) {
	nodes := []topology.Node{
		{ID: "bridge:pve1:vmbr0", Kind: "bridge", Layer: topology.LayerL2, Status: topology.StatusOK, Badges: []string{}},
	}
	f := findings.Finding{ID: "health:1", Source: findings.SourceHealth, Check: "bridge_no_carrier",
		Severity: findings.SeverityError, Refs: []string{"bridge:pve1:vmbr0"}}

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     driftTestAuth(map[string]bool{"netRead": true}),
		Topology: fakeTopologyService{nodes: nodes},
		Findings: fakeFindingsService{findings: []findings.Finding{f}},
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
	if len(got.Nodes) != 1 || !containsString(got.Nodes[0].Badges, "drift") {
		t.Errorf("nodes = %+v, want bridge:pve1:vmbr0 painted with the finding badge from the unified stream", got.Nodes)
	}
}
