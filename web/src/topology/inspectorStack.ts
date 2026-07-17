// T-908: pure pane-selection logic for the pinnable multi-inspector stack
// (InspectorStack.tsx). Kept in its own module, out of the React
// component, so the container's core state-transition rules — the task
// card's "pinning keeps an inspector open ... selecting a new entity opens
// an additional (not replacing) inspector, up to a reasonable cap" — are
// directly unit-testable without mounting anything.

export interface InspectorPane {
  ref: string;
  pinned: boolean;
}

/** Reasonable cap on simultaneously open inspector panes (task card: "up to
 * a reasonable cap"). Beyond this, further additive selections are dropped
 * rather than silently evicting a pinned pane the user is actively
 * comparing against. */
export const MAX_INSPECTOR_PANES = 4;

/**
 * Applies a new selection (a canvas/search/related click) to the current
 * pane list:
 * - Already open: no-op (never duplicates a pane for the same ref).
 * - Nothing pinned: replaces the whole stack with just the new selection —
 *   this is the pre-T-908 single-inspector behavior, preserved exactly for
 *   a user who never pins anything.
 * - Something pinned, under the cap: appends the new ref as an additional
 *   unpinned pane (InspectorStack decides how to lay out 2+ panes —
 *   side-by-side compare for two same-kind entities, plain side-by-side
 *   panes otherwise).
 * - Something pinned, at the cap: evicts the single oldest unpinned pane to
 *   make room, if one exists; if every pane at the cap is pinned, the new
 *   selection is dropped rather than evicting a pin.
 */
export function selectPane(panes: readonly InspectorPane[], ref: string): InspectorPane[] {
  if (panes.some((p) => p.ref === ref)) return [...panes];

  const anyPinned = panes.some((p) => p.pinned);
  if (!anyPinned) {
    return [{ ref, pinned: false }];
  }
  if (panes.length >= MAX_INSPECTOR_PANES) {
    const unpinnedIdx = panes.findIndex((p) => !p.pinned);
    if (unpinnedIdx === -1) return [...panes];
    const next = panes.filter((_, i) => i !== unpinnedIdx);
    return [...next, { ref, pinned: false }];
  }
  return [...panes, { ref, pinned: false }];
}

/** Removes one pane by ref (its own Close button) — never touches the
 * others, pinned or not. */
export function closePane(panes: readonly InspectorPane[], ref: string): InspectorPane[] {
  return panes.filter((p) => p.ref !== ref);
}

/** Flips one pane's pinned flag in place, preserving pane order. */
export function togglePin(panes: readonly InspectorPane[], ref: string): InspectorPane[] {
  return panes.map((p) => (p.ref === ref ? { ...p, pinned: !p.pinned } : p));
}
