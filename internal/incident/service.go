// SPDX-License-Identifier: Apache-2.0

// service.go owns the incident RECORD: opening, closing, reopening and
// annotating. Everything about the timeline itself lives in timeline.go,
// because the two halves have opposite properties and keeping them in one
// file blurs that: this half writes exactly two tables and reads nothing
// else; that half reads five sources and writes nothing at all.

package incident

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/backup"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// Store is the incident record's own persistence — the only writable seam
// this package holds, and it reaches exactly two tables.
type Store interface {
	Insert(ctx context.Context, i store.Incident) error
	Get(ctx context.Context, id string) (store.Incident, error)
	List(ctx context.Context) ([]store.Incident, error)
	SetStatus(ctx context.Context, id, status string, endedAt, closedAt int64) error
	InsertAnnotation(ctx context.Context, a store.IncidentAnnotation) error
	ListAnnotations(ctx context.Context, incidentID string) ([]store.IncidentAnnotation, error)
}

// FindingEventSource is the subset of *store.FindingEventRepo the timeline
// needs: the time-ranged transition history GET /history/events already
// reads. One method, and it reads.
type FindingEventSource interface {
	ListByTimeRange(ctx context.Context, from, to int64) ([]store.FindingEvent, error)
}

// AuditSource is the subset of *store.AuditRepo the timeline needs. It backs
// TWO of the five sources — changesets (the T-205 lifecycle actions plus
// `changeset.create`, i.e. "staged") and diagnosis-ladder runs (`diagnose.run`,
// which internal/api/diagnose.go has audited since T-1307) — because both are
// already recorded there and re-recording either would be the "collects data
// that is not already collected" this feature refuses.
type AuditSource interface {
	ListActionsInRange(ctx context.Context, actions []string, from, to int64) ([]store.AuditEntry, error)
}

// CaptureSource is the subset of *store.CaptureRepo the timeline needs.
//
// capture_sessions has no time-ranged query (a cluster's capture list is
// small and bounded by retention), so this seam lists and the assembly
// filters — the honest shape rather than a new index for a table with tens of
// rows.
type CaptureSource interface {
	List(ctx context.Context) ([]store.CaptureSession, error)
}

// FlowSource is the subset of *store.FlowSampleRepo the timeline needs — the
// same keyset-paginated query GET /flows serves.
type FlowSource interface {
	Query(ctx context.Context, filter store.FlowFilter, cursor string, limit int) ([]store.FlowSample, string, error)
}

// TopologyDiffService is T-2704's seam, reused verbatim rather than forked:
// one method, and it is a read (internal/api.TopologyDiffService declares the
// identical signature against the same *change.Service).
type TopologyDiffService interface {
	TopologyDiff(ctx context.Context, from, to string) (*change.TopologyDiff, error)
}

// DefaultFlowLimit bounds how many flow samples one timeline carries.
//
// A busy cluster produces thousands of flow rows a minute; a timeline is read
// by a human. 200 is the same order as DefaultBundleFindingEvents and is
// reported when it binds (StatusTruncated), never silently applied.
const DefaultFlowLimit = 200

// Config wires the service. Every source is optional: a node with no flow
// listener still gets a timeline, with `flow` reported unavailable rather
// than silently missing.
//
//nolint:govet // fieldalignment: a wiring struct grouped by meaning, read top-to-bottom at the composition root.
type Config struct {
	Store         Store
	FindingEvents FindingEventSource
	Audit         AuditSource
	Captures      CaptureSource
	Flows         FlowSource
	Diff          TopologyDiffService
	// Bundler produces the export artifact. Nil disables
	// GET /incidents/{id}/export only (ErrExportUnavailable); everything
	// else works.
	Bundler Bundler
	// ExportBase is the support-bundle configuration this node was wired
	// with. The incident document and the destination are the only fields
	// Export sets; everything else is whatever `vnproxctl support-bundle`
	// would have used, so the two artifacts describe the same install.
	ExportBase backup.BundleOptions
	// FlowLimit defaults to DefaultFlowLimit.
	FlowLimit int
	// Now is injectable for tests; defaults to time.Now.
	Now    func() time.Time
	Logger *slog.Logger
}

// Service is the incident view.
type Service struct {
	cfg Config
}

// New constructs a Service. A nil Store is a programming error rather than a
// degraded mode — there is no incident without a record of it — so callers
// that have no store simply do not mount the routes.
func New(cfg Config) *Service {
	if cfg.FlowLimit <= 0 {
		cfg.FlowLimit = DefaultFlowLimit
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{cfg: cfg}
}

func (s *Service) now() int64 { return s.cfg.Now().Unix() }

// OpenRequest is Open's input.
type OpenRequest struct {
	Title string
	Actor string
	// StartedAt is the window start; 0 means "now" (a live incident).
	StartedAt int64
	// EndedAt is the inclusive window end; 0 means "runs to now". A request
	// carrying both StartedAt and EndedAt in the past is the retroactive
	// case, and it is an ordinary request rather than a special mode.
	EndedAt int64
}

// Open records a new incident.
//
// It reads no source and starts nothing. That is acceptance criterion 2 in
// one sentence, and incident_ac_test.go asserts it against counting fakes
// rather than trusting this comment.
func (s *Service) Open(ctx context.Context, req OpenRequest) (Incident, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return Incident{}, ErrTitleRequired
	}
	now := s.now()
	startedAt := req.StartedAt
	if startedAt <= 0 {
		startedAt = now
	}
	if req.EndedAt > 0 && req.EndedAt < startedAt {
		return Incident{}, fmt.Errorf("%w: started %d, ended %d", ErrWindowInverted, startedAt, req.EndedAt)
	}

	row := store.Incident{
		ID:        store.NewULID(),
		Title:     title,
		Status:    store.IncidentStatusOpen,
		OpenedBy:  req.Actor,
		OpenedAt:  now,
		StartedAt: startedAt,
		EndedAt:   req.EndedAt,
	}
	if err := s.cfg.Store.Insert(ctx, row); err != nil {
		return Incident{}, fmt.Errorf("incident: opening %q: %w", title, err)
	}
	s.cfg.Logger.Info("incident opened",
		"incidentId", row.ID, "startedAt", row.StartedAt, "endedAt", row.EndedAt,
		"retroactive", row.OpenedAt > row.StartedAt)
	return fromStoreIncident(row), nil
}

// Get returns one incident with its annotations attached.
func (s *Service) Get(ctx context.Context, id string) (Incident, error) {
	row, err := s.cfg.Store.Get(ctx, id)
	if err != nil {
		return Incident{}, fmt.Errorf("incident: reading %s: %w", id, err)
	}
	return s.withAnnotations(ctx, row)
}

func (s *Service) withAnnotations(ctx context.Context, row store.Incident) (Incident, error) {
	out := fromStoreIncident(row)
	notes, err := s.cfg.Store.ListAnnotations(ctx, row.ID)
	if err != nil {
		return Incident{}, fmt.Errorf("incident: reading annotations of %s: %w", row.ID, err)
	}
	for _, n := range notes {
		out.Annotations = append(out.Annotations, fromStoreAnnotation(n))
	}
	return out, nil
}

// List returns every incident, most recent window first, without annotations
// (the list view shows counts, not bodies — and an annotation body is free
// text an operator typed, which is not something to fan out by default).
func (s *Service) List(ctx context.Context) ([]Incident, error) {
	rows, err := s.cfg.Store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("incident: listing: %w", err)
	}
	out := make([]Incident, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromStoreIncident(row))
	}
	return out, nil
}

// Close marks an incident closed and freezes an open-ended window at the
// close instant.
//
// It deletes nothing. There is nothing of the timeline to delete — that is
// the schema's property, not this method's discretion — so a closed incident
// answers GET /incidents/{id}/timeline exactly as it did a second earlier
// (acceptance criterion 5).
func (s *Service) Close(ctx context.Context, id string) (Incident, error) {
	row, err := s.cfg.Store.Get(ctx, id)
	if err != nil {
		return Incident{}, fmt.Errorf("incident: reading %s: %w", id, err)
	}
	if row.Status == store.IncidentStatusClosed {
		return Incident{}, ErrAlreadyClosed
	}
	now := s.now()
	endedAt := row.EndedAt
	if endedAt <= 0 {
		endedAt = now
	}
	if err := s.cfg.Store.SetStatus(ctx, id, store.IncidentStatusClosed, endedAt, now); err != nil {
		return Incident{}, fmt.Errorf("incident: closing %s: %w", id, err)
	}
	row.Status, row.EndedAt, row.ClosedAt = store.IncidentStatusClosed, endedAt, now
	s.cfg.Logger.Info("incident closed", "incidentId", id, "endedAt", endedAt)
	return s.withAnnotations(ctx, row)
}

// Reopen puts an incident back into the open state and its window back to
// "runs to now", so an investigation that turned out not to be over keeps
// accumulating events instead of needing a second incident.
//
// Every event the closed incident showed is still in the window, because the
// window start never moved and nothing was ever copied out of the sources.
func (s *Service) Reopen(ctx context.Context, id string) (Incident, error) {
	row, err := s.cfg.Store.Get(ctx, id)
	if err != nil {
		return Incident{}, fmt.Errorf("incident: reading %s: %w", id, err)
	}
	if row.Status == store.IncidentStatusOpen {
		return Incident{}, ErrAlreadyOpen
	}
	if err := s.cfg.Store.SetStatus(ctx, id, store.IncidentStatusOpen, 0, 0); err != nil {
		return Incident{}, fmt.Errorf("incident: reopening %s: %w", id, err)
	}
	row.Status, row.EndedAt, row.ClosedAt = store.IncidentStatusOpen, 0, 0
	s.cfg.Logger.Info("incident reopened", "incidentId", id)
	return s.withAnnotations(ctx, row)
}

// AnnotateRequest is Annotate's input.
type AnnotateRequest struct {
	Body   string
	Author string
	// At is when the observation is ABOUT, not when it was typed; 0 means
	// now. Back-dating is the normal case ten minutes into an incident.
	At int64
}

// Annotate records one operator observation against an incident. Permitted on
// a closed incident: a conclusion is usually written after the fact, and
// refusing it would push the one thing only a human knows out of the record.
func (s *Service) Annotate(ctx context.Context, id string, req AnnotateRequest) (Annotation, error) {
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return Annotation{}, ErrAnnotationEmpty
	}
	if _, err := s.cfg.Store.Get(ctx, id); err != nil {
		return Annotation{}, fmt.Errorf("incident: reading %s: %w", id, err)
	}
	at := req.At
	if at <= 0 {
		at = s.now()
	}
	row := store.IncidentAnnotation{
		ID: store.NewULID(), IncidentID: id, At: at, Author: req.Author, Body: body,
	}
	if err := s.cfg.Store.InsertAnnotation(ctx, row); err != nil {
		return Annotation{}, fmt.Errorf("incident: annotating %s: %w", id, err)
	}
	return fromStoreAnnotation(row), nil
}

// IsNotFound reports whether err is the store's not-found, wrapped anywhere
// in the chain — so internal/api maps a missing incident to 404 without
// importing internal/store's error just for that.
func IsNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
