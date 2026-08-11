package api

// T-2807's HTTP surface, added at the wave-6 merge rather than by the card
// (see digest.go's header for why the card shipped without it).
//
// The assertions that carry weight here are the call counters and the
// round-trip, not the status codes. A PUT that answered 403 but had already
// written the schedule would be worse than a wrong code, so every capability
// assertion rests on the store not having been reached — each with a control
// leg proving it IS reached when the request is legitimate.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

const digestPath = "/api/v1/digest/schedule"

// stubDigestSchedule is the DigestScheduleService seam under test.
//
//nolint:govet // fieldalignment: test double; counters sit with what they count.
type stubDigestSchedule struct {
	sched       store.DigestSchedule
	run         store.DigestRun
	schedErr    error
	runErr      error
	upsertErr   error
	upserted    []store.DigestSchedule
	readCalls   int
	upsertCalls int
}

func (s *stubDigestSchedule) Schedule(_ context.Context, id string) (store.DigestSchedule, error) {
	s.readCalls++
	if s.schedErr != nil {
		return store.DigestSchedule{}, s.schedErr
	}
	out := s.sched
	out.ID = id
	return out, nil
}

func (s *stubDigestSchedule) UpsertSchedule(_ context.Context, sc store.DigestSchedule) error {
	s.upsertCalls++
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upserted = append(s.upserted, sc)
	s.sched = sc
	return nil
}

func (s *stubDigestSchedule) LatestRun(_ context.Context, _ string) (store.DigestRun, error) {
	if s.runErr != nil {
		return store.DigestRun{}, s.runErr
	}
	return s.run, nil
}

func digestRouter(svc DigestScheduleService, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, DigestSchedule: svc,
	})
}

func okDigestSchedule() *stubDigestSchedule {
	return &stubDigestSchedule{
		sched: store.DigestSchedule{
			ID: "default", Enabled: true, EverySec: 604800,
			RuleIDs: []string{"rule-a"}, UpdatedAt: 1750000000, UpdatedBy: "alice",
		},
		run: store.DigestRun{
			ID: "01JRUN", ScheduleID: "default", PeriodStart: 1749395200, PeriodEnd: 1750000000,
			GeneratedAt: 1750000001, Status: "delivered", Quiet: true, Detail: "nothing to report",
		},
	}
}

func putDigest(t *testing.T, r http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, digestPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

// TestDigestSchedule_GetReturnsScheduleAndLastRun is the reachability the
// feature shipped without: a schedule readable over HTTP rather than only by
// writing to SQLite by hand.
func TestDigestSchedule_GetReturnsScheduleAndLastRun(t *testing.T) {
	svc := okDigestSchedule()
	r := digestRouter(svc, fullCapsAuth("alice"))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, digestPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got digestScheduleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !got.Enabled || got.EverySec != 604800 {
		t.Errorf("enabled=%v everySec=%d, want true/604800", got.Enabled, got.EverySec)
	}
	if len(got.RuleIDs) != 1 || got.RuleIDs[0] != "rule-a" {
		t.Errorf("ruleIds = %v, want [rule-a]", got.RuleIDs)
	}
	// The last run is the whole reason an operator opens this: "did last
	// week's digest go out, and was it the quiet form".
	if got.LastRun == nil {
		t.Fatal("lastRun is absent; the schedule alone does not answer whether the digest was delivered")
	}
	if got.LastRun.Status != "delivered" || !got.LastRun.Quiet {
		t.Errorf("lastRun = %+v, want delivered/quiet", *got.LastRun)
	}
}

// TestDigestSchedule_NeverConfiguredReadsAsDisabled: a daemon that has never
// had a schedule written still has one — the disabled one. Answering 404 would
// make every client special-case "not set up yet" for no gain.
func TestDigestSchedule_NeverConfiguredReadsAsDisabled(t *testing.T) {
	svc := &stubDigestSchedule{schedErr: store.ErrNotFound}
	r := digestRouter(svc, fullCapsAuth("alice"))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, digestPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET with no schedule row = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got digestScheduleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Enabled {
		t.Error("a daemon with no schedule row reported an ENABLED digest")
	}
	if got.RuleIDs == nil {
		t.Error("ruleIds is null; an absent list must serialise as [] so a client need not nil-check")
	}
}

// TestDigestSchedule_PutRoundTrips covers the point of the route: a cadence
// change reaches the row the runner re-reads, which is what makes T-2807 AC5
// ("takes effect without a restart") true through the API and not only through
// a hand-written UPDATE.
func TestDigestSchedule_PutRoundTrips(t *testing.T) {
	svc := okDigestSchedule()
	r := digestRouter(svc, fullCapsAuth("alice"))

	rec := putDigest(t, r, `{"everySec":86400,"ruleIds":["rule-b","rule-c"],"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if svc.upsertCalls != 1 {
		t.Fatalf("upsertCalls = %d, want 1", svc.upsertCalls)
	}
	saved := svc.upserted[0]
	if saved.EverySec != 86400 {
		t.Errorf("saved everySec = %d, want 86400", saved.EverySec)
	}
	if len(saved.RuleIDs) != 2 || saved.RuleIDs[0] != "rule-b" {
		t.Errorf("saved ruleIds = %v, want [rule-b rule-c]", saved.RuleIDs)
	}
	if saved.ID != digestScheduleID {
		t.Errorf("saved id = %q, want %q", saved.ID, digestScheduleID)
	}
	// Attribution: who changed the cadence is the audit question a schedule
	// change actually raises.
	if saved.UpdatedBy != "alice" {
		t.Errorf("saved updatedBy = %q, want alice", saved.UpdatedBy)
	}
	if saved.UpdatedAt == 0 {
		t.Error("saved updatedAt is zero; a schedule change with no timestamp is not auditable")
	}
}

// TestDigestSchedule_OmittedFieldsKeepTheirValue: PUT starts from what is
// stored, so omitting a field does not silently reset it to zero. A PUT that
// turned an omitted everySec into "every 0 seconds" is the foot-gun this
// guards.
func TestDigestSchedule_OmittedFieldsKeepTheirValue(t *testing.T) {
	svc := okDigestSchedule()
	r := digestRouter(svc, fullCapsAuth("alice"))

	// Disable only. Cadence and recipients must survive untouched.
	rec := putDigest(t, r, `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	saved := svc.upserted[0]
	if saved.Enabled {
		t.Error("enabled=false was not applied")
	}
	if saved.EverySec != 604800 {
		t.Errorf("omitted everySec became %d; it must keep its stored value (604800)", saved.EverySec)
	}
	if len(saved.RuleIDs) != 1 || saved.RuleIDs[0] != "rule-a" {
		t.Errorf("omitted ruleIds became %v; it must keep its stored value", saved.RuleIDs)
	}
}

// TestDigestSchedule_EnabledRequiresAWorkableCadence. A disabled schedule may
// carry any cadence — disabling is how an operator silences a digest without
// losing the cadence they chose — but an enabled one may not, or the runner
// delivers on every tick.
func TestDigestSchedule_EnabledRequiresAWorkableCadence(t *testing.T) {
	svc := okDigestSchedule()
	r := digestRouter(svc, fullCapsAuth("alice"))

	rec := putDigest(t, r, `{"enabled":true,"everySec":60}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PUT enabled with everySec=60 = %d, want 400", rec.Code)
	}
	if svc.upsertCalls != 0 {
		t.Errorf("a rejected schedule was still written (upsertCalls=%d)", svc.upsertCalls)
	}

	// Control: the same sub-hour cadence on a DISABLED schedule is accepted,
	// so the check is about enablement and not merely about the number.
	rec = putDigest(t, r, `{"enabled":false,"everySec":60}`)
	if rec.Code != http.StatusOK {
		t.Errorf("PUT disabled with everySec=60 = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if svc.upsertCalls != 1 {
		t.Errorf("control failed: the disabled schedule was not written (upsertCalls=%d)", svc.upsertCalls)
	}
}

// TestDigestSchedule_RequiresWriteCapability. Reading the schedule is netRead;
// changing it is netWrite. The assertion is that the store was never reached,
// with a control leg proving a capable session does reach it.
func TestDigestSchedule_RequiresWriteCapability(t *testing.T) {
	svc := okDigestSchedule()
	readOnly := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: true},
	}
	r := digestRouter(svc, readOnly)

	rec := putDigest(t, r, `{"everySec":86400,"enabled":true}`)
	if rec.Code == http.StatusOK {
		t.Errorf("a netRead-only session changed the digest schedule (status %d)", rec.Code)
	}
	if svc.upsertCalls != 0 {
		t.Errorf("a session without netWrite reached the store (upsertCalls=%d)", svc.upsertCalls)
	}

	// Reading is netRead: seeing the cadence must not require the capability
	// to change it.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, digestPath, nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET with netRead = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// --- control: the same PUT with netWrite DOES reach the store ----------
	full := digestRouter(svc, fullCapsAuth("alice"))
	if rec := putDigest(t, full, `{"everySec":86400,"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("control failed: a capable session got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if svc.upsertCalls != 1 {
		t.Fatalf("control failed: a fully-capable session did not reach the store either (upsertCalls=%d)", svc.upsertCalls)
	}
}

// TestDigestSchedule_AbsentServiceAnswersHonestly: the routes are mounted even
// when nothing backs them, so a client can tell "digests are off" from "this
// build has no digests". A silently absent route cannot express that.
func TestDigestSchedule_AbsentServiceAnswersHonestly(t *testing.T) {
	r := digestRouter(nil, fullCapsAuth("alice"))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, digestPath, nil))
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("GET with no service = %d, want 501", rec.Code)
	}
	if rec := putDigest(t, r, `{"enabled":true,"everySec":86400}`); rec.Code != http.StatusNotImplemented {
		t.Errorf("PUT with no service = %d, want 501", rec.Code)
	}
}

// TestDigestSchedule_MalformedBodyIsRefused, and refused before the store is
// touched.
func TestDigestSchedule_MalformedBodyIsRefused(t *testing.T) {
	svc := okDigestSchedule()
	r := digestRouter(svc, fullCapsAuth("alice"))

	if rec := putDigest(t, r, `{"everySec":`); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT with malformed JSON = %d, want 400", rec.Code)
	}
	if svc.upsertCalls != 0 {
		t.Errorf("a malformed body reached the store (upsertCalls=%d)", svc.upsertCalls)
	}
}
