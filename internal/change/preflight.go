// SPDX-License-Identifier: Apache-2.0

// preflight.go wires T-1604's failure-impact simulator into the scheduler as
// an *additive* pre-flight veto on unattended applies (the hook point the
// T-1604 card names). At windowStart the scheduler already refuses any
// changeset that touches a management path outright (schedule.go's
// TouchesMgmtPath exclusion, no override). This adds one more reason to abort:
// if the failure-impact model rates the changeset's touched entities as
// putting quorum or a management path at risk, the unattended apply is
// blocked and audited distinctly.
//
// It is deliberately additive, never an override: fireSchedule runs the
// mgmt-path exclusion FIRST and returns early, so a clean impact verdict can
// never grant a bypass of that gate (T-1604 safety analysis: "The T-1103 hook
// is additive, not an override").

package change

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// ImpactPreflighter is the seam onto internal/failsim. The concrete
// implementation (cmd/vnproxd's failsim adapter) computes the worst failure
// impact among a changeset's touched refs against the live inventory + the
// corosync/Ceph/tunnel side-tables, and decides whether that impact must veto
// an unattended apply — kept as an interface here so internal/change does not
// import internal/failsim (and its inventory/topology/ceph dependency web)
// directly, matching every other injected seam in this package.
type ImpactPreflighter interface {
	// PreflightImpact returns whether the failure impact of removing the
	// worst-affected of refs vetoes an unattended apply, plus a machine
	// reason for audit ("quorum_risk" | "mgmt_path_loss") and optional
	// structured detail. An implementation error should be surfaced (err
	// non-nil); the scheduler fails closed on it, since it cannot prove the
	// change is safe to apply unattended.
	PreflightImpact(ctx context.Context, refs []inventory.Ref) (veto bool, reason string, detail map[string]any, err error)
}

// touchedTargetRefs returns the distinct, non-zero target Refs of ops — the
// "changeset's touched refs" the preflight impact is computed over. Op.Target
// is the entity each op mutates, so this is exactly the set of entities whose
// post-change failure blast radius the scheduler wants to weigh.
func touchedTargetRefs(ops []Op) []inventory.Ref {
	seen := map[inventory.Ref]bool{}
	var out []inventory.Ref
	for _, op := range ops {
		if op.Target.IsZero() || seen[op.Target] {
			continue
		}
		seen[op.Target] = true
		out = append(out, op.Target)
	}
	return out
}

// preflightImpactBlocks consults the injected ImpactPreflighter (if wired) for
// the changeset's touched refs. It returns block=true when the unattended
// apply must be aborted, with a schedule audit reason. A nil preflighter
// (feature not wired) never blocks. A preflighter error fails closed —
// blocking with a distinct reason — because an unassessable impact is not a
// proof of safety, and this path is the unattended one with no operator
// watching.
func (s *Service) preflightImpactBlocks(ctx context.Context, ops []Op) (block bool, reason string) {
	if s.impactPreflight == nil {
		return false, ""
	}
	veto, why, _, err := s.impactPreflight.PreflightImpact(ctx, touchedTargetRefs(ops))
	if err != nil {
		s.log.Warn("change: failure-impact pre-flight errored; failing closed on unattended apply", "error", err)
		return true, "failure_impact_error"
	}
	if veto {
		return true, "failure_impact_" + why
	}
	return false, ""
}
