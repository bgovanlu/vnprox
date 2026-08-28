// SPDX-License-Identifier: Apache-2.0

// The client half of T-2602's canary apply: which strategies this changeset
// could possibly be applied with, and the exact request body a chosen one
// becomes.
//
// Everything here MIRRORS internal/change/apply_staged.go's
// validateApplyStrategy. It is not the enforcement — the server refuses an
// impossible strategy before any snapshot or mutation, with `422
// invalid_apply_strategy` and its own operator-facing reason, and that
// refusal stands whatever this module thinks. The mirror exists so the
// picker never *offers* a configuration the server would refuse, which is
// the difference between "canary is available here" and "canary threw an
// error at you after you clicked Apply".
//
// Framework-free and directly Vitest-able, like planPreview.ts and
// approvalGate.ts next door.
import type { ApplyStrategy, PlanStep } from "../api/types";

/** internal/change.MinCanaryHold / MaxCanaryHold (= MaxConfirmTimeout). */
export const MIN_CANARY_HOLD_SEC = 10;
export const MAX_CANARY_HOLD_SEC = 600;
/** internal/change.DefaultCanaryHold. */
export const DEFAULT_CANARY_HOLD_SEC = 60;

// Both sets below are keyed on the raw kind string rather than on
// PlanStep["kind"]: `switch_apply` is a real server-side step kind
// (change.StepSwitchApply) that this client's union does not yet name, and a
// set that cannot express it would silently stop refusing the one plan shape
// the server refuses hardest.

/** Plan step kinds a canary split cannot reorder around — mirrors
 * apply_staged.go's canaryUnstageableKinds. Their documented position is
 * BEFORE every per-node step, so they belong to neither stage. */
const UNSTAGEABLE_KINDS: ReadonlySet<string> = new Set(["switch_apply", "sdn_stage", "ipam_alloc"]);

/** Step kinds that can only execute with a live user PVE ticket — mirrors
 * apply_staged.go's planRequiresPVESession. `gate: "auto"` promotes from a
 * timer with no session, so a plan containing one of these cannot use it. */
const PVE_SESSION_KINDS: ReadonlySet<string> = new Set([
  "sdn_stage",
  "sdn_apply",
  "fw_apply",
  "fw_verify",
  "ipam_alloc",
]);

/** Every node the plan's per-node steps touch, in first-appearance order —
 * mirrors Plan.affectedNodes(). Cluster-scope steps carry no node and are
 * deliberately not represented here. */
export function affectedNodes(steps: readonly PlanStep[]): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const step of steps) {
    if (!step.node || seen.has(step.node)) continue;
    seen.add(step.node);
    out.push(step.node);
  }
  return out;
}

export interface CanaryEligibility {
  /** Whether `mode: "canary"` is offerable for this plan at all. */
  eligible: boolean;
  /** The nodes selectable as canary nodes. Empty when not eligible. */
  nodes: string[];
  /** Why not — shown next to the disabled option, so "canary is missing"
   * and "canary does not apply to this change" never look the same. */
  reason?: string;
}

/** Whether this plan can be staged, and over which nodes. */
export function canaryEligibility(steps: readonly PlanStep[]): CanaryEligibility {
  const unstageable = [...new Set(steps.filter((s) => UNSTAGEABLE_KINDS.has(s.kind)).map((s) => s.kind))].sort();
  if (unstageable.length > 0) {
    return {
      eligible: false,
      nodes: [],
      reason: `This plan carries cluster-scope steps that must run before any per-node step (${unstageable.join(", ")}), so it cannot be split into a canary stage.`,
    };
  }
  const nodes = affectedNodes(steps);
  if (nodes.length < 2) {
    return {
      eligible: false,
      nodes: [],
      reason: "A canary apply needs at least two affected nodes; this changeset touches fewer.",
    };
  }
  return { eligible: true, nodes };
}

/** Why `gate: "auto"` is unavailable for this plan, or undefined if the
 * plan itself permits it.
 *
 * Note the other half of the server's auto-gate check — whether this daemon
 * has a canary health checker wired — is NOT knowable from the browser. The
 * picker therefore offers `auto` for a plan that permits it and lets the
 * server's own refusal surface if the daemon has no checker; claiming here
 * that it will work would be the over-claim, and claiming it will not would
 * hide a working feature. */
export function autoGateUnavailableReason(steps: readonly PlanStep[]): string | undefined {
  const blocking = [...new Set(steps.filter((s) => PVE_SESSION_KINDS.has(s.kind)).map((s) => s.kind))].sort();
  if (blocking.length === 0) return undefined;
  return `Automatic promotion runs from a timer with no user session, and these steps need a live PVE session: ${blocking.join(", ")}.`;
}

/** The picker's own state. `mode: "all"` carries no other field, exactly as
 * the wire format demands. */
export interface StrategySelection {
  mode: ApplyStrategy["mode"];
  canaryNodes: string[];
  holdForSec: number;
  gate: NonNullable<ApplyStrategy["gate"]>;
}

export function defaultSelection(): StrategySelection {
  return { mode: "all", canaryNodes: [], holdForSec: DEFAULT_CANARY_HOLD_SEC, gate: "manual" };
}

/**
 * The reason this selection would be refused, or undefined if it is
 * sendable. Mirrors validateApplyStrategy's canary branch in order.
 *
 * `confirmTimeoutSec` matters: the commit-confirm window covers the WHOLE
 * staged sequence, so a hold that fills it leaves no time to apply the
 * remaining nodes.
 */
export function selectionError(
  selection: StrategySelection,
  eligibility: CanaryEligibility,
  confirmTimeoutSec: number,
): string | undefined {
  if (selection.mode === "all") return undefined;
  if (!eligibility.eligible) return eligibility.reason ?? "This changeset cannot be applied as a canary.";

  const chosen = selection.canaryNodes.filter((n) => eligibility.nodes.includes(n));
  if (chosen.length === 0) {
    return "Choose at least one canary node.";
  }
  if (chosen.length === eligibility.nodes.length) {
    return "Canary nodes cover every node this changeset affects, so there would be no second stage to hold before.";
  }
  if (selection.holdForSec < MIN_CANARY_HOLD_SEC || selection.holdForSec > MAX_CANARY_HOLD_SEC) {
    return `Hold must be between ${String(MIN_CANARY_HOLD_SEC)} and ${String(MAX_CANARY_HOLD_SEC)} seconds.`;
  }
  if (selection.holdForSec >= confirmTimeoutSec) {
    return `The hold (${String(selection.holdForSec)}s) must be shorter than the commit-confirm window (${String(confirmTimeoutSec)}s): the window covers the whole staged sequence, so a hold that fills it leaves no time to apply the remaining nodes.`;
  }
  return undefined;
}

/**
 * The `applyStrategy` field for the apply request body — or undefined for
 * `mode: "all"`.
 *
 * Undefined, not `{mode: "all"}`: omitting the field is documented as
 * exactly what apply has always done, and this card must not alter the
 * existing apply path's request body. The canary nodes are emitted in the
 * plan's own node order rather than click order, so the body is a function
 * of the selection and not of how the operator got there.
 */
export function buildApplyStrategy(
  selection: StrategySelection,
  eligibility: CanaryEligibility,
): ApplyStrategy | undefined {
  if (selection.mode === "all") return undefined;
  return {
    mode: "canary",
    canaryNodes: eligibility.nodes.filter((n) => selection.canaryNodes.includes(n)),
    holdForSec: selection.holdForSec,
    gate: selection.gate,
  };
}
