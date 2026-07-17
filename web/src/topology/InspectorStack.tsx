// T-908: the container TopologyPage mounts instead of a single
// InspectorPanel — owns the pinned/compare pane list (inspectorStack.ts's
// pure reducer) and decides layout:
//   0 panes            -> nothing.
//   1 unpinned pane     -> the original single modal InspectorPanel,
//                          unchanged look and behavior (Escape/overlay-click
//                          to close, as before T-908).
//   1 pinned pane, or
//   2+ panes            -> a non-modal side-by-side row (2 same-kind panes
//                          get InspectorCompareView's aligned layout;
//                          everything else is plain independent embedded
//                          panes). Non-modal is required the moment
//                          anything is pinned: the whole point of pinning
//                          is to keep working elsewhere (canvas, spotlight
//                          search) with the pane still open, which a modal
//                          Dialog's full-screen overlay would block.
// selectedRef is the topology store's last-clicked ref (canvas/search/FDB
// owner click/etc — the same signal that drives canvas selection
// highlighting); this container watches it and, per selectPane's rules,
// either replaces the whole stack (nothing pinned — pre-T-908 behavior) or
// adds an additional pane (something pinned).
import { useEffect, useState } from "react";
import type { WsClient } from "../api/ws";
import { InspectorCompareView } from "./InspectorCompareView";
import { InspectorPanel } from "./InspectorPanel";
import { closePane, selectPane, togglePin, type InspectorPane } from "./inspectorStack";

export interface InspectorStackProps {
  /** The topology store's selectedId — undefined only ever comes from an
   * explicit deselect (e.g. clicking empty canvas), which this container
   * intentionally ignores: pane close/pin state is owned locally, not
   * mirrored 1:1 from the store. */
  selectedRef: string | undefined;
  /** Fires once the pane list goes from non-empty to empty, so the caller
   * can clear the store's selection (canvas/switch-view highlight) to
   * match. */
  onAllClosed: () => void;
  /** Injectable WS client for every pane's Metrics tab — tests only. */
  metricsWsClient?: WsClient;
}

export function InspectorStack({ selectedRef, onAllClosed, metricsWsClient }: InspectorStackProps) {
  const [panes, setPanes] = useState<InspectorPane[]>([]);

  useEffect(() => {
    if (selectedRef === undefined) return;
    setPanes((prev) => selectPane(prev, selectedRef));
  }, [selectedRef]);

  function handleSelectRelated(ref: string): void {
    setPanes((prev) => selectPane(prev, ref));
  }

  function handleClose(ref: string): void {
    setPanes((prev) => {
      const next = closePane(prev, ref);
      if (next.length === 0) onAllClosed();
      return next;
    });
  }

  function handleTogglePin(ref: string): void {
    setPanes((prev) => togglePin(prev, ref));
  }

  if (panes.length === 0) return null;

  const [firstPane, secondPane] = panes;

  if (panes.length === 1 && firstPane && !firstPane.pinned) {
    return (
      <InspectorPanel
        selectedRef={firstPane.ref}
        onClose={() => {
          handleClose(firstPane.ref);
        }}
        onSelectRelated={handleSelectRelated}
        metricsWsClient={metricsWsClient}
        pinned={firstPane.pinned}
        onTogglePin={() => {
          handleTogglePin(firstPane.ref);
        }}
      />
    );
  }

  return (
    // Bottom-right-anchored with a bounded max-height — the same pattern
    // ChangesetDrawer.tsx uses (see that file / AppShell.tsx's doc comment:
    // a `fixed inset-y-0` overlay spanning the *full* viewport height
    // collides with the page's own top-row controls, e.g. the "Search"
    // button TopologyPage's toolbar renders — fine for the single modal
    // Drawer case above, since a modal blocks all other interaction
    // anyway, but fatal here: this region is deliberately non-modal so the
    // Search button stays clickable for the next selection.
    <div
      role="region"
      aria-label={`Inspector panes (${String(panes.length)})`}
      className="fixed bottom-4 right-4 z-30 flex max-h-[80vh] max-w-full items-stretch gap-3 overflow-x-auto rounded-lg border border-slate-200 bg-white/95 p-4 shadow-xl backdrop-blur dark:border-slate-700 dark:bg-slate-900/95"
    >
      {panes.length === 2 && firstPane && secondPane ? (
        <InspectorCompareView
          refA={firstPane.ref}
          refB={secondPane.ref}
          pinnedA={firstPane.pinned}
          pinnedB={secondPane.pinned}
          onCloseA={() => {
            handleClose(firstPane.ref);
          }}
          onCloseB={() => {
            handleClose(secondPane.ref);
          }}
          onTogglePinA={() => {
            handleTogglePin(firstPane.ref);
          }}
          onTogglePinB={() => {
            handleTogglePin(secondPane.ref);
          }}
          onSelectRelated={handleSelectRelated}
          metricsWsClient={metricsWsClient}
        />
      ) : (
        panes.map((pane) => (
          <InspectorPanel
            key={pane.ref}
            embedded
            selectedRef={pane.ref}
            onClose={() => {
              handleClose(pane.ref);
            }}
            onSelectRelated={handleSelectRelated}
            metricsWsClient={metricsWsClient}
            pinned={pane.pinned}
            onTogglePin={() => {
              handleTogglePin(pane.ref);
            }}
          />
        ))
      )}
    </div>
  );
}
