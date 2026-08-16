// The read model behind the rollout-state view (T-2602's `applyStage`).
//
// This is the load-bearing half of canary apply in the UI: a canary that
// pauses with no way to see that it paused is worse than no canary. Every
// value here is derived from the server's own `applyStage` — persisted in
// `changeset_apply_stages`, so it survives a daemon restart AND a browser
// reload — and from nothing held on this side.
//
// THE RULE THIS MODULE EXISTS TO ENFORCE: a node whose stage state the
// server did not tell us renders as `unknown`, never as `pending` and never
// as `done`. "We were not told" and "nothing was applied there" are
// different facts about a half-applied cluster, and rendering the first as
// the second is how an operator confirms a change they cannot see.
import type { Changeset, StagedApplyState } from "../api/types";
import { affectedNodes } from "./applyStrategy";

/** internal/store's closed set of staged-apply states. */
const STATE_CANARY_HOLD = "canary_hold";
const STATE_PROMOTING = "promoting";

export type NodeRolloutStatus =
  /** The canary stage ran on this node: it has been mutated. */
  | "done"
  /** Named as not-yet-contacted: nothing has been written there. */
  | "pending"
  /** The server did not place this node in either list, or placed it in
   * both. Never collapse this into one of the two above. */
  | "unknown";

export interface NodeRollout {
  node: string;
  status: NodeRolloutStatus;
  /** Present for `unknown` only: why we cannot say. */
  note?: string;
}

export interface RolloutView {
  /** The raw server state string, verbatim. */
  state: string;
  /** Whether `state` is one this client recognises. An unrecognised state
   * is reported as unrecognised rather than mapped onto a known one. */
  recognized: boolean;
  /** Paused between stages, waiting for a decision. */
  paused: boolean;
  /** The remaining stage is executing right now. */
  promoting: boolean;
  headline: string;
  /** What the gate is waiting for, in one sentence. */
  gateExplanation: string;
  nodes: NodeRollout[];
  /** True when the server named no nodes at all — the panel must say "we
   * cannot tell you which nodes ran", not draw an empty (i.e. reassuring)
   * list. */
  nodesUnknown: boolean;
  /** Whether `POST /changesets/{id}/continue` would be accepted right now.
   * The server re-checks (409 invalid_transition otherwise); this only
   * decides whether to offer the button. */
  canContinue: boolean;
  holdDeadline?: number;
  confirmDeadline?: number;
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((v) => typeof v === "string");
}

/** `applyStage` if this changeset actually carries one, else undefined.
 * Narrowed at runtime rather than cast: the field is absent on every
 * ordinary apply and on every pre-T-2602 daemon's response. */
export function stagedApplyStateOf(changeset: Changeset): StagedApplyState | undefined {
  const stage = changeset.applyStage;
  if (stage === undefined || typeof stage.state !== "string" || stage.state.length === 0) {
    return undefined;
  }
  return stage;
}

function gateSentence(stage: StagedApplyState, phase: "paused" | "promoting" | "unrecognized"): string {
  if (phase === "promoting") {
    return "The remaining nodes are being applied now. The commit-confirm window opens when they finish.";
  }
  if (phase === "unrecognized") {
    return "This interface cannot say what this stage is waiting for. Check the changeset with `vnproxctl` or the API before acting on it.";
  }
  switch (stage.strategy.gate) {
    case "manual":
      return "Waiting for you: the remaining nodes are applied when you continue. If nobody continues before the commit-confirm deadline, everything applied so far is rolled back.";
    case "auto":
      return "Waiting for the hold to elapse: vnprox promotes automatically only if the canary nodes are healthy and no new error-severity finding is attributable to them. Otherwise it aborts and restores the applied nodes. You can also continue now.";
    default:
      // An absent or unrecognised gate is not silently assumed to be the
      // default one — the operator is told the daemon did not say.
      return "The gate this hold is waiting on was not reported by the server. Continue promotes the remaining nodes; rolling back restores only the nodes already applied.";
  }
}

/**
 * Derives the rollout view for a changeset mid-hold, or undefined when
 * there is no staged apply to show.
 *
 * The node list is the union of what the server reported, ordered
 * applied-then-pending, plus any node the persisted plan touches that the
 * stage did not mention at all — the latter as `unknown`, because a node in
 * a half-applied plan that nothing accounts for is precisely the case that
 * must not disappear from the screen.
 */
export function deriveRollout(changeset: Changeset): RolloutView | undefined {
  const stage = stagedApplyStateOf(changeset);
  if (!stage) return undefined;

  const paused = stage.state === STATE_CANARY_HOLD;
  const promoting = stage.state === STATE_PROMOTING;
  const recognized = paused || promoting;

  const applied = isStringArray(stage.appliedNodes) ? stage.appliedNodes : undefined;
  const pending = isStringArray(stage.pendingNodes) ? stage.pendingNodes : undefined;
  const appliedSet = new Set(applied ?? []);
  const pendingSet = new Set(pending ?? []);

  const nodes: NodeRollout[] = [];
  const seen = new Set<string>();
  const push = (node: string, status: NodeRolloutStatus, note?: string): void => {
    if (seen.has(node)) return;
    seen.add(node);
    nodes.push(note === undefined ? { node, status } : { node, status, note });
  };

  for (const node of applied ?? []) {
    // A node the server put in both lists is a contradiction, not a
    // half-truth to pick the friendlier side of.
    if (pendingSet.has(node)) {
      push(node, "unknown", "The server reported this node as both applied and pending.");
      continue;
    }
    push(node, "done");
  }
  for (const node of pending ?? []) {
    if (appliedSet.has(node)) continue; // already pushed as unknown above
    push(node, "pending");
  }
  // Nodes the plan touches that the stage accounted for nowhere.
  for (const node of affectedNodes(changeset.plan?.steps ?? [])) {
    push(node, "unknown", "This node is in the plan but the server did not report it as applied or pending.");
  }
  // The canary list itself, for the same reason.
  for (const node of stage.strategy.canaryNodes ?? []) {
    push(node, "unknown", "This node was chosen as a canary but the server did not report its stage state.");
  }

  const nodesUnknown = applied === undefined && pending === undefined;

  let headline: string;
  if (!recognized) {
    headline = `This changeset is in an apply stage this version of the interface does not recognise (${stage.state}). Treat it as partially applied until the server says otherwise.`;
  } else if (paused) {
    headline = "Canary hold — the canary nodes have been applied and the remaining nodes have not been contacted.";
  } else {
    headline = "Promoting — the remaining nodes are being applied now.";
  }

  return {
    state: stage.state,
    recognized,
    paused,
    promoting,
    headline,
    gateExplanation: gateSentence(stage, !recognized ? "unrecognized" : paused ? "paused" : "promoting"),
    nodes,
    nodesUnknown,
    canContinue: paused,
    ...(typeof stage.holdDeadline === "number" ? { holdDeadline: stage.holdDeadline } : {}),
    ...(typeof stage.confirmDeadline === "number" ? { confirmDeadline: stage.confirmDeadline } : {}),
  };
}
