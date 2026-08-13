package annotate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// ErrInvalid is returned for a rejected create. Callers map it to
// docs/api.md's `validation_failed` (internal/api does exactly that); the
// message is the human half.
var ErrInvalid = errors.New("annotate: invalid annotation")

// MaxContentLen bounds one note's text and MaxLabelLen one region's label —
// generous for free text, small enough that a pathological client cannot
// wedge an enormous blob into a set that is always listed in full.
const (
	MaxContentLen = 4000
	MaxLabelLen   = 200
)

// Note is one entity-pinned sticky note as a reader sees it: the stored row
// plus the two facts computed fresh on every read.
type Note struct {
	ID        string
	Ref       string
	Content   string
	CreatedBy string
	CreatedAt int64
	UpdatedAt int64
	// ExpiresAt is unix seconds; 0 means never.
	ExpiresAt int64
	// Expired is ExpiresAt judged against the service's clock at the
	// instant of this read — never a stored flag, never the output of a
	// background job (T-2806 AC3).
	Expired bool
	// Orphaned reports that Ref names no entity in the current inventory:
	// the annotated thing has been deleted, and this note survives it
	// (T-2806 AC2). Derived, never stored; false whenever the inventory
	// cannot answer (see the package doc's fail-safe rule).
	Orphaned bool
}

// Region is one labelled canvas rectangle as a reader sees it.
type Region struct {
	ID        string
	Label     string
	Color     string
	CreatedBy string
	X         float64
	Y         float64
	W         float64
	H         float64
	CreatedAt int64
	UpdatedAt int64
	ExpiresAt int64
	Expired   bool
}

// NoteInput is a create request for a note. CreatedBy is server-stamped by
// the caller from the authenticated session, never client-supplied.
type NoteInput struct {
	Ref       string
	Content   string
	CreatedBy string
	ExpiresAt int64
}

// RegionInput is a create request for a region.
type RegionInput struct {
	Label     string
	Color     string
	CreatedBy string
	X         float64
	Y         float64
	W         float64
	H         float64
	ExpiresAt int64
}

// NoteStore is the subset of *store.AnnotationRepo this package needs.
type NoteStore interface {
	List(ctx context.Context) ([]store.Annotation, error)
	Insert(ctx context.Context, a store.Annotation) error
	Delete(ctx context.Context, id string) error
}

// RegionStore is the subset of *store.MapRegionRepo this package needs.
type RegionStore interface {
	List(ctx context.Context) ([]store.MapRegion, error)
	Insert(ctx context.Context, m store.MapRegion) error
	Delete(ctx context.Context, id string) error
}

// EntitySource is the live inventory an annotation's ref is checked
// against — the same one-method seam internal/docexport and internal/api
// already use for the shared *inventory.Graph. Nil means "no inventory
// wired": no note is then reported orphaned.
type EntitySource interface {
	Snapshot() inventory.Snapshot
}

// Config configures a Service. Notes and Regions are required.
type Config struct {
	Notes    NoteStore
	Regions  RegionStore
	Entities EntitySource
	Now      func() time.Time
}

// Service is the read/write model over the annotation layer.
type Service struct {
	notes    NoteStore
	regions  RegionStore
	entities EntitySource
	now      func() time.Time
}

// NewService constructs a Service. Config.Notes and Config.Regions are
// required; Entities may be nil (orphan derivation is then always false),
// and Now defaults to time.Now.
func NewService(cfg Config) (*Service, error) {
	if cfg.Notes == nil {
		return nil, fmt.Errorf("annotate: Config.Notes is required")
	}
	if cfg.Regions == nil {
		return nil, fmt.Errorf("annotate: Config.Regions is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{notes: cfg.Notes, regions: cfg.Regions, entities: cfg.Entities, now: now}, nil
}

// IsExpired is the single expiry rule for the whole feature: a zero
// ExpiresAt never expires, and any other value expires once now has
// reached it. Exported so every reader (the API, the doc export, this
// package's own filters) judges expiry identically, against whatever clock
// the caller passes rather than a second one of its own.
func IsExpired(expiresAt, now int64) bool {
	return expiresAt != 0 && expiresAt <= now
}

// Notes returns every pinned note, oldest first, with Expired and Orphaned
// computed against this instant.
//
// includeExpired=false — what every DISPLAY surface asks for (the map, the
// inspector, the doc export) — drops expired notes from the result. The
// drop happens here, on the read, so a note that expired while the daemon
// was stopped is gone from the very first read after it comes back: there
// is no sweep whose having-run is a precondition for correctness. The rows
// themselves are never deleted; includeExpired=true still returns them, so
// an operator can read and unpin an expired note.
func (s *Service) Notes(ctx context.Context, includeExpired bool) ([]Note, error) {
	rows, err := s.notes.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("annotate: listing notes: %w", err)
	}
	now := s.now().Unix()
	known := s.knownRefs()

	out := make([]Note, 0, len(rows))
	for _, a := range rows {
		expired := IsExpired(a.ExpiresAt, now)
		if expired && !includeExpired {
			continue
		}
		out = append(out, Note{
			ID: a.ID, Ref: a.Ref, Content: a.Content, CreatedBy: a.CreatedBy,
			CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, ExpiresAt: a.ExpiresAt,
			Expired:  expired,
			Orphaned: known != nil && !known[a.Ref],
		})
	}
	return out, nil
}

// Regions returns every canvas region, oldest first, with Expired computed
// against this instant. Same read-time rule as Notes; a region is never
// orphaned, since it is anchored to canvas coordinates rather than to any
// entity.
func (s *Service) Regions(ctx context.Context, includeExpired bool) ([]Region, error) {
	rows, err := s.regions.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("annotate: listing regions: %w", err)
	}
	now := s.now().Unix()

	out := make([]Region, 0, len(rows))
	for _, m := range rows {
		expired := IsExpired(m.ExpiresAt, now)
		if expired && !includeExpired {
			continue
		}
		out = append(out, Region{
			ID: m.ID, Label: m.Label, Color: m.Color, CreatedBy: m.CreatedBy,
			X: m.X, Y: m.Y, W: m.W, H: m.H,
			CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, ExpiresAt: m.ExpiresAt,
			Expired: expired,
		})
	}
	return out, nil
}

// CreateNote pins a new note and returns it as a reader would see it.
func (s *Service) CreateNote(ctx context.Context, in NoteInput) (Note, error) {
	now := s.now().Unix()
	if in.Ref == "" {
		return Note{}, fmt.Errorf("%w: ref is required", ErrInvalid)
	}
	if in.Content == "" || len(in.Content) > MaxContentLen {
		return Note{}, fmt.Errorf("%w: content must be 1..%d characters", ErrInvalid, MaxContentLen)
	}
	if err := validateExpiry(in.ExpiresAt, now); err != nil {
		return Note{}, err
	}

	a := store.Annotation{
		ID: store.NewULID(), Ref: in.Ref, Content: in.Content, CreatedBy: in.CreatedBy,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: in.ExpiresAt,
	}
	if err := s.notes.Insert(ctx, a); err != nil {
		return Note{}, fmt.Errorf("annotate: creating note on %s: %w", in.Ref, err)
	}
	known := s.knownRefs()
	return Note{
		ID: a.ID, Ref: a.Ref, Content: a.Content, CreatedBy: a.CreatedBy,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, ExpiresAt: a.ExpiresAt,
		Expired:  false,
		Orphaned: known != nil && !known[a.Ref],
	}, nil
}

// DeleteNote unpins a note. Idempotent, and the only way a note ever
// leaves the store — see store.AnnotationRepo.Delete's doc comment.
func (s *Service) DeleteNote(ctx context.Context, id string) error {
	if err := s.notes.Delete(ctx, id); err != nil {
		return fmt.Errorf("annotate: deleting note %s: %w", id, err)
	}
	return nil
}

// CreateRegion draws a new labelled region on the shared canvas.
func (s *Service) CreateRegion(ctx context.Context, in RegionInput) (Region, error) {
	now := s.now().Unix()
	if in.Label == "" || len(in.Label) > MaxLabelLen {
		return Region{}, fmt.Errorf("%w: label must be 1..%d characters", ErrInvalid, MaxLabelLen)
	}
	if in.W <= 0 || in.H <= 0 {
		return Region{}, fmt.Errorf("%w: w and h must be positive", ErrInvalid)
	}
	if err := validateExpiry(in.ExpiresAt, now); err != nil {
		return Region{}, err
	}

	m := store.MapRegion{
		ID: store.NewULID(), Label: in.Label, Color: in.Color, CreatedBy: in.CreatedBy,
		X: in.X, Y: in.Y, W: in.W, H: in.H,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: in.ExpiresAt,
	}
	if err := s.regions.Insert(ctx, m); err != nil {
		return Region{}, fmt.Errorf("annotate: creating region %s: %w", m.ID, err)
	}
	return Region{
		ID: m.ID, Label: m.Label, Color: m.Color, CreatedBy: m.CreatedBy,
		X: m.X, Y: m.Y, W: m.W, H: m.H,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, ExpiresAt: m.ExpiresAt,
	}, nil
}

// DeleteRegion removes a region. Idempotent.
func (s *Service) DeleteRegion(ctx context.Context, id string) error {
	if err := s.regions.Delete(ctx, id); err != nil {
		return fmt.Errorf("annotate: deleting region %s: %w", id, err)
	}
	return nil
}

// knownRefs is the set of entity ref strings the current inventory knows
// about, or nil when the inventory cannot answer — no source wired, or a
// snapshot with no entities in it at all (a daemon in degraded mode, or one
// that has not finished its first collection). nil means "do not judge",
// which is what makes orphan derivation fail safe: see the package doc.
func (s *Service) knownRefs() map[string]bool {
	if s.entities == nil {
		return nil
	}
	snap := s.entities.Snapshot()
	all := snap.All()
	if len(all) == 0 {
		return nil
	}
	refs := make(map[string]bool, len(all))
	for _, e := range all {
		refs[e.GetRef().String()] = true
	}
	return refs
}

func validateExpiry(expiresAt, now int64) error {
	if expiresAt < 0 {
		return fmt.Errorf("%w: expiresAt must not be negative", ErrInvalid)
	}
	if expiresAt != 0 && expiresAt <= now {
		return fmt.Errorf("%w: expiresAt must be in the future", ErrInvalid)
	}
	return nil
}
