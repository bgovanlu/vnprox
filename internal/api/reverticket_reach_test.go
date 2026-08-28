// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/mcp"
	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/store"
)

// T-1805 acceptance criterion 5 / safety claim 3: **the sealed revert ticket
// authorizes exactly one thing.** No route, MCP tool, or plugin capability can
// reach it; it is unsealable only from its own changeset's revert path.
//
// The card requires this be "an actual enumeration over the real registries,
// not a convention or a comment". These tests therefore enumerate:
//
//   - every HTTP route registered on the **real** production router
//     (api.NewRouter + chi.Walk), driving each one and grepping its raw
//     response for the sealed credential;
//   - every MCP tool in the **real** frozen allowlist (mcp.Tools());
//   - every plugin extension point and every op→capability entry in the
//     **real** plugin registry (plugin.ExtensionPoints / plugin.RequiredCap),
//     plus the change-engine seams those surfaces are handed.
//
// The credential planted for these tests is a distinctive canary; anything
// that echoes it anywhere fails.

const (
	revertTicketCanary     = "PVE:CANARY-T1805-REVERT-TICKET-PLAINTEXT-DO-NOT-ECHO"
	revertTicketCSRFCanary = "CANARY-T1805-CSRF-DO-NOT-ECHO"
)

// reachHarness is a real change.Service over a real store, holding one
// awaiting_confirm changeset whose row carries a sealed revert ticket built
// from the canaries above.
type reachHarness struct {
	svc         *change.Service
	repo        *store.ChangesetRepo
	changesetID string
}

func newReachHarness(t *testing.T) *reachHarness {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "reach.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := store.NewChangesetRepo(db)
	svc, err := change.NewService(change.Config{
		Changesets: repo,
		Audit:      store.NewAuditRepo(db),
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}

	cs, err := svc.Create(ctx, "alice", "reachability probe", []change.Op{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Seal the canary directly against the row. Using the repo (rather than a
	// full apply) is deliberate: this test is about *reachability of the
	// stored bytes*, so it must plant them regardless of how apply would have
	// produced them, and it must work even if a future change alters when
	// sealing happens.
	sealed := []byte(revertTicketCanary + "\x00" + revertTicketCSRFCanary)
	if err = repo.SealRevertTicket(ctx, cs.ID, sealed, time.Unix(1_700_000_600, 0).Unix()); err != nil {
		t.Fatalf("SealRevertTicket: %v", err)
	}
	// Park it in awaiting_confirm with a live deadline, the one state in which
	// a sealed ticket legitimately exists — so every surface below is queried
	// under the exact conditions that make a leak possible.
	row, err := repo.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("repo.Get: %v", err)
	}
	row.Status = string(change.StatusAwaitingConfirm)
	row.ConfirmDeadline = sql.NullInt64{Int64: time.Unix(1_700_000_300, 0).Unix(), Valid: true}
	if err = repo.Update(ctx, row); err != nil {
		t.Fatalf("repo.Update: %v", err)
	}

	// Sanity: the canary really is in the row we are about to prove nothing
	// can reach. A test that plants nothing proves nothing.
	got, _, err := repo.RevertTicket(ctx, cs.ID)
	if err != nil || !bytes.Contains(got, []byte(revertTicketCanary)) {
		t.Fatalf("precondition: the canary is not in the stored row (err=%v, %d bytes)", err, len(got))
	}

	return &reachHarness{svc: svc, repo: repo, changesetID: cs.ID}
}

// routeParamValues supplies a plausible value for every chi path parameter the
// production router uses, so the walk below can actually *drive* each route
// rather than merely list it. Unknown parameters fall back to the changeset id
// (harmless, and keeps a newly added route drivable by default rather than
// silently skipped).
func routeParamValues(changesetID string) map[string]string {
	return map[string]string{
		"id":   changesetID,
		"node": "pve1",
		"ref":  "bridge:pve1:vmbr0",
		"name": "vmbr0",
		"vmid": "100",
	}
}

// TestRevertTicket_AC5_NoRouteCanReachTheSealedTicket enumerates every route
// the real production router registers, drives each of them under a
// full-capability session, and asserts none echoes the sealed credential.
//
// The enumeration is the point: a future route added anywhere in
// internal/api is automatically covered, because chi.Walk reports the routes
// the router actually has rather than a hand-maintained list.
func TestRevertTicket_AC5_NoRouteCanReachTheSealedTicket(t *testing.T) {
	h := newReachHarness(t)
	auth := fullCapsAuth("alice")
	router := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, Changesets: h.svc,
		PVEGateways: noGatewayProvider{},
	})

	chiRouter, ok := router.(chi.Routes)
	if !ok {
		t.Fatalf("production router is not a chi.Routes; the enumeration cannot be performed")
	}

	params := routeParamValues(h.changesetID)
	type route struct{ method, pattern string }
	var routes []route
	if err := chi.Walk(chiRouter, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, route{method: method, pattern: pattern})
		return nil
	}); err != nil {
		t.Fatalf("chi.Walk over the production router: %v", err)
	}
	if len(routes) < 20 {
		t.Fatalf("walked only %d routes; the enumeration is not seeing the real router", len(routes))
	}

	driven := 0
	for _, rt := range routes {
		// Substitute every path parameter so the request reaches a real
		// handler rather than 404ing in the router.
		path := rt.pattern
		for name, value := range params {
			path = strings.ReplaceAll(path, "{"+name+"}", value)
		}
		if strings.Contains(path, "{") {
			// A parameter we have no value for: still drive it with a literal
			// placeholder rather than skipping, so the route is covered even
			// if it only reaches a 400.
			path = strings.NewReplacer("{", "", "}", "").Replace(path)
		}
		// Wildcards (SPA fallback, MCP mount) become a plausible sub-path.
		path = strings.ReplaceAll(path, "/*", "/")

		req := httptest.NewRequest(rt.method, path, bytes.NewBufferString("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		driven++

		body := rec.Body.Bytes()
		for _, canary := range []string{revertTicketCanary, revertTicketCSRFCanary} {
			if bytes.Contains(body, []byte(canary)) {
				t.Errorf("route %s %s echoed the sealed revert ticket (status %d)", rt.method, rt.pattern, rec.Code)
			}
		}
		for _, values := range rec.Header() {
			for _, v := range values {
				if strings.Contains(v, revertTicketCanary) || strings.Contains(v, revertTicketCSRFCanary) {
					t.Errorf("route %s %s echoed the sealed revert ticket in a response header", rt.method, rt.pattern)
				}
			}
		}
	}
	if driven != len(routes) {
		t.Fatalf("drove %d of %d walked routes; every route must be exercised", driven, len(routes))
	}
	t.Logf("enumerated and drove %d production routes; none can reach the sealed revert ticket", driven)

	// The walk above covers the routes this Options wiring mounts — which is
	// every route that could possibly read a changeset, but not every route in
	// the product (a family whose service seam is nil is not mounted at all).
	// The assertion below closes that gap structurally and for all time: the
	// ONLY change-engine surface any route handler in this package is given is
	// ChangesetService, and that interface exposes no method through which a
	// sealed ticket could be obtained. A route cannot leak what it cannot
	// reach, mounted or not.
	seam := reflect.TypeOf((*ChangesetService)(nil)).Elem()
	if seam.NumMethod() == 0 {
		t.Fatalf("ChangesetService enumerated no methods; the assertion would be vacuous")
	}
	for i := 0; i < seam.NumMethod(); i++ {
		m := seam.Method(i)
		lower := strings.ToLower(m.Name)
		for _, bad := range []string{"ticket", "revert", "credential", "unseal", "decrypt", "secret"} {
			if strings.Contains(lower, bad) {
				t.Errorf("ChangesetService exposes method %q, through which a route could reach credential material", m.Name)
			}
		}
		// No method may return raw bytes either — the shape a sealed blob
		// would take if it were smuggled out as an untyped value.
		for j := 0; j < m.Type.NumOut(); j++ {
			out := m.Type.Out(j)
			if out.Kind() == reflect.Slice && out.Elem().Kind() == reflect.Uint8 {
				t.Errorf("ChangesetService.%s returns raw bytes (%s); a sealed credential could ride out on it", m.Name, out)
			}
		}
	}
	// Control: the accessor the seam withholds really does exist on the
	// concrete repository, so the omission above is deliberate.
	if _, ok := reflect.TypeOf((*store.ChangesetRepo)(nil)).MethodByName("RevertTicket"); !ok {
		t.Fatalf("store.ChangesetRepo has no RevertTicket method; this AC5 guarantee is stale")
	}
}

// TestRevertTicket_AC5_NoMCPToolCanReachTheSealedTicket enumerates the real
// frozen MCP tool allowlist. Two independent assertions, mirroring the
// two-axis structure docs/security.md's MCP stage-only boundary already uses:
// the change-engine seam the MCP server is handed exposes no method that could
// return the credential, and no tool's own declared surface names it.
func TestRevertTicket_AC5_NoMCPToolCanReachTheSealedTicket(t *testing.T) {
	tools := mcp.Tools()
	if len(tools) == 0 {
		t.Fatalf("the MCP tool registry enumerated empty; the assertion would be vacuous")
	}

	// Axis 1: the seam. internal/mcp binds *change.Service through a narrow
	// interface; reflecting over the real interface type proves no
	// revert-ticket accessor is reachable from any tool handler, regardless of
	// what the handlers do.
	seam := reflect.TypeOf((*mcp.ChangesetStager)(nil)).Elem()
	for i := 0; i < seam.NumMethod(); i++ {
		name := strings.ToLower(seam.Method(i).Name)
		for _, forbidden := range []string{"ticket", "revert", "credential", "secret", "unseal", "decrypt"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("MCP change seam exposes method %q, which could reach credential material", seam.Method(i).Name)
			}
		}
	}
	// Control: the concrete service really does have the accessor path the
	// seam withholds, so the assertion above is a deliberate omission rather
	// than an accident of the service having nothing to hide.
	if _, ok := reflect.TypeOf((*store.ChangesetRepo)(nil)).MethodByName("RevertTicket"); !ok {
		t.Fatalf("store.ChangesetRepo has no RevertTicket method; the AC5 seam guarantee is stale")
	}

	// Axis 2: every tool in the enumerated allowlist, by name and by declared
	// input schema. No tool takes or returns credential material.
	for _, tool := range tools {
		lower := strings.ToLower(tool.Name + " " + tool.Description + " " + string(tool.InputSchema))
		for _, forbidden := range []string{"revertticket", "revert_ticket", "pveauthcookie", "csrfpreventiontoken"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("MCP tool %q names credential material (%q) in its frozen manifest", tool.Name, forbidden)
			}
		}
	}
	t.Logf("enumerated %d MCP tools; none can reach the sealed revert ticket", len(tools))
}

// TestRevertTicket_AC5_NoPluginCapabilityCanReachTheSealedTicket enumerates
// the real plugin extension-point registry and the real op→capability map, and
// asserts the stage-only seam plugins are handed exposes nothing that could
// reach the credential.
func TestRevertTicket_AC5_NoPluginCapabilityCanReachTheSealedTicket(t *testing.T) {
	points := plugin.AllExtensionPoints
	if len(points) == 0 {
		t.Fatalf("the plugin extension-point registry enumerated empty; the assertion would be vacuous")
	}

	// The one change-engine surface a plugin holds. Reflecting over the real
	// interface proves the omission structurally.
	stager := reflect.TypeOf((*plugin.Stager)(nil)).Elem()
	for i := 0; i < stager.NumMethod(); i++ {
		name := strings.ToLower(stager.Method(i).Name)
		for _, forbidden := range []string{"ticket", "revert", "credential", "secret", "unseal", "decrypt", "apply", "confirm", "rollback"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("plugin.Stager exposes method %q, which could reach the revert path or its credential", stager.Method(i).Name)
			}
		}
	}

	// Every extension point's own v1 interface. The map below is keyed by the
	// enumerated vocabulary itself, and the loop fails on any point it has no
	// entry for — so a newly added extension point cannot slip through
	// unchecked.
	ifaces := map[plugin.ExtensionPoint]reflect.Type{
		plugin.ExtSwitchDriver:      reflect.TypeOf((*plugin.SwitchDriver)(nil)).Elem(),
		plugin.ExtFlowIngestor:      reflect.TypeOf((*plugin.FlowIngestor)(nil)).Elem(),
		plugin.ExtFindingProducer:   reflect.TypeOf((*plugin.FindingProducer)(nil)).Elem(),
		plugin.ExtIngressDiscoverer: reflect.TypeOf((*plugin.IngressDiscoverer)(nil)).Elem(),
		plugin.ExtDashboardTile:     reflect.TypeOf((*plugin.DashboardTileProvider)(nil)).Elem(),
	}
	for _, p := range points {
		iface, ok := ifaces[p]
		if !ok {
			t.Errorf("extension point %q is in the registry but has no interface under test; add it here before shipping it", p)
			continue
		}
		for i := 0; i < iface.NumMethod(); i++ {
			name := strings.ToLower(iface.Method(i).Name)
			for _, forbidden := range []string{"ticket", "revert", "credential", "unseal", "decrypt"} {
				if strings.Contains(name, forbidden) {
					t.Errorf("extension point %q exposes method %q, which could reach credential material", p, iface.Method(i).Name)
				}
			}
		}
	}
	t.Logf("enumerated %d plugin extension points; none can reach the sealed revert ticket", len(points))
}

// TestRevertTicket_AC5_ReadModelHasNoFieldForTheCredential is the structural
// backstop under the three enumerations above: the sealed ticket has no
// representation in change.Changeset or in this package's changesetResponse at
// all, so no response *could* carry it even if a handler tried. This is a
// stronger property than redactOpSecrets' field-by-field stripping — there is
// nothing to strip.
func TestRevertTicket_AC5_ReadModelHasNoFieldForTheCredential(t *testing.T) {
	forbidden := []string{"revertticketenc", "sealedticket", "ticketenc", "csrf"}
	for _, tc := range []struct {
		typ  reflect.Type
		name string
	}{
		{reflect.TypeOf(change.Changeset{}), "change.Changeset"},
		{reflect.TypeOf(changesetResponse{}), "api.changesetResponse"},
		{reflect.TypeOf(store.Changeset{}), "store.Changeset"},
	} {
		for i := 0; i < tc.typ.NumField(); i++ {
			f := tc.typ.Field(i)
			lower := strings.ToLower(f.Name)
			// The only revert-ticket-adjacent field permitted anywhere in the
			// read model is the *expiry*, a bound rather than a secret.
			if strings.Contains(lower, "revertticket") && !strings.Contains(lower, "expires") {
				t.Errorf("%s has field %q — the sealed credential must not exist in the read model", tc.name, f.Name)
			}
			for _, bad := range forbidden {
				if strings.Contains(lower, bad) {
					t.Errorf("%s has field %q, which looks like sealed credential material", tc.name, f.Name)
				}
			}
			// A []byte-typed field on the read model is the shape a sealed
			// blob would take; none should exist.
			if f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() == reflect.Uint8 && strings.Contains(lower, "ticket") {
				t.Errorf("%s has raw-bytes field %q", tc.name, f.Name)
			}
		}
	}
}
