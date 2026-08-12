package api

// T-2805's API-layer coverage: the advisory-lock warning on staging (AC1),
// the proof that a held lock never blocks an apply (AC4), and the identity-
// disclosure gate on both read surfaces (AC5).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/presence"
	"github.com/bgovanlu/vnprox/internal/store"
)

// lockTestAuth is fakeAuthWithCaps plus the two seams the lock routes reach
// for by type assertion: SessionLookup (whose session a lock belongs to) and
// DiagnoseCapabilityChecker (whether this caller may be told WHO holds one).
type lockTestAuth struct {
	sessionID string
	fakeAuthWithCaps
}

func (a lockTestAuth) SessionID(context.Context) (string, bool) {
	if a.sessionID == "" {
		return "", false
	}
	return a.sessionID, true
}

func (a lockTestAuth) HasCap(_ context.Context, cap string) bool { return a.caps[cap] }

func lockAuth(username, sessionID string, caps ...string) lockTestAuth {
	capSet := map[string]bool{capNetRead: true, capNetWrite: true}
	for _, c := range caps {
		capSet[c] = true
	}
	return lockTestAuth{
		fakeAuthWithCaps: fakeAuthWithCaps{
			fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: username},
			caps:             capSet,
		},
		sessionID: sessionID,
	}
}

type lockFixture struct {
	presence *presence.Service
	audit    *store.AuditRepo
	change   *change.Service
	clock    *testLockClock
}

type testLockClock struct{ now time.Time }

func (c *testLockClock) Now() time.Time { return c.now }

// newLockFixture builds a fully apply-capable change service plus a presence
// service over the SAME database, so an apply and a lock can be observed
// against one another.
func newLockFixture(t *testing.T) lockFixture {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})
	audit := store.NewAuditRepo(db)
	changeSvc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      audit,
		Snapshots:  store.NewSnapshotRepo(db),
		Blobs:      store.NewBlobRepo(db),
		Nodes:      newFakeNodeAgentAPI(map[string]string{"pve1": snapshotTestBaseInterfaces}),
		Inventory:  snapshotFakeInventory{snap: oneNodeInventorySnapshot()},
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
		TimerFunc:  func(time.Duration, func()) change.Stopper { return inertTimer{} },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	clock := &testLockClock{now: time.Unix(1_700_000_000, 0)}
	presenceSvc, err := presence.NewService(presence.Config{
		Locks:  store.NewEntityLockRepo(db),
		Audit:  audit,
		Now:    clock.Now,
		Logger: testLogger(),
	})
	if err != nil {
		t.Fatalf("presence.NewService: %v", err)
	}
	return lockFixture{presence: presenceSvc, audit: audit, change: changeSvc, clock: clock}
}

func (f lockFixture) router(auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, Changesets: f.change,
		Locks: f.presence, Presence: f.presence,
	})
}

func postJSON(t *testing.T, r http.Handler, path, body string) (int, changesetResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var out changesetResponse
	if rec.Body.Len() > 0 {
		if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
			t.Fatalf("decoding %s response: %v", path, err)
		}
	}
	return rec.Code, out
}

func getLockJSON(t *testing.T, r http.Handler, path string, out any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if out != nil && rec.Body.Len() > 0 {
		if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(out); err != nil {
			t.Fatalf("decoding GET %s (%s): %v", path, rec.Body.String(), err)
		}
	}
	return rec.Code
}

const vmbr0Op = `[{"op":"bridge.create","target":"bridge:pve1:vmbr0","params":{"comments":"shared bridge"}}]`

// TestChangesets_SecondStagerIsWarnedAndCanProceed is T-2805 AC1 over HTTP:
// a second staging attempt against a locked entity is warned with the
// holder's identity, is NOT refused, and the deliberate override is audited.
func TestChangesets_SecondStagerIsWarnedAndCanProceed(t *testing.T) {
	f := newLockFixture(t)
	aliceR := f.router(lockAuth("alice@pam", "sess-alice", capAuditIdentities))
	bobR := f.router(lockAuth("bob@pam", "sess-bob", capAuditIdentities))

	code, alice := postJSON(t, aliceR, "/api/v1/changesets", `{"title":"alice","ops":`+vmbr0Op+`}`)
	if code != http.StatusCreated {
		t.Fatalf("alice create status = %d", code)
	}
	// Control: an uncontended staging response is byte-shape-unchanged —
	// no `locks` object at all.
	if alice.Locks != nil {
		t.Errorf("uncontended staging carried a locks object %+v; it must be omitted entirely", alice.Locks)
	}

	code, bob := postJSON(t, bobR, "/api/v1/changesets", `{"title":"bob","ops":`+vmbr0Op+`}`)
	if code != http.StatusCreated {
		t.Fatalf("bob create status = %d, want 201 — a lock must never refuse a staging attempt", code)
	}
	if bob.ID == "" {
		t.Fatal("bob's changeset was not created; the lock blocked staging, which it must never do")
	}
	if bob.Locks == nil || len(bob.Locks.Held) != 1 {
		t.Fatalf("bob's response locks = %+v, want one held entry warning about alice's claim", bob.Locks)
	}
	warn := bob.Locks.Held[0]
	if warn.Holder != "alice@pam" {
		t.Errorf("warning holder = %q, want alice@pam — AC1 requires the holder to be named", warn.Holder)
	}
	if warn.ChangesetID != alice.ID {
		t.Errorf("warning changesetId = %q, want alice's changeset %q", warn.ChangesetID, alice.ID)
	}
	if warn.Ref != "bridge:pve1:vmbr0" {
		t.Errorf("warning ref = %q", warn.Ref)
	}
	if warn.Mine {
		t.Error("another operator's lock was reported as mine")
	}

	// "can proceed deliberately": the same staging request with the override
	// flag takes the lock over, and the takeover is audited.
	req := httptest.NewRequest(http.MethodPut, "/api/v1/changesets/"+bob.ID,
		bytes.NewBufferString(`{"ops":`+vmbr0Op+`,"lockOverride":true}`))
	rec := httptest.NewRecorder()
	bobR.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bob override PUT status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var overridden changesetResponse
	if err := json.NewDecoder(rec.Body).Decode(&overridden); err != nil {
		t.Fatalf("decoding override response: %v", err)
	}
	if overridden.Locks == nil || len(overridden.Locks.Overridden) != 1 {
		t.Fatalf("override response locks = %+v, want one overridden entry", overridden.Locks)
	}
	if overridden.Locks.Overridden[0].Holder != "alice@pam" {
		t.Errorf("overridden holder = %q, want alice@pam", overridden.Locks.Overridden[0].Holder)
	}
	if len(overridden.Locks.Held) != 0 {
		t.Errorf("override response still reported held locks %+v; an override resolves them", overridden.Locks.Held)
	}

	entries, err := f.audit.List(context.Background(), bob.ID, 50)
	if err != nil {
		t.Fatalf("listing audit: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Action == presence.AuditActionLockOverride && e.Username == "bob@pam" {
			found = true
		}
	}
	if !found {
		t.Errorf("no %s audit row for bob's override; AC1 requires the override to be recorded", presence.AuditActionLockOverride)
	}
}

// TestChangesets_HeldLockNeverBlocksApply is T-2805 AC4, the criterion the
// card states twice: "a held lock on an entity in an approved changeset does
// not prevent applying it."
//
// The control leg is essential and is asserted at the moment of the apply,
// not before it: GET /locks shows the entity locked by SOMEONE ELSE while
// the apply is being made. Without it, a green apply would prove nothing —
// it would be equally consistent with a build that never took a lock.
func TestChangesets_HeldLockNeverBlocksApply(t *testing.T) {
	f := newLockFixture(t)
	aliceR := f.router(lockAuth("alice@pam", "sess-alice", capAuditIdentities))
	bobAuth := lockAuth("bob@pam", "sess-bob", capAuditIdentities)
	bobR := f.router(bobAuth)

	// Alice stages first and therefore holds the lock on vmbr0.
	if code, _ := postJSON(t, aliceR, "/api/v1/changesets", `{"title":"alice","ops":`+vmbr0Op+`}`); code != http.StatusCreated {
		t.Fatalf("alice create status = %d", code)
	}

	// Bob stages the same entity, is warned, and proceeds without overriding —
	// so alice's lock is still standing when bob applies.
	code, bob := postJSON(t, bobR, "/api/v1/changesets", `{"title":"bob","ops":`+vmbr0Op+`}`)
	if code != http.StatusCreated {
		t.Fatalf("bob create status = %d", code)
	}
	if bob.Locks == nil || len(bob.Locks.Held) != 1 || bob.Locks.Held[0].Holder != "alice@pam" {
		t.Fatalf("precondition: bob must be warned about alice's lock, got %+v", bob.Locks)
	}

	// Control leg, read through the API immediately before the apply: the
	// entity bob is about to change IS locked, by someone who is not bob.
	var before locksListResponse
	if st := getLockJSON(t, bobR, "/api/v1/locks", &before); st != http.StatusOK {
		t.Fatalf("GET /locks status = %d", st)
	}
	if len(before.Locks) != 1 {
		t.Fatalf("locks before apply = %+v, want exactly one", before.Locks)
	}
	if before.Locks[0].Ref != "bridge:pve1:vmbr0" || before.Locks[0].Holder != "alice@pam" || before.Locks[0].Mine {
		t.Fatalf("locks before apply = %+v, want vmbr0 held by alice@pam and not by bob — the control leg failed, so the apply assertion below proves nothing", before.Locks[0])
	}
	if before.Locks[0].ExpiresAt <= f.clock.Now().Unix() {
		t.Fatalf("the lock had already expired at apply time (expiresAt %d, now %d) — the control leg failed",
			before.Locks[0].ExpiresAt, f.clock.Now().Unix())
	}

	// The apply itself. This is the whole criterion.
	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+bob.ID+"/apply", nil)
	applyRec := httptest.NewRecorder()
	bobR.ServeHTTP(applyRec, applyReq)
	if applyRec.Code != http.StatusOK && applyRec.Code != http.StatusAccepted {
		t.Fatalf("apply of a changeset touching a LOCKED entity returned %d: %s\n\n"+
			"T-2805 AC4: a lock is advisory. It must never sit in the apply path as a refusal — "+
			"that would be a second gate on stage → validate → diff → apply → confirm/rollback.",
			applyRec.Code, applyRec.Body.String())
	}
	var applied changesetResponse
	if err := json.NewDecoder(applyRec.Body).Decode(&applied); err != nil {
		t.Fatalf("decoding apply response: %v", err)
	}
	if applied.Status != string(change.StatusAwaitingConfirm) {
		t.Errorf("status after apply = %q, want awaiting_confirm", applied.Status)
	}

	// And the lock is still exactly where it was: applying is not a takeover,
	// and nothing about the apply path touched the lock table.
	var after locksListResponse
	if st := getLockJSON(t, bobR, "/api/v1/locks", &after); st != http.StatusOK {
		t.Fatalf("GET /locks after apply status = %d", st)
	}
	if len(after.Locks) != 1 || after.Locks[0].Holder != "alice@pam" {
		t.Errorf("locks after apply = %+v, want alice's lock untouched", after.Locks)
	}
}

// TestPresence_DoesNotLeakIdentitiesWithoutTheCapability is T-2805 AC5, with
// one assertion per restricted surface (GET /presence and GET /locks) and a
// control leg showing a capable caller does see the names.
func TestPresence_DoesNotLeakIdentitiesWithoutTheCapability(t *testing.T) {
	f := newLockFixture(t)

	// Alice is present on a changeset scope and holds a lock.
	f.presence.ConnOpened("c-alice", "alice@pam", "sess-alice")
	f.presence.ConnTopics("c-alice", []string{"presence:changeset:cs-1"})
	stagerR := f.router(lockAuth("alice@pam", "sess-alice", capAuditIdentities))
	if code, _ := postJSON(t, stagerR, "/api/v1/changesets", `{"title":"alice","ops":`+vmbr0Op+`}`); code != http.StatusCreated {
		t.Fatalf("alice create status = %d", code)
	}

	capable := f.router(lockAuth("carol@pam", "sess-carol", capAuditIdentities))
	incapable := f.router(lockAuth("dan@pam", "sess-dan")) // netRead + netWrite, no audit

	// --- surface 1: GET /presence -------------------------------------
	var capablePresence, blindPresence presenceListResponse
	if st := getLockJSON(t, capable, "/api/v1/presence?scope=changeset:cs-1", &capablePresence); st != http.StatusOK {
		t.Fatalf("capable GET /presence status = %d", st)
	}
	if len(capablePresence.Scopes) != 1 || capablePresence.Scopes[0].Count != 1 {
		t.Fatalf("capable presence = %+v, want one scope with one viewer", capablePresence.Scopes)
	}
	// Control leg: a caller WITH the capability really does see the name.
	if len(capablePresence.Scopes[0].Viewers) != 1 || capablePresence.Scopes[0].Viewers[0].User != "alice@pam" {
		t.Fatalf("capable presence viewers = %+v, want alice@pam — the control leg failed, so the redaction assertion below proves nothing",
			capablePresence.Scopes[0].Viewers)
	}

	if st := getLockJSON(t, incapable, "/api/v1/presence?scope=changeset:cs-1", &blindPresence); st != http.StatusOK {
		t.Fatalf("incapable GET /presence status = %d, want 200 — being unable to see a NAME is not a reason to refuse the read", st)
	}
	if len(blindPresence.Scopes) != 1 {
		t.Fatalf("incapable presence = %+v, want the scope still reported", blindPresence.Scopes)
	}
	if blindPresence.Scopes[0].Count != 1 {
		t.Errorf("incapable presence count = %d, want 1 — the COUNT is not an identity", blindPresence.Scopes[0].Count)
	}
	if len(blindPresence.Scopes[0].Viewers) != 0 {
		t.Errorf("GET /presence leaked viewers %+v to a caller without the %q capability (AC5)",
			blindPresence.Scopes[0].Viewers, capAuditIdentities)
	}

	// --- surface 2: GET /locks ----------------------------------------
	var capableLocks, blindLocks locksListResponse
	if st := getLockJSON(t, capable, "/api/v1/locks", &capableLocks); st != http.StatusOK {
		t.Fatalf("capable GET /locks status = %d", st)
	}
	if len(capableLocks.Locks) != 1 || capableLocks.Locks[0].Holder != "alice@pam" {
		t.Fatalf("capable locks = %+v, want the holder named — the control leg failed", capableLocks.Locks)
	}
	if st := getLockJSON(t, incapable, "/api/v1/locks", &blindLocks); st != http.StatusOK {
		t.Fatalf("incapable GET /locks status = %d, want 200", st)
	}
	if len(blindLocks.Locks) != 1 {
		t.Fatalf("incapable locks = %+v, want the lock still reported", blindLocks.Locks)
	}
	if blindLocks.Locks[0].Ref != "bridge:pve1:vmbr0" {
		t.Errorf("incapable locks ref = %q, want the ref still visible — that an entity is spoken for is an ordinary read",
			blindLocks.Locks[0].Ref)
	}
	if blindLocks.Locks[0].Holder != "" {
		t.Errorf("GET /locks leaked holder %q to a caller without the %q capability (AC5)",
			blindLocks.Locks[0].Holder, capAuditIdentities)
	}

	// --- surface 3: the staging warning -------------------------------
	// The same rule applies where the warning is rendered, or a caller could
	// enumerate identities by staging against every entity in turn.
	code, warned := postJSON(t, incapable, "/api/v1/changesets", `{"title":"dan","ops":`+vmbr0Op+`}`)
	if code != http.StatusCreated {
		t.Fatalf("dan create status = %d", code)
	}
	if warned.Locks == nil || len(warned.Locks.Held) != 1 {
		t.Fatalf("dan's response locks = %+v, want the collision still reported", warned.Locks)
	}
	if warned.Locks.Held[0].Holder != "" {
		t.Errorf("the staging warning leaked holder %q to a caller without the %q capability (AC5)",
			warned.Locks.Held[0].Holder, capAuditIdentities)
	}
}

// TestPresence_ScopeValidation: an unrecognised scope is a 400, not a
// silently-empty answer that a client would read as "nobody is here".
func TestPresence_ScopeValidation(t *testing.T) {
	f := newLockFixture(t)
	r := f.router(lockAuth("alice@pam", "sess-alice", capAuditIdentities))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/presence?scope=nonsense", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /presence?scope=nonsense status = %d, want 400", rec.Code)
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decoding error: %v", err)
	}
	if errResp.Error.Code != "validation_failed" {
		t.Errorf("error code = %q, want validation_failed", errResp.Error.Code)
	}
}

// TestChangesets_DiscardReleasesItsLocks: the draft is gone, so its claim is
// meaningless. Control leg: the other draft's lock survives.
func TestChangesets_DiscardReleasesItsLocks(t *testing.T) {
	f := newLockFixture(t)
	aliceR := f.router(lockAuth("alice@pam", "sess-alice", capAuditIdentities))
	bobR := f.router(lockAuth("bob@pam", "sess-bob", capAuditIdentities))

	_, alice := postJSON(t, aliceR, "/api/v1/changesets", `{"title":"alice","ops":`+vmbr0Op+`}`)
	if _, bob := postJSON(t, bobR, "/api/v1/changesets",
		`{"title":"bob","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr9","params":{"comments":"bob"}}]}`); bob.ID == "" {
		t.Fatal("bob's changeset was not created")
	}

	var before locksListResponse
	if st := getLockJSON(t, aliceR, "/api/v1/locks", &before); st != http.StatusOK || len(before.Locks) != 2 {
		t.Fatalf("locks before discard = %+v (status %d), want two", before.Locks, st)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/changesets/"+alice.ID, nil)
	rec := httptest.NewRecorder()
	aliceR.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("discard status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var after locksListResponse
	if st := getLockJSON(t, aliceR, "/api/v1/locks", &after); st != http.StatusOK {
		t.Fatalf("GET /locks status = %d", st)
	}
	if len(after.Locks) != 1 || after.Locks[0].Ref != "bridge:pve1:vmbr9" {
		t.Errorf("locks after discarding alice's draft = %+v, want only bob's", after.Locks)
	}
}

// TestLockRoutes_NotMountedWithoutTheService: a deployment with no lock
// service serves no lock routes, and — more importantly — stages exactly as
// it did before, with no `locks` field anywhere.
func TestLockRoutes_NotMountedWithoutTheService(t *testing.T) {
	svc := newChangesetTestService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{}, Changesets: svc,
	})

	for _, path := range []string{"/api/v1/locks", "/api/v1/presence"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s without a lock service: status = %d, want 404", path, rec.Code)
		}
	}

	code, created := postJSON(t, r, "/api/v1/changesets", `{"title":"x","ops":`+vmbr0Op+`}`)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d", code)
	}
	if created.Locks != nil {
		t.Errorf("locks object present with no lock service wired: %+v", created.Locks)
	}
}
