// SPDX-License-Identifier: Apache-2.0

// Pure drag-to-reorder state transitions for the rule table (docs/features/
// firewall.md §2: "drag-to-reorder ... reorders are fw.rule.move ops").
// Framework-free (no React import), mirroring web/src/changesets/
// dragDropOps.ts's "translate a drag gesture into an Op" pattern — the
// actual HTML5 drag event wiring lives in the RuleTable component; this
// module only answers "given rules and a from/to index, what's the
// resulting array and the op that produces it".
import type { Op, RuleView } from "../api/types";
import { buildFwRuleMoveOp } from "./opBuilders";

export interface ReorderResult {
  op: Op;
  /** The rules array as it will read once the move op applies — pos
   * renumbered contiguously — so the UI can render the new order
   * immediately instead of waiting for a round trip. */
  optimistic: RuleView[];
}

/**
 * Computes the fw.rule.move op (and its optimistic result) for dragging
 * the rule at `fromIndex` so it ends up at `toIndex` in the resulting
 * array (both are array indices into `rules`, which must already be
 * sorted by `pos`; `toIndex` is the item's desired *final* index, matching
 * fw.rule.move's own ToPos semantics — internal/pvemock's moveFwRule
 * inserts at exactly this index after removing the source item).
 *
 * Returns undefined for a no-op drag: equal indices, or either index out
 * of `rules`' bounds — callers should treat undefined as "nothing to
 * stage", not an error.
 */
export function computeReorderMove(target: string, rules: RuleView[], fromIndex: number, toIndex: number): ReorderResult | undefined {
  if (fromIndex === toIndex) return undefined;
  if (fromIndex < 0 || fromIndex >= rules.length) return undefined;
  if (toIndex < 0 || toIndex >= rules.length) return undefined;

  const moved = rules[fromIndex];
  if (!moved) return undefined;

  const rest = rules.filter((_, i) => i !== fromIndex);
  const reinserted = [...rest.slice(0, toIndex), moved, ...rest.slice(toIndex)];
  const optimistic = reinserted.map((r, i) => ({ ...r, pos: i }));

  return { op: buildFwRuleMoveOp(target, moved, toIndex), optimistic };
}
