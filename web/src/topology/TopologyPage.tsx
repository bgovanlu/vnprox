import { useEffect, useMemo, useRef, useState } from "react";
import { ReactFlowProvider, useReactFlow } from "@xyflow/react";
import { useNavigate } from "react-router-dom";
import { EmptyState } from "../components/EmptyState";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import { useSession } from "../api/useSession";
import { useTopologyShortcutTargetStore } from "../keyboard/topologyShortcutTarget";
import type { Layer, TopologyEdge, TopologyNode } from "../api/types";
import { capsForNode } from "../changesets/capabilities";
import { computeDragOp } from "../changesets/dragDropOps";
import { EditorLauncher } from "../changesets/EditorLauncher";
import { refNode, summarizeOp } from "../changesets/opSummary";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { isTraceableEntityKind, traceFromPath, traceToExternalPath, traceToPath } from "../simulator/traceLink";
import { ContextMenu, type ContextMenuItem } from "./ContextMenu";
import { InspectorPanel } from "./InspectorPanel";
import { NewEntityMenu } from "./NewEntityMenu";
import { LayerToggleBar } from "./LayerToggleBar";
import { METRICS_KINDS } from "./metricsKinds";
import { useLiveMetrics, utilizationMap } from "./metricsQueries";
import { SpotlightSearch } from "./SpotlightSearch";
import { StalenessBanner } from "./StalenessBanner";
import { summarizeStaleness } from "./staleness";
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
  const { data: session } = useSession();
  const { toast } = useToast();
  const { addOps, replaceOps } = useDrawerActions();

  const activeLayers = useTopologyStore((s) => s.activeLayers);
  const vlanFilter = useTopologyStore((s) => s.vlanFilter);
  const selectedId = useTopologyStore((s) => s.selectedId);
  const hoveredId = useTopologyStore((s) => s.hoveredId);
  const spotlightOpen = useTopologyStore((s) => s.spotlightOpen);
  const expandedGroups = useTopologyStore((s) => s.expandedGroups);
  const positions = useTopologyStore((s) => s.positions);
  const trafficMode = useTopologyStore((s) => s.trafficMode);
  const toggleTrafficMode = useTopologyStore((s) => s.toggleTrafficMode);
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

  // §5 staleness: grey the bands whose node-scoped collector data is stale
  // (cluster-wide staleness is the banner's job — see StalenessBanner).
  const staleSummary = useMemo(() => summarizeStaleness(topology?.staleness), [topology?.staleness]);

  // Traffic paint mode (docs/features/monitoring.md §1): only subscribes to
  // refs that could ever have live data, and only while the mode is on —
  // per docs/api.md, a client should stream just the refs it needs, not
  // everything.
  const metricsCandidateRefs = useMemo(
    () => (topology?.nodes ?? []).filter((n) => METRICS_KINDS.has(n.kind)).map((n) => n.id),
    [topology],
  );
  const liveMetrics = useLiveMetrics(metricsCandidateRefs, trafficMode);
  const utilizationByRef = useMemo(() => utilizationMap(liveMetrics), [liveMetrics]);

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
        staleNodeGroups: staleSummary.staleNodeGroups,
        layoutPositions,
        manualPositions: positions,
        trafficMode,
        utilizationByRef,
      }),
    [
      topology,
      extraNodes,
      extraEdges,
      expandedGroups,
      activeLayers,
      vlanFilter,
      hoveredId,
      selectedId,
      staleSummary,
      layoutPositions,
      positions,
      trafficMode,
      utilizationByRef,
    ],
  );

  const overCap = elements.nodes.length + elements.edges.length > RENDER_CAP;
  const navigate = useNavigate();

  // --- "Trace path" (T-504 AC5): right-click on the canvas or the -------
  // inspector's own quick action, both build the same three items via
  // traceLink.ts (only guest-nic entities are traceable — see that
  // module's doc comment) and navigate to the Path simulator pre-filled.
  const [contextMenu, setContextMenu] = useState<{ id: string; x: number; y: number } | undefined>(undefined);

  function traceItemsFor(kind: string, ref: string): ContextMenuItem[] {
    if (!isTraceableEntityKind(kind)) return [];
    const items: ContextMenuItem[] = [];
    const fromPath = traceFromPath(kind, ref);
    const toPath = traceToPath(kind, ref);
    const toExternalPath = traceToExternalPath(kind, ref);
    if (fromPath) {
      items.push({ label: "Trace path from here", onSelect: () => { void navigate(fromPath); } });
    }
    if (toPath) {
      items.push({ label: "Trace path to here", onSelect: () => { void navigate(toPath); } });
    }
    if (toExternalPath) {
      items.push({ label: "Trace path to external", onSelect: () => { void navigate(toExternalPath); } });
    }
    return items;
  }

  function handleNodeContextMenu(id: string, clientX: number, clientY: number): void {
    const node = topology?.nodes.find((n) => n.id === id);
    if (!node || traceItemsFor(node.kind, id).length === 0) {
      setContextMenu(undefined);
      return;
    }
    setContextMenu({ id, x: clientX, y: clientY });
  }

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

  /**
   * Map drag-drop edits (docs/features/topology.md §2 / T-207 acceptance
   * criterion 1): drop a NIC on a bond/bridge, or a guest NIC on a
   * bridge/VNet, to draft the corresponding op. The node's *visual*
   * position is never persisted for a drag that produced an op — nothing
   * has actually moved in the real topology yet, only a change was
   * drafted — so this always falls through to a plain `setPosition` only
   * when no recognized drag-drop op applies (ordinary manual repositioning).
   * "snap-back on validation failure": if the freshly-drafted op turns out
   * to carry an error-severity finding for its own target, it's removed
   * from the draft again and the user is told why.
   */
  function handleNodeDragStop(id: string, pos: XYPosition): void {
    if (!topology) {
      setPosition(id, pos);
      return;
    }
    const dragged = topology.nodes.find((n) => n.id === id);
    if (!dragged) {
      setPosition(id, pos);
      return;
    }
    // Intersection is computed from the drop *rectangle* built from `pos`
    // (the drag-stop position React Flow reports), NOT `getIntersectingNodes
    // ({id})`: the canvas runs React Flow with fully controlled nodes and a
    // no-op onNodesChange (see TopologyCanvas), so the store's copy of the
    // dragged node never moves during a drag — querying by id would test the
    // node's *original* column, never the drop target. Querying by an
    // explicit rect at `pos` uses where the user actually dropped it.
    // partially=true: same-size chips rarely fully contain each other, so
    // full-containment mode would miss most drops. When several nodes
    // overlap the drop, pick the one whose position is nearest `pos` — the
    // node the user most plausibly aimed at.
    const droppedFlowNode = reactFlow.getNode(id);
    const width = droppedFlowNode?.measured?.width ?? droppedFlowNode?.width ?? 150;
    const height = droppedFlowNode?.measured?.height ?? droppedFlowNode?.height ?? 40;
    const dropRect = { x: pos.x, y: pos.y, width, height };
    const targetFlow = reactFlow
      .getIntersectingNodes(dropRect, true)
      .filter((n) => n.id !== id)
      .sort((a, b) => {
        const dist = (p: { x: number; y: number }) => (p.x - pos.x) ** 2 + (p.y - pos.y) ** 2;
        return dist(a.position) - dist(b.position);
      })[0];
    const target = targetFlow ? topology.nodes.find((n) => n.id === targetFlow.id) : undefined;
    const op = target ? computeDragOp(dragged, target, topology) : undefined;

    if (!op) {
      setPosition(id, pos);
      return;
    }
    const opNode = op.target ? refNode(op.target) : "";
    if (!op.target || !capsForNode(session, opNode === "" ? dragged.nodeGroup : opNode).netWrite) {
      toast({ title: "Read-only", description: "You don't have network-write on this node.", variant: "error" });
      setPosition(id, pos);
      return;
    }
    void addOps([op], `Map edit: ${summarizeOp(op)}`)
      .then((updated) => {
        const errored = updated.findings.some((f) => f.severity === "error" && f.ref === op.target);
        if (errored) {
          // addOps appends — the just-drafted (invalid) op is always the
          // last entry, so reverting it is a simple pop rather than a
          // reference-equality filter (the op the server echoes back is a
          // distinct, re-serialized object, not the same reference).
          void replaceOps(updated.ops.slice(0, -1)).then(() => {
            toast({ title: "Drag reverted", description: "That change would be invalid — see the drawer for details.", variant: "error" });
          });
          return;
        }
        toast({ title: "Added to changeset", description: summarizeOp(op) });
      })
      .catch(() => {
        toast({ title: "Could not draft that change", variant: "error" });
      });
    // Deliberately do not call setPosition: the entity hasn't actually
    // moved yet (nothing applies until Apply), so the node stays at its
    // real, computed layout position rather than wherever it was dropped.
  }

  const noLldpData = topology ? !topology.nodes.some((n) => n.kind === "lldp-neighbor") : false;

  return (
    <div className="flex h-full flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">Topology</h1>
        <div className="flex flex-wrap items-center gap-2">
          <LayerToggleBar activeLayers={activeLayers} onToggle={toggleLayer} layerOrder={LAYER_ORDER} />
          <Button
            size="sm"
            variant={trafficMode ? "primary" : "secondary"}
            aria-pressed={trafficMode}
            onClick={toggleTrafficMode}
          >
            Traffic
          </Button>
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
          <NewEntityMenu nodes={Array.from(new Set(topology?.nodes.map((n) => n.nodeGroup).filter(Boolean) ?? []))} />
        </div>
      </div>

      <StalenessBanner staleness={topology?.staleness} />

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
            onNodeDragStop={handleNodeDragStop}
            onNodeContextMenu={handleNodeContextMenu}
            onPaneClick={() => {
              select(undefined);
              setContextMenu(undefined);
            }}
          />
        )}
      </div>

      {contextMenu && (
        <ContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          items={traceItemsFor(topology?.nodes.find((n) => n.id === contextMenu.id)?.kind ?? "", contextMenu.id)}
          onClose={() => {
            setContextMenu(undefined);
          }}
        />
      )}

      {Array.from(expandedGroups).map((id) => (
        <GuestGroupExpansion key={id} groupId={id} onData={handleExpandedData} />
      ))}

      <SpotlightSearch open={spotlightOpen} onOpenChange={setSpotlightOpen} onSelect={handleSearchSelect} />
      <EditorLauncher />
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
