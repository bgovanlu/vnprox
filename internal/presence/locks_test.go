package presence_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/presence"
	"github.com/bgovanlu/vnprox/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testClock is the injected clock every timing assertion in this package
// runs against. T-2805 AC2 requires lock expiry to be proven "with a clock
// rather than a wait": a time.Sleep would make the assertion a bet on the
// scheduler, and this repository has a recorded history of load-sensitive
// tests failing under CPU pressure. Advance() is the only thing that ever
// moves time here.
type testClock struct {
	now time.Time
}

func newTestClock() *testClock { return &testClock{now: time.Unix(1_700_000_000, 0)} }

func (c *testClock) Now() time.Time          { return c.now }
func (c *testClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

type presenceFixture struct {
	svc   *presence.Service
	clock *testClock
	audit *store.AuditRepo
	locks *store.EntityLockRepo
	ws    *recordingBroadcaster
}

type recordingBroadcaster struct {
	msgs []broadcastMsg
}

type broadcastMsg struct {
	topic   string
	payload []byte
}

func (b *recordingBroadcaster) Broadcast(topic string, payload []byte) {
	b.msgs = append(b.msgs, broadcastMsg{topic: topic, payload: append([]byte(nil), payload...)})
}

func newFixture(t *testing.T, ttl time.Duration) presenceFixture {
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
	clock := newTestClock()
	audit := store.NewAuditRepo(db)
	locks := store.NewEntityLockRepo(db)
	ws := &recordingBroadcaster{}
	svc, err := presence.NewService(presence.Config{
		Locks:  locks,
		Audit:  audit,
		WS:     ws,
		Now:    clock.Now,
		TTL:    ttl,
		Logger: testLogger(),
	})
	if err != nil {
		t.Fatalf("presence.NewService: %v", err)
	}
	return presenceFixture{svc: svc, clock: clock, audit: audit, locks: locks, ws: ws}
}

func alice() presence.Principal {
	return presence.Principal{Username: "alice@pam", SessionID: "sess-alice"}
}

func bob() presence.Principal {
	return presence.Principal{Username: "bob@pam", SessionID: "sess-bob"}
}

// TestStage_SecondStagerIsWarnedAndProceeds is T-2805 AC1's engine half: a
// second staging attempt against a locked entity is warned with the
// holder's identity — and is NOT refused. Nothing in StageResult can refuse
// anything; the assertion that matters as much as the warning is that
// Acquired covers the uncontended ref and the call returns no error.
func TestStage_SecondStagerIsWarnedAndProceeds(t *testing.T) {
	f := newFixture(t, 15*time.Minute)
	ctx := context.Background()

	first, err := f.svc.Stage(ctx, "cs-alice", []string{"bridge:pve1:vmbr0"}, alice(), false)
	if err != nil {
		t.Fatalf("alice Stage: %v", err)
	}
	if len(first.Acquired) != 1 || first.Acquired[0] != "bridge:pve1:vmbr0" {
		t.Fatalf("alice acquired %v, want [bridge:pve1:vmbr0]", first.Acquired)
	}
	if first.Warned() {
		t.Errorf("the first stager was warned about %+v; nobody else holds anything", first)
	}

	second, err := f.svc.Stage(ctx, "cs-bob", []string{"bridge:pve1:vmbr0", "bridge:pve1:vmbr9"}, bob(), false)
	if err != nil {
		t.Fatalf("bob Stage: %v", err)
	}
	if len(second.Conflicts) != 1 {
		t.Fatalf("bob got %d conflicts, want 1: %+v", len(second.Conflicts), second)
	}
	got := second.Conflicts[0]
	if got.Holder != "alice@pam" {
		t.Errorf("conflict holder = %q, want alice@pam — AC1 requires the warning to NAME the holder", got.Holder)
	}
	if got.ChangesetID != "cs-alice" {
		t.Errorf("conflict changesetId = %q, want cs-alice", got.ChangesetID)
	}
	if got.Ref != "bridge:pve1:vmbr0" {
		t.Errorf("conflict ref = %q, want bridge:pve1:vmbr0", got.Ref)
	}

	// "and can proceed": the uncontended ref was still taken, and the
	// contended one was left with its original holder rather than the whole
	// staging attempt being rejected.
	if len(second.Acquired) != 1 || second.Acquired[0] != "bridge:pve1:vmbr9" {
		t.Errorf("bob acquired %v, want only the uncontended [bridge:pve1:vmbr9]", second.Acquired)
	}
	if len(second.Overridden) != 0 {
		t.Errorf("bob overrode %+v without asking to", second.Overridden)
	}
	held, err := f.locks.Get(ctx, "bridge:pve1:vmbr0")
	if err != nil {
		t.Fatalf("reading the contended lock: %v", err)
	}
	if held.Holder != "alice@pam" {
		t.Errorf("contended lock holder = %q after a non-override stage, want alice@pam", held.Holder)
	}
}

// TestStage_OverrideTakesTheLockAndIsAudited is AC1's second half: the
// override succeeds and is recorded. T-2805: "Overriding is recorded."
func TestStage_OverrideTakesTheLockAndIsAudited(t *testing.T) {
	f := newFixture(t, 15*time.Minute)
	ctx := context.Background()

	if _, err := f.svc.Stage(ctx, "cs-alice", []string{"bridge:pve1:vmbr0"}, alice(), false); err != nil {
		t.Fatalf("alice Stage: %v", err)
	}
	res, err := f.svc.Stage(ctx, "cs-bob", []string{"bridge:pve1:vmbr0"}, bob(), true)
	if err != nil {
		t.Fatalf("bob override Stage: %v", err)
	}
	if len(res.Overridden) != 1 || res.Overridden[0].Holder != "alice@pam" {
		t.Fatalf("overridden = %+v, want one entry naming alice@pam", res.Overridden)
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("an overriding stage reported conflicts %+v; an override resolves them", res.Conflicts)
	}
	held, err := f.locks.Get(ctx, "bridge:pve1:vmbr0")
	if err != nil {
		t.Fatalf("reading the overridden lock: %v", err)
	}
	if held.Holder != "bob@pam" || held.ChangesetID != "cs-bob" {
		t.Errorf("lock after override = (%q, %q), want (bob@pam, cs-bob)", held.Holder, held.ChangesetID)
	}

	entries, err := f.audit.List(ctx, "cs-bob", 50)
	if err != nil {
		t.Fatalf("listing audit: %v", err)
	}
	var found *store.AuditEntry
	for i := range entries {
		if entries[i].Action == presence.AuditActionLockOverride {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no %s audit row after an override; AC1 requires the override to be audited", presence.AuditActionLockOverride)
	}
	if found.Username != "bob@pam" {
		t.Errorf("audit username = %q, want bob@pam (who took the override)", found.Username)
	}
	if !found.Target.Valid || found.Target.String != "bridge:pve1:vmbr0" {
		t.Errorf("audit target = %+v, want the overridden ref", found.Target)
	}
	if !found.ChangesetID.Valid || found.ChangesetID.String != "cs-bob" {
		t.Errorf("audit changesetId = %+v, want cs-bob", found.ChangesetID)
	}
	var detail map[string]any
	if !found.DetailJSON.Valid {
		t.Fatalf("audit row carries no detail; the previous holder must be recorded")
	}
	if err := json.Unmarshal([]byte(found.DetailJSON.String), &detail); err != nil {
		t.Fatalf("decoding audit detail: %v", err)
	}
	if detail["previousHolder"] != "alice@pam" || detail["previousChangeset"] != "cs-alice" {
		t.Errorf("audit detail = %v, want the previous holder and their changeset", detail)
	}
}

// TestStage_RenewingYourOwnLockIsNeverAConflict: the same principal
// re-staging (the drawer saves on every edit) must not warn about itself.
func TestStage_RenewingYourOwnLockIsNeverAConflict(t *testing.T) {
	f := newFixture(t, 15*time.Minute)
	ctx := context.Background()

	if _, err := f.svc.Stage(ctx, "cs-alice", []string{"bridge:pve1:vmbr0"}, alice(), false); err != nil {
		t.Fatalf("first Stage: %v", err)
	}
	f.clock.Advance(time.Minute)
	res, err := f.svc.Stage(ctx, "cs-alice", []string{"bridge:pve1:vmbr0"}, alice(), false)
	if err != nil {
		t.Fatalf("second Stage: %v", err)
	}
	if res.Warned() {
		t.Fatalf("re-staging your own draft warned: %+v", res)
	}
	held, err := f.locks.Get(ctx, "bridge:pve1:vmbr0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := f.clock.Now().Add(15 * time.Minute).Unix()
	if held.ExpiresAt != want {
		t.Errorf("expiry after renewal = %d, want %d (a renewal must push the expiry out)", held.ExpiresAt, want)
	}
}

// TestLockExpiry_FreesTheEntity is T-2805 AC2: a lock expires on timeout,
// proven with a CLOCK and not a wait, and expiry frees the entity.
//
// The control leg is the first half: before the clock moves, the lock is
// held and a second stager is warned. Without it, "no conflict after
// advancing the clock" would also pass against a build that never took the
// lock at all.
func TestLockExpiry_FreesTheEntity(t *testing.T) {
	f := newFixture(t, 10*time.Minute)
	ctx := context.Background()

	if _, err := f.svc.Stage(ctx, "cs-alice", []string{"bridge:pve1:vmbr0"}, alice(), false); err != nil {
		t.Fatalf("alice Stage: %v", err)
	}

	// Control: the lock is genuinely held right now.
	before, err := f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks before expiry: %v", err)
	}
	if len(before) != 1 || before[0].Holder != "alice@pam" {
		t.Fatalf("locks before expiry = %+v, want one held by alice@pam", before)
	}
	blocked, err := f.svc.Stage(ctx, "cs-bob", []string{"bridge:pve1:vmbr0"}, bob(), false)
	if err != nil {
		t.Fatalf("bob Stage before expiry: %v", err)
	}
	if len(blocked.Conflicts) != 1 {
		t.Fatalf("bob saw %d conflicts before expiry, want 1 — the control leg failed, so the expiry assertion below proves nothing", len(blocked.Conflicts))
	}

	// One second short of the TTL: still held. Expiry is inclusive at the
	// deadline, so this boundary is worth pinning.
	f.clock.Advance(10*time.Minute - time.Second)
	stillHeld, err := f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks just before expiry: %v", err)
	}
	if len(stillHeld) != 1 {
		t.Fatalf("locks 1s before the TTL = %+v, want the lock still held", stillHeld)
	}

	// Past the TTL: gone, with no wait of any kind.
	f.clock.Advance(2 * time.Second)
	after, err := f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks after expiry: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("locks after the TTL elapsed = %+v, want none", after)
	}

	// "and expiry frees the entity": a fresh stager takes it with no warning.
	free, err := f.svc.Stage(ctx, "cs-bob2", []string{"bridge:pve1:vmbr0"}, bob(), false)
	if err != nil {
		t.Fatalf("bob Stage after expiry: %v", err)
	}
	if free.Warned() {
		t.Errorf("staging against an EXPIRED lock warned: %+v", free)
	}
	if len(free.Acquired) != 1 {
		t.Errorf("bob acquired %v after expiry, want the freed ref", free.Acquired)
	}
	held, err := f.locks.Get(ctx, "bridge:pve1:vmbr0")
	if err != nil {
		t.Fatalf("Get after re-acquire: %v", err)
	}
	if held.Holder != "bob@pam" {
		t.Errorf("holder after expiry + re-acquire = %q, want bob@pam", held.Holder)
	}
}

// TestReleaseChangeset_FreesEverythingThatDraftHeld covers the discarded-
// draft path, with a control leg proving the other draft's locks survive.
func TestReleaseChangeset_FreesEverythingThatDraftHeld(t *testing.T) {
	f := newFixture(t, 15*time.Minute)
	ctx := context.Background()

	if _, err := f.svc.Stage(ctx, "cs-alice", []string{"bridge:pve1:vmbr0", "bridge:pve1:vmbr1"}, alice(), false); err != nil {
		t.Fatalf("alice Stage: %v", err)
	}
	if _, err := f.svc.Stage(ctx, "cs-bob", []string{"bridge:pve2:vmbr0"}, bob(), false); err != nil {
		t.Fatalf("bob Stage: %v", err)
	}

	n, err := f.svc.ReleaseChangeset(ctx, "cs-alice")
	if err != nil {
		t.Fatalf("ReleaseChangeset: %v", err)
	}
	if n != 2 {
		t.Errorf("released %d locks, want 2", n)
	}
	remaining, err := f.svc.Locks(ctx)
	if err != nil {
		t.Fatalf("Locks: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ChangesetID != "cs-bob" {
		t.Errorf("locks after releasing cs-alice = %+v, want only cs-bob's", remaining)
	}
}

// TestHeld reports the unexpired lock on one ref, and reports nothing for an
// expired one — the read-time expiry judgement again, on the single-ref path.
func TestHeld_IgnoresExpired(t *testing.T) {
	f := newFixture(t, 5*time.Minute)
	ctx := context.Background()

	if _, err := f.svc.Stage(ctx, "cs-alice", []string{"bridge:pve1:vmbr0"}, alice(), false); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	l, ok, err := f.svc.Held(ctx, "bridge:pve1:vmbr0")
	if err != nil || !ok {
		t.Fatalf("Held = (%+v, %v, %v), want the held lock", l, ok, err)
	}
	f.clock.Advance(6 * time.Minute)
	if _, ok, err = f.svc.Held(ctx, "bridge:pve1:vmbr0"); err != nil || ok {
		t.Errorf("Held after expiry = (%v, %v), want (false, nil)", ok, err)
	}
	if _, ok, err = f.svc.Held(ctx, "bridge:pve1:absent"); err != nil || ok {
		t.Errorf("Held(absent) = (%v, %v), want (false, nil)", ok, err)
	}
}
