// twoperson.go implements T-2604's enforced two-person rule on protected op
// classes, plus its emergency break-glass override.
//
// T-2003 already answers "may this changeset be applied at all without a
// review decision?" (ApprovalConfig.Required) and "may the author be the one
// who decides?" (AllowSelfApproval). What did not exist before this file is a
// rule that SOME CLASSES OF CHANGE REQUIRE APPROVAL AT ALL, by N distinct
// people — so an operator with the capability could stage and apply a
// management-path change alone at 3am.
//
// WHERE THIS IS ENFORCED, AND WHY THERE: inside beginApply (apply.go), in the
// same locked prologue as T-2003's approval gate and before any snapshot or
// mutation. It is an AUTHORIZATION check, not a validation one — it can never
// be satisfied by anything on the apply request itself. The only things that
// can satisfy it are rows another, separately-audited request wrote
// (changeset_signoffs, changeset_breakglass), read back server-side on every
// attempt. A request crafted directly against the API, bypassing the UI
// entirely, therefore meets exactly the same gate the UI does (AC2): there is
// no second apply path, and no field a caller can send that this code reads.
//
// HOW A PROTECTED CLASS IS DECLARED. Three kinds, all named as strings in one
// config list ([[changesets.protected_class]]):
//
//   - an OP-TYPE GLOB ("fw.*", "sdn.*", "iface.raw.replace") — matched with
//     the same path.Match globbing a T-2601 policy rule's `op` condition uses
//     (policyGlobMatch), so the two can never disagree about what "fw.*"
//     selects;
//   - "mgmtPath" — anything TouchesMgmtPath says touches a node's resolved
//     management path, computed fresh here from the same MgmtStatus the apply
//     ceremony and the scheduler's own gate use, never from a request field;
//   - "tag:<name>" — anything a T-2601 policy rule carrying that tag matched,
//     read from PolicyResult.TaggedOps.
//
// The tag form is the extension point, and it is deliberately the ONE place
// this card adds vocabulary: an organisation that wants "every change to the
// storage nodes' vmbr9 needs three people" writes that as a policy rule with
// a tag, rather than as a new hard-coded class here. TaggedOps is keyed on
// MATCHED ops rather than violating ones precisely because a tag declares a
// class of change independently of whether the rule's assertion held — see
// PolicyResult.TaggedOps.
//
// THE DEFAULT IS OFF. An empty class list means no changeset is ever in a
// protected class, so every pre-T-2604 deployment's apply behaviour is
// byte-identical (AC6) — the same opt-in convention approval_required and
// auto_rollback_on_error already follow.

package change

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// ProtectedClassMgmtPath is the reserved class name for "this changeset
// touches a node's resolved management path" — the one class that is not a
// property of an op's type but of the cluster's current topology, and
// therefore cannot be expressed as a glob.
const ProtectedClassMgmtPath = "mgmtPath"

// protectedClassTagPrefix marks a class declared by a T-2601 policy rule's
// tag rather than by an op-type glob.
const protectedClassTagPrefix = "tag:"

// BreakGlassAckFloor is how long a break-glass finding cannot be
// acknowledged for (T-2604: "raises an `error` finding that cannot be acked
// for 24 hours"). A break-glass invocation is meant to be reviewed by
// someone who was not in the room when it was taken; letting the person who
// took it silence the finding on their way out would leave the ceremony
// with no consequence at all.
const BreakGlassAckFloor = 24 * time.Hour

// ProtectedClass declares that changesets in one class of change require
// Approvals distinct principals before they may be applied.
type ProtectedClass struct {
	// Class is an op-type glob ("fw.*"), ProtectedClassMgmtPath, or
	// "tag:<policy rule tag>". See this file's doc comment.
	Class string
	// Approvals is how many DISTINCT principals must have approved. A value
	// below 2 is meaningless as a two-person rule and is normalized up to 2
	// by NormalizeProtectedClasses, which is also where a malformed entry is
	// rejected.
	Approvals int
}

// MatchedClass is one protected class a specific changeset falls into, with
// the number of ops that put it there (evidence for the refusal message and
// the audit entry — "which class, and on account of what").
type MatchedClass struct {
	Class     string `json:"class"`
	Approvals int    `json:"approvals"`
	Ops       int    `json:"ops"`
}

// BreakGlassRecord is one changeset's emergency override as the API and the
// findings stream render it.
type BreakGlassRecord struct {
	ChangesetID string `json:"changesetId"`
	Reason      string `json:"reason"`
	InvokedBy   string `json:"invokedBy"`
	// OpsFingerprint pins the override to the ops it was invoked for.
	// Never rendered on the wire — it is an internal interlock, not
	// information an operator acts on.
	OpsFingerprint string `json:"-"`
	InvokedAt      int64  `json:"invokedAt"`
	// AckableAt is the instant the finding this record raises becomes
	// acknowledgeable: InvokedAt + BreakGlassAckFloor.
	AckableAt int64 `json:"ackableAt"`
}

// TwoPersonState is one changeset's two-person-rule read model: which
// protected classes it is in, how many distinct approvals that needs, who
// has approved so far, and whether an emergency override is on record.
// Reported by the API alongside T-2003's ApprovalState; it is a read of the
// same server-side state the apply gate itself consults, never a separate
// opinion about it.
type TwoPersonState struct {
	BreakGlass *BreakGlassRecord `json:"breakGlass,omitempty"`
	Classes    []MatchedClass    `json:"classes,omitempty"`
	Approvers  []string          `json:"approvers,omitempty"`
	Required   int               `json:"required"`
	Satisfied  bool              `json:"satisfied"`
}

// ErrTwoPersonRequired is returned by Apply when the changeset falls in a
// protected op class and fewer than the required number of DISTINCT
// principals have approved it. Like ErrApprovalRequired it is an orthogonal
// authorization refusal, not a validation failure (the ops may be perfectly
// valid) and not an illegal transition (the status state machine knows
// nothing about it); the API layer maps it to 422 with the stable code
// `two_person_required`.
//
// The message names the class and the count required — AC1 — because "you
// may not apply this" without naming which rule refused and what would
// satisfy it is exactly the refusal an operator cannot act on.
type ErrTwoPersonRequired struct {
	ID        string
	Class     string
	Approvers []string
	Classes   []MatchedClass
	Required  int
	Have      int
}

func (e *ErrTwoPersonRequired) Error() string {
	who := "no approvals are recorded"
	if len(e.Approvers) > 0 {
		who = fmt.Sprintf("%d recorded (%s)", len(e.Approvers), strings.Join(e.Approvers, ", "))
	}
	return fmt.Sprintf(
		"change: changeset %s is in protected op class %q, which requires %d distinct approvers before it can be applied; %s",
		e.ID, e.Class, e.Required, who)
}

// ErrBreakGlassReasonRequired is returned by InvokeBreakGlass when no
// written reason was supplied. A break-glass with no reason is an
// unexplained override, which is worse than the refusal it replaces — the
// same argument findings' ErrAckReasonRequired makes for acknowledgements.
type ErrBreakGlassReasonRequired struct{}

func (e *ErrBreakGlassReasonRequired) Error() string {
	return "change: emergency break-glass requires a written reason"
}

// ErrBreakGlassNotConfigured is returned by the break-glass API when this
// Service was built with no break-glass store wired — mirrors
// ErrReviewNotConfigured's identical role for the review surface.
type ErrBreakGlassNotConfigured struct{}

func (e *ErrBreakGlassNotConfigured) Error() string {
	return "change: break-glass storage is not configured on this Service"
}

// maxBreakGlassReasonLen bounds the stored reason, for the same reason
// findings' maxAckReasonLen does: a justification, not a blob store.
const maxBreakGlassReasonLen = 1000

// NormalizeProtectedClasses validates and canonicalizes a configured
// protected-class list. A malformed entry is an error rather than a silently
// dropped rule: a deployment that meant to require two people and typed the
// class name wrong must find out at startup, not at 3am when the gate it
// thought it had turns out never to have existed.
func NormalizeProtectedClasses(in []ProtectedClass) ([]ProtectedClass, error) {
	out := make([]ProtectedClass, 0, len(in))
	seen := map[string]bool{}
	for i, pc := range in {
		class := strings.TrimSpace(pc.Class)
		if class == "" {
			return nil, fmt.Errorf("change: protected class %d: class is required", i)
		}
		if seen[class] {
			return nil, fmt.Errorf("change: protected class %q is declared more than once", class)
		}
		if strings.HasPrefix(class, protectedClassTagPrefix) {
			if strings.TrimSpace(strings.TrimPrefix(class, protectedClassTagPrefix)) == "" {
				return nil, fmt.Errorf("change: protected class %q names no policy tag", class)
			}
		} else if class != ProtectedClassMgmtPath && !validOpTypeGlob(class) {
			return nil, fmt.Errorf("change: protected class %q matches no op type in the v1 vocabulary, so it could never fire", class)
		}
		approvals := pc.Approvals
		if approvals < 2 {
			// A "two-person rule" that requires one person is not one. This
			// normalizes rather than refuses because 0 is the zero value an
			// operator gets by omitting the field, and "I listed the class"
			// unambiguously means "I want it gated".
			approvals = 2
		}
		seen[class] = true
		out = append(out, ProtectedClass{Class: class, Approvals: approvals})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })
	return out, nil
}

// validOpTypeGlob reports whether pattern selects at least one op type in the
// current vocabulary — the same "a rule that can never fire is a typo, not a
// rule" check PolicySet.Validate applies to an `op` condition.
func validOpTypeGlob(pattern string) bool {
	for t := range paramFactories {
		if policyGlobMatch(pattern, string(t)) {
			return true
		}
	}
	return false
}

// matchedProtectedClasses reports which declared classes cs falls into.
//
// report is the policy evaluation the caller already ran (beginApply reads
// the one the pre-apply revalidation produced, so a changeset is never
// policy-evaluated twice per apply and the gate can never see a different
// rule set than the validator did). A zero report simply contributes no
// tagged classes.
//
// Fails CLOSED on the mgmtPath class: if the management-path status cannot
// be computed, this returns an error and the apply is refused, rather than
// concluding "no management path, therefore unprotected".
func (s *Service) matchedProtectedClasses(ctx context.Context, cs Changeset, report PolicyResult) ([]MatchedClass, error) {
	if len(s.protectedClasses) == 0 {
		return nil, nil
	}
	var tagged map[string][]int
	var out []MatchedClass
	for _, pc := range s.protectedClasses {
		switch {
		case pc.Class == ProtectedClassMgmtPath:
			mgmtStatus, err := s.MgmtStatus(ctx)
			if err != nil {
				return nil, fmt.Errorf("change: deciding whether changeset %s is in protected class %q: %w", cs.ID, pc.Class, err)
			}
			if TouchesMgmtPath(mgmtStatus.Nodes, s.wgTunnelCarriers(ctx), nil, cs.Ops) {
				out = append(out, MatchedClass{Class: pc.Class, Approvals: pc.Approvals, Ops: len(cs.Ops)})
			}
		case strings.HasPrefix(pc.Class, protectedClassTagPrefix):
			if tagged == nil {
				tagged = report.TaggedOps()
			}
			if n := len(tagged[strings.TrimPrefix(pc.Class, protectedClassTagPrefix)]); n > 0 {
				out = append(out, MatchedClass{Class: pc.Class, Approvals: pc.Approvals, Ops: n})
			}
		default:
			n := 0
			for _, op := range cs.Ops {
				if policyGlobMatch(pc.Class, string(op.Type)) {
					n++
				}
			}
			if n > 0 {
				out = append(out, MatchedClass{Class: pc.Class, Approvals: pc.Approvals, Ops: n})
			}
		}
	}
	return out, nil
}

// bindingClass returns the class driving the requirement (the largest
// Approvals; ties broken by class name so the refusal message is stable) and
// that count. An empty list yields ("", 0).
func bindingClass(classes []MatchedClass) (string, int) {
	binding, required := "", 0
	for _, c := range classes {
		if c.Approvals > required || (c.Approvals == required && c.Class < binding) {
			binding, required = c.Class, c.Approvals
		}
	}
	return binding, required
}

// signoffPrincipals returns the DISTINCT principals who have approved
// changesetID, in a deterministic order.
//
// Distinctness is a property of the storage, not of this function: the
// changeset_signoffs primary key is (changeset_id, principal), so the same
// person approving through two different API tokens — whose sessions carry
// the same username, since a bearer token's identity is its creating user —
// upserts one row twice rather than inserting two (AC3).
//
// Fails CLOSED, like isApproved: a read error is returned rather than
// swallowed into an empty set, and a Service with no sign-off store wired
// reports nobody — which refuses every protected-class apply rather than
// permitting one it cannot prove was approved.
func (s *Service) signoffPrincipals(ctx context.Context, changesetID string) ([]string, error) {
	if s.signoffs == nil {
		return nil, nil
	}
	rows, err := s.signoffs.List(ctx, changesetID)
	if err != nil {
		return nil, fmt.Errorf("change: reading sign-offs for changeset %s: %w", changesetID, err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Principal)
	}
	return out, nil
}

// recordSignoff notes that principal endorses changesetID. Best-effort by
// design: it runs alongside T-2003's own approval upsert, which has already
// succeeded by the time this is called, and a failure here must not make the
// approval itself look like it failed. A lost sign-off can only ever make
// the gate STRICTER (one fewer approver counted), never more permissive.
func (s *Service) recordSignoff(ctx context.Context, changesetID, principal string, atUnix int64) {
	if s.signoffs == nil {
		return
	}
	if err := s.signoffs.Upsert(ctx, store.ChangesetSignoff{ChangesetID: changesetID, Principal: principal, DecidedAt: atUnix}); err != nil {
		s.log.Error("change: recording two-person sign-off", "changeset_id", changesetID, "principal", principal, "error", err)
	}
}

// withdrawSignoff removes principal's endorsement of changesetID — the
// rejection path.
func (s *Service) withdrawSignoff(ctx context.Context, changesetID, principal string) {
	if s.signoffs == nil {
		return
	}
	if err := s.signoffs.Delete(ctx, changesetID, principal); err != nil {
		s.log.Error("change: withdrawing two-person sign-off", "changeset_id", changesetID, "principal", principal, "error", err)
	}
}

// clearSignoffs removes every endorsement of changesetID — called on every
// UpdateDraft, exactly like clearApproval, and for the same reason: people
// endorsed a specific set of ops.
func (s *Service) clearSignoffs(ctx context.Context, changesetID string) {
	if s.signoffs == nil {
		return
	}
	if err := s.signoffs.Clear(ctx, changesetID); err != nil {
		s.log.Error("change: clearing two-person sign-offs after edit", "changeset_id", changesetID, "error", err)
	}
}

// opsFingerprint is a stable digest of a changeset's ops, used to pin a
// break-glass override to the ops it was invoked for. Ops carry stable
// server-assigned ids (review.go's assignOpIDs), so an unedited round trip
// reproduces the same digest; any material edit changes it.
func opsFingerprint(ops []Op) string {
	b, err := json.Marshal(ops)
	if err != nil {
		// Unmarshalable ops cannot happen for a changeset that was persisted
		// (the store round-trips the same JSON), but a digest that silently
		// collided on error would be a way to make a stale override look
		// fresh — so degrade to a value that matches nothing instead.
		return "unfingerprintable"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// InvokeBreakGlass records an emergency override of the two-person rule for
// changeset id, attributed to actor and justified by reason (required —
// *ErrBreakGlassReasonRequired otherwise, before anything is written or
// audited).
//
// It is audited under its OWN action, `change.breakglass` — deliberately not
// a result value on `changeset.apply`, so an auditor filtering the log for
// overrides finds them without having to know which apply results imply one.
// It also raises an error-severity finding (findings' change_break_glass
// check, computed from the row this writes) that cannot be acknowledged for
// BreakGlassAckFloor.
//
// This does NOT itself apply the changeset: the caller still drives the
// ordinary apply flow afterwards, and that apply still runs every other gate
// — validation, T-2003 approval, peer compatibility. Break-glass overrides
// exactly one thing: the distinct-approver count.
func (s *Service) InvokeBreakGlass(ctx context.Context, id, actor, reason string) (BreakGlassRecord, error) {
	if s.breakGlass == nil {
		return BreakGlassRecord{}, &ErrBreakGlassNotConfigured{}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return BreakGlassRecord{}, &ErrBreakGlassReasonRequired{}
	}
	if len(reason) > maxBreakGlassReasonLen {
		return BreakGlassRecord{}, fmt.Errorf("change: break-glass reason exceeds %d characters", maxBreakGlassReasonLen)
	}
	cs, err := s.Get(ctx, id)
	if err != nil {
		return BreakGlassRecord{}, err
	}
	now := s.now().Unix()
	row := store.ChangesetBreakGlass{
		ChangesetID:    id,
		Reason:         reason,
		InvokedBy:      actor,
		InvokedAt:      now,
		OpsFingerprint: opsFingerprint(cs.Ops),
	}
	if err := s.breakGlass.Upsert(ctx, row); err != nil {
		return BreakGlassRecord{}, fmt.Errorf("change: recording break-glass for changeset %s: %w", id, err)
	}
	rec := breakGlassFromRow(row)
	s.appendAudit(ctx, actor, "change.breakglass", "invoked", id, map[string]any{
		"reason":    reason,
		"ackableAt": rec.AckableAt,
	})
	s.log.Warn("change: emergency break-glass invoked on the two-person rule",
		"changeset_id", id, "actor", actor, "reason", reason)
	return rec, nil
}

func breakGlassFromRow(row store.ChangesetBreakGlass) BreakGlassRecord {
	return BreakGlassRecord{
		ChangesetID:    row.ChangesetID,
		Reason:         row.Reason,
		InvokedBy:      row.InvokedBy,
		InvokedAt:      row.InvokedAt,
		AckableAt:      row.InvokedAt + int64(BreakGlassAckFloor.Seconds()),
		OpsFingerprint: row.OpsFingerprint,
	}
}

// breakGlassFor returns changesetID's override, or (zero, false) when none
// is on record. A read error is reported as "no override" AND logged: the
// consequence of that choice is a refused apply, never a permitted one.
func (s *Service) breakGlassFor(ctx context.Context, changesetID string) (BreakGlassRecord, bool) {
	if s.breakGlass == nil {
		return BreakGlassRecord{}, false
	}
	row, err := s.breakGlass.Get(ctx, changesetID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("change: reading break-glass record", "changeset_id", changesetID, "error", err)
		}
		return BreakGlassRecord{}, false
	}
	return breakGlassFromRow(row), true
}

// BreakGlassEvents returns every recorded break-glass invocation — the input
// the findings engine's change_break_glass check is computed from. Context-
// free error handling (a read failure yields nil and a log line) matches
// MissedSchedules' identical contract for the same reason: a findings cycle
// has no caller to return an error to.
func (s *Service) BreakGlassEvents(ctx context.Context) []BreakGlassRecord {
	if s.breakGlass == nil {
		return nil
	}
	rows, err := s.breakGlass.List(ctx)
	if err != nil {
		s.log.Warn("change: listing break-glass records for findings", "error", err)
		return nil
	}
	out := make([]BreakGlassRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, breakGlassFromRow(row))
	}
	return out
}

// TwoPersonState reports changesetID's two-person-rule state — the read
// model behind the API's `approval.twoPerson` field. It runs the SAME class
// matching and the SAME distinct-principal count the apply gate runs, so the
// two can never disagree about whether an apply would be refused.
//
// A deployment with no protected classes configured gets the zero state with
// no work done at all (no policy evaluation, no management-path resolution),
// which is what keeps this cheap enough to decorate every changeset response
// with.
func (s *Service) TwoPersonState(ctx context.Context, changesetID string) (TwoPersonState, error) {
	if len(s.protectedClasses) == 0 {
		return TwoPersonState{Satisfied: true}, nil
	}
	cs, err := s.Get(ctx, changesetID)
	if err != nil {
		return TwoPersonState{}, err
	}
	var report PolicyResult
	if s.usesPolicyTagClass() {
		report, err = s.EvaluatePolicySet(ctx, PolicySet{}, cs.Ops)
		if err != nil {
			return TwoPersonState{}, fmt.Errorf("change: evaluating policy for changeset %s's protected classes: %w", changesetID, err)
		}
	}
	classes, err := s.matchedProtectedClasses(ctx, cs, report)
	if err != nil {
		return TwoPersonState{}, err
	}
	approvers, err := s.signoffPrincipals(ctx, changesetID)
	if err != nil {
		return TwoPersonState{}, err
	}
	_, required := bindingClass(classes)
	state := TwoPersonState{
		Classes:   classes,
		Approvers: approvers,
		Required:  required,
		Satisfied: len(approvers) >= required,
	}
	if bg, ok := s.breakGlassFor(ctx, changesetID); ok {
		state.BreakGlass = &bg
	}
	return state, nil
}

// usesPolicyTagClass reports whether any configured class is tag-based, so
// the read model can skip policy evaluation entirely when none is.
func (s *Service) usesPolicyTagClass() bool {
	for _, pc := range s.protectedClasses {
		if strings.HasPrefix(pc.Class, protectedClassTagPrefix) {
			return true
		}
	}
	return false
}

// enforceTwoPerson is the gate itself, called from beginApply.
//
// It returns nil when the changeset is in no protected class, when enough
// distinct principals have approved it, or when a break-glass override
// invoked for THESE ops is on record. It returns *ErrTwoPersonRequired
// otherwise — before any snapshot, any plan, and any mutation.
//
// report is the policy result the pre-apply revalidation already produced.
func (s *Service) enforceTwoPerson(ctx context.Context, cs Changeset, author string, report PolicyResult) error {
	if len(s.protectedClasses) == 0 {
		return nil
	}
	classes, err := s.matchedProtectedClasses(ctx, cs, report)
	if err != nil {
		return err
	}
	if len(classes) == 0 {
		return nil
	}
	binding, required := bindingClass(classes)

	approvers, err := s.signoffPrincipals(ctx, cs.ID)
	if err != nil {
		return err
	}
	if len(approvers) >= required {
		return nil
	}

	// Break-glass: the reasoned override. Honoured only when it was invoked
	// for the ops being applied right now — an override taken for one change
	// must not silently authorize whatever the draft was edited into
	// afterwards.
	if bg, ok := s.breakGlassFor(ctx, cs.ID); ok {
		if bg.OpsFingerprint == opsFingerprint(cs.Ops) {
			s.appendAudit(ctx, author, "changeset.apply", "break_glass", cs.ID, map[string]any{
				"class":     binding,
				"required":  required,
				"have":      len(approvers),
				"invokedBy": bg.InvokedBy,
				"invokedAt": bg.InvokedAt,
				"reason":    bg.Reason,
				"classes":   classes,
				"approvers": approvers,
				"ackableAt": bg.AckableAt,
			})
			s.log.Warn("change: applying a protected-class changeset under emergency break-glass",
				"changeset_id", cs.ID, "class", binding, "required", required, "have", len(approvers),
				"invoked_by", bg.InvokedBy, "reason", bg.Reason)
			return nil
		}
		s.log.Warn("change: ignoring a break-glass override taken for a different set of ops",
			"changeset_id", cs.ID, "invoked_by", bg.InvokedBy, "invoked_at", bg.InvokedAt)
	}

	return &ErrTwoPersonRequired{
		ID:        cs.ID,
		Class:     binding,
		Required:  required,
		Have:      len(approvers),
		Approvers: approvers,
		Classes:   classes,
	}
}
