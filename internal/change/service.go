package change

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// topicChangesets is the WS subscription topic name clients use for
// changeset.status events (docs/api.md's WebSocket section:
// `{"subscribe": ["topology", "changesets", "metrics:<ref>", "tasks"]}`).
const topicChangesets = "changesets"

// Broadcaster is the seam this package uses to fan out changeset.status WS
// events without depending on internal/topology's Hub type directly —
// mirrors the seam pattern internal/api's AuthService/TopologyService/
// LayoutStore interfaces already use for this codebase's cross-package
// dependencies. topology.Service.Broadcast (see that package's hub.go)
// satisfies this: docs/api.md documents a single shared /api/ws connection
// multiplexing "topology", "changesets", "metrics:<ref>", and "tasks"
// topics alike, so this package reuses T-106's hub rather than standing up
// a second WebSocket endpoint.
type Broadcaster interface {
	Broadcast(topic string, payload []byte)
}

// Config configures a Service. Changesets and Audit are required; WS is
// optional (nil disables WS broadcasting, e.g. in tests that don't need
// it) and Now/Logger default sensibly when zero, mirroring
// internal/auth.Config's same conventions.
type Config struct {
	Changesets *store.ChangesetRepo
	Audit      *store.AuditRepo
	WS         Broadcaster
	Now        func() time.Time
	Logger     *slog.Logger
}

// Service implements T-201's changeset draft CRUD: store-backed
// persistence on top of T-003's *store.ChangesetRepo, the status state
// machine (changeset.go), WS `changeset.status` broadcasts on every status
// transition, and audit entries on create/discard. Validate/Diff/Apply/
// Confirm/Rollback are later tasks' (T-202/T-205) responsibility — see
// doc.go.
type Service struct {
	repo  *store.ChangesetRepo
	audit *store.AuditRepo
	ws    Broadcaster
	now   func() time.Time
	log   *slog.Logger
}

// NewService constructs a Service. Config.Changesets and Config.Audit are
// required.
func NewService(cfg Config) (*Service, error) {
	if cfg.Changesets == nil {
		return nil, fmt.Errorf("change: Config.Changesets is required")
	}
	if cfg.Audit == nil {
		return nil, fmt.Errorf("change: Config.Audit is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: cfg.Changesets, audit: cfg.Audit, ws: cfg.WS, now: now, log: logger}, nil
}

// List returns changesets ordered newest-first, optionally filtered to a
// single status (an empty string lists all).
func (s *Service) List(ctx context.Context, status string) ([]Changeset, error) {
	rows, err := s.repo.List(ctx, status)
	if err != nil {
		return nil, fmt.Errorf("change: listing changesets: %w", err)
	}
	out := make([]Changeset, 0, len(rows))
	for _, row := range rows {
		c, err := fromStoreRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Get returns the changeset with the given id. The returned error wraps
// store.ErrNotFound (checkable with errors.Is) if no such changeset
// exists.
func (s *Service) Get(ctx context.Context, id string) (Changeset, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return Changeset{}, fmt.Errorf("change: getting changeset %s: %w", id, err)
	}
	return fromStoreRow(row)
}

// Create persists a new draft changeset authored by author, audits the
// creation, and broadcasts its initial `changeset.status` (draft) event.
// A nil ops is stored as an empty list, never a JSON null.
func (s *Service) Create(ctx context.Context, author, title string, ops []Op) (Changeset, error) {
	if ops == nil {
		ops = []Op{}
	}
	nowUnix := s.now().Unix()
	c := Changeset{
		ID: store.NewULID(), Title: title, Author: author, Status: StatusDraft,
		Ops: ops, CreatedAt: nowUnix, UpdatedAt: nowUnix,
	}
	row, err := toStoreRow(c)
	if err != nil {
		return Changeset{}, err
	}
	if err := s.repo.Insert(ctx, row); err != nil {
		return Changeset{}, fmt.Errorf("change: creating changeset %s: %w", c.ID, err)
	}
	s.appendAudit(ctx, author, "changeset.create", "success", c.ID, map[string]any{"title": title, "opCount": len(ops)})
	s.broadcastStatus(c)
	return c, nil
}

// UpdateDraft replaces a draft or validated changeset's ops (docs/api.md:
// "PUT /changesets/{id} — replace ops on a draft (revalidates)"). Editing a
// validated changeset invalidates it back to draft (its findings, computed
// against the old ops, no longer apply — T-202 repopulates them on the
// next validate). It returns *ErrIllegalTransition if the changeset is not
// currently editable (Changeset.Editable).
func (s *Service) UpdateDraft(ctx context.Context, id, author string, title *string, ops []Op) (Changeset, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return Changeset{}, err
	}
	if !c.Editable() {
		return Changeset{}, &ErrIllegalTransition{From: c.Status, To: StatusDraft}
	}

	prevStatus := c.Status
	if c.Status == StatusValidated {
		if transErr := c.Transition(StatusDraft, s.now().Unix()); transErr != nil {
			return Changeset{}, transErr
		}
	}

	if ops == nil {
		ops = []Op{}
	}
	c.Ops = ops
	c.Findings = nil // stale as of this edit; T-202 repopulates on next validate
	if title != nil {
		c.Title = *title
	}
	c.UpdatedAt = s.now().Unix()

	row, err := toStoreRow(c)
	if err != nil {
		return Changeset{}, err
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return Changeset{}, fmt.Errorf("change: updating changeset %s: %w", id, err)
	}
	if prevStatus != c.Status {
		s.broadcastStatus(c)
	}
	return c, nil
}

// Discard transitions a draft or validated changeset to StatusDiscarded
// (docs/api.md: "DELETE /changesets/{id} — discard draft"), audits it, and
// broadcasts the resulting `changeset.status` event. It returns
// *ErrIllegalTransition for a changeset that is no longer a draft (already
// applying, or a terminal historical record).
func (s *Service) Discard(ctx context.Context, id, author string) error {
	c, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if transErr := c.Transition(StatusDiscarded, s.now().Unix()); transErr != nil {
		return transErr
	}
	row, err := toStoreRow(c)
	if err != nil {
		return err
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return fmt.Errorf("change: discarding changeset %s: %w", id, err)
	}
	s.appendAudit(ctx, author, "changeset.discard", "success", id, nil)
	s.broadcastStatus(c)
	return nil
}

func (s *Service) appendAudit(ctx context.Context, username, action, result, changesetID string, detail map[string]any) {
	var detailJSON sql.NullString
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			detailJSON = sql.NullString{String: string(b), Valid: true}
		}
	}
	entry := store.AuditEntry{
		At:          s.now().Unix(),
		Username:    username,
		Action:      action,
		Result:      result,
		ChangesetID: sql.NullString{String: changesetID, Valid: changesetID != ""},
		DetailJSON:  detailJSON,
	}
	if _, err := s.audit.Append(ctx, entry); err != nil {
		s.log.Error("change: appending audit entry", "action", action, "changeset_id", changesetID, "error", err)
	}
}

// statusEvent is the wire shape of a changeset.status push, per
// docs/api.md's WebSocket section: `{id, status, confirmDeadline?}`, plus
// the flat "event" field every hub-broadcast message in this codebase
// carries (see internal/topology/hub.go's deltaEvent).
type statusEvent struct {
	ConfirmDeadline *int64 `json:"confirmDeadline,omitempty"`
	Event           string `json:"event"`
	ID              string `json:"id"`
	Status          string `json:"status"`
}

func (s *Service) broadcastStatus(c Changeset) {
	if s.ws == nil {
		return
	}
	evt := statusEvent{Event: "changeset.status", ID: c.ID, Status: string(c.Status), ConfirmDeadline: c.ConfirmDeadline}
	data, err := json.Marshal(evt)
	if err != nil {
		s.log.Error("change: marshaling changeset.status event", "error", err)
		return
	}
	s.ws.Broadcast(topicChangesets, data)
}

// toStoreRow converts the typed aggregate to store.ChangesetRepo's
// flat/JSON-string row shape.
func toStoreRow(c Changeset) (store.Changeset, error) {
	opsJSON, err := json.Marshal(c.Ops)
	if err != nil {
		return store.Changeset{}, fmt.Errorf("change: marshaling ops for changeset %s: %w", c.ID, err)
	}
	row := store.Changeset{
		ID: c.ID, Title: c.Title, Author: c.Author, Status: string(c.Status),
		OpsJSON: string(opsJSON), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.Findings != nil {
		findingsJSON, err := json.Marshal(c.Findings)
		if err != nil {
			return store.Changeset{}, fmt.Errorf("change: marshaling findings for changeset %s: %w", c.ID, err)
		}
		row.FindingsJSON = sql.NullString{String: string(findingsJSON), Valid: true}
	}
	if len(c.Plan) > 0 {
		row.PlanJSON = sql.NullString{String: string(c.Plan), Valid: true}
	}
	if len(c.ApplyLog) > 0 {
		row.ApplyLogJSON = sql.NullString{String: string(c.ApplyLog), Valid: true}
	}
	if c.ConfirmDeadline != nil {
		row.ConfirmDeadline = sql.NullInt64{Int64: *c.ConfirmDeadline, Valid: true}
	}
	return row, nil
}

// fromStoreRow is toStoreRow's inverse.
func fromStoreRow(row store.Changeset) (Changeset, error) {
	var ops []Op
	if err := json.Unmarshal([]byte(row.OpsJSON), &ops); err != nil {
		return Changeset{}, fmt.Errorf("change: decoding stored ops for changeset %s: %w", row.ID, err)
	}
	c := Changeset{
		ID: row.ID, Title: row.Title, Author: row.Author, Status: Status(row.Status),
		Ops: ops, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.FindingsJSON.Valid {
		if err := json.Unmarshal([]byte(row.FindingsJSON.String), &c.Findings); err != nil {
			return Changeset{}, fmt.Errorf("change: decoding stored findings for changeset %s: %w", row.ID, err)
		}
	}
	if row.PlanJSON.Valid {
		c.Plan = json.RawMessage(row.PlanJSON.String)
	}
	if row.ApplyLogJSON.Valid {
		c.ApplyLog = json.RawMessage(row.ApplyLogJSON.String)
	}
	if row.ConfirmDeadline.Valid {
		d := row.ConfirmDeadline.Int64
		c.ConfirmDeadline = &d
	}
	return c, nil
}
