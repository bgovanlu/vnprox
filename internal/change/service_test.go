package change

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	return db
}

// fakeBroadcaster records every Broadcast call for assertions.
type fakeBroadcaster struct {
	events []recordedEvent
	mu     sync.Mutex
}

type recordedEvent struct {
	topic   string
	payload string
}

func (f *fakeBroadcaster) Broadcast(topic string, payload []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, recordedEvent{topic: topic, payload: string(payload)})
}

func (f *fakeBroadcaster) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

func newTestService(t *testing.T, ws Broadcaster) *Service {
	t.Helper()
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		WS:         ws,
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func sampleOps() []Op {
	return []Op{{
		Type:   OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr5"},
		Params: &BridgeCreateParams{MTU: 1500},
	}}
}

func TestService_Create_PersistsAsDraft(t *testing.T) {
	ws := &fakeBroadcaster{}
	svc := newTestService(t, ws)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "add vmbr5", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Status != StatusDraft {
		t.Errorf("Status = %s, want draft", c.Status)
	}
	if c.Author != "alice@pam" {
		t.Errorf("Author = %s, want alice@pam", c.Author)
	}
	if len(c.Ops) != 1 {
		t.Fatalf("Ops = %+v, want 1 op", c.Ops)
	}
	if c.CreatedAt != 1_700_000_000 || c.UpdatedAt != 1_700_000_000 {
		t.Errorf("CreatedAt/UpdatedAt = %d/%d, want 1700000000/1700000000", c.CreatedAt, c.UpdatedAt)
	}

	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != c.ID || got.Status != StatusDraft || len(got.Ops) != 1 {
		t.Errorf("Get() = %+v, want it to match the created changeset", got)
	}

	if ws.count() != 1 {
		t.Errorf("broadcast count = %d, want 1 (the initial draft status)", ws.count())
	}
}

func TestService_Create_NilOpsStoredAsEmptyArray(t *testing.T) {
	svc := newTestService(t, nil)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "empty", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Ops == nil || len(c.Ops) != 0 {
		t.Errorf("Ops = %+v, want a non-nil empty slice", c.Ops)
	}

	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Ops == nil || len(got.Ops) != 0 {
		t.Errorf("Get().Ops = %+v, want a non-nil empty slice round-tripped through the store", got.Ops)
	}
}

func TestService_Create_AuditsCreation(t *testing.T) {
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets: store.NewChangesetRepo(db), Audit: store.NewAuditRepo(db),
		Now: func() time.Time { return time.Unix(42, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "add vmbr5", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries, err := store.NewAuditRepo(db).List(ctx, c.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries = %+v, want exactly 1", entries)
	}
	if entries[0].Action != "changeset.create" || entries[0].Username != "alice@pam" || entries[0].Result != "success" {
		t.Errorf("audit entry = %+v, want action=changeset.create username=alice@pam result=success", entries[0])
	}
}

func TestService_Get_NotFound(t *testing.T) {
	svc := newTestService(t, nil)
	_, err := svc.Get(context.Background(), "nope")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get(missing) error = %v, want store.ErrNotFound", err)
	}
}

func TestService_List_FiltersByStatusAndCoexistsAcrossAuthors(t *testing.T) {
	// T-201 acceptance criterion 4: two parked drafts by different users
	// coexist and list correctly.
	svc := newTestService(t, nil)
	ctx := context.Background()

	alice, err := svc.Create(ctx, "alice@pam", "alice's draft", sampleOps())
	if err != nil {
		t.Fatalf("Create(alice): %v", err)
	}
	bob, err := svc.Create(ctx, "bob@pam", "bob's draft", sampleOps())
	if err != nil {
		t.Fatalf("Create(bob): %v", err)
	}

	all, err := svc.List(ctx, "")
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List(all) = %d changesets, want 2", len(all))
	}
	byID := map[string]Changeset{}
	for _, c := range all {
		byID[c.ID] = c
	}
	if byID[alice.ID].Author != "alice@pam" {
		t.Errorf("alice's changeset author = %q, want alice@pam", byID[alice.ID].Author)
	}
	if byID[bob.ID].Author != "bob@pam" {
		t.Errorf("bob's changeset author = %q, want bob@pam", byID[bob.ID].Author)
	}

	drafts, err := svc.List(ctx, string(StatusDraft))
	if err != nil {
		t.Fatalf("List(draft): %v", err)
	}
	if len(drafts) != 2 {
		t.Errorf("List(draft) = %d, want 2", len(drafts))
	}

	none, err := svc.List(ctx, string(StatusCommitted))
	if err != nil {
		t.Fatalf("List(committed): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("List(committed) = %d, want 0", len(none))
	}
}

func TestService_UpdateDraft_ReplacesOps(t *testing.T) {
	ws := &fakeBroadcaster{}
	svc := newTestService(t, ws)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "draft", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	initialBroadcasts := ws.count()

	newOps := []Op{{Type: OpBondDelete, Target: inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}, Params: &BondDeleteParams{}}}
	updated, err := svc.UpdateDraft(ctx, c.ID, "alice@pam", nil, newOps)
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	if len(updated.Ops) != 1 || updated.Ops[0].Type != OpBondDelete {
		t.Errorf("Ops = %+v, want the replaced op", updated.Ops)
	}
	if updated.Status != StatusDraft {
		t.Errorf("Status = %s, want draft (unchanged)", updated.Status)
	}
	// No status transition occurred (draft -> draft isn't one), so no new
	// broadcast beyond the initial create should have fired.
	if ws.count() != initialBroadcasts {
		t.Errorf("broadcast count = %d, want unchanged at %d (no status transition on a same-status edit)", ws.count(), initialBroadcasts)
	}

	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Ops) != 1 || got.Ops[0].Type != OpBondDelete {
		t.Errorf("Get().Ops = %+v, want the replaced op persisted", got.Ops)
	}
}

func TestService_UpdateDraft_RenamesTitle(t *testing.T) {
	svc := newTestService(t, nil)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "original title", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newTitle := "renamed"
	updated, err := svc.UpdateDraft(ctx, c.ID, "alice@pam", &newTitle, c.Ops)
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	if updated.Title != "renamed" {
		t.Errorf("Title = %q, want renamed", updated.Title)
	}
}

func TestService_UpdateDraft_ValidatedRevertsToDraftAndBroadcasts(t *testing.T) {
	svc := newTestService(t, nil)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "draft", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate T-202 having validated it clean.
	if transErr := c.Transition(StatusValidated, 1_700_000_000); transErr != nil {
		t.Fatalf("Transition to validated: %v", transErr)
	}
	row, err := toStoreRow(c)
	if err != nil {
		t.Fatalf("toStoreRow: %v", err)
	}
	if updateErr := svc.repo.Update(ctx, row); updateErr != nil {
		t.Fatalf("seeding validated status: %v", updateErr)
	}

	ws := &fakeBroadcaster{}
	svc.ws = ws

	updated, err := svc.UpdateDraft(ctx, c.ID, "alice@pam", nil, sampleOps())
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	if updated.Status != StatusDraft {
		t.Errorf("Status after editing a validated changeset = %s, want draft", updated.Status)
	}
	if ws.count() != 1 {
		t.Errorf("broadcast count = %d, want 1 (validated -> draft is a real status transition)", ws.count())
	}
}

func TestService_UpdateDraft_NotEditable_ErrIllegalTransition(t *testing.T) {
	svc := newTestService(t, nil)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "draft", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if transErr := c.Transition(StatusApplying, 1_700_000_000); transErr != nil {
		t.Fatalf("Transition to applying: %v", transErr)
	}
	row, err := toStoreRow(c)
	if err != nil {
		t.Fatalf("toStoreRow: %v", err)
	}
	if updateErr := svc.repo.Update(ctx, row); updateErr != nil {
		t.Fatalf("seeding applying status: %v", updateErr)
	}

	_, err = svc.UpdateDraft(ctx, c.ID, "alice@pam", nil, sampleOps())
	var illegal *ErrIllegalTransition
	if !errors.As(err, &illegal) {
		t.Fatalf("UpdateDraft on an applying changeset: error = %v, want *ErrIllegalTransition", err)
	}
}

func TestService_Discard_TransitionsAndAudits(t *testing.T) {
	ws := &fakeBroadcaster{}
	svc := newTestService(t, ws)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "draft", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := ws.count()

	if discardErr := svc.Discard(ctx, c.ID, "alice@pam"); discardErr != nil {
		t.Fatalf("Discard: %v", discardErr)
	}

	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after discard: %v", err)
	}
	if got.Status != StatusDiscarded {
		t.Errorf("Status = %s, want discarded", got.Status)
	}
	if ws.count() != before+1 {
		t.Errorf("broadcast count = %d, want %d (draft -> discarded transition)", ws.count(), before+1)
	}
}

// TestService_Discard_Audits verifies the audit entry directly (kept
// separate from TestService_Discard_TransitionsAndAudits to avoid needing
// to thread the underlying *store.DB through the Service under test).
func TestService_Discard_Audits(t *testing.T) {
	db := openTestDB(t)
	audit := store.NewAuditRepo(db)
	svc, err := NewService(Config{Changesets: store.NewChangesetRepo(db), Audit: audit, Now: func() time.Time { return time.Unix(7, 0) }})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "draft", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if discardErr := svc.Discard(ctx, c.ID, "alice@pam"); discardErr != nil {
		t.Fatalf("Discard: %v", discardErr)
	}

	entries, err := audit.List(ctx, c.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	var discardEntries int
	for _, e := range entries {
		if e.Action == "changeset.discard" {
			discardEntries++
			if e.Username != "alice@pam" || e.Result != "success" {
				t.Errorf("discard audit entry = %+v, want username=alice@pam result=success", e)
			}
		}
	}
	if discardEntries != 1 {
		t.Errorf("changeset.discard audit entries = %d, want 1", discardEntries)
	}
}

func TestService_Discard_AlreadyDiscarded_ErrIllegalTransition(t *testing.T) {
	svc := newTestService(t, nil)
	ctx := context.Background()

	c, err := svc.Create(ctx, "alice@pam", "draft", sampleOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if discardErr := svc.Discard(ctx, c.ID, "alice@pam"); discardErr != nil {
		t.Fatalf("first Discard: %v", discardErr)
	}

	err = svc.Discard(ctx, c.ID, "alice@pam")
	var illegal *ErrIllegalTransition
	if !errors.As(err, &illegal) {
		t.Fatalf("second Discard: error = %v, want *ErrIllegalTransition", err)
	}
}

func TestService_Discard_NotFound(t *testing.T) {
	svc := newTestService(t, nil)
	err := svc.Discard(context.Background(), "nope", "alice@pam")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Discard(missing) error = %v, want store.ErrNotFound", err)
	}
}
