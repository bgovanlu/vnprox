// Pure helpers for the walkthrough's step 2 ("Protected interfaces" —
// docs/user-guide.md §1.2). The step renders one checkbox per node/ref pair
// vnprox detected (GET /protected-interfaces/suggest), pre-checked, plus
// any already-confirmed refs a prior run of the walkthrough (or a direct
// PUT) saved that the detector no longer suggests — the union of both, so
// confirming never silently drops a previously-protected ref the detector
// happens to miss this time. Unchecking a ref is the "correct" half of
// "confirm or correct" (docs/features/blueprints.md §3); there is no
// separate "add a ref the detector missed" affordance in this first cut —
// out of scope for what AC1/AC2 ask for, flagged in the task report.
import type { ProtectedInterfacesResponse, ProtectedInterfacesSuggestResponse } from "../api/types";

/** node -> the set of refs currently checked in the confirmation UI. */
export type ProtectedDraft = Record<string, string[]>;

function dedupSorted(refs: string[]): string[] {
  return [...new Set(refs)].sort();
}

/** Builds the initial draft: the union, per node, of the detector's
 * suggestion and whatever was already confirmed before (if anything) —
 * so a re-run of the walkthrough never loses a manually-added ref the
 * current detection pass doesn't happen to redetect. Both inputs are
 * optional so the component can call this before either query has
 * resolved (an in-flight query renders an empty draft, not a crash). */
export function draftFromSuggestion(
  suggest: ProtectedInterfacesSuggestResponse | undefined,
  existing: ProtectedInterfacesResponse | undefined,
): ProtectedDraft {
  const draft: ProtectedDraft = {};
  const nodes = new Set([...Object.keys(suggest?.nodes ?? {}), ...Object.keys(existing?.nodes ?? {})]);
  for (const node of nodes) {
    draft[node] = dedupSorted([...(suggest?.nodes[node] ?? []), ...(existing?.nodes[node] ?? [])]);
  }
  return draft;
}

/** Whether `ref` is currently checked for `node` in `draft`. */
export function isRefSelected(draft: ProtectedDraft, node: string, ref: string): boolean {
  return (draft[node] ?? []).includes(ref);
}

/** Toggles one ref for one node, returning a new draft (never mutates the
 * input — every draft-consuming component re-renders off the returned
 * value, matching every other pure-reducer helper in this codebase). */
export function toggleRef(draft: ProtectedDraft, node: string, ref: string): ProtectedDraft {
  const current = draft[node] ?? [];
  const next = current.includes(ref) ? current.filter((r) => r !== ref) : dedupSorted([...current, ref]);
  return { ...draft, [node]: next };
}

/** Total number of checked refs across every node — used for the "N
 * interfaces selected" summary line and to gate the Confirm button on at
 * least one selection. */
export function selectedCount(draft: ProtectedDraft): number {
  return Object.values(draft).reduce((sum, refs) => sum + refs.length, 0);
}

/** Drops nodes with an empty ref list before PUTting — an onboarding-run
 * draft that started with a suggestion for a node the user then unchecked
 * entirely should not send `{"<node>": []}` (indistinguishable on the wire
 * from "confirmed, and deliberately protects nothing on this node", which
 * change.DetectProtected's safety interlocks would then honor literally). */
export function draftToRequestNodes(draft: ProtectedDraft): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const [node, refs] of Object.entries(draft)) {
    if (refs.length > 0) out[node] = refs;
  }
  return out;
}
