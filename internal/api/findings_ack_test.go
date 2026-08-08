package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// ackTestStore is a findings.AckStore backed by a map, so these router tests
// exercise the REAL findings.AckService (its validation, its expiry rule)
// rather than a hand-rolled fake of it. A fake service here would let the
// handler and the service drift apart, which is precisely the failure mode
// T-2108 found four times.
type ackTestStore struct{ rows map[string]findings.Ack }

func newAckTestStore() *ackTestStore { return &ackTestStore{rows: map[string]findings.Ack{}} }

func (s *ackTestStore) ListAcks(context.Context) (map[string]findings.Ack, error) {
	out := make(map[string]findings.Ack, len(s.rows))
	for k, v := range s.rows {
		out[k] = v
	}
	return out, nil
}

func (s *ackTestStore) UpsertAck(_ context.Context, id string, a findings.Ack) error {
	s.rows[id] = a
	return nil
}

func (s *ackTestStore) DeleteAck(_ context.Context, id string) error {
	delete(s.rows, id)
	return nil
}

// recordingAudit captures audit rows so a test can assert the reason really
// reaches the audit log — "why is this muted" must be answerable after the
// acknowledgement itself has expired.
type recordingAudit struct{ entries []store.AuditEntry }

func (a *recordingAudit) Append(_ context.Context, e store.AuditEntry) (int64, error) {
	a.entries = append(a.entries, e)
	return int64(len(a.entries)), nil
}

func ackTestRouter(t *testing.T, st *ackTestStore, audit *recordingAudit, svc FindingsService, cs ChangesetService) http.Handler {
	t.Helper()
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:         driftTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Topology:     fakeTopologyService{},
		Findings:     svc,
		Changesets:   cs,
		FindingAcks:  findings.NewAckService(st, func() time.Time { return time.Unix(1_700_000_000, 0) }),
		FindingAudit: audit,
	})
}

func doJSON(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader([]byte("{}"))
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// recordingChangesetService wraps the REAL change.Service (via
// newChangesetTestService) and records each Create. Wrapping rather than
// faking matters: a hand-written fake would accept op lists the real engine
// rejects, so "the batch staged one changeset" would be true of a changeset
// that could never validate.
type recordingChangesetService struct {
	ChangesetService
	created []struct {
		title string
		ops   []change.Op
	}
}

func newRecordingChangesets(t *testing.T) *recordingChangesetService {
	t.Helper()
	return &recordingChangesetService{ChangesetService: newChangesetTestService(t)}
}

func (r *recordingChangesetService) Create(ctx context.Context, author, title string, ops []change.Op) (change.Changeset, error) {
	cs, err := r.ChangesetService.Create(ctx, author, title, ops)
	if err != nil {
		return cs, err
	}
	r.created = append(r.created, struct {
		title string
		ops   []change.Op
	}{title: title, ops: ops})
	return cs, nil
}

func decodeFindings(t *testing.T, rec *httptest.ResponseRecorder) []findings.Finding {
	t.Helper()
	var body struct {
		Items []findings.Finding `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding findings: %v (body %s)", err, rec.Body.String())
	}
	return body.Items
}

// AC1 end-to-end: ack a finding, then read the stream back and see the
// acknowledgement attached — and see the finding STILL THERE, because
// acknowledgement is not suppression.
func TestFindingAck_AttachesToTheStreamWithoutHidingTheFinding(t *testing.T) {
	st := newAckTestStore()
	audit := &recordingAudit{}
	svc := fakeFindingsService{findings: mixedSourceFindings()}
	r := ackTestRouter(t, st, audit, svc, nil)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/health:1/ack",
		map[string]any{"reason": "deliberate — staging node, no guests yet"})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST ack status = %d, body %s", rec.Code, rec.Body.String())
	}

	items := decodeFindings(t, doJSON(t, r, http.MethodGet, "/api/v1/findings", nil))
	if len(items) != len(mixedSourceFindings()) {
		t.Fatalf("acking removed a finding from the stream: %d items, want %d", len(items), len(mixedSourceFindings()))
	}
	var acked int
	for _, f := range items {
		if f.Ack == nil {
			continue
		}
		acked++
		if f.ID != "health:1" {
			t.Errorf("the acknowledgement landed on %s", f.ID)
		}
		if f.Ack.Reason != "deliberate — staging node, no guests yet" || f.Ack.AckedBy != "root@pam" {
			t.Errorf("ack = %+v", f.Ack)
		}
	}
	if acked != 1 {
		t.Fatalf("%d findings came back acked, want exactly 1", acked)
	}
}

// The reason must reach the audit log, not just the ack row: an expired
// acknowledgement is deleted, and "why did we mute that" has to stay
// answerable afterwards.
func TestFindingAck_AuditsTheDecisionWithItsReason(t *testing.T) {
	st := newAckTestStore()
	audit := &recordingAudit{}
	r := ackTestRouter(t, st, audit, fakeFindingsService{findings: mixedSourceFindings()}, nil)

	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/health:1/ack",
		map[string]any{"reason": "known asymmetry"}); rec.Code != http.StatusOK {
		t.Fatalf("ack: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodDelete, "/api/v1/findings/health:1/ack", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unack: %d %s", rec.Code, rec.Body.String())
	}

	if len(audit.entries) != 2 {
		t.Fatalf("got %d audit entries, want 2 (ack + unack)", len(audit.entries))
	}
	ack, unack := audit.entries[0], audit.entries[1]
	if ack.Action != "finding.ack" || unack.Action != "finding.unack" {
		t.Fatalf("actions = %q, %q", ack.Action, unack.Action)
	}
	if ack.Target.String != "health:1" {
		t.Errorf("audit target = %q", ack.Target.String)
	}
	if !strings.Contains(ack.DetailJSON.String, "known asymmetry") {
		t.Errorf("the ack reason did not reach the audit detail: %q", ack.DetailJSON.String)
	}
	if ack.Username != "root@pam" {
		t.Errorf("audit username = %q", ack.Username)
	}
}

func TestFindingAck_FilterOnlyAndExclude(t *testing.T) {
	st := newAckTestStore()
	r := ackTestRouter(t, st, &recordingAudit{}, fakeFindingsService{findings: mixedSourceFindings()}, nil)
	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/health:1/ack",
		map[string]any{"reason": "deliberate"}); rec.Code != http.StatusOK {
		t.Fatalf("ack: %d", rec.Code)
	}

	only := decodeFindings(t, doJSON(t, r, http.MethodGet, "/api/v1/findings?acked=only", nil))
	if len(only) != 1 || only[0].ID != "health:1" {
		t.Fatalf("?acked=only returned %d items: %+v", len(only), only)
	}
	excluded := decodeFindings(t, doJSON(t, r, http.MethodGet, "/api/v1/findings?acked=exclude", nil))
	if len(excluded) != len(mixedSourceFindings())-1 {
		t.Fatalf("?acked=exclude returned %d items, want %d", len(excluded), len(mixedSourceFindings())-1)
	}
	for _, f := range excluded {
		if f.ID == "health:1" {
			t.Fatal("?acked=exclude returned the acked finding")
		}
	}
	// The default must show everything — an operator who has not asked for a
	// filter must never be shown a silently truncated stream.
	all := decodeFindings(t, doJSON(t, r, http.MethodGet, "/api/v1/findings", nil))
	if len(all) != len(mixedSourceFindings()) {
		t.Fatalf("the unfiltered stream returned %d items, want %d", len(all), len(mixedSourceFindings()))
	}
}

// AC3 / AC4 at the HTTP layer, with the status codes the contract promises.
func TestFindingAck_Rejections(t *testing.T) {
	cases := []struct {
		body   map[string]any
		name   string
		id     string
		status int
	}{
		{name: "no reason", id: "health:1", body: map[string]any{}, status: http.StatusBadRequest},
		{name: "blank reason", id: "health:1", body: map[string]any{"reason": "   "}, status: http.StatusBadRequest},
		{name: "unknown finding", id: "health:nope", body: map[string]any{"reason": "x"}, status: http.StatusNotFound},
		{name: "expiry in the past", id: "health:1", body: map[string]any{"reason": "x", "expiresAt": 1}, status: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newAckTestStore()
			r := ackTestRouter(t, st, &recordingAudit{}, fakeFindingsService{findings: mixedSourceFindings()}, nil)
			rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/"+tc.id+"/ack", tc.body)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.status, rec.Body.String())
			}
			if len(st.rows) != 0 {
				t.Fatalf("a refused acknowledgement still wrote %d rows", len(st.rows))
			}
		})
	}
}

func TestFindingAck_RequiresNetWriteCapability(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:        driftTestAuth(map[string]bool{"netRead": true}), // no netWrite
		Topology:    fakeTopologyService{},
		Findings:    fakeFindingsService{findings: mixedSourceFindings()},
		FindingAcks: findings.NewAckService(newAckTestStore(), nil),
	})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/health:1/ack", map[string]any{"reason": "x"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// With no ack service configured, GET /findings must behave exactly as it did
// before T-2402 — this is the degraded-startup path.
func TestFindings_WithoutAnAckServiceBehavesAsBefore(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     driftTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Topology: fakeTopologyService{}, Findings: fakeFindingsService{findings: mixedSourceFindings()},
	})
	items := decodeFindings(t, doJSON(t, r, http.MethodGet, "/api/v1/findings", nil))
	if len(items) != len(mixedSourceFindings()) {
		t.Fatalf("got %d findings, want %d", len(items), len(mixedSourceFindings()))
	}
	for _, f := range items {
		if f.Ack != nil {
			t.Fatal("a finding came back acked with no acknowledgement service configured")
		}
	}
	// And the ack route must not be mounted at all, rather than 500ing.
	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/health:1/ack",
		map[string]any{"reason": "x"}); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("ack route status with no service = %d, want 404/405", rec.Code)
	}
}

// -------------------------------------------------------------------------
// T-2408 — batch fix
// -------------------------------------------------------------------------

func bridgeRef(node, id string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: id}
}

// batchFixService reports three fixable findings, two of which target the same
// entity with the same op type — the conflict case.
func batchFixService() fakeFindingsService {
	return fakeFindingsService{
		findings: []findings.Finding{
			{ID: "drift:a", Source: findings.SourceDrift, Severity: findings.SeverityWarning, Fixable: true},
			{ID: "drift:b", Source: findings.SourceDrift, Severity: findings.SeverityWarning, Fixable: true},
			{ID: "drift:c", Source: findings.SourceDrift, Severity: findings.SeverityWarning, Fixable: true},
		},
		fixOps: map[string][]change.Op{
			"drift:a": {{Type: change.OpBridgeUpdate, Target: bridgeRef("pve1", "vmbr0")}},
			"drift:b": {{Type: change.OpBridgeUpdate, Target: bridgeRef("pve2", "vmbr0")}},
			// c collides with a: same op type, same entity.
			"drift:c": {{Type: change.OpBridgeUpdate, Target: bridgeRef("pve1", "vmbr0")}},
		},
		fixTitle: map[string]string{"drift:a": "Fix A", "drift:b": "Fix B", "drift:c": "Fix C"},
	}
}

// AC1: N fixable findings produce ONE changeset carrying all N findings' ops.
func TestBatchFix_StagesEveryFindingIntoOneChangeset(t *testing.T) {
	cs := newRecordingChangesets(t)
	r := ackTestRouter(t, newAckTestStore(), &recordingAudit{}, batchFixService(), cs)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix", map[string]any{"ids": []string{"drift:a", "drift:b"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(cs.created) != 1 {
		t.Fatalf("created %d changesets, want exactly 1", len(cs.created))
	}
	if got := len(cs.created[0].ops); got != 2 {
		t.Fatalf("the changeset carries %d ops, want 2", got)
	}
	if !strings.Contains(cs.created[0].title, "2") {
		t.Errorf("a multi-finding batch should say how many it fixes: %q", cs.created[0].title)
	}
}

// A batch of one is indistinguishable from the single-finding route's output,
// including its title — so the UI can use one code path.
func TestBatchFix_OfOneKeepsThatFindingsOwnTitle(t *testing.T) {
	cs := newRecordingChangesets(t)
	r := ackTestRouter(t, newAckTestStore(), &recordingAudit{}, batchFixService(), cs)

	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix",
		map[string]any{"ids": []string{"drift:a"}}); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if cs.created[0].title != "Fix A" {
		t.Fatalf("title = %q, want the finding's own title", cs.created[0].title)
	}
}

// AC2: one bad id stages NOTHING. Asserted on the changeset count, not on the
// status code — a handler that returned 404 *after* creating the changeset
// would pass a status-only assertion and strand a half-batch.
func TestBatchFix_OneBadIDStagesNothing(t *testing.T) {
	cs := newRecordingChangesets(t)
	r := ackTestRouter(t, newAckTestStore(), &recordingAudit{}, batchFixService(), cs)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix",
		map[string]any{"ids": []string{"drift:a", "drift:nope", "drift:b"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if len(cs.created) != 0 {
		t.Fatalf("a refused batch created %d changesets; nothing must be staged", len(cs.created))
	}
}

// AC3: a conflict names BOTH findings, so the operator knows what to do next.
func TestBatchFix_ConflictingOpsAreRefusedNamingBothFindings(t *testing.T) {
	cs := newRecordingChangesets(t)
	r := ackTestRouter(t, newAckTestStore(), &recordingAudit{}, batchFixService(), cs)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix", map[string]any{"ids": []string{"drift:a", "drift:c"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "drift:a") || !strings.Contains(body, "drift:c") {
		t.Fatalf("the conflict must name both findings: %s", body)
	}
	if len(cs.created) != 0 {
		t.Fatalf("a conflicting batch created %d changesets", len(cs.created))
	}
}

// Two findings touching DIFFERENT entities with the same op type are not a
// conflict — the control for the test above. Without this, a conflict rule
// that refused everything would still pass.
func TestBatchFix_SameOpTypeOnDifferentEntitiesIsNotAConflict(t *testing.T) {
	cs := newRecordingChangesets(t)
	r := ackTestRouter(t, newAckTestStore(), &recordingAudit{}, batchFixService(), cs)

	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix",
		map[string]any{"ids": []string{"drift:a", "drift:b"}}); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — pve1:vmbr0 and pve2:vmbr0 are different entities", rec.Code)
	}
}

// AC4: an acknowledged finding is refused, so a batch fix cannot quietly undo
// a deliberate decision.
func TestBatchFix_RefusesAnAcknowledgedFinding(t *testing.T) {
	st := newAckTestStore()
	cs := newRecordingChangesets(t)
	r := ackTestRouter(t, st, &recordingAudit{}, batchFixService(), cs)

	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/drift:a/ack",
		map[string]any{"reason": "intentional"}); rec.Code != http.StatusOK {
		t.Fatalf("ack: %d %s", rec.Code, rec.Body.String())
	}
	rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix", map[string]any{"ids": []string{"drift:a", "drift:b"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if len(cs.created) != 0 {
		t.Fatalf("a batch containing an acked finding created %d changesets", len(cs.created))
	}

	// Control: un-ack and the identical batch succeeds. Without this, a
	// handler that refused every batch would pass the assertion above.
	if rec := doJSON(t, r, http.MethodDelete, "/api/v1/findings/drift:a/ack", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unack: %d", rec.Code)
	}
	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix",
		map[string]any{"ids": []string{"drift:a", "drift:b"}}); rec.Code != http.StatusCreated {
		t.Fatalf("after un-acking, status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestBatchFix_EmptyAndOversizedRequests(t *testing.T) {
	cs := newRecordingChangesets(t)
	r := ackTestRouter(t, newAckTestStore(), &recordingAudit{}, batchFixService(), cs)

	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix",
		map[string]any{"ids": []string{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty ids status = %d, want 400", rec.Code)
	}
	// Blank and duplicate ids are normalised away, so a list of blanks is an
	// empty batch rather than a batch of blanks.
	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix",
		map[string]any{"ids": []string{"", "  "}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank ids status = %d, want 400", rec.Code)
	}
	big := make([]string, maxBatchFixIDs+1)
	for i := range big {
		big[i] = "drift:" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix",
		map[string]any{"ids": big}); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized batch status = %d, want 400", rec.Code)
	}
	if len(cs.created) != 0 {
		t.Fatalf("refused batches created %d changesets", len(cs.created))
	}
}

// A duplicated id is deduplicated rather than staged twice.
func TestBatchFix_DeduplicatesRepeatedIDs(t *testing.T) {
	cs := newRecordingChangesets(t)
	r := ackTestRouter(t, newAckTestStore(), &recordingAudit{}, batchFixService(), cs)

	if rec := doJSON(t, r, http.MethodPost, "/api/v1/findings/fix",
		map[string]any{"ids": []string{"drift:a", "drift:a"}}); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := len(cs.created[0].ops); got != 1 {
		t.Fatalf("a repeated id staged %d ops, want 1", got)
	}
}
