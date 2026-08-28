// SPDX-License-Identifier: Apache-2.0

// The product's own three-way vocabulary — spec, config, live — and the
// phrasings the cockpit renders it with.
//
// The daemon reports three positions per entity (docs/api.md's
// `spec_reconciliation` paragraph):
//
//   spec    the declarative document — what the cluster is supposed to be
//   config  /etc/network/interfaces as PVE reports it — what it will be
//           after the next reload
//   live    the running kernel — what it is right now
//
// Two distinctions in that data are load-bearing and are preserved here
// rather than flattened for display:
//
//   * `known: false` on a field value means that position never reported the
//     field. It is not an empty value, and it is not agreement.
//   * `comparable: false` on a pair means the two positions shared no field
//     either of them reported. "They agree" and "there was nothing to
//     compare" are different statements.
import type { FieldPositions, PairDiff, PositionValue, Reconciliation, SpecPosition } from "../api/types";

export const POSITIONS: readonly SpecPosition[] = ["spec", "config", "live"] as const;

export const POSITION_LABEL: Record<SpecPosition, string> = {
  spec: "Spec",
  config: "Config",
  live: "Live",
};

export const POSITION_MEANING: Record<SpecPosition, string> = {
  spec: "the declarative document — what this cluster is supposed to be",
  config: "/etc/network/interfaces as PVE reports it — what it will be after the next reload",
  live: "the running kernel — what it is right now",
};

/** How a position answers "do you have this entity at all". */
export function presenceLabel(position: SpecPosition, present: boolean): string {
  if (present) {
    return position === "spec" ? "declared" : "present";
  }
  return position === "spec" ? "not declared" : "absent";
}

/** One field's value at one position, or `undefined` when that position is
 * missing from the list entirely (which the daemon does not do today, and
 * which is treated exactly like `known: false` if it ever did). */
export function valueAt(field: FieldPositions, position: SpecPosition): PositionValue | undefined {
  return field.values.find((v) => v.position === position);
}

/** Display text for one cell, plus whether it is a real value. Callers style
 * `known: false` differently — it must never look like a value. */
export function cellText(value: PositionValue | undefined): { text: string; known: boolean } {
  if (value?.known !== true) {
    return { text: "not reported", known: false };
  }
  return { text: value.value === "" ? "(empty)" : value.value, known: true };
}

/** The pair heading, e.g. "Spec vs Live". */
export function pairLabel(pair: PairDiff): string {
  return `${POSITION_LABEL[pair.a]} vs ${POSITION_LABEL[pair.b]}`;
}

/** What one pairwise comparison actually says. All three pairs are rendered,
 * including the agreeing ones — that a pair agrees is often the fact that
 * identifies which position is the odd one out. */
export function pairSummary(pair: PairDiff): string {
  if (!pair.comparable) {
    return "nothing to compare — neither position reported a field the other did";
  }
  if (pair.fields.length === 0) {
    return "agree on every field both reported";
  }
  return `differ on ${pair.fields.join(", ")}`;
}

/** Which position is the odd one out, when exactly one pair agrees and the
 * other two do not — the question an operator opens this screen with.
 * `undefined` when the shape does not resolve to a single answer. */
export function oddPositionOut(rec: Reconciliation): SpecPosition | undefined {
  const comparable = rec.pairs.filter((p) => p.comparable);
  if (comparable.length !== 3) {
    return undefined;
  }
  const agreeing = comparable.filter((p) => p.fields.length === 0);
  if (agreeing.length !== 1) {
    return undefined;
  }
  const [pair] = agreeing;
  if (pair === undefined) {
    return undefined;
  }
  return POSITIONS.find((p) => p !== pair.a && p !== pair.b);
}

/** `POST /spec/import`'s `ops` count as a sentence. Rendered separately from
 * the `notInSpec` sentence below because they answer different questions and a
 * clean plan has to state both — "0 ops" alone would leave an operator unsure
 * whether undeclared entities were even looked for. */
export function opsSummary(count: number): string {
  if (count === 0) {
    return "No operations: live state already matches everything this document declares.";
  }
  return count === 1
    ? "1 operation would bring live state to what this document declares."
    : `${String(count)} operations would bring live state to what this document declares.`;
}

/** `POST /spec/import`'s `notInSpec` count as a sentence. These entities are
 * reported, never deleted — import has no prune path. */
export function notInSpecSummary(count: number): string {
  if (count === 0) {
    return "Nothing undeclared: every managed entity this cluster has appears in the document.";
  }
  return count === 1
    ? "1 entity exists on the cluster without appearing in this document. It is reported, never deleted."
    : `${String(count)} entities exist on the cluster without appearing in this document. They are reported, never deleted.`;
}
