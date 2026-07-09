import { useEffect, useMemo, useRef, useState } from "react";
import { ReactFlowProvider, useReactFlow } from "@xyflow/react";
import { EmptyState } from "../components/EmptyState";
import { Button } from "../components/Button";
import { useTopologyShortcutTargetStore } from "../keyboard/topologyShortcutTarget";
import type { Layer, TopologyEdge, TopologyNode } from "../api/types";
import { InspectorPanel } from "./InspectorPanel";
import { LayerToggleBar } from "./LayerToggleBar";
import { SpotlightSearch } from "./SpotlightSearch";
import { TopologyCanvas } from "./TopologyCanvas";
import { VlanFilterInput } from "./VlanFilterInput";
import { computeLayout, type XYPosition } from "./layout";
import { isGuestGroupId } from "./projection";
import {
  useGuestGroupExpandQuery,
  useLayoutQuery,
  useSaveLayoutMutation,
  useTopologyQuery,
  useTopologyWsBridge,
} from "./queries";
import { currentLayoutPayload, useTopologyStore } from "./store";
import { toFlowElements } from "./toFlowElements";

const LAYER_ORDER: readonly Layer[] = ["phys", "l2", "sdn", "guest"];
const SAVE_DEBOUNCE_MS = 1000;
// docs/features/topology.md §4: "Hard render cap ~2,000 visible elements;
// beyond, require a filter (UI prompts)."
const RENDER_CAP = 2000;

/** Invisible data-fetching child: one per currently-expanded guest-group
 * pill (see expand.ts). Rendered as a list of these rather than calling
 * useGuestGroupExpandQuery in a loop directly in TopologyPage, since the
 * number of expanded groups varies at runtime and each needs its own
 * query — a list of child components, each a stable hook call site keyed
 * by groupId, is the standard React way to do a dynamic number of
 * per-item queries. */
function GuestGroupExpansion({
  groupId,
  onData,
}: {
  groupId: string;
  onData: (groupId: string, nodes: TopologyNode[], edges: TopologyEdge[]) => void;
}) {
  const { data } = useGuestGroupExpandQuery(groupId, true);
  useEffect(() => {
    if (data) onData(groupId, data.nodes, data.edges);
  }, [groupId, data, onData]);
  return null;
}

export function TopologyPage() {
  return (
    <ReactFlowProvider>
      <TopologyPageContent />
    </ReactFlowProvider>
  );
}

function TopologyPageContent() {
  const { data: topology, isLoading, isError, dataUpdatedAt } = useTopologyQuery();
  useTopologyWsBridge();
  const reactFlow = useReactFlow();

  const activeLayers = useTopologyStore((s) => s.activeLayers);
  const vlanFilter = useTopologyStore((s) => s.vlanFilter);
  const selectedId = useTopologyStore((s) => s.selectedId);
  const hoveredId = useTopologyStore((s) => s.hoveredId);
  const spotlightOpen = useTopologyStore((s) => s.spotlightOpen);
  const expandedGroups = useTopologyStore((s) => s.expandedGroups);
  const positions = useTopologyStore((s) => s.positions);
  const toggleLayer = useTopologyStore((s) => s.toggleLayer);
  const setVlanFilter = useTopologyStore((s) => s.setVlanFilter);
  const select = useTopologyStore((s) => s.select);
  const hover = useTopologyStore((s) => s.hover);
  const setSpotlightOpen = useTopologyStore((s) => s.setSpotlightOpen);
  const toggleExpanded = useTopologyStore((s) => s.toggleExpanded);
  const setPosition = useTopologyStore((s) => s.setPosition);
  const hydrateFromLayout = useTopologyStore((s) => s.hydrateFromLayout);

  const vlanInputRef = useRef<HTMLInputElement>(null);

  // --- Saved layout: load once on mount, save (debounced) on change ------
  const { data: savedLayout } = useLayoutQuery();
  const saveLayoutMutation = useSaveLayoutMutation();
  const hydratedRef = useRef(false);
  useEffect(() => {
    if (hydratedRef.current || savedLayout === undefined) return;
    hydratedRef.current = true;
    hydrateFromLayout(savedLayout);
  }, [savedLayout, hydrateFromLayout]);

  useEffect(() => {
    if (!hydratedRef.current) return; // don't save back what we just loaded
    const state = useTopologyStore.getState();
    const payload = currentLayoutPayload(state);
    const timer = setTimeout(() => {
      saveLayoutMutation.mutate(payload);
    }, SAVE_DEBOUNCE_MS);
    return () => {
      clearTimeout(timer);
    };
    // Re-runs whenever the persisted subset of state changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [positions, activeLayers, vlanFilter]);

  // --- Register the real keyboard-shortcut handlers for this page -------
  const setShortcutTarget = useTopologyShortcutTargetStore((s) => s.setTarget);
  useEffect(() => {
    setShortcutTarget({
      toggleLayer,
      openVlanFilter: () => {
        vlanInputRef.current?.focus();
      },
      openSearch: () => {
        setSpotlightOpen(true);
      },
    });
    return () => {
      setShortcutTarget(null);
    };
  }, [toggleLayer, setSpotlightOpen, setShortcutTarget]);

  // --- Auto-layout (elkjs) recomputed whenever the node/edge set changes -
  const [layoutPositions, setLayoutPositions] = useState<Map<string, XYPosition>>(new Map());
  const layoutSignature = topology
    ? `${String(topology.nodes.length)}:${String(topology.edges.length)}:${topology.nodes.map((n) => n.id).join(",")}`
    : "";
  useEffect(() => {
    if (!topology) return;
    let cancelled = false;
    void computeLayout(topology.nodes, topology.edges).then((result) => {
      if (!cancelled) setLayoutPositions(result);
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on layoutSignature, not `topology` itself
  }, [layoutSignature]);

  // --- Expanded guest-group pills: one query per expanded pill ----------
  const [expandedData, setExpandedData] = useState<Record<string, { nodes: TopologyNode[]; edges: TopologyEdge[] }>>(
    {},
  );
  const handleExpandedData = (groupId: string, nodes: TopologyNode[], edges: TopologyEdge[]) => {
    setExpandedData((prev) => ({ ...prev, [groupId]: { nodes, edges } }));
  };
  const extraNodes = useMemo(
    () => Array.from(expandedGroups).flatMap((id) => expandedData[id]?.nodes ?? []),
    [expandedGroups, expandedData],
  );
  const extraEdges = useMemo(
    () => Array.from(expandedGroups).flatMap((id) => expandedData[id]?.edges ?? []),
    [expandedGroups, expandedData],
  );

  const elements = useMemo(
    () =>
      toFlowElements({
        nodes: topology?.nodes ?? [],
        edges: topology?.edges ?? [],
        extraNodes,
        extraEdges,
        expandedGroups,
        activeLayers,
        vlanFilter,
        hoveredId,
        selectedId,
        layoutPositions,
        manualPositions: positions,
      }),
    [topology, extraNodes, extraEdges, expandedGroups, activeLayers, vlanFilter, hoveredId, selectedId, layoutPositions, positions],
  );

  const overCap = elements.nodes.length + elements.edges.length > RENDER_CAP;

  function handleNodeClick(id: string): void {
    if (isGuestGroupId(id)) {
      toggleExpanded(id);
      return;
    }
    select(id);
  }

  function handleSearchSelect(ref: string): void {
    select(ref);
    const flowNode = elements.nodes.find((n) => n.id === ref);
    if (flowNode) {
      void reactFlow.fitView({ nodes: [{ id: ref }], duration: 500, maxZoom: 1.25 });
    }
  }

  const noLldpData = topology ? !topology.nodes.some((n) => n.kind === "lldp-neighbor") : false;

  return (
    <div className="flex h-full flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">Topology</h1>
        <div className="flex flex-wrap items-center gap-2">
          <LayerToggleBar activeLayers={activeLayers} onToggle={toggleLayer} layerOrder={LAYER_ORDER} />
          <VlanFilterInput ref={vlanInputRef} value={vlanFilter} onChange={setVlanFilter} />
          <Button
            size="sm"
            variant="secondary"
            onClick={() => {
              setSpotlightOpen(true);
            }}
          >
            Search ( / )
          </Button>
        </div>
      </div>

      {noLldpData && (
        <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
          No LLDP data yet — the physical layer shows NICs only.{" "}
          <a href="https://man7.org/linux/man-pages/man8/lldpd.8.html" className="underline" target="_blank" rel="noreferrer">
            Set up lldpd
          </a>{" "}
          to see real switch names and ports.
        </div>
      )}

      {overCap && (
        <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
          This cluster has {elements.nodes.length + elements.edges.length} visible elements — above the ~2,000
          render cap (docs/features/topology.md §4). Use the VLAN filter or a layer toggle to narrow the view.
        </div>
      )}

      <div className="min-h-0 flex-1 rounded-lg border border-slate-200 dark:border-slate-800">
        {isLoading && (
          <div className="flex h-full items-center justify-center">
            <EmptyState title="Loading the map…" description="Fetching the current cluster topology." />
          </div>
        )}
        {isError && (
          <div className="flex h-full items-center justify-center">
            <EmptyState
              title="Could not load the topology"
              description="Check that vnproxd is reachable and try reloading."
            />
          </div>
        )}
        {!isLoading && !isError && topology && topology.nodes.length === 0 && (
          <div className="flex h-full items-center justify-center">
            <EmptyState
              title="Nothing discovered yet"
              description="vnproxd hasn't polled any inventory yet — check node connectivity, or wait for the next poll cycle."
            />
          </div>
        )}
        {!isLoading && !isError && topology && topology.nodes.length > 0 && (
          <TopologyCanvas
            elements={elements}
            onNodeClick={handleNodeClick}
            onNodeHover={hover}
            onNodeDragStop={(id, pos) => {
              setPosition(id, pos);
            }}
            onPaneClick={() => {
              select(undefined);
            }}
          />
        )}
      </div>

      {Array.from(expandedGroups).map((id) => (
        <GuestGroupExpansion key={id} groupId={id} onData={handleExpandedData} />
      ))}

      <SpotlightSearch open={spotlightOpen} onOpenChange={setSpotlightOpen} onSelect={handleSearchSelect} />
      <InspectorPanel
        selectedRef={selectedId && !isGuestGroupId(selectedId) ? selectedId : undefined}
        onClose={() => {
          select(undefined);
        }}
        onSelectRelated={(ref) => {
          select(ref);
        }}
      />

      <p className="sr-only" aria-live="polite">
        Topology last updated {new Date(dataUpdatedAt).toLocaleTimeString()}
      </p>
    </div>
  );
}
