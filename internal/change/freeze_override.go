// SPDX-License-Identifier: Apache-2.0

// freeze_override.go implements T-4006's audited escape hatch for a
// declared freeze-window policy rule: a reasoned, recorded override that
// downgrades a freeze rule's otherwise-blocking `deny` finding to a
// visible warning — following the exact shape T-2604's two-person
// break-glass already established (twoperson.go): a written reason is
// required, the invocation is recorded server-side, and it is audited
// under its own action so a filtered audit log finds every override
// without knowing which validate/apply results imply one.
//
// WHY THIS IS NOT twoperson.go's break-glass, reused. Two-person
// break-glass overrides an AUTHORIZATION check (enforceTwoPerson, run once,
// only inside beginApply, only after validation has already passed). A
// freeze window is not an authorization check — it is an ordinary
// VALIDATE-time policy finding (policyValidate), by the task card's own
// requirement: a freeze must refuse a changeset "before an operator
// invests in staging and review, not at the moment of apply." So this
// override has to be visible to EVERY path that evaluates policy for a
// specific changeset — Service.validate (draft mutation, POST .../validate,
// and Schedule's pre-schedule validate), beginApply's pre-apply
// revalidation, and policyDenial's early Diff refusal — not only to
// beginApply. Reusing changeset_breakglass/change.breakglass for a
// conceptually different override would also make an auditor's "which
// ceremony happened here" question ambiguous (see the migration's own
// comment).
//
// ONE OVERRIDE RECORD, SEEN IDENTICALLY EVERYWHERE. overriddenPolicyTags is
// the single read every one of those paths calls (via
// Service.validationInputs / policyDenial) — so a freeze declared, then
// overridden, then edited-and-not-reinvoked can never look bypassed to one
// caller and blocked to another.

package change

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bgovanlu/vnprox/internal/store"
)

// PolicyTagFreeze is the reserved policy-rule tag convention a freeze
// window declares itself with (`tags: [freeze]`). It is a convention, not
// a new field on PolicyRule or a new rule type: InvokeFreezeOverride
// downgrades a violation only when the violating rule carries this tag
// (via PolicyInput.OverriddenTags, policy_eval.go), and freeze_calendar.go's
// best-effort renderer uses the same tag to find freeze rules among an
// arbitrary policy set. A deployment is free to also use PolicyDeny/
// PolicyWarn rules with `time.*` conditions WITHOUT this tag (e.g. "warn
// outside business hours") — those are ordinary policy rules, just not
// ones the freeze override or the calendar recognize as a freeze window.
const PolicyTagFreeze = "freeze"

// maxFreezeOverrideReasonLen bounds the stored reason, mirroring
// maxBreakGlassReasonLen for the identical reason: a justification, not a
// blob store.
const maxFreezeOverrideReasonLen = 1000

// FreezeOverrideRecord is one changeset's freeze-window override, as the
// API renders it.
type FreezeOverrideRecord struct {
	ChangesetID string `json:"changesetId"`
	Reason      string `json:"reason"`
	InvokedBy   string `json:"invokedBy"`
	// OpsFingerprint pins the override to the ops it was invoked for.
	// Never rendered on the wire — an internal interlock, not information
	// an operator acts on (mirrors BreakGlassRecord's identical field).
	OpsFingerprint string `json:"-"`
	InvokedAt      int64  `json:"invokedAt"`
}

// ErrFreezeOverrideReasonRequired is returned by InvokeFreezeOverride when
// no written reason was supplied.
type ErrFreezeOverrideReasonRequired struct{}

func (e *ErrFreezeOverrideReasonRequired) Error() string {
	return "change: freeze-window override requires a written reason"
}

// ErrFreezeOverrideNotConfigured is returned by the freeze-override API
// when this Service was built with no freeze-override store wired.
type ErrFreezeOverrideNotConfigured struct{}

func (e *ErrFreezeOverrideNotConfigured) Error() string {
	return "change: freeze-override storage is not configured on this Service"
}

// InvokeFreezeOverride records changeset id's freeze-window override,
// attributed to actor and justified by reason (required —
// *ErrFreezeOverrideReasonRequired otherwise, before anything is written
// or audited).
//
// It is audited under its OWN action, `change.freeze_override` —
// deliberately not folded into `changeset.validate`/`changeset.apply`'s own
// audit entries, for the same "an auditor filtering for overrides finds
// them without knowing which result implies one" reason T-2604's
// break-glass gives.
//
// This does NOT itself validate or apply the changeset: the very next
// validate/apply/Diff call for id sees the override (via
// overriddenPolicyTags), and every OTHER policy rule — including any
// non-freeze deny — still applies in full. It downgrades exactly the
// tagged freeze rule(s)' findings from blocking to visible-and-warning.
func (s *Service) InvokeFreezeOverride(ctx context.Context, id, actor, reason string) (FreezeOverrideRecord, error) {
	if s.freezeOverrides == nil {
		return FreezeOverrideRecord{}, &ErrFreezeOverrideNotConfigured{}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return FreezeOverrideRecord{}, &ErrFreezeOverrideReasonRequired{}
	}
	if len(reason) > maxFreezeOverrideReasonLen {
		return FreezeOverrideRecord{}, fmt.Errorf("change: freeze-override reason exceeds %d characters", maxFreezeOverrideReasonLen)
	}
	cs, err := s.Get(ctx, id)
	if err != nil {
		return FreezeOverrideRecord{}, err
	}
	now := s.now().Unix()
	row := store.ChangesetFreezeOverride{
		ChangesetID:    id,
		Reason:         reason,
		InvokedBy:      actor,
		InvokedAt:      now,
		OpsFingerprint: opsFingerprint(cs.Ops),
	}
	if err := s.freezeOverrides.Upsert(ctx, row); err != nil {
		return FreezeOverrideRecord{}, fmt.Errorf("change: recording freeze override for changeset %s: %w", id, err)
	}
	rec := freezeOverrideFromRow(row)
	s.appendAudit(ctx, actor, "change.freeze_override", "invoked", id, map[string]any{
		"reason": reason,
	})
	s.log.Warn("change: freeze-window override invoked",
		"changeset_id", id, "actor", actor, "reason", reason)
	return rec, nil
}

func freezeOverrideFromRow(row store.ChangesetFreezeOverride) FreezeOverrideRecord {
	return FreezeOverrideRecord{
		ChangesetID:    row.ChangesetID,
		Reason:         row.Reason,
		InvokedBy:      row.InvokedBy,
		InvokedAt:      row.InvokedAt,
		OpsFingerprint: row.OpsFingerprint,
	}
}

// freezeOverrideFor returns changesetID's override, or (zero, false) when
// none is on record. A read error is reported as "no override" AND logged
// — the consequence is a freeze that still blocks, never one that silently
// stops blocking (mirrors breakGlassFor's identical fail-closed contract).
func (s *Service) freezeOverrideFor(ctx context.Context, changesetID string) (FreezeOverrideRecord, bool) {
	if s.freezeOverrides == nil {
		return FreezeOverrideRecord{}, false
	}
	row, err := s.freezeOverrides.Get(ctx, changesetID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("change: reading freeze-override record", "changeset_id", changesetID, "error", err)
		}
		return FreezeOverrideRecord{}, false
	}
	return freezeOverrideFromRow(row), true
}

// FreezeOverrideEvents returns every recorded freeze-window override,
// newest first — mirrors BreakGlassEvents' contract so a future findings
// check (T-4007's maintenance-window suppression is the next card to touch
// this calendar/window model) can compute visibility from these rows the
// same way change_break_glass already does from BreakGlassEvents, without
// this file needing to know anything about internal/findings.
func (s *Service) FreezeOverrideEvents(ctx context.Context) []FreezeOverrideRecord {
	if s.freezeOverrides == nil {
		return nil
	}
	rows, err := s.freezeOverrides.List(ctx)
	if err != nil {
		s.log.Warn("change: listing freeze-override records", "error", err)
		return nil
	}
	out := make([]FreezeOverrideRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, freezeOverrideFromRow(row))
	}
	return out
}

// overriddenPolicyTags builds the PolicyInput.OverriddenTags a changeset's
// policy evaluation needs: when changesetID has a freeze override on
// record AND it is still pinned to ops (an edit after the override
// invalidates it — the same rule twoperson.go's break-glass follows via
// its own OpsFingerprint check in enforceTwoPerson), every freeze-tagged
// rule's otherwise-blocking finding is downgraded, with a note naming who
// overrode it and why.
//
// changesetID == "" (a changeset that does not exist yet — Create/
// CreateRequest) always returns nil: there is nothing an override could
// have been recorded against.
func (s *Service) overriddenPolicyTags(ctx context.Context, changesetID string, ops []Op) map[string]string {
	if changesetID == "" {
		return nil
	}
	fo, ok := s.freezeOverrideFor(ctx, changesetID)
	if !ok || fo.OpsFingerprint != opsFingerprint(ops) {
		return nil
	}
	return map[string]string{
		PolicyTagFreeze: fmt.Sprintf("freeze override by %s: %s", fo.InvokedBy, fo.Reason),
	}
}
