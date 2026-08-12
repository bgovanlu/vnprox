// changesets_preview_test.go covers T-2605's GET /changesets/{id}/preview at
// the route level, and in particular AC4: the endpoint is side-effect free.
//
// A ZERO-CALL ASSERTION IS VACUOUS WITHOUT A CONTROL LEG. "The PVE mock
// recorded no writes" proves nothing unless the same mock, reached through the
// same client the daemon would use, is shown to record a write when something
// actually writes. The same goes for the store: "the checksum did not change"
// proves nothing unless the checksum is shown to change when a row does. Both
// control legs are here, in the same tests as the assertions they underwrite.

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// --- the PVE call recorder -------------------------------------------------

// pveCallRecorder wraps a pvemock server and records every request that
// reaches it, excluding the login exchange (authentication is not a write to
// the cluster's config, and a ticket-auth client always makes one).
type pveCallRecorder struct {
	inner http.Handler
	calls []string
}

func (r *pveCallRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/api2/json/access/ticket" {
		r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	}
	r.inner.ServeHTTP(w, req)
}

func (r *pveCallRecorder) writes() []string {
	var out []string
	for _, c := range r.calls {
		if !strings.HasPrefix(c, http.MethodGet+" ") {
			out = append(out, c)
		}
	}
	return out
}

// previewGateway is a change.PVEGateway that reaches real PVE (the mock) for
// the one method the control leg exercises. Every other method is inherited
// from the embedded nil interface and is never called — this exists to give
// the router a gateway that demonstrably CAN write, so "the preview route
// wrote nothing" is a statement about the route rather than about a test that
// had nothing to write with.
type previewGateway struct {
	change.PVEGateway
	client *pve.Client
}

func (g previewGateway) SDNStageOp(ctx context.Context, op change.Op, _ string) error {
	if err := g.client.CreateSDNZone(ctx, pve.SDNZone{ID: op.Target.ID, Type: "simple"}); err != nil {
		return fmt.Errorf("staging %s: %w", op.Type, err)
	}
	return nil
}

type previewGatewayProvider struct{ gw change.PVEGateway }

func (p previewGatewayProvider) GatewayFor(context.Context) (change.PVEGateway, bool) {
	return p.gw, true
}

// --- the store checksum ----------------------------------------------------

// storeChecksum hashes the ENTIRE logical content of the store: every user
// table, every row, every column. It is deliberately not a hash of the
// database file — in WAL mode a read touches the sidecar files and a write may
// not touch the main one, so a file hash is both flakier and less sensitive
// than this.
func storeChecksum(t *testing.T, db *store.DB) string {
	t.Helper()
	conn := db.Conn()
	tableRows, err := conn.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	var tables []string
	for tableRows.Next() {
		var name string
		if scanErr := tableRows.Scan(&name); scanErr != nil {
			t.Fatalf("scanning table name: %v", scanErr)
		}
		tables = append(tables, name)
	}
	if err := tableRows.Err(); err != nil {
		t.Fatalf("iterating tables: %v", err)
	}
	if closeErr := tableRows.Close(); closeErr != nil {
		t.Fatalf("closing table cursor: %v", closeErr)
	}
	if len(tables) == 0 {
		t.Fatal("the store has no tables; a checksum over nothing would pass every assertion")
	}

	h := sha256.New()
	for _, table := range tables {
		_, _ = fmt.Fprintf(h, "table:%s\n", table)
		//nolint:gosec // table names come from sqlite_master, not from a request
		rows, queryErr := conn.Query("SELECT * FROM " + table)
		if queryErr != nil {
			t.Fatalf("selecting from %s: %v", table, queryErr)
		}
		cols, colErr := rows.Columns()
		if colErr != nil {
			t.Fatalf("columns of %s: %v", table, colErr)
		}
		var rendered []string
		for rows.Next() {
			cells := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range cells {
				ptrs[i] = &cells[i]
			}
			if scanErr := rows.Scan(ptrs...); scanErr != nil {
				t.Fatalf("scanning %s: %v", table, scanErr)
			}
			rendered = append(rendered, fmt.Sprintf("%v", cells))
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			t.Fatalf("iterating %s: %v", table, rowsErr)
		}
		if closeErr := rows.Close(); closeErr != nil {
			t.Fatalf("closing %s cursor: %v", table, closeErr)
		}
		// SQLite makes no ordering promise without ORDER BY, and this hash must
		// answer "did the content change", not "did the storage order change".
		sort.Strings(rendered)
		for _, r := range rendered {
			_, _ = fmt.Fprintf(h, "%s\n", r)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- the harness -----------------------------------------------------------

type previewHarness struct {
	router   http.Handler
	svc      *change.Service
	db       *store.DB
	recorder *pveCallRecorder
	gateway  change.PVEGateway
}

func newPreviewHarness(t *testing.T) *previewHarness {
	t.Helper()

	fx, err := pvemock.LoadFixture("../../testdata/clusters/single-node.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	rec := &pveCallRecorder{inner: pvemock.NewServer(fx)}
	ts := httptest.NewServer(rec)
	t.Cleanup(ts.Close)
	client, err := pve.New(pve.Config{
		APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

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
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Inventory:  previewTestInventory{},
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}

	gw := previewGateway{client: client}
	return &previewHarness{
		router: NewRouter(Options{
			Version: "test", DistFS: testDistFS(), Logger: testLogger(),
			Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
			Changesets: svc, PVEGateways: previewGatewayProvider{gw: gw},
		}),
		svc: svc, db: db, recorder: rec, gateway: gw,
	}
}

// previewTestInventory is a one-node, one-bridge graph — enough for a
// changeset's referential validation to pass so the preview is not refused for
// an unrelated reason.
type previewTestInventory struct{}

func (previewTestInventory) Snapshot() inventory.Snapshot {
	g := inventory.NewGraph()
	ents := []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online"},
		&inventory.PhysNic{Ref: inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}, Name: "eno1", LinkUp: true, LinkUpSet: true},
		&inventory.Bridge{
			Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
			Name: "vmbr0", Virt: inventory.BridgeLinux,
			PortNames: []string{"eno1"}, DeclaredPortNames: []string{"eno1"},
			Addresses: []string{"10.0.0.10/24"},
		},
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, ents)
	g.ApplyPoll(inventory.SourceHostInterfaces, inventory.Scope{}, ents)
	return g.Snapshot()
}

func getPreview(t *testing.T, r http.Handler, id string) (*httptest.ResponseRecorder, change.Preview) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+id+"/preview", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var preview change.Preview
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
			t.Fatalf("decoding preview: %v (body %s)", err, rec.Body.String())
		}
	}
	return rec, preview
}

// --- AC4 -------------------------------------------------------------------

// AC4: the endpoint is side-effect free. The store's checksum is identical
// before and after, and the PVE mock records zero write calls — each paired
// with a control leg proving the assertion could have failed.
func TestChangesetPreviewRoute_IsSideEffectFree(t *testing.T) {
	h := newPreviewHarness(t)
	ctx := t.Context()

	cs, err := h.svc.Create(ctx, "alice", "add a bridge", []change.Op{{
		Type:   change.OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"},
		Params: &change.BridgeCreateParams{MTU: 1500, Comments: "preview me"},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	before := storeChecksum(t, h.db)
	callsBefore := len(h.recorder.calls)

	// Call it twice: a side effect that only happens on first touch (a lazily
	// created row, a cached fetch) would otherwise hide behind a single call.
	for i := range 2 {
		rec, preview := getPreview(t, h.router, cs.ID)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, body %s", i, rec.Code, rec.Body.String())
		}
		if preview.ChangesetID != cs.ID {
			t.Fatalf("call %d: changesetId = %q, want %q", i, preview.ChangesetID, cs.ID)
		}
	}

	if after := storeChecksum(t, h.db); after != before {
		t.Error("the store changed across a preview request; the preview must write nothing")
	}
	if got := h.recorder.calls[callsBefore:]; len(got) != 0 {
		t.Errorf("the preview reached PVE: %v — it must touch neither the store nor PVE", got)
	}

	// CONTROL LEG 1: the recorder does count a write when something writes.
	// Same mock, same client, same gateway instance the router holds.
	if err := h.gateway.SDNStageOp(ctx, change.Op{
		Type:   change.OpSdnZoneCreate,
		Target: inventory.Ref{Kind: inventory.KindSDNZone, ID: "ctrlzone"},
		Params: &change.SdnZoneCreateParams{Type: "simple"},
	}, ""); err != nil {
		t.Fatalf("control leg: SDNStageOp: %v", err)
	}
	if writes := h.recorder.writes(); len(writes) == 0 {
		t.Fatal("control leg: the recorder counted no write for a real PVE write — " +
			"the zero-call assertion above proves nothing")
	}

	// CONTROL LEG 2: the checksum does change when the store changes. An
	// ordinary draft mutation through the same router.
	body := strings.NewReader(`{"title":"add a bridge","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr9","params":{"mtu":1500,"comments":"changed"}}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/changesets/"+cs.ID, body)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("control leg: PUT /changesets/%s = %d, body %s", cs.ID, rec.Code, rec.Body.String())
	}
	if after := storeChecksum(t, h.db); after == before {
		t.Fatal("control leg: the store checksum did not change after a real store write — " +
			"the identical-checksum assertion above proves nothing")
	}
}

// The route is a netRead route: seeing what the map would look like must not
// require the capability to make it so.
func TestChangesetPreviewRoute_RequiresOnlyNetRead(t *testing.T) {
	h := newPreviewHarness(t)
	cs, err := h.svc.Create(t.Context(), "alice", "add a bridge", []change.Op{{
		Type:   change.OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"},
		Params: &change.BridgeCreateParams{},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	readOnly := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "reader"},
		caps:             map[string]bool{capNetRead: true},
	}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: readOnly, Topology: fakeTopologyService{}, Changesets: h.svc,
	})
	rec, _ := getPreview(t, r, cs.ID)
	if rec.Code != http.StatusOK {
		t.Errorf("status with netRead only = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// AC5 through the route: a changeset with blocking findings is refused with the
// documented 422 validation_failed envelope, not projected into nonsense.
func TestChangesetPreviewRoute_RefusesAnInvalidChangeset(t *testing.T) {
	h := newPreviewHarness(t)
	cs, err := h.svc.Create(t.Context(), "alice", "widen a bridge that isn't there", []change.Op{{
		Type:   change.OpBridgeUpdate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr404"},
		Params: &change.BridgeUpdateParams{},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rec, _ := getPreview(t, h.router, cs.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Findings []struct {
					Severity string `json:"severity"`
					Code     string `json:"code"`
				} `json:"findings"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding error envelope: %v (body %s)", err, rec.Body.String())
	}
	if envelope.Error.Code != "validation_failed" {
		t.Errorf("error code = %q, want validation_failed", envelope.Error.Code)
	}
	if len(envelope.Error.Details.Findings) == 0 {
		t.Error("the refusal carried no findings; the operator cannot act on it")
	}
}

func TestChangesetPreviewRoute_UnknownChangesetIs404(t *testing.T) {
	h := newPreviewHarness(t)
	rec, _ := getPreview(t, h.router, "cs-does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}
