// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// This file covers GET /changesets/{id}/diff's 200 success path through the
// mounted router with a fully apply-configured Service (audit finding F-09:
// only the 503-unconfigured route test existed; the multi-node diff logic
// was tested only by calling ifaces.DiffChangeset directly).

// diffRouteNodeAgent satisfies change.NodeAgent for diff-only tests by
// reading each node's /etc/network/interfaces from the pvemock fixture.
// The write-side methods are never reached by the diff route.
type diffRouteNodeAgent struct {
	reader *pvemock.FixtureHostReader
}

func (a diffRouteNodeAgent) ReadInterfaces(ctx context.Context, node string) (string, error) {
	return a.reader.InterfacesFile(ctx, node, false)
}

func (diffRouteNodeAgent) StageInterfaces(context.Context, string, string) error { return nil }
func (diffRouteNodeAgent) ReloadInterfaces(context.Context, string) error        { return nil }
func (diffRouteNodeAgent) DiscardStaged(context.Context, string) error           { return nil }

// newDiffRouteTestService builds a change.Service whose apply engine is
// configured (NodeAgent + SnapshotRepo present) against the three-node
// pvemock fixture, so Service.Diff renders real per-node diffs instead of
// returning 503 apply_unavailable.
func newDiffRouteTestService(t *testing.T) *change.Service {
	t.Helper()
	fx, err := pvemock.LoadFixture("../../testdata/clusters/three-node-vlan.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(fx)

	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})

	svc, err := change.NewService(change.Config{
		Changesets:    store.NewChangesetRepo(db),
		Audit:         store.NewAuditRepo(db),
		Snapshots:     store.NewSnapshotRepo(db),
		Blobs:         store.NewBlobRepo(db),
		Nodes:         diffRouteNodeAgent{reader: pvemock.NewFixtureHostReader(srv)},
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"),
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

// diffResponse mirrors docs/api.md's diff endpoint shape ("rendered diff:
// per-file unified diffs + structured op summaries"), i.e. the JSON form of
// ifaces.ChangesetDiff. Field names are asserted here so the wire contract
// stays pinned independently of the Go struct tags.
type diffResponse struct {
	Files []struct {
		Node    string `json:"node"`
		Path    string `json:"path"`
		Unified string `json:"unified"`
		Changed bool   `json:"changed"`
	} `json:"files"`
	Ops []struct {
		Op      string `json:"op"`
		Target  string `json:"target"`
		Node    string `json:"node"`
		Summary string `json:"summary"`
	} `json:"ops"`
}

// TestChangesetsDiffRoute_MultiNode200 drives a three-node draft through
// the mounted internal/api router to a 200 on GET /changesets/{id}/diff
// (T-204 AC4 at the HTTP level) and asserts the documented response body.
func TestChangesetsDiffRoute_MultiNode200(t *testing.T) {
	svc := newDiffRouteTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	createBody := `{"title":"multi-node edit","ops":[
		{"op":"iface.update","target":"bridge:pve1:vmbr0","params":{"mtu":9000}},
		{"op":"vlan.create","target":"vlan:pve2:vmbr0.100","params":{"parent":"vmbr0","vid":100,"addresses":["10.100.0.12/24"]}},
		{"op":"iface.update","target":"physnic:pve3:eno2","params":{"comments":"spare uplink"}}
	]}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(createBody))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /changesets status = %d, want 201, body: %s", createRec.Code, createRec.Body.String())
	}
	var created changesetResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	diffReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID+"/diff", nil)
	diffRec := httptest.NewRecorder()
	r.ServeHTTP(diffRec, diffReq)
	if diffRec.Code != http.StatusOK {
		t.Fatalf("GET /changesets/{id}/diff status = %d, want 200, body: %s", diffRec.Code, diffRec.Body.String())
	}
	if ct := diffRec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var diff diffResponse
	if err := json.NewDecoder(diffRec.Body).Decode(&diff); err != nil {
		t.Fatalf("decoding diff response: %v", err)
	}

	if len(diff.Files) != 3 {
		t.Fatalf("len(files) = %d, want 3 (one per node), body: %+v", len(diff.Files), diff)
	}
	// Files come back in first-appearance order across ops.
	for i, wantNode := range []string{"pve1", "pve2", "pve3"} {
		fd := diff.Files[i]
		if fd.Node != wantNode {
			t.Errorf("files[%d].node = %q, want %q", i, fd.Node, wantNode)
		}
		if fd.Path != "/etc/network/interfaces" {
			t.Errorf("files[%d].path = %q, want /etc/network/interfaces", i, fd.Path)
		}
		if !fd.Changed || fd.Unified == "" {
			t.Errorf("files[%d] (%s): expected a non-empty changed diff, got changed=%v unified=%q", i, fd.Node, fd.Changed, fd.Unified)
		}
		if !strings.Contains(fd.Unified, "--- /etc/network/interfaces") ||
			!strings.Contains(fd.Unified, "+++ /etc/network/interfaces") ||
			!strings.Contains(fd.Unified, "@@ ") {
			t.Errorf("files[%d] (%s): unified diff missing headers/hunks:\n%s", i, fd.Node, fd.Unified)
		}
	}
	if !strings.Contains(diff.Files[0].Unified, "+\tmtu 9000") {
		t.Errorf("pve1 diff missing mtu bump:\n%s", diff.Files[0].Unified)
	}
	if !strings.Contains(diff.Files[1].Unified, "+iface vmbr0.100 inet static") {
		t.Errorf("pve2 diff missing new VLAN stanza:\n%s", diff.Files[1].Unified)
	}
	if !strings.Contains(diff.Files[1].Unified, "managed by vnprox (changeset "+created.ID+")") {
		t.Errorf("pve2 diff missing managed-by-vnprox marker:\n%s", diff.Files[1].Unified)
	}
	if !strings.Contains(diff.Files[2].Unified, "+\t#spare uplink") {
		t.Errorf("pve3 diff missing comment addition:\n%s", diff.Files[2].Unified)
	}

	if len(diff.Ops) != 3 {
		t.Fatalf("len(ops) = %d, want 3, body: %+v", len(diff.Ops), diff)
	}
	wantOps := []struct{ op, target, node string }{
		{"iface.update", "bridge:pve1:vmbr0", "pve1"},
		{"vlan.create", "vlan:pve2:vmbr0.100", "pve2"},
		{"iface.update", "physnic:pve3:eno2", "pve3"},
	}
	for i, want := range wantOps {
		got := diff.Ops[i]
		if got.Op != want.op || got.Target != want.target || got.Node != want.node {
			t.Errorf("ops[%d] = {op:%q target:%q node:%q}, want {op:%q target:%q node:%q}", i, got.Op, got.Target, got.Node, want.op, want.target, want.node)
		}
		if got.Summary == "" {
			t.Errorf("ops[%d].summary is empty", i)
		}
	}
}

// TestChangesetsDiffRoute_NotFound404 pins the configured-engine 404 (the
// unconfigured 503 is covered in changesets_test.go).
func TestChangesetsDiffRoute_NotFound404(t *testing.T) {
	svc := newDiffRouteTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/does-not-exist/diff", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}
