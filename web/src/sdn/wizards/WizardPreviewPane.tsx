// SPDX-License-Identifier: Apache-2.0

// The wizard's live topology preview: the REAL map components
// (TopologyCanvas + toFlowElements + computeLayout, exactly what
// TopologyPage.tsx itself uses) rendering synthetic entities from
// previewEntities.ts — never a separate mini-renderer (T-403's task
// brief). Debounced (useDebouncedValue) so a rapid burst of wizard field
// edits settles to one recompute rather than re-laying-out on every
// keystroke.
import { useEffect, useMemo, useState } from "react";
import { ALL_LAYERS } from "../../api/types";
import { EmptyState } from "../../components/EmptyState";
import { computeLayout, type XYPosition } from "../../topology/layout";
import { TopologyCanvas } from "../../topology/TopologyCanvas";
import { toFlowElements } from "../../topology/toFlowElements";
import type { PreviewGraph } from "./previewEntities";
import { wizardStrings } from "./strings";
import { PREVIEW_DEBOUNCE_MS, useDebouncedValue } from "./useDebouncedValue";

const ACTIVE_LAYERS = new Set(ALL_LAYERS);
const NO_EXPANDED = new Set<string>();
const NO_MANUAL_POSITIONS = {};

export interface WizardPreviewPaneProps {
  graph: PreviewGraph;
  /** Overridable for tests (WizardPreviewPane.test.tsx asserts the
   * debounce timing directly against this prop rather than a hardcoded
   * import, keeping the test independent of the production constant). */
  debounceMs?: number;
}

export function WizardPreviewPane({ graph, debounceMs = PREVIEW_DEBOUNCE_MS }: WizardPreviewPaneProps) {
  const debounced = useDebouncedValue(graph, debounceMs);
  const [layoutPositions, setLayoutPositions] = useState<Map<string, XYPosition>>(new Map());
  const [hoveredId, setHoveredId] = useState<string | undefined>();

  const signature = `${String(debounced.nodes.length)}:${String(debounced.edges.length)}:${debounced.nodes.map((n) => n.id).join(",")}`;
  useEffect(() => {
    if (debounced.nodes.length === 0) {
      setLayoutPositions(new Map());
      return;
    }
    let cancelled = false;
    void computeLayout(debounced.nodes, debounced.edges).then((result) => {
      if (!cancelled) setLayoutPositions(result);
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on signature, not `debounced` itself
  }, [signature]);

  const elements = useMemo(
    () =>
      toFlowElements({
        nodes: debounced.nodes,
        edges: debounced.edges,
        expandedGroups: NO_EXPANDED,
        activeLayers: ACTIVE_LAYERS,
        hoveredId,
        layoutPositions,
        manualPositions: NO_MANUAL_POSITIONS,
      }),
    [debounced, hoveredId, layoutPositions],
  );

  if (debounced.nodes.length === 0) {
    return <EmptyState title="Preview" description={wizardStrings.common.previewEmpty} className="h-full" />;
  }

  return (
    <div className="h-full min-h-[22rem] overflow-hidden rounded-lg border border-slate-200 dark:border-slate-800">
      <TopologyCanvas
        elements={elements}
        onNodeClick={() => {
          /* Preview nodes are synthetic — nothing to open/inspect. */
        }}
        onNodeHover={setHoveredId}
        onNodeDragStop={() => {
          /* Manual positioning makes no sense for a not-yet-real preview. */
        }}
        onPaneClick={() => {
          setHoveredId(undefined);
        }}
      />
    </div>
  );
}
