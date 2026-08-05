// review.go implements T-2003's change review surface: per-op/changeset
// comments and an explicit review-approval gate, generalizing T-1703's
// tenant request-changeset approval queue (internal/tenant's CanApprove/
// RecordRequest/MarkApproved) from "only a tenant member's request" to every
// changeset, rather than building a second approval mechanism.
//
// SECURITY (docs/security.md's Safety-interlocks stance, and the T-2003 task
// card's own note): approval is an authorization surface, not a UI feature.
// Whether a changeset may apply is decided HERE, server-side, from the
// changeset_approvals row this file's ReviewApprove/ReviewReject write and
// beginApply (apply.go) reads — never from a client-supplied field, and
// never merely by a frontend choosing not to render an Apply button. See
// apply.go's approval gate and apply_errors.go's ErrApprovalRequired.
//
// Deliberately orthogonal to Changeset.Status: approval does not add a new
// lifecycle state (unlike T-1703's StatusRequested, which genuinely is a new
// state in that changeset's own life before it is even a draft). A draft or
// validated changeset carries its own independent approval decision
// alongside its ordinary status, exactly the way its Findings do — editing
// the ops (UpdateDraft) invalidates both a stale StatusValidated (back to
// draft) and a stale approval decision (cleared outright), for the same
// reason: the decision was made against a specific set of ops.

package change

import (
	"context"
	"errors"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/store"
)

// Comment is one review comment on a changeset (T-2003), attributed and
// timestamped, either attached to a single op (OpID matching that op's own
// stable Op.ID — op.go) or to the changeset as a whole (OpID empty).
// Comments are pure review metadata: they never affect validation, diff, or
// apply, and — unlike Findings — are never recomputed, only added/removed.
type Comment struct {
	ID          string
	ChangesetID string
	OpID        string
	Author      string
	Body        string
	CreatedAt   int64
}

func commentFromRow(r store.ChangesetComment) Comment {
	return Comment{ID: r.ID, ChangesetID: r.ChangesetID, OpID: r.OpID, Author: r.Author, Body: r.Body, CreatedAt: r.CreatedAt}
}

// ApprovalStatus is a changeset's current review-approval decision.
type ApprovalStatus string

const (
	// ApprovalNone is the implicit state of every changeset that has never
	// had a decision recorded — the only state a pre-T-2003 changeset can
	// ever be in.
	ApprovalNone     ApprovalStatus = "none"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
)

// ApprovalState is one changeset's review-approval read model (docs/api.md's
// changesets section): whether this deployment's policy currently requires
// approval before apply, and — once a decision has been made — who made it,
// when, and (for a rejection) why.
type ApprovalState struct {
	Status    ApprovalStatus
	DecidedBy string
	Reason    string
	DecidedAt int64
	// Required reports whether THIS changeset's apply is currently gated on
	// approval, per the deployment's [changesets] policy (ApprovalConfig) —
	// computed fresh on every read, never stored, so a policy change is
	// reflected immediately rather than needing every changeset re-stamped.
	Required bool
}

// ApprovalConfig is the deployment-wide review-approval policy (T-2003,
// generalizing T-1703's tenant approval queue to every changeset). The zero
// value (Required false) is a complete no-op: every pre-T-2003 deployment's
// apply behavior is byte-identical, since nothing gates apply on approval
// state unless an admin opts in ([changesets] approval_required = true).
type ApprovalConfig struct {
	// Approvers, when non-empty, restricts WHO may record a review decision
	// to this exact identity list (usernames — the same principal string the
	// session/audit layer already uses). Empty means "anyone who can reach
	// the route" (i.e. anyone with netWrite) may decide — the coarsest,
	// zero-config default; an admin who wants real separation of duties sets
	// this list explicitly.
	Approvers []string
	// Required, when true, blocks POST /changesets/{id}/apply server-side
	// (beginApply, apply.go) until the changeset carries an "approved"
	// changeset_approvals row. Checked fresh from the store on every apply
	// attempt — never cached, never a client-supplied assertion.
	Required bool
	// AllowSelfApproval, when false, refuses a review-approval decision made
	// by the changeset's own Author — mirrors internal/tenant.CanApprove's
	// separation-of-duties rule ("a tenant member — even an approver — can
	// never approve their own request"), generalized to every changeset.
	// internal/config defaults this true (self-approval permitted) when
	// unset, matching every pre-T-2003 deployment's implicit single-admin
	// workflow, where "the approver" and "the author" are routinely the same
	// person — this field's own Go zero value is intentionally the stricter
	// "false"; internal/config.Load is what supplies the permissive default,
	// so a caller that constructs ApprovalConfig directly (e.g. a test) is
	// never silently more permissive than it asked for.
	AllowSelfApproval bool
}

// allowed reports whether identity may record a review decision under cfg.
func (cfg ApprovalConfig) allowed(identity string) bool {
	if len(cfg.Approvers) == 0 {
		return true
	}
	for _, a := range cfg.Approvers {
		if a == identity {
			return true
		}
	}
	return false
}

// ErrReviewNotConfigured is returned by every review method when this
// Service was built with no Comments/Approvals store repo wired (a
// validation-only or otherwise minimal Service, e.g. many existing unit
// tests) — lets the API layer distinguish "this build can't do review" from
// a real failure, mirroring ErrApplyNotConfigured's identical role for the
// apply engine.
type ErrReviewNotConfigured struct{}

func (e *ErrReviewNotConfigured) Error() string {
	return "change: comment/approval storage is not configured on this Service"
}

// ErrCommentOpNotFound is returned by AddComment when opID is non-empty but
// names no op in the changeset's current ops — a comment must never attach
// to an op that doesn't exist (which would make it un-orphanable-by-design,
// the exact failure mode T-2003's card warns against).
type ErrCommentOpNotFound struct {
	OpID string
}

func (e *ErrCommentOpNotFound) Error() string {
	return fmt.Sprintf("change: no op %q on this changeset", e.OpID)
}

// ErrSelfApprovalForbidden is returned by ReviewApprove when this
// deployment's policy ([changesets] allow_self_approval = false) forbids a
// changeset's own author from approving it.
type ErrSelfApprovalForbidden struct{}

func (e *ErrSelfApprovalForbidden) Error() string {
	return "change: this deployment's policy forbids approving your own changeset"
}

// ErrNotAnApprover is returned by ReviewApprove/ReviewReject when this
// deployment's policy names an explicit approvers list ([changesets]
// approvers) and identity is not on it.
type ErrNotAnApprover struct {
	Identity string
}

func (e *ErrNotAnApprover) Error() string {
	return fmt.Sprintf("change: %q is not in this deployment's configured approvers list", e.Identity)
}

// opIDExists reports whether any op in ops carries the given (non-empty) id.
func opIDExists(ops []Op, id string) bool {
	for _, op := range ops {
		if op.ID == id {
			return true
		}
	}
	return false
}

// assignOpIDs stamps a stable, opaque id (Op.ID) onto every op in ops that
// doesn't already carry one — called at every point ops are persisted
// (create/CreateRequest/UpdateDraft) so a per-op review Comment has
// something stable to key off that survives the changeset's own validate/
// diff cycles (neither of which ever touches Ops). An op arriving with an id
// already set — an unedited op round-tripped from a previous GET, exactly
// how useDrawerActions.ts's addOps/replaceOps spread the existing ops array
// back in on every edit — keeps it, so its comments stay attached across an
// edit that leaves it untouched. Mutates ops in place.
func assignOpIDs(ops []Op) {
	for i := range ops {
		if ops[i].ID == "" {
			ops[i].ID = store.NewULID()
		}
	}
}

// opIDSet returns the set of every non-empty Op.ID in ops.
func opIDSet(ops []Op) map[string]bool {
	set := make(map[string]bool, len(ops))
	for _, op := range ops {
		if op.ID != "" {
			set[op.ID] = true
		}
	}
	return set
}

// AddComment records a new review comment on changesetID, attributed to
// author. opID, when non-empty, must name an op currently on the changeset
// (*ErrCommentOpNotFound otherwise); empty attaches a changeset-level
// comment. Audited as changeset.comment_add.
func (s *Service) AddComment(ctx context.Context, changesetID, author, opID, body string) (Comment, error) {
	if s.comments == nil {
		return Comment{}, &ErrReviewNotConfigured{}
	}
	cs, err := s.Get(ctx, changesetID)
	if err != nil {
		return Comment{}, err
	}
	if opID != "" && !opIDExists(cs.Ops, opID) {
		return Comment{}, &ErrCommentOpNotFound{OpID: opID}
	}
	row := store.ChangesetComment{
		ID: store.NewULID(), ChangesetID: changesetID, OpID: opID, Author: author, Body: body, CreatedAt: s.now().Unix(),
	}
	if err := s.comments.Insert(ctx, row); err != nil {
		return Comment{}, fmt.Errorf("change: adding comment to changeset %s: %w", changesetID, err)
	}
	s.appendAudit(ctx, author, "changeset.comment_add", "success", changesetID, map[string]any{"commentId": row.ID, "opId": opID})
	return commentFromRow(row), nil
}

// ListComments returns every comment on changesetID, oldest first. Returns
// (nil, nil) — not an error — when this Service has no comment store wired,
// mirroring how a nil Inventory validates against an empty snapshot rather
// than failing.
func (s *Service) ListComments(ctx context.Context, changesetID string) ([]Comment, error) {
	if s.comments == nil {
		return nil, nil
	}
	rows, err := s.comments.ListForChangeset(ctx, changesetID)
	if err != nil {
		return nil, fmt.Errorf("change: listing comments for changeset %s: %w", changesetID, err)
	}
	out := make([]Comment, 0, len(rows))
	for _, r := range rows {
		out = append(out, commentFromRow(r))
	}
	return out, nil
}

// DeleteComment removes commentID from changesetID, audited as
// changeset.comment_delete. Returns store.ErrNotFound if commentID doesn't
// exist or belongs to a different changeset (the latter never confirms the
// comment's existence under the wrong id, mirroring internal/tenant's
// not-found-not-forbidden convention for an out-of-scope lookup).
func (s *Service) DeleteComment(ctx context.Context, changesetID, commentID, actor string) error {
	if s.comments == nil {
		return &ErrReviewNotConfigured{}
	}
	row, err := s.comments.Get(ctx, commentID)
	if err != nil {
		return fmt.Errorf("change: deleting comment %s: %w", commentID, err)
	}
	if row.ChangesetID != changesetID {
		return store.ErrNotFound
	}
	if err := s.comments.Delete(ctx, commentID); err != nil {
		return fmt.Errorf("change: deleting comment %s: %w", commentID, err)
	}
	s.appendAudit(ctx, actor, "changeset.comment_delete", "success", changesetID, map[string]any{"commentId": commentID})
	return nil
}

// cleanupOrphanedComments removes every comment attached to one of
// removedOpIDs on changesetID (UpdateDraft's op-removal path) and, if any
// were actually removed, audits the cleanup — so a comment's op disappearing
// is never silent, per T-2003's card. A no-op (not an error) when this
// Service has no comment store wired.
func (s *Service) cleanupOrphanedComments(ctx context.Context, changesetID, actor string, removedOpIDs []string) {
	if s.comments == nil || len(removedOpIDs) == 0 {
		return
	}
	n, err := s.comments.DeleteForOps(ctx, changesetID, removedOpIDs)
	if err != nil {
		s.log.Error("change: cleaning up comments for removed ops", "changeset_id", changesetID, "error", err)
		return
	}
	if n > 0 {
		s.appendAudit(ctx, actor, "changeset.comment_orphan_cleanup", "success", changesetID, map[string]any{"opIds": removedOpIDs, "count": n})
	}
}

// clearApproval removes any recorded review decision for changesetID —
// called on every UpdateDraft (the ops changed, so any prior decision was
// made against a now-stale set). A no-op when this Service has no approval
// store wired.
func (s *Service) clearApproval(ctx context.Context, changesetID string) {
	if s.approvals == nil {
		return
	}
	if err := s.approvals.Clear(ctx, changesetID); err != nil {
		s.log.Error("change: clearing approval state after edit", "changeset_id", changesetID, "error", err)
	}
}

// GetApproval returns changesetID's current ApprovalState — Status
// ApprovalNone (with Required reflecting the live policy) when no decision
// has ever been recorded or this Service has no approval store wired.
func (s *Service) GetApproval(ctx context.Context, changesetID string) (ApprovalState, error) {
	state := ApprovalState{Status: ApprovalNone, Required: s.approval.Required}
	if s.approvals == nil {
		return state, nil
	}
	a, err := s.approvals.Get(ctx, changesetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return state, nil
		}
		return ApprovalState{}, fmt.Errorf("change: reading approval state for changeset %s: %w", changesetID, err)
	}
	state.Status = ApprovalStatus(a.Status)
	state.DecidedBy = a.DecidedBy
	state.Reason = a.Reason
	state.DecidedAt = a.DecidedAt
	return state, nil
}

// isApproved reports whether changesetID currently carries an "approved"
// decision. Fails CLOSED: no approval store configured, or any read error,
// answers false — apply's gate (beginApply) must never treat "couldn't
// prove approval" as "approved".
func (s *Service) isApproved(ctx context.Context, changesetID string) (bool, error) {
	if s.approvals == nil {
		return false, nil
	}
	a, err := s.approvals.Get(ctx, changesetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("change: reading approval state for changeset %s: %w", changesetID, err)
	}
	return a.Status == string(ApprovalApproved), nil
}

// ReviewApprove records an approval decision for id, attributed to approver.
// Refuses with *ErrSelfApprovalForbidden when this deployment's policy
// disallows self-approval and approver is the changeset's own Author, and
// with *ErrNotAnApprover when an explicit approvers list is configured and
// approver isn't on it. Audited as changeset.review_approve. This does NOT
// itself apply the changeset — the caller still drives the ordinary
// apply/confirm flow afterward, exactly like T-1703's tenant Approve.
func (s *Service) ReviewApprove(ctx context.Context, id, approver string) (Changeset, error) {
	if s.approvals == nil {
		return Changeset{}, &ErrReviewNotConfigured{}
	}
	cs, err := s.Get(ctx, id)
	if err != nil {
		return Changeset{}, err
	}
	if !s.approval.AllowSelfApproval && approver == cs.Author {
		return Changeset{}, &ErrSelfApprovalForbidden{}
	}
	if !s.approval.allowed(approver) {
		return Changeset{}, &ErrNotAnApprover{Identity: approver}
	}
	now := s.now().Unix()
	if err := s.approvals.Upsert(ctx, store.ChangesetApproval{
		ChangesetID: id, Status: string(ApprovalApproved), DecidedBy: approver, DecidedAt: now,
	}); err != nil {
		return Changeset{}, fmt.Errorf("change: recording approval for changeset %s: %w", id, err)
	}
	s.appendAudit(ctx, approver, "changeset.review_approve", "success", id, nil)
	return cs, nil
}

// ReviewReject records a rejection decision for id, attributed to rejecter,
// with an optional human-readable reason. Refuses with *ErrNotAnApprover
// when an explicit approvers list is configured and rejecter isn't on it
// (rejection has no self-rejection restriction — an author declining their
// own draft is ordinary). Audited as changeset.review_reject. A rejected
// changeset is NOT auto-discarded: it stays exactly where it was (draft or
// validated), editable, so the author can revise and resubmit for review.
func (s *Service) ReviewReject(ctx context.Context, id, rejecter, reason string) (Changeset, error) {
	if s.approvals == nil {
		return Changeset{}, &ErrReviewNotConfigured{}
	}
	cs, err := s.Get(ctx, id)
	if err != nil {
		return Changeset{}, err
	}
	if !s.approval.allowed(rejecter) {
		return Changeset{}, &ErrNotAnApprover{Identity: rejecter}
	}
	now := s.now().Unix()
	if err := s.approvals.Upsert(ctx, store.ChangesetApproval{
		ChangesetID: id, Status: string(ApprovalRejected), DecidedBy: rejecter, Reason: reason, DecidedAt: now,
	}); err != nil {
		return Changeset{}, fmt.Errorf("change: recording rejection for changeset %s: %w", id, err)
	}
	s.appendAudit(ctx, rejecter, "changeset.review_reject", "rejected", id, map[string]any{"reason": reason})
	return cs, nil
}
