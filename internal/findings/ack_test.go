package findings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeAckStore is an in-memory AckStore. It stores what it is given verbatim,
// including expired rows, so a test can plant an already-expired
// acknowledgement directly — which is how AC2 proves expiry is decided at read
// time rather than by some cleanup pass.
type fakeAckStore struct {
	rows    map[string]Ack
	listErr error
	upserts int
	deletes int
}

func newFakeAckStore() *fakeAckStore { return &fakeAckStore{rows: map[string]Ack{}} }

func (f *fakeAckStore) ListAcks(context.Context) (map[string]Ack, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make(map[string]Ack, len(f.rows))
	for k, v := range f.rows {
		out[k] = v
	}
	return out, nil
}

func (f *fakeAckStore) UpsertAck(_ context.Context, id string, a Ack) error {
	f.upserts++
	f.rows[id] = a
	return nil
}

func (f *fakeAckStore) DeleteAck(_ context.Context, id string) error {
	f.deletes++
	delete(f.rows, id)
	return nil
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func ackFinding(id string) Finding {
	return Finding{ID: id, Source: SourceHealth, Check: "mtu_mismatch", Severity: SeverityWarning, Nodes: []string{"pve1"}}
}

// AC1: an acknowledgement is keyed on the finding's stable id, so it survives
// a full recompute. Simulated the way a recompute actually behaves — the
// producers are re-run and hand back *freshly constructed* Finding values, not
// the same slice — so a decorator that mutated its input in place, or one that
// cached by pointer identity, would fail here.
func TestAckSurvivesARecomputeCycle(t *testing.T) {
	st := newFakeAckStore()
	now := time.Unix(1_000_000, 0)
	svc := NewAckService(st, fixedClock(now))
	ctx := context.Background()

	first := []Finding{ackFinding("health:mtu_mismatch|bridge:pve1:vmbr0")}
	if _, err := svc.Ack(ctx, first[0].ID, "deliberate, jumbo on storage only", "brian", 0, PresentFindings(first)); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	// A second cycle: same state, brand-new Finding values.
	second := []Finding{ackFinding("health:mtu_mismatch|bridge:pve1:vmbr0")}
	if second[0].Ack != nil {
		t.Fatal("a freshly produced finding must not arrive pre-acked; the producer knows nothing about acks")
	}
	got, acked, err := svc.Decorate(ctx, second)
	if err != nil {
		t.Fatalf("Decorate: %v", err)
	}
	if acked != 1 {
		t.Fatalf("acked count = %d, want 1", acked)
	}
	if got[0].Ack == nil {
		t.Fatal("the acknowledgement did not survive the recompute cycle")
	}
	if got[0].Ack.Reason != "deliberate, jumbo on storage only" || got[0].Ack.AckedBy != "brian" {
		t.Fatalf("ack round-tripped wrong: %+v", got[0].Ack)
	}
}

// Decorate must not mutate its input. The API hands it the engine's own slice.
func TestDecorateDoesNotMutateItsInput(t *testing.T) {
	st := newFakeAckStore()
	now := time.Unix(1_000_000, 0)
	st.rows["f1"] = Ack{Reason: "r", AckedBy: "b", AckedAt: now.Unix()}
	svc := NewAckService(st, fixedClock(now))

	in := []Finding{ackFinding("f1")}
	out, _, err := svc.Decorate(context.Background(), in)
	if err != nil {
		t.Fatalf("Decorate: %v", err)
	}
	if in[0].Ack != nil {
		t.Fatal("Decorate mutated the caller's slice")
	}
	if out[0].Ack == nil {
		t.Fatal("Decorate did not decorate its output")
	}
}

// AC2: expiry is evaluated at read time. The row is planted directly in the
// store already expired and no service call that could have cleaned it up is
// made first — so this fails for any implementation that relies on a sweeper,
// a startup pass, or a delete-on-write. The store is then re-read to prove the
// row is still there: an expired ack is inactive, not deleted.
func TestExpiredAckDoesNotApplyAndIsNotDeleted(t *testing.T) {
	st := newFakeAckStore()
	now := time.Unix(2_000_000, 0)
	st.rows["f1"] = Ack{Reason: "temporary", AckedBy: "brian", AckedAt: 1_000_000, ExpiresAt: now.Unix() - 1}
	svc := NewAckService(st, fixedClock(now))

	got, acked, err := svc.Decorate(context.Background(), []Finding{ackFinding("f1")})
	if err != nil {
		t.Fatalf("Decorate: %v", err)
	}
	if acked != 0 || got[0].Ack != nil {
		t.Fatalf("an expired acknowledgement still applied: acked=%d ack=%+v", acked, got[0].Ack)
	}
	if st.deletes != 0 {
		t.Fatalf("a read path deleted %d rows; expiry must not make Decorate a writer", st.deletes)
	}
	if _, ok := st.rows["f1"]; !ok {
		t.Fatal("the expired row was removed from the store by a read")
	}
}

// The boundary itself: an ack "until T" must not still be muting at T.
func TestAckExpiresAtTheInstantItSays(t *testing.T) {
	expiry := int64(2_000_000)
	a := Ack{ExpiresAt: expiry}
	if !a.Active(time.Unix(expiry-1, 0)) {
		t.Fatal("ack should still be active one second before its expiry")
	}
	if a.Active(time.Unix(expiry, 0)) {
		t.Fatal("ack must not be active at its own expiry instant")
	}
	if a.Active(time.Unix(expiry+1, 0)) {
		t.Fatal("ack must not be active after its expiry")
	}
	if never := (Ack{ExpiresAt: 0}); !never.Active(time.Unix(1<<40, 0)) {
		t.Fatal("ExpiresAt 0 means never expires")
	}
}

// AC3: a reason is required, and whitespace is not a reason.
func TestAckRequiresANonBlankReason(t *testing.T) {
	for _, reason := range []string{"", "   ", "\t\n "} {
		st := newFakeAckStore()
		svc := NewAckService(st, fixedClock(time.Unix(1_000, 0)))
		present := map[string]Finding{"f1": {ID: "f1"}}
		if _, err := svc.Ack(context.Background(), "f1", reason, "brian", 0, present); !errors.Is(err, ErrAckReasonRequired) {
			t.Fatalf("reason %q: err = %v, want ErrAckReasonRequired", reason, err)
		}
		if st.upserts != 0 {
			t.Fatalf("reason %q: a refused ack still wrote to the store", reason)
		}
	}
}

func TestAckStoresTheTrimmedReasonAndBoundsItsLength(t *testing.T) {
	st := newFakeAckStore()
	svc := NewAckService(st, fixedClock(time.Unix(1_000, 0)))
	present := map[string]Finding{"f1": {ID: "f1"}}

	if _, err := svc.Ack(context.Background(), "f1", "  padded  ", "brian", 0, present); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if got := st.rows["f1"].Reason; got != "padded" {
		t.Fatalf("reason = %q, want the trimmed form", got)
	}
	if _, err := svc.Ack(context.Background(), "f1", strings.Repeat("x", maxAckReasonLen+1), "brian", 0, present); err == nil {
		t.Fatal("an over-long reason should be refused")
	}
}

// AC4: acking an id no producer reports is refused rather than creating a row
// nothing can ever surface or clear from the UI.
func TestAckOfAnUnknownFindingIsRefusedAndWritesNothing(t *testing.T) {
	st := newFakeAckStore()
	svc := NewAckService(st, fixedClock(time.Unix(1_000, 0)))
	present := map[string]Finding{"f1": {ID: "f1"}}

	_, err := svc.Ack(context.Background(), "does-not-exist", "because", "brian", 0, present)
	if !errors.Is(err, ErrNoSuchFinding) {
		t.Fatalf("err = %v, want ErrNoSuchFinding", err)
	}
	if st.upserts != 0 {
		t.Fatalf("a refused ack wrote %d rows", st.upserts)
	}
}

// An expiry already in the past would record an acknowledgement that never
// applies — a silent no-op the operator would believe had worked.
func TestAckRefusesAnExpiryAlreadyInThePast(t *testing.T) {
	st := newFakeAckStore()
	now := time.Unix(2_000_000, 0)
	svc := NewAckService(st, fixedClock(now))
	present := map[string]Finding{"f1": {ID: "f1"}}

	for _, exp := range []int64{now.Unix() - 1, now.Unix()} {
		if _, err := svc.Ack(context.Background(), "f1", "because", "brian", exp, present); !errors.Is(err, ErrAckExpiryInPast) {
			t.Fatalf("expiry %d: err = %v, want ErrAckExpiryInPast", exp, err)
		}
	}
	if _, err := svc.Ack(context.Background(), "f1", "because", "brian", now.Unix()+1, present); err != nil {
		t.Fatalf("an expiry one second in the future should be accepted: %v", err)
	}
}

// Re-acking replaces rather than duplicating or refusing: extending a mute
// should not require un-acking first.
func TestReAckingReplacesTheReasonActorAndExpiry(t *testing.T) {
	st := newFakeAckStore()
	now := time.Unix(1_000_000, 0)
	svc := NewAckService(st, fixedClock(now))
	present := map[string]Finding{"f1": {ID: "f1"}}
	ctx := context.Background()

	if _, err := svc.Ack(ctx, "f1", "first", "alice", 0, present); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if _, err := svc.Ack(ctx, "f1", "second", "brian", now.Unix()+3600, present); err != nil {
		t.Fatalf("re-Ack: %v", err)
	}
	if len(st.rows) != 1 {
		t.Fatalf("re-acking created %d rows, want 1", len(st.rows))
	}
	got := st.rows["f1"]
	if got.Reason != "second" || got.AckedBy != "brian" || got.ExpiresAt != now.Unix()+3600 {
		t.Fatalf("re-ack did not replace the row: %+v", got)
	}
}

// Unack deliberately does NOT require the finding to still be present: an
// operator must always be able to clear a stale row, including one whose
// finding has since gone away. Asserting the opposite would strand it.
func TestUnackWorksForAFindingThatNoLongerExists(t *testing.T) {
	st := newFakeAckStore()
	st.rows["gone"] = Ack{Reason: "r", AckedBy: "b", AckedAt: 1}
	svc := NewAckService(st, fixedClock(time.Unix(1_000, 0)))

	if err := svc.Unack(context.Background(), "gone"); err != nil {
		t.Fatalf("Unack: %v", err)
	}
	if _, ok := st.rows["gone"]; ok {
		t.Fatal("Unack left the row in place")
	}
}

// AC6 — the deliberate semantics, stated and pinned.
//
// A finding whose condition CLEARS and later RETURNS with the same stable id is
// STILL ACKED. That is the intent: a flapping condition is precisely what an
// operator mutes, and an ack that evaporated on the first clear would be
// defeated by the findings it is most needed for. Expiry, not flapping, is what
// ends an acknowledgement.
//
// This test exists so that changing the behaviour is a decision someone has to
// make deliberately rather than a regression nobody notices.
func TestAckSurvivesTheFindingClearingAndReturning(t *testing.T) {
	st := newFakeAckStore()
	now := time.Unix(1_000_000, 0)
	svc := NewAckService(st, fixedClock(now))
	ctx := context.Background()

	cycle1 := []Finding{ackFinding("health:mtu_mismatch|bridge:pve1:vmbr0")}
	if _, err := svc.Ack(ctx, cycle1[0].ID, "known-good asymmetry", "brian", 0, PresentFindings(cycle1)); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	// Cycle 2: the condition clears — the finding is not produced at all.
	got, acked, err := svc.Decorate(ctx, nil)
	if err != nil {
		t.Fatalf("Decorate (cleared): %v", err)
	}
	if len(got) != 0 || acked != 0 {
		t.Fatalf("a cleared cycle should report nothing: %d findings, %d acked", len(got), acked)
	}

	// Cycle 3: it returns. Same condition, same refs, therefore same id.
	cycle3 := []Finding{ackFinding("health:mtu_mismatch|bridge:pve1:vmbr0")}
	got, acked, err = svc.Decorate(ctx, cycle3)
	if err != nil {
		t.Fatalf("Decorate (returned): %v", err)
	}
	if acked != 1 || got[0].Ack == nil {
		t.Fatal("the acknowledgement did not survive the finding clearing and returning — see this test's comment: that is the intended behaviour")
	}
}

// The counterpart: an acknowledgement is scoped to one finding id, so a
// DIFFERENT finding from the same check on a different ref is untouched.
func TestAckAppliesToOneFindingIDOnly(t *testing.T) {
	st := newFakeAckStore()
	now := time.Unix(1_000_000, 0)
	svc := NewAckService(st, fixedClock(now))
	ctx := context.Background()

	all := []Finding{
		ackFinding("health:mtu_mismatch|bridge:pve1:vmbr0"),
		ackFinding("health:mtu_mismatch|bridge:pve1:vmbr1"),
	}
	if _, err := svc.Ack(ctx, all[0].ID, "deliberate", "brian", 0, PresentFindings(all)); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	got, acked, err := svc.Decorate(ctx, all)
	if err != nil {
		t.Fatalf("Decorate: %v", err)
	}
	if acked != 1 {
		t.Fatalf("acked = %d, want exactly 1", acked)
	}
	if got[0].Ack == nil || got[1].Ack != nil {
		t.Fatal("the acknowledgement leaked onto a different finding from the same check")
	}
}

// A nil service (acknowledgement storage not configured — e.g. a degraded
// startup) must pass findings through rather than fail the read.
func TestNilAckServiceIsAPassThrough(t *testing.T) {
	var svc *AckService
	in := []Finding{ackFinding("f1")}
	got, acked, err := svc.Decorate(context.Background(), in)
	if err != nil || acked != 0 || len(got) != 1 {
		t.Fatalf("nil service should pass through: got %d findings, %d acked, err %v", len(got), acked, err)
	}
}

func TestDecorateSurfacesAStoreError(t *testing.T) {
	st := newFakeAckStore()
	st.listErr = errors.New("boom")
	svc := NewAckService(st, fixedClock(time.Unix(1, 0)))

	if _, _, err := svc.Decorate(context.Background(), []Finding{ackFinding("f1")}); err == nil {
		t.Fatal("a store failure must not be reported as zero acknowledgements")
	}
}

func TestPresentFindings(t *testing.T) {
	got := PresentFindings([]Finding{ackFinding("a"), ackFinding("b")})
	if len(got) != 2 || got["a"].ID != "a" || got["b"].ID != "b" {
		t.Fatalf("PresentFindings = %v", got)
	}
	if len(PresentFindings(nil)) != 0 {
		t.Fatal("PresentFindings(nil) should be empty, not nil-mapped")
	}
}
