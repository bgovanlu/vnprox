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
	"github.com/bgovanlu/vnprox/internal/store"
)

// policies_test.go proves T-2601's acceptance criterion 2 at the API — not
// only inside the engine: a `warn` rule's annotation must survive onto the
// changeset the review surface reads. Every request here goes through the
// real router against a REAL *change.Service, so nothing about the answer is
// faked by a stub.

func newPolicyConfiguredChangesetService(t *testing.T) *change.Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
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
		Snapshots:  store.NewSnapshotRepo(db),
		Blobs:      store.NewBlobRepo(db),
		Nodes:      panicNodeAgent{},
		Policies:   store.NewPolicySetRepo(db),
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

func newPolicyTestRouter(t *testing.T) (http.Handler, *change.Service) {
	t.Helper()
	svc := newPolicyConfiguredChangesetService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
		Changesets: svc, Policy: svc,
	})
	return r, svc
}

func doJSONRequest(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

const warnPolicyBody = `{"version":1,"rules":[{
  "id":"new-bridges-should-be-documented",
  "description":"A newly created bridge should carry a comment saying what it is for.",
  "severity":"warn",
  "match":[{"field":"op","op":"eq","value":"bridge.create"}],
  "assert":[{"field":"params.comments","op":"exists"}]
}]}`

const denyPolicyBody = `{"version":1,"rules":[{
  "id":"no-vmbr9",
  "description":"vmbr9 is managed out of band.",
  "severity":"deny",
  "match":[{"field":"target.id","op":"eq","value":"vmbr9"}]
}]}`

// TestPolicies_WarnAnnotationSurvivesToTheReviewSurface is acceptance
// criterion 2: the warning does not block, and a plain GET of the changeset
// — what the review surface reads — carries it.
func TestPolicies_WarnAnnotationSurvivesToTheReviewSurface(t *testing.T) {
	r, _ := newPolicyTestRouter(t)

	if rec := doJSONRequest(t, r, http.MethodPut, "/api/v1/policies", warnPolicyBody); rec.Code != http.StatusOK {
		t.Fatalf("PUT /policies status = %d, body: %s", rec.Code, rec.Body.String())
	}

	createRec := doJSONRequest(t, r, http.MethodPost, "/api/v1/changesets",
		`{"title":"add vmbr1","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","params":{}}]}`)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body: %s", createRec.Code, createRec.Body.String())
	}
	created := mustDecodeChangeset(t, createRec)

	// Not blocked: validate promotes it, because a warn never blocks.
	validateRec := doJSONRequest(t, r, http.MethodPost, "/api/v1/changesets/"+created.ID+"/validate", "")
	if validateRec.Code != http.StatusOK {
		t.Fatalf("validate status = %d, body: %s", validateRec.Code, validateRec.Body.String())
	}
	if got := mustDecodeChangeset(t, validateRec); got.Status != "validated" {
		t.Errorf("status = %q, want validated (a warn rule must not block)", got.Status)
	}

	// The annotation is on the changeset the review surface reads.
	getRec := doJSONRequest(t, r, http.MethodGet, "/api/v1/changesets/"+created.ID, "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body: %s", getRec.Code, getRec.Body.String())
	}
	got := mustDecodeChangeset(t, getRec)
	var found bool
	for _, f := range got.Findings {
		if f.Code != "policy.violation" {
			continue
		}
		found = true
		if f.Severity != "warning" {
			t.Errorf("severity = %q, want warning", f.Severity)
		}
		if !strings.Contains(f.Message, "new-bridges-should-be-documented") {
			t.Errorf("message = %q, want it to name the rule id", f.Message)
		}
		if !strings.Contains(f.Message, "should carry a comment") {
			t.Errorf("message = %q, want it to name the rule description", f.Message)
		}
	}
	if !found {
		t.Fatalf("findings = %+v, want a policy.violation annotation on the review surface", got.Findings)
	}
}

// TestPolicies_DenyRefusesApplyAndDiffOverTheAPI is the API-level half of
// acceptance criteria 1 and 3: the same installed policy that blocks apply
// also refuses the diff route, both with the documented error envelope.
func TestPolicies_DenyRefusesApplyAndDiffOverTheAPI(t *testing.T) {
	r, _ := newPolicyTestRouter(t)

	if rec := doJSONRequest(t, r, http.MethodPut, "/api/v1/policies", denyPolicyBody); rec.Code != http.StatusOK {
		t.Fatalf("PUT /policies status = %d, body: %s", rec.Code, rec.Body.String())
	}
	createRec := doJSONRequest(t, r, http.MethodPost, "/api/v1/changesets",
		`{"title":"touch vmbr9","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr9","params":{}}]}`)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body: %s", createRec.Code, createRec.Body.String())
	}
	created := mustDecodeChangeset(t, createRec)

	// The diff route refuses. panicNodeAgent is the structural proof that
	// nothing was computed: reaching the node layer would panic (and the
	// router's recovery middleware would answer 500 internal_error), not
	// return 422.
	diffRec := doJSONRequest(t, r, http.MethodGet, "/api/v1/changesets/"+created.ID+"/diff", "")
	if diffRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("diff status = %d, want 422, body: %s", diffRec.Code, diffRec.Body.String())
	}
	if code := decodeAPIErrorCode(t, diffRec); code != "validation_failed" {
		t.Errorf("diff error code = %q, want validation_failed", code)
	}

	applyRec := doJSONRequest(t, r, http.MethodPost, "/api/v1/changesets/"+created.ID+"/apply", "")
	if applyRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply status = %d, want 422, body: %s", applyRec.Code, applyRec.Body.String())
	}

	getRec := doJSONRequest(t, r, http.MethodGet, "/api/v1/changesets/"+created.ID, "")
	if got := mustDecodeChangeset(t, getRec); got.Status != "draft" {
		t.Errorf("status = %q, want draft (a denied changeset never starts applying)", got.Status)
	}
}

// TestPolicies_GetAndPutRoundTrip covers the admin surface itself.
func TestPolicies_GetAndPutRoundTrip(t *testing.T) {
	r, _ := newPolicyTestRouter(t)

	getRec := doJSONRequest(t, r, http.MethodGet, "/api/v1/policies", "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /policies status = %d, body: %s", getRec.Code, getRec.Body.String())
	}
	var empty struct {
		Set struct {
			Rules []json.RawMessage `json:"rules"`
		} `json:"set"`
		Revision int64 `json:"revision"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&empty); err != nil {
		t.Fatalf("decoding GET /policies: %v", err)
	}
	if empty.Revision != 0 || len(empty.Set.Rules) != 0 {
		t.Errorf("a fresh deployment reported %+v, want revision 0 and no rules", empty)
	}

	putRec := doJSONRequest(t, r, http.MethodPut, "/api/v1/policies", denyPolicyBody)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT /policies status = %d, body: %s", putRec.Code, putRec.Body.String())
	}

	getRec = doJSONRequest(t, r, http.MethodGet, "/api/v1/policies", "")
	var installed struct {
		UpdatedBy string `json:"updatedBy"`
		Rules     []struct {
			RuleID                string `json:"ruleId"`
			ProbablyMisconfigured bool   `json:"probablyMisconfigured"`
		} `json:"rules"`
		Revision int64 `json:"revision"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&installed); err != nil {
		t.Fatalf("decoding GET /policies: %v", err)
	}
	if installed.Revision != 1 {
		t.Errorf("revision = %d, want 1", installed.Revision)
	}
	if installed.UpdatedBy != "alice" {
		t.Errorf("updatedBy = %q, want alice", installed.UpdatedBy)
	}
	if len(installed.Rules) != 1 || installed.Rules[0].RuleID != "no-vmbr9" {
		t.Errorf("rules = %+v, want the installed rule's status", installed.Rules)
	}
	if installed.Rules[0].ProbablyMisconfigured {
		t.Errorf("a freshly installed rule was reported as probably misconfigured")
	}
}

// TestPolicies_PutRejectsMalformedRuleNamingRuleAndField is acceptance
// criterion 5 at the API: the refusal names the rule and the field.
func TestPolicies_PutRejectsMalformedRuleNamingRuleAndField(t *testing.T) {
	r, _ := newPolicyTestRouter(t)

	bad := `{"version":1,"rules":[{"id":"broken","description":"d","severity":"deny",
	  "match":[{"field":"target.whatever","op":"eq","value":"x"}]}]}`
	rec := doJSONRequest(t, r, http.MethodPut, "/api/v1/policies", bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				RuleID string `json:"ruleId"`
				Field  string `json:"field"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decoding error: %v", err)
	}
	if errResp.Error.Code != "validation_failed" {
		t.Errorf("code = %q, want validation_failed", errResp.Error.Code)
	}
	if errResp.Error.Details.RuleID != "broken" {
		t.Errorf("details.ruleId = %q, want broken", errResp.Error.Details.RuleID)
	}
	if !strings.Contains(errResp.Error.Details.Field, "field") {
		t.Errorf("details.field = %q, want it to name the offending field", errResp.Error.Details.Field)
	}
}

// TestPolicies_TestRouteEvaluatesWithoutStaging backs
// `vnproxctl policy test`: a candidate rule set is evaluated against a real
// changeset and neither the changeset nor the installed policy moves.
func TestPolicies_TestRouteEvaluatesWithoutStaging(t *testing.T) {
	r, _ := newPolicyTestRouter(t)

	createRec := doJSONRequest(t, r, http.MethodPost, "/api/v1/changesets",
		`{"title":"touch vmbr9","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr9","params":{}}]}`)
	created := mustDecodeChangeset(t, createRec)

	body := `{"changesetId":"` + created.ID + `","policy":` + denyPolicyBody + `}`
	rec := doJSONRequest(t, r, http.MethodPost, "/api/v1/policies/test", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /policies/test status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Findings []struct {
			Severity string `json:"severity"`
			Code     string `json:"code"`
		} `json:"findings"`
		Rules []struct {
			RuleID       string `json:"ruleId"`
			ViolatingOps []int  `json:"violatingOps"`
		} `json:"rules"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decoding result: %v", err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Severity != "error" {
		t.Errorf("findings = %+v, want one blocking policy finding", result.Findings)
	}
	if len(result.Rules) != 1 || len(result.Rules[0].ViolatingOps) != 1 {
		t.Errorf("rules = %+v, want the rule reported with one violating op", result.Rules)
	}

	// Nothing was installed by testing a candidate.
	getRec := doJSONRequest(t, r, http.MethodGet, "/api/v1/policies", "")
	var status struct {
		Revision int64 `json:"revision"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&status); err != nil {
		t.Fatalf("decoding GET /policies: %v", err)
	}
	if status.Revision != 0 {
		t.Errorf("revision = %d, want 0 (testing a candidate must not install it)", status.Revision)
	}
	// ...and the changeset is untouched.
	if got := mustDecodeChangeset(t, doJSONRequest(t, r, http.MethodGet, "/api/v1/changesets/"+created.ID, "")); got.Status != "draft" {
		t.Errorf("status = %q, want draft", got.Status)
	}
}

func TestPolicies_TestRouteRequiresExactlyOneSubject(t *testing.T) {
	r, _ := newPolicyTestRouter(t)
	rec := doJSONRequest(t, r, http.MethodPost, "/api/v1/policies/test", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}
