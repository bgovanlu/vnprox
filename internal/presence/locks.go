// SPDX-License-Identifier: Apache-2.0

package presence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// AuditActionLockOverride is the audit action one deliberate takeover of
// another operator's advisory lock is recorded under. It gets its own
// action — not a result value on `changeset.create`/`changeset.update` —
// for the same reason T-2604's break-glass does: an auditor filtering for
// overrides must find them without knowing which staging results imply one.
const AuditActionLockOverride = "changeset.lock_override"

// DefaultLockTTL is how long an advisory lock survives with nothing keeping
// it alive. It is deliberately short relative to a working session: the lock
// exists to stop two operators colliding inside one editing window, not to
// reserve an entity for an afternoon. The dropped-connection release
// (ConnClosed) is the normal way a lock ends; this is the backstop for a
// principal that never held a WebSocket connection at all — an API-token
// caller, or a browser that staged and then crashed before its socket even
// opened.
const DefaultLockTTL = 15 * time.Minute

// Lock is one advisory lock as this package reports it.
type Lock struct {
	Ref         string `json:"ref"`
	ChangesetID string `json:"changesetId"`
	// Holder is the username the lock is attributed to. Every read surface
	// that renders it decides for itself whether the caller may see it —
	// this package always populates it, because the staging warning is
	// useless without a name and the audit record is meaningless without
	// one.
	Holder     string `json:"holder"`
	SessionID  string `json:"-"`
	AcquiredAt int64  `json:"acquiredAt"`
	ExpiresAt  int64  `json:"expiresAt"`
}

// Principal is who is taking or holding a lock: a username for display and
// attribution, and the session id the lock's lifetime is tied to. An empty
// SessionID is legal and means "not bound to a live connection" — such a
// lock can only ever be freed by expiry or by discarding its draft.
type Principal struct {
	Username  string
	SessionID string
}

// StageResult is what one staging attempt did to the lock table.
//
// Nothing in it is a refusal. Conflicts names the entities this staging
// attempt did NOT take because someone else holds them — the staging itself
// succeeded regardless, which is what "advisory" means.
type StageResult struct {
	// Acquired names the refs this principal now holds (freshly taken, or
	// already theirs and renewed).
	Acquired []string
	// Conflicts are the unexpired locks held by SOMEONE ELSE that this
	// attempt left alone. This is the warning: each entry names the holder,
	// their draft, and when their claim expires.
	Conflicts []Lock
	// Overridden are the locks this attempt took over from someone else
	// because the caller explicitly asked to. Every entry here has a
	// corresponding `changeset.lock_override` audit row.
	Overridden []Lock
}

// Warned reports whether this staging attempt has anything to tell the
// operator: someone else's claim it stepped around, or one it took over.
func (r StageResult) Warned() bool { return len(r.Conflicts) > 0 || len(r.Overridden) > 0 }

// AuditAppender is the audit-log seam this package needs.
// *store.AuditRepo satisfies it.
type AuditAppender interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

// Broadcaster is the WS fan-out seam — the same one-method shape
// internal/change.Service uses, satisfied by internal/topology.Service. This
// package never opens a channel of its own: presence rides the existing
// event stream (see doc.go).
type Broadcaster interface {
	Broadcast(topic string, payload []byte)
}

// Config configures a Service. Locks is required; everything else is
// optional and degrades to a documented no-op.
type Config struct {
	Locks  *store.EntityLockRepo
	Audit  AuditAppender
	WS     Broadcaster
	Now    func() time.Time
	Logger *slog.Logger
	// TTL is the advisory lock lifetime; zero means DefaultLockTTL.
	TTL time.Duration
}

// Service owns the advisory lock table and the derived presence view.
type Service struct {
	locks *store.EntityLockRepo
	audit AuditAppender
	ws    Broadcaster
	now   func() time.Time
	log   *slog.Logger

	// conns/sessions is the presence registry: live WS connections and their
	// declared scopes, plus the per-session connection count that decides
	// when a session's locks are released. Never persisted — see doc.go.
	conns    map[string]*connState
	sessions map[string]int

	ttl time.Duration
	mu  sync.Mutex
}

// NewService constructs a Service. Config.Locks is required.
func NewService(cfg Config) (*Service, error) {
	if cfg.Locks == nil {
		return nil, fmt.Errorf("presence: Config.Locks is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultLockTTL
	}
	return &Service{
		locks:    cfg.Locks,
		audit:    cfg.Audit,
		ws:       cfg.WS,
		now:      now,
		log:      log,
		ttl:      ttl,
		conns:    map[string]*connState{},
		sessions: map[string]int{},
	}, nil
}

// Stage records p's claim on refs for changesetID and reports what it found.
//
// It never fails a staging attempt. For each ref:
//
//   - free, expired, or already p's own — p takes (or renews) it;
//   - held by someone else and override is false — left alone, reported in
//     Conflicts. The draft is staged either way; this is a warning;
//   - held by someone else and override is true — taken over, reported in
//     Overridden, and audited as `changeset.lock_override` naming the
//     previous holder.
//
// A storage error is returned, but callers are expected to treat it as
// non-fatal to the staging itself (internal/api logs and continues): a
// changeset that could not be advisory-locked is still a perfectly valid
// changeset, and refusing to stage it would turn an advisory mechanism into
// a mandatory one through the back door.
func (s *Service) Stage(ctx context.Context, changesetID string, refs []string, p Principal, override bool) (StageResult, error) {
	var res StageResult
	if changesetID == "" || len(refs) == 0 {
		return res, nil
	}
	now := s.now().Unix()
	expires := s.now().Add(s.ttl).Unix()

	for _, ref := range dedupe(refs) {
		existing, err := s.locks.Get(ctx, ref)
		switch {
		case err == nil:
			// Someone's row is on file. Expired, or ours, means it is ours to
			// take. Expiry is judged here against the injected clock, never in
			// SQL, so one clock decides it everywhere.
			held := existing.ExpiresAt > now
			mine := existing.SessionID == p.SessionID && existing.Holder == p.Username
			if held && !mine {
				prev := toLock(existing)
				if !override {
					res.Conflicts = append(res.Conflicts, prev)
					continue
				}
				res.Overridden = append(res.Overridden, prev)
				s.auditOverride(ctx, p, changesetID, prev)
			}
		case isNotFound(err):
			// free
		default:
			return res, fmt.Errorf("presence: reading lock on %s: %w", ref, err)
		}

		if err := s.locks.Upsert(ctx, store.EntityLock{
			Ref:         ref,
			ChangesetID: changesetID,
			Holder:      p.Username,
			SessionID:   p.SessionID,
			AcquiredAt:  now,
			ExpiresAt:   expires,
		}); err != nil {
			return res, fmt.Errorf("presence: taking lock on %s: %w", ref, err)
		}
		res.Acquired = append(res.Acquired, ref)
	}
	return res, nil
}

// Locks returns every currently-held lock, expired ones excluded, ordered by
// ref. It sweeps expired rows on the way through: expiry is already decided
// at read time, so the delete only keeps the table bounded.
func (s *Service) Locks(ctx context.Context) ([]Lock, error) {
	now := s.now().Unix()
	rows, err := s.locks.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("presence: listing locks: %w", err)
	}
	out := make([]Lock, 0, len(rows))
	expired := false
	for _, row := range rows {
		if row.ExpiresAt <= now {
			expired = true
			continue
		}
		out = append(out, toLock(row))
	}
	if expired {
		if _, delErr := s.locks.DeleteExpired(ctx, now); delErr != nil {
			s.log.Warn("presence: sweeping expired locks", "error", delErr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

// Held reports the unexpired lock on ref, if any.
func (s *Service) Held(ctx context.Context, ref string) (Lock, bool, error) {
	row, err := s.locks.Get(ctx, ref)
	if isNotFound(err) {
		return Lock{}, false, nil
	}
	if err != nil {
		return Lock{}, false, fmt.Errorf("presence: reading lock on %s: %w", ref, err)
	}
	if row.ExpiresAt <= s.now().Unix() {
		return Lock{}, false, nil
	}
	return toLock(row), true, nil
}

// ReleaseChangeset frees every lock taken for changesetID — the discarded-
// draft path.
func (s *Service) ReleaseChangeset(ctx context.Context, changesetID string) (int, error) {
	n, err := s.locks.DeleteByChangeset(ctx, changesetID)
	if err != nil {
		return 0, fmt.Errorf("presence: releasing locks for changeset %s: %w", changesetID, err)
	}
	return n, nil
}

// ReleaseSession frees every lock held by sessionID — the dropped-connection
// path (T-2805 AC3).
func (s *Service) ReleaseSession(ctx context.Context, sessionID string) (int, error) {
	n, err := s.locks.DeleteBySession(ctx, sessionID)
	if err != nil {
		return 0, fmt.Errorf("presence: releasing locks for a disconnected session: %w", err)
	}
	return n, nil
}

// auditOverride records one deliberate takeover. T-2805: "Overriding is
// recorded." Best-effort like every other non-critical side effect in this
// codebase — a failed audit write is logged, never turned into a refusal,
// because a refusal is exactly what this feature must never produce.
func (s *Service) auditOverride(ctx context.Context, p Principal, changesetID string, prev Lock) {
	s.log.Info("presence: advisory lock overridden",
		"ref", prev.Ref, "previous_holder", prev.Holder, "previous_changeset", prev.ChangesetID,
		"actor", p.Username, "changeset", changesetID)
	if s.audit == nil {
		return
	}
	var detail sql.NullString
	if b, err := json.Marshal(map[string]any{
		"ref":               prev.Ref,
		"previousHolder":    prev.Holder,
		"previousChangeset": prev.ChangesetID,
	}); err == nil {
		detail = sql.NullString{String: string(b), Valid: true}
	}
	if _, err := s.audit.Append(ctx, store.AuditEntry{
		At:          s.now().Unix(),
		Username:    p.Username,
		Action:      AuditActionLockOverride,
		Target:      sql.NullString{String: prev.Ref, Valid: true},
		ChangesetID: sql.NullString{String: changesetID, Valid: changesetID != ""},
		Result:      "success",
		DetailJSON:  detail,
	}); err != nil {
		s.log.Error("presence: recording lock override", "error", err, "ref", prev.Ref)
	}
}

func toLock(l store.EntityLock) Lock {
	return Lock{
		Ref:         l.Ref,
		ChangesetID: l.ChangesetID,
		Holder:      l.Holder,
		SessionID:   l.SessionID,
		AcquiredAt:  l.AcquiredAt,
		ExpiresAt:   l.ExpiresAt,
	}
}

func isNotFound(err error) bool {
	return err != nil && errors.Is(err, store.ErrNotFound)
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
