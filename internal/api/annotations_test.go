package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/annotate"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeAnnotationStore is an in-memory annotate.NoteStore test double. The
// route tests below drive a REAL *annotate.Service over it rather than a
// fake service, so the read-time expiry and orphan derivation the routes
// promise are exercised through the actual code that implements them.
type fakeAnnotationStore struct {
	items map[string]store.Annotation
}

func newFakeAnnotationStore() *fakeAnnotationStore {
	return &fakeAnnotationStore{items: map[string]store.Annotation{}}
}

func (f *fakeAnnotationStore) List(context.Context) ([]store.Annotation, error) {
	out := make([]store.Annotation, 0, len(f.items))
	for _, a := range f.items {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (f *fakeAnnotationStore) Insert(_ context.Context, a store.Annotation) error {
	f.items[a.ID] = a
	return nil
}

func (f *fakeAnnotationStore) Delete(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

// fakeRegionStore is the same in-memory double for annotate.RegionStore.
type fakeRegionStore struct {
	items map[string]store.MapRegion
}

func newFakeRegionStore() *fakeRegionStore {
	return &fakeRegionStore{items: map[string]store.MapRegion{}}
}

func (f *fakeRegionStore) List(context.Context) ([]store.MapRegion, error) {
	out := make([]store.MapRegion, 0, len(f.items))
	for _, m := range f.items {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeRegionStore) Insert(_ context.Context, m store.MapRegion) error {
	f.items[m.ID] = m
	return nil
}

func (f *fakeRegionStore) Delete(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

// newTestAnnotationService builds a real *annotate.Service over in-memory
// stores and a fixed clock, so route tests can move time without sleeping.
func newTestAnnotationService(t *testing.T, notes *fakeAnnotationStore, now func() time.Time) *annotate.Service {
	t.Helper()
	svc, err := annotate.NewService(annotate.Config{
		Notes: notes, Regions: newFakeRegionStore(), Now: now,
	})
	if err != nil {
		t.Fatalf("annotate.NewService: %v", err)
	}
	return svc
}

// newTestAnnotations is the common case: a real service over empty stores
// on the wall clock.
func newTestAnnotations(t *testing.T) *annotate.Service {
	t.Helper()
	return newTestAnnotationService(t, newFakeAnnotationStore(), nil)
}

// TestAnnotationsRoutes_NotMountedWithoutUsernameLookup mirrors
// TestLayoutsRoutes_NotMountedWithoutUsernameLookup: no safe way to stamp
// createdBy without username resolution, so the routes aren't mounted.
func TestAnnotationsRoutes_NotMountedWithoutUsernameLookup(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Annotations: newTestAnnotations(t),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/annotations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted), body: %s", rec.Code, rec.Body.String())
	}
}

// TestAnnotationsRoutes_Unauthenticated401 proves the annotations routes
// are gated by the same session middleware as layouts/topology.
func TestAnnotationsRoutes_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:        fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: false}, username: "alice"},
		Topology:    fakeTopologyService{},
		Annotations: newTestAnnotations(t),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/annotations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401, body: %s", rec.Code, rec.Body.String())
	}
}

// TestAnnotationsRoutes_CreateListDelete_RoundTrips is the end-to-end
// pin/list/unpin round trip (T-907 AC3): pinning a note against an entity
// ref persists it, GET /annotations re-lists it (stamped with the creating
// user), and DELETE removes it.
func TestAnnotationsRoutes_CreateListDelete_RoundTrips(t *testing.T) {
	annotations := newTestAnnotations(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:        fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice@pve"},
		Topology:    fakeTopologyService{},
		Annotations: annotations,
	})

	createBody := bytes.NewBufferString(`{"ref":"bridge:pve1:vmbr0","content":"double-check VLAN tags before Friday"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", createBody)
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201, body: %s", createRec.Code, createRec.Body.String())
	}
	var created annotationResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding POST body: %v", err)
	}
	if created.Ref != "bridge:pve1:vmbr0" || created.Content != "double-check VLAN tags before Friday" {
		t.Errorf("created = %+v, fields don't match request", created)
	}
	if created.CreatedBy != "alice@pve" {
		t.Errorf("createdBy = %q, want alice@pve (server-stamped, not client-supplied)", created.CreatedBy)
	}
	if created.ID == "" {
		t.Error("expected a non-empty server-assigned id")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/annotations", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200, body: %s", listRec.Code, listRec.Body.String())
	}
	var listed annotationsListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decoding GET body: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("Items = %+v, want exactly the just-created annotation", listed.Items)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/annotations/"+created.ID, nil)
	delRec := httptest.NewRecorder()
	r.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204, body: %s", delRec.Code, delRec.Body.String())
	}

	listReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/annotations", nil)
	listRec2 := httptest.NewRecorder()
	r.ServeHTTP(listRec2, listReq2)
	var listedAfterDelete annotationsListResponse
	if err := json.NewDecoder(listRec2.Body).Decode(&listedAfterDelete); err != nil {
		t.Fatalf("decoding second GET body: %v", err)
	}
	if len(listedAfterDelete.Items) != 0 {
		t.Errorf("Items after delete = %+v, want empty", listedAfterDelete.Items)
	}
}

// TestAnnotationsRoutes_CreateMissingFields_400 proves ref/content are
// required — an annotation pinned to nothing, or with no text, is rejected
// rather than silently stored.
func TestAnnotationsRoutes_CreateMissingFields_400(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:        fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		Topology:    fakeTopologyService{},
		Annotations: newTestAnnotations(t),
	})

	for _, body := range []string{``, `{}`, `not json`, `{"ref":""}`, `{"ref":"bridge:pve1:vmbr0"}`, `{"content":"orphaned"}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400, response: %s", body, rec.Code, rec.Body.String())
		}
	}
}

// annotationTestRouter wires a router around svc with an authenticated user.
func annotationTestRouter(t *testing.T, svc AnnotationService) http.Handler {
	t.Helper()
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:        fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice@pve"},
		Topology:    fakeTopologyService{},
		Annotations: svc,
	})
}

func doAnnotationReq(t *testing.T, r http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, bytes.NewBufferString(body))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func listAnnotations(t *testing.T, r http.Handler, target string) annotationsListResponse {
	t.Helper()
	rec := doAnnotationReq(t, r, http.MethodGet, target, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200, body: %s", target, rec.Code, rec.Body.String())
	}
	var out annotationsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decoding %s body: %v", target, err)
	}
	return out
}

// TestAnnotationsRoutes_ExpiryIsComputedAtReadTime is T-2806 AC3 over the
// HTTP surface. The clock is INJECTED and stepped forward by the test —
// there is no sleep and no background sweep anywhere in the path, which is
// the whole point: a daemon that was stopped when the expiry passed still
// must not serve the note on its next read.
func TestAnnotationsRoutes_ExpiryIsComputedAtReadTime(t *testing.T) {
	notes := newFakeAnnotationStore()
	now := time.Unix(1_700_000_000, 0)
	svc := newTestAnnotationService(t, notes, func() time.Time { return now })
	r := annotationTestRouter(t, svc)

	expiresAt := now.Unix() + 3600
	rec := doAnnotationReq(t, r, http.MethodPost, "/api/v1/annotations",
		fmt.Sprintf(`{"ref":"bridge:pve1:vmbr0","content":"temporary uplink until the switch swap","expiresAt":%d}`, expiresAt))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	var created annotationResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding POST body: %v", err)
	}
	if created.ExpiresAt != expiresAt || created.Expired {
		t.Fatalf("created = {expiresAt: %d, expired: %v}, want {%d, false}", created.ExpiresAt, created.Expired, expiresAt)
	}

	if got := listAnnotations(t, r, "/api/v1/annotations"); len(got.Items) != 1 {
		t.Fatalf("before expiry: Items = %+v, want the live note", got.Items)
	}

	// The daemon is "stopped" for a week: nothing runs, and time passes.
	now = now.Add(7 * 24 * time.Hour)

	live := listAnnotations(t, r, "/api/v1/annotations")
	if len(live.Items) != 0 {
		t.Errorf("after expiry: Items = %+v, want none — an expired note must not be displayed", live.Items)
	}

	all := listAnnotations(t, r, "/api/v1/annotations?includeExpired=true")
	if len(all.Items) != 1 || !all.Items[0].Expired {
		t.Fatalf("includeExpired: Items = %+v, want the one note flagged expired", all.Items)
	}
	if all.Items[0].Content != "temporary uplink until the switch swap" {
		t.Errorf("expired note content = %q, want it readable verbatim", all.Items[0].Content)
	}

	// And the row itself was never deleted by anything on the read path.
	if len(notes.items) != 1 {
		t.Errorf("stored rows = %d, want 1 — expiry hides a note, it must never delete it", len(notes.items))
	}
}

// TestAnnotationsRoutes_CreateAlreadyExpired_400 rejects a note born
// expired: it would be invisible the instant it was written, which is a
// confusing silent no-op rather than a feature.
func TestAnnotationsRoutes_CreateAlreadyExpired_400(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	svc := newTestAnnotationService(t, newFakeAnnotationStore(), func() time.Time { return now })
	r := annotationTestRouter(t, svc)

	rec := doAnnotationReq(t, r, http.MethodPost, "/api/v1/annotations",
		fmt.Sprintf(`{"ref":"bridge:pve1:vmbr0","content":"already stale","expiresAt":%d}`, now.Unix()-1))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

// TestAnnotationsRoutes_ContentIsEscapedInJSON is one of T-2806 AC6's
// per-path assertions: the API's own render path. A note is free text one
// operator typed and another's browser receives; the JSON encoder must not
// emit raw `<`/`>` that could break out of a surrounding <script> block in
// any page that inlines this response.
func TestAnnotationsRoutes_ContentIsEscapedInJSON(t *testing.T) {
	const hostile = `</script><img src=x onerror=alert(1)>`
	r := annotationTestRouter(t, newTestAnnotations(t))

	body, err := json.Marshal(annotationCreateRequest{Ref: "bridge:pve1:vmbr0", Content: hostile})
	if err != nil {
		t.Fatalf("marshalling request: %v", err)
	}
	if rec := doAnnotationReq(t, r, http.MethodPost, "/api/v1/annotations", string(body)); rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}

	rec := doAnnotationReq(t, r, http.MethodGet, "/api/v1/annotations", "")
	raw := rec.Body.String()
	if strings.Contains(raw, "</script>") || strings.Contains(raw, "<img") {
		t.Errorf("GET /annotations emitted raw markup:\n%s", raw)
	}
	if !strings.Contains(raw, `\u003c`) {
		t.Errorf("GET /annotations did not escape the note text as \\u003c:\n%s", raw)
	}

	// Escaped on the wire, but still the exact text once decoded — escaping
	// must not corrupt what the operator wrote.
	var listed annotationsListResponse
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&listed); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].Content != hostile {
		t.Fatalf("Items = %+v, want the note decoding back to the original text", listed.Items)
	}
}

// TestMapRegionsRoutes_CreateListDelete_RoundTrips covers T-2806's canvas
// regions end to end, including that a region carries its author and is
// visible to every user (no per-region ACL, matching annotations).
func TestMapRegionsRoutes_CreateListDelete_RoundTrips(t *testing.T) {
	r := annotationTestRouter(t, newTestAnnotations(t))

	rec := doAnnotationReq(t, r, http.MethodPost, "/api/v1/map-regions",
		`{"label":"vendor-managed, do not touch","x":10,"y":20,"w":300,"h":180,"color":"amber"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	var created mapRegionResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding POST body: %v", err)
	}
	if created.Label != "vendor-managed, do not touch" || created.W != 300 || created.H != 180 {
		t.Errorf("created = %+v, fields don't match request", created)
	}
	if created.CreatedBy != "alice@pve" {
		t.Errorf("createdBy = %q, want alice@pve (server-stamped)", created.CreatedBy)
	}

	listRec := doAnnotationReq(t, r, http.MethodGet, "/api/v1/map-regions", "")
	var listed mapRegionsListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decoding GET body: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("Items = %+v, want exactly the just-created region", listed.Items)
	}

	if delRec := doAnnotationReq(t, r, http.MethodDelete, "/api/v1/map-regions/"+created.ID, ""); delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", delRec.Code)
	}
	afterRec := doAnnotationReq(t, r, http.MethodGet, "/api/v1/map-regions", "")
	var after mapRegionsListResponse
	if err := json.NewDecoder(afterRec.Body).Decode(&after); err != nil {
		t.Fatalf("decoding second GET body: %v", err)
	}
	if len(after.Items) != 0 {
		t.Errorf("Items after delete = %+v, want empty", after.Items)
	}
}

// TestMapRegionsRoutes_CreateInvalid_400 — a region with no label, or with
// no area, is rejected rather than stored as an invisible artifact.
func TestMapRegionsRoutes_CreateInvalid_400(t *testing.T) {
	r := annotationTestRouter(t, newTestAnnotations(t))

	for _, body := range []string{
		``, `not json`, `{}`,
		`{"label":"","x":0,"y":0,"w":10,"h":10}`,
		`{"label":"zero width","x":0,"y":0,"w":0,"h":10}`,
		`{"label":"zero height","x":0,"y":0,"w":10,"h":0}`,
		`{"label":"negative","x":0,"y":0,"w":-5,"h":10}`,
	} {
		rec := doAnnotationReq(t, r, http.MethodPost, "/api/v1/map-regions", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400, response: %s", body, rec.Code, rec.Body.String())
		}
	}
}
