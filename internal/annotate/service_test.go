package annotate

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// --- test doubles ---------------------------------------------------------

// memNotes is an in-memory NoteStore. It records every Delete it is asked
// to perform, so a test can assert that a code path did NOT delete rather
// than merely that rows remain.
type memNotes struct {
	rows    map[string]store.Annotation
	listErr error
	deleted []string
}

func newMemNotes() *memNotes { return &memNotes{rows: map[string]store.Annotation{}} }

func (m *memNotes) List(context.Context) ([]store.Annotation, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	out := make([]store.Annotation, 0, len(m.rows))
	for _, a := range m.rows {
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

func (m *memNotes) Insert(_ context.Context, a store.Annotation) error {
	m.rows[a.ID] = a
	return nil
}

func (m *memNotes) Delete(_ context.Context, id string) error {
	m.deleted = append(m.deleted, id)
	delete(m.rows, id)
	return nil
}

type memRegions struct {
	rows map[string]store.MapRegion
}

func newMemRegions() *memRegions { return &memRegions{rows: map[string]store.MapRegion{}} }

func (m *memRegions) List(context.Context) ([]store.MapRegion, error) {
	out := make([]store.MapRegion, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (m *memRegions) Insert(_ context.Context, r store.MapRegion) error {
	m.rows[r.ID] = r
	return nil
}

func (m *memRegions) Delete(_ context.Context, id string) error {
	delete(m.rows, id)
	return nil
}

// clock is a test clock the test itself advances. Every expiry assertion in
// this file steps it explicitly — nothing here sleeps or waits, so these
// tests cannot flake under CPU pressure.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

// openTestStore opens a real, migrated SQLite store — used only where the
// point of the test is the actual schema (the regions-vs-layouts
// independence test below), not the service logic.
func openTestStore(t *testing.T) *store.DB {
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
	return db
}

func newTestService(t *testing.T, notes NoteStore, regions RegionStore, entities EntitySource, now func() time.Time) *Service {
	t.Helper()
	svc, err := NewService(Config{Notes: notes, Regions: regions, Entities: entities, Now: now})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// bridgeGraph returns a graph holding exactly the named bridges on pve1.
func bridgeGraph(t *testing.T, names ...string) *inventory.Graph {
	t.Helper()
	g := inventory.NewGraph()
	applyBridges(g, names...)
	return g
}

func applyBridges(g *inventory.Graph, names ...string) {
	ents := make([]inventory.Entity, 0, len(names))
	for _, n := range names {
		ref := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: n}
		ents = append(ents, &inventory.Bridge{Ref: ref, Name: n})
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, ents)
}

func bridgeRef(name string) string {
	return inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: name}.String()
}

// --- AC1: an annotation survives re-collection ----------------------------

// TestNotes_SurvivesEntityRecollection is T-2806 AC1. The graph is polled
// twice (a re-collection: new snapshot, new sequence number, entities
// rebuilt from scratch), and the note must come back identical and NOT
// orphaned — annotations are keyed by the entity's stable Ref string, and
// no collector touches the annotations table.
func TestNotes_SurvivesEntityRecollection(t *testing.T) {
	ctx := context.Background()
	notes := newMemNotes()
	g := bridgeGraph(t, "vmbr0")
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	svc := newTestService(t, notes, newMemRegions(), g, c.now)

	created, err := svc.CreateNote(ctx, NoteInput{
		Ref: bridgeRef("vmbr0"), Content: "temporary uplink until the switch swap", CreatedBy: "alice@pve",
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if created.Orphaned {
		t.Fatalf("a note on a live entity reads as orphaned: %+v", created)
	}

	// Re-collect: the same entity observed again, a whole new snapshot.
	applyBridges(g, "vmbr0")

	got, err := svc.Notes(ctx, false)
	if err != nil {
		t.Fatalf("Notes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Notes() = %+v, want the one note to survive re-collection", got)
	}
	if got[0].ID != created.ID || got[0].Content != created.Content || got[0].CreatedBy != "alice@pve" {
		t.Errorf("Notes()[0] = %+v, want the note unchanged across re-collection", got[0])
	}
	if got[0].Orphaned {
		t.Error("note reads as orphaned after its entity was re-collected — the ref anchor broke")
	}
}

// --- AC2: a note on a deleted entity is retained and marked orphaned ------

// TestNotes_DeletedEntityRetainsNoteAsOrphaned is T-2806 AC2, the criterion
// the whole feature exists for: the note may be the only record of WHY the
// entity was removed, so deleting the entity must never delete the note.
//
// The assertion is deliberately in three parts — the note is still returned,
// it is flagged orphaned, and its text is intact — plus a fourth that no
// Delete was issued at all, because "the row happens to still be there"
// would not catch a path that deletes and re-creates.
func TestNotes_DeletedEntityRetainsNoteAsOrphaned(t *testing.T) {
	ctx := context.Background()
	notes := newMemNotes()
	g := bridgeGraph(t, "vmbr0", "vmbr9")
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	svc := newTestService(t, notes, newMemRegions(), g, c.now)

	const why = "removed because the vendor switch could not trunk VLAN 40"
	if _, err := svc.CreateNote(ctx, NoteInput{Ref: bridgeRef("vmbr9"), Content: why, CreatedBy: "alice@pve"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := svc.CreateNote(ctx, NoteInput{Ref: bridgeRef("vmbr0"), Content: "still here", CreatedBy: "bob@pve"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	// The entity is deleted from the cluster: the next poll simply omits it.
	applyBridges(g, "vmbr0")
	if _, ok := g.Snapshot().Get(inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"}); ok {
		t.Fatal("fixture bug: vmbr9 is still in the inventory after being omitted from the poll")
	}

	got, err := svc.Notes(ctx, false)
	if err != nil {
		t.Fatalf("Notes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Notes() = %+v, want BOTH notes — the orphan must not be dropped", got)
	}

	byRef := map[string]Note{}
	for _, n := range got {
		byRef[n.Ref] = n
	}
	orphan, ok := byRef[bridgeRef("vmbr9")]
	if !ok {
		t.Fatalf("the note on the deleted entity is gone: %+v", got)
	}
	if !orphan.Orphaned {
		t.Error("note on a deleted entity is not flagged orphaned")
	}
	if orphan.Content != why {
		t.Errorf("orphan note content = %q, want it preserved verbatim", orphan.Content)
	}
	if live := byRef[bridgeRef("vmbr0")]; live.Orphaned {
		t.Error("note on a still-present entity is wrongly flagged orphaned")
	}
	if len(notes.deleted) != 0 {
		t.Errorf("Delete was called %v — nothing on the read path may remove an annotation", notes.deleted)
	}
}

// TestNotes_OrphanDerivationFailsSafe: with no inventory to compare
// against, nothing is orphaned. Marking every note "the entity is gone"
// because vnprox currently cannot see any entity would be the most
// alarming possible way to be wrong.
func TestNotes_OrphanDerivationFailsSafe(t *testing.T) {
	ctx := context.Background()
	c := &clock{t: time.Unix(1_700_000_000, 0)}

	for _, tc := range []struct {
		entities EntitySource
		name     string
	}{
		{name: "no inventory wired", entities: nil},
		{name: "inventory has not collected yet", entities: inventory.NewGraph()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t, newMemNotes(), newMemRegions(), tc.entities, c.now)
			if _, err := svc.CreateNote(ctx, NoteInput{Ref: bridgeRef("vmbr0"), Content: "note", CreatedBy: "alice@pve"}); err != nil {
				t.Fatalf("CreateNote: %v", err)
			}
			got, err := svc.Notes(ctx, false)
			if err != nil {
				t.Fatalf("Notes: %v", err)
			}
			if len(got) != 1 || got[0].Orphaned {
				t.Errorf("Notes() = %+v, want one note NOT flagged orphaned", got)
			}
		})
	}
}

// --- AC3: expiry is computed at read time ---------------------------------

// TestNotes_ExpiryIsComputedAtReadTime is T-2806 AC3. The clock is stepped
// by the test, never waited on: the note is live before its expiry and gone
// from the display read after it, with no sweep, no timer and no background
// goroutine anywhere in the path. That is exactly the property the card
// asks for — a daemon that was STOPPED when the expiry passed comes back
// and still does not display the note, because the judgement is made on the
// read rather than by something that had to be running.
func TestNotes_ExpiryIsComputedAtReadTime(t *testing.T) {
	ctx := context.Background()
	notes := newMemNotes()
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	svc := newTestService(t, notes, newMemRegions(), bridgeGraph(t, "vmbr0"), c.now)

	expiresAt := c.now().Unix() + 3600
	if _, err := svc.CreateNote(ctx, NoteInput{
		Ref: bridgeRef("vmbr0"), Content: "temporary", CreatedBy: "alice@pve", ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := svc.CreateNote(ctx, NoteInput{
		Ref: bridgeRef("vmbr0"), Content: "permanent", CreatedBy: "alice@pve",
	}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	before, err := svc.Notes(ctx, false)
	if err != nil {
		t.Fatalf("Notes: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("before expiry: Notes() = %+v, want both notes", before)
	}
	for _, n := range before {
		if n.Expired {
			t.Errorf("before expiry: %q reads as expired", n.Content)
		}
	}

	// The daemon is stopped for a week. Nothing runs. Time passes.
	c.add(7 * 24 * time.Hour)

	live, err := svc.Notes(ctx, false)
	if err != nil {
		t.Fatalf("Notes: %v", err)
	}
	if len(live) != 1 || live[0].Content != "permanent" {
		t.Fatalf("after expiry: Notes() = %+v, want only the permanent note", live)
	}

	all, err := svc.Notes(ctx, true)
	if err != nil {
		t.Fatalf("Notes(includeExpired): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("includeExpired: Notes() = %+v, want both rows still readable", all)
	}
	var sawExpired bool
	for _, n := range all {
		if n.Content == "temporary" {
			sawExpired = n.Expired
		}
	}
	if !sawExpired {
		t.Error("includeExpired: the expired note is not flagged Expired")
	}
	if len(notes.deleted) != 0 {
		t.Errorf("Delete was called %v — expiry hides a note, it must never delete one", notes.deleted)
	}
}

// TestRegions_ExpiryIsComputedAtReadTime applies the same read-time rule to
// canvas regions, on the same stepped clock.
func TestRegions_ExpiryIsComputedAtReadTime(t *testing.T) {
	ctx := context.Background()
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	regions := newMemRegions()
	svc := newTestService(t, newMemNotes(), regions, nil, c.now)

	if _, err := svc.CreateRegion(ctx, RegionInput{
		Label: "maintenance window", CreatedBy: "alice@pve", X: 1, Y: 2, W: 10, H: 20,
		ExpiresAt: c.now().Unix() + 60,
	}); err != nil {
		t.Fatalf("CreateRegion: %v", err)
	}

	if got, err := svc.Regions(ctx, false); err != nil || len(got) != 1 {
		t.Fatalf("Regions() = %+v, %v; want the live region", got, err)
	}

	c.add(time.Hour)

	got, err := svc.Regions(ctx, false)
	if err != nil {
		t.Fatalf("Regions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("after expiry: Regions() = %+v, want none displayed", got)
	}
	all, err := svc.Regions(ctx, true)
	if err != nil {
		t.Fatalf("Regions(includeExpired): %v", err)
	}
	if len(all) != 1 || !all[0].Expired {
		t.Fatalf("includeExpired: Regions() = %+v, want the row retained and flagged expired", all)
	}
	if len(regions.rows) != 1 {
		t.Errorf("stored regions = %d, want 1 — an expired region is hidden, never deleted", len(regions.rows))
	}
}

// TestIsExpired is the one expiry rule, table-driven, including the 0
// sentinel and the exact boundary second.
func TestIsExpired(t *testing.T) {
	for _, tc := range []struct {
		name      string
		expiresAt int64
		now       int64
		want      bool
	}{
		{name: "zero never expires", expiresAt: 0, now: 1 << 40, want: false},
		{name: "future", expiresAt: 200, now: 100, want: false},
		{name: "exactly at the boundary", expiresAt: 100, now: 100, want: true},
		{name: "past", expiresAt: 100, now: 101, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsExpired(tc.expiresAt, tc.now); got != tc.want {
				t.Errorf("IsExpired(%d, %d) = %v, want %v", tc.expiresAt, tc.now, got, tc.want)
			}
		})
	}
}

// --- AC5 (storage half): regions are independent of layouts ---------------

// TestRegions_AreIndependentOfLayouts is the storage half of T-2806 AC5.
// The annotation layer's own store is the only thing that can remove a
// region; a layout write is a different table entirely, which is why
// "regions persist across layout changes and view switches" is a property
// of the schema rather than a promise about client behaviour. The frontend
// half (a view switch re-renders them) is asserted in
// web/src/topology/AnnotationLayer.test.tsx.
func TestRegions_AreIndependentOfLayouts(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t)
	svc := newTestService(t, store.NewAnnotationRepo(db), store.NewMapRegionRepo(db), nil, nil)
	layouts := store.NewLayoutRepo(db)

	created, err := svc.CreateRegion(ctx, RegionInput{
		Label: "vendor-managed", CreatedBy: "alice@pve", X: 5, Y: 6, W: 100, H: 50,
	})
	if err != nil {
		t.Fatalf("CreateRegion: %v", err)
	}

	// Two layout writes by two different users — the canvas auto-save that
	// happens every time anyone drags a node.
	for _, username := range []string{"alice@pve", "bob@pve"} {
		if putErr := layouts.Put(ctx, store.Layout{
			Username: username, Name: "topology", LayoutJSON: `{"nodes":[]}`, UpdatedAt: 1,
		}); putErr != nil {
			t.Fatalf("layouts.Put(%s): %v", username, putErr)
		}
	}

	got, err := svc.Regions(ctx, false)
	if err != nil {
		t.Fatalf("Regions: %v", err)
	}
	if len(got) != 1 || got[0].ID != created.ID || got[0].Label != "vendor-managed" {
		t.Fatalf("Regions() = %+v, want the region untouched by layout writes", got)
	}
	if got[0].X != 5 || got[0].Y != 6 || got[0].W != 100 || got[0].H != 50 {
		t.Errorf("Regions()[0] geometry = (%v, %v, %v, %v), want (5, 6, 100, 50)", got[0].X, got[0].Y, got[0].W, got[0].H)
	}
}

// --- validation -----------------------------------------------------------

func TestCreate_Validation(t *testing.T) {
	ctx := context.Background()
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	svc := newTestService(t, newMemNotes(), newMemRegions(), nil, c.now)

	longContent := make([]byte, MaxContentLen+1)
	for i := range longContent {
		longContent[i] = 'x'
	}
	longLabel := make([]byte, MaxLabelLen+1)
	for i := range longLabel {
		longLabel[i] = 'x'
	}

	noteCases := []struct {
		name string
		in   NoteInput
	}{
		{name: "no ref", in: NoteInput{Content: "text"}},
		{name: "no content", in: NoteInput{Ref: "bridge:pve1:vmbr0"}},
		{name: "content too long", in: NoteInput{Ref: "bridge:pve1:vmbr0", Content: string(longContent)}},
		{name: "expiry in the past", in: NoteInput{Ref: "bridge:pve1:vmbr0", Content: "t", ExpiresAt: c.now().Unix() - 1}},
		{name: "negative expiry", in: NoteInput{Ref: "bridge:pve1:vmbr0", Content: "t", ExpiresAt: -5}},
	}
	for _, tc := range noteCases {
		t.Run("note/"+tc.name, func(t *testing.T) {
			if _, err := svc.CreateNote(ctx, tc.in); !errors.Is(err, ErrInvalid) {
				t.Errorf("CreateNote(%+v) error = %v, want ErrInvalid", tc.in, err)
			}
		})
	}

	regionCases := []struct {
		name string
		in   RegionInput
	}{
		{name: "no label", in: RegionInput{W: 1, H: 1}},
		{name: "label too long", in: RegionInput{Label: string(longLabel), W: 1, H: 1}},
		{name: "zero width", in: RegionInput{Label: "l", W: 0, H: 1}},
		{name: "negative height", in: RegionInput{Label: "l", W: 1, H: -1}},
		{name: "expiry in the past", in: RegionInput{Label: "l", W: 1, H: 1, ExpiresAt: c.now().Unix() - 1}},
	}
	for _, tc := range regionCases {
		t.Run("region/"+tc.name, func(t *testing.T) {
			if _, err := svc.CreateRegion(ctx, tc.in); !errors.Is(err, ErrInvalid) {
				t.Errorf("CreateRegion(%+v) error = %v, want ErrInvalid", tc.in, err)
			}
		})
	}
}

func TestNewService_RequiresStores(t *testing.T) {
	if _, err := NewService(Config{Regions: newMemRegions()}); err == nil {
		t.Error("NewService with no note store: want an error")
	}
	if _, err := NewService(Config{Notes: newMemNotes()}); err == nil {
		t.Error("NewService with no region store: want an error")
	}
}

func TestNotes_ListErrorIsWrapped(t *testing.T) {
	notes := newMemNotes()
	notes.listErr = errors.New("disk on fire")
	svc := newTestService(t, notes, newMemRegions(), nil, nil)
	if _, err := svc.Notes(context.Background(), false); err == nil || !errors.Is(err, notes.listErr) {
		t.Errorf("Notes() error = %v, want it to wrap the store error", err)
	}
}
