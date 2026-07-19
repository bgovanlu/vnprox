import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ReactFlowProvider, useReactFlow } from "@xyflow/react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { EmptyState } from "../components/EmptyState";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import { useSession } from "../api/useSession";
import { usePaletteActions, type PaletteAction } from "../keyboard/actions";
import { useTopologyShortcutTargetStore } from "../keyboard/topologyShortcutTarget";
import { useRovingFocus } from "../keyboard/useRovingFocus";
import type { Layer, TopologyEdge, TopologyNode } from "../api/types";
import { capsForNode } from "../changesets/capabilities";
import { computeDragOp } from "../changesets/dragDropOps";
import { editorKindForInventoryKind, useEditorLauncherStore } from "../changesets/editorLauncherStore";
import { EditorLauncher } from "../changesets/EditorLauncher";
import { refNode, summarizeOp } from "../changesets/opSummary";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { isTraceableEntityKind, traceFromPath, traceToExternalPath, traceToPath } from "../simulator/traceLink";
import type { Viewport } from "./canvasScene";
import { useThemeStore } from "../store/theme";
import { ContextMenu, type ContextMenuItem } from "./ContextMenu";
import { buildCaptionLines, sceneFromFlowElements, sceneFromSwitchTopology, type ExportScene } from "./export";
import { ExportMapMenu } from "./ExportMapMenu";
import { computeFlowEdges, flowEdgeStrokeWidth, type FlowEdge } from "./flowEdges";
import { useLiveFlowRecords } from "../flows/flowsQueries";
import { computeLatencyOverlayEdges } from "./latencyMode";
import { useLatMeshHeatmapQuery } from "./latMeshQueries";
import { computeMTUOverlayEdges } from "./mtuOverlay";
import { useMTUProbeResultsQuery } from "./mtuProbeQueries";
import { FlowPairPanel } from "./FlowPairPanel";
import { HistoryTimeline, type HistoryPlaybackState } from "./history/HistoryTimeline";
import { InspectorStack } from "./InspectorStack";
import { NewEntityMenu } from "./NewEntityMenu";
import { LayerToggleBar } from "./LayerToggleBar";
import { METRICS_KINDS } from "./metricsKinds";
import { useLiveMetrics, utilizationMap } from "./metricsQueries";
import { decodeViewFromSearch, type SavedViewState } from "./savedViews";
import { SavedViewsMenu } from "./SavedViewsMenu";
import { SpotlightSearch } from "./SpotlightSearch";
import { StalenessBanner } from "./StalenessBanner";
import { summarizeStaleness } from "./staleness";
import { TopologyCanvas } from "./TopologyCanvas";
import { TopologyCanvasV2 } from "./TopologyCanvasV2";
import { SwitchView } from "./SwitchView";
import { buildSwitchModel } from "./switchModel";
import { ViewModeToggle } from "./ViewModeToggle";
import { VlanFilterInput } from "./VlanFilterInput";
import { computeLayout, type XYPosition } from "./layout";
import { useReducedMotion, motionConfig } from "../lib/useReducedMotion";
import { isGuestGroupId } from "./projection";
import {
  useGuestGroupExpandQuery,
  useLayoutQuery,
  useSaveLayoutMutation,
  useTopologyQuery,
  useTopologyWsBridge,
} from "./queries";
import { currentLayoutPayload, useTopologyStore } from "./store";
import { toFlowElements, type FlowElements } from "./toFlowElements";

const DEFAULT_VIEWPORT: Viewport = { x: 48, y: 48, zoom: 1 };

const LAYER_ORDER: readonly Layer[] = ["phys", "l2", "sdn", "guest"];
const SAVE_DEBOUNCE_MS = 1000;
// docs/features/topology.md §4: "Hard render cap ~2,000 visible elements;
// beyond, require a filter (UI prompts)."
// Exported (T-607) so scaleLab.render.test.tsx can assert the filter-prompt
// threshold directly against this real constant instead of duplicating the
// magic number.
export const RENDER_CAP = 2000;

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
  // T-905: `prefers-reduced-motion: reduce` collapses the search-select
  // fit-view pan/zoom below to an instant jump instead of an eased pan.
  const motion = motionConfig(useReducedMotion());

  const activeLayers = useTopologyStore((s) => s.activeLayers);
  const viewMode = useTopologyStore((s) => s.viewMode);
  const setViewMode = useTopologyStore((s) => s.setViewMode);
  const rendererVersion = useTopologyStore((s) => s.rendererVersion);
  const setRendererVersion = useTopologyStore((s) => s.setRendererVersion);
  const vlanFilter = useTopologyStore((s) => s.vlanFilter);
  const selectedId = useTopologyStore((s) => s.selectedId);
  const hoveredId = useTopologyStore((s) => s.hoveredId);
  const spotlightOpen = useTopologyStore((s) => s.spotlightOpen);
  const expandedGroups = useTopologyStore((s) => s.expandedGroups);
  const positions = useTopologyStore((s) => s.positions);
  const trafficMode = useTopologyStore((s) => s.trafficMode);
  const toggleTrafficMode = useTopologyStore((s) => s.toggleTrafficMode);
  const flowsLayerActive = useTopologyStore((s) => s.flowsLayerActive);
  const toggleFlowsLayer = useTopologyStore((s) => s.toggleFlowsLayer);
  const latencyLayerActive = useTopologyStore((s) => s.latencyLayerActive);
  const toggleLatencyLayer = useTopologyStore((s) => s.toggleLatencyLayer);
  const mtuLayerActive = useTopologyStore((s) => s.mtuLayerActive);
  const toggleMTULayer = useTopologyStore((s) => s.toggleMTULayer);
  const toggleLayer = useTopologyStore((s) => s.toggleLayer);
  const setActiveLayers = useTopologyStore((s) => s.setActiveLayers);
  const setVlanFilter = useTopologyStore((s) => s.setVlanFilter);
  const select = useTopologyStore((s) => s.select);
  const hover = useTopologyStore((s) => s.hover);
  const setSpotlightOpen = useTopologyStore((s) => s.setSpotlightOpen);
  const toggleExpanded = useTopologyStore((s) => s.toggleExpanded);
  const setPosition = useTopologyStore((s) => s.setPosition);
  const hydrateFromLayout = useTopologyStore((s) => s.hydrateFromLayout);

  const vlanInputRef = useRef<HTMLInputElement>(null);
  // T-903: the shared wrapper around whichever view (Switch/Graph) is
  // currently mounted — see the roving-focus registration below.
  const entityContainerRef = useRef<HTMLDivElement>(null);

  // --- T-907 shareable URLs: a `?svLayers=...` link carries its own view
  // state (docs/api.md's Saved views & annotations section: "state lives in
  // the URL, not only server-side"). Read once, on mount, like
  // SimulatorPage.tsx's identical `decodeSimState(searchParams)` idiom —
  // this is the "paste a link and land on it" read, not a live subscription.
  const [searchParams] = useSearchParams();
  const urlView = useMemo(() => decodeViewFromSearch(searchParams), []); // eslint-disable-line react-hooks/exhaustive-deps

  // v2 canvas viewport: TopologyCanvasV2 owns pan/zoom internally (an
  // uncontrolled seed/notify seam — see its initialViewport/onViewportChange
  // doc comments), so this ref mirrors its live value for "Save view"/
  // "Copy link" to read without forcing the canvas into a fully controlled
  // component. pendingV2Viewport seeds a (re)mount (initial page load from a
  // share link, or loading a different saved view mid-session — the latter
  // needs v2RemountKey bumped since initialViewport is only read once per
  // mount, React's own uncontrolled-input convention).
  const viewportRef = useRef<Viewport>(
    urlView ? { x: urlView.viewport.x, y: urlView.viewport.y, zoom: urlView.zoom } : DEFAULT_VIEWPORT,
  );
  const [pendingV2Viewport, setPendingV2Viewport] = useState<Viewport | undefined>(() =>
    urlView ? { x: urlView.viewport.x, y: urlView.viewport.y, zoom: urlView.zoom } : undefined,
  );
  const [v2RemountKey, setV2RemountKey] = useState(0);
  const handleViewportChange = useCallback((vp: Viewport) => {
    viewportRef.current = vp;
  }, []);

  // --- Saved layout: load once on mount, save (debounced) on change ------
  // Skipped entirely when a share-link URL carries its own view state (see
  // urlView above) — the URL is authoritative for a share link; the
  // viewer's own auto-saved canvas layout (if any) would otherwise silently
  // override the state the link was supposed to reproduce.
  const { data: savedLayout } = useLayoutQuery();
  const saveLayoutMutation = useSaveLayoutMutation();
  const hydratedRef = useRef(false);
  useEffect(() => {
    if (hydratedRef.current || savedLayout === undefined || urlView !== undefined) return;
    hydratedRef.current = true;
    hydrateFromLayout(savedLayout);
  }, [savedLayout, hydrateFromLayout, urlView]);

  // --- Apply the URL's saved-view state (if any) once, on mount ----------
  const urlViewAppliedRef = useRef(false);
  useEffect(() => {
    if (urlViewAppliedRef.current || !urlView) return;
    urlViewAppliedRef.current = true;
    setActiveLayers(urlView.layers);
    setVlanFilter(urlView.vlanFilter);
    select(urlView.selection);
    setViewMode(urlView.view);
  }, [urlView, setActiveLayers, setVlanFilter, select, setViewMode]);

  // v1 (React Flow) tracks its own viewport internally too; unlike v2's
  // initialViewport seed, applying a target viewport to an already-mounted
  // ReactFlow instance is a plain imperative call — but only once *that*
  // renderer is actually the active one (Graph view + v1), since calling it
  // earlier has nothing to apply to yet.
  const v1ViewportAppliedRef = useRef(false);
  useEffect(() => {
    if (v1ViewportAppliedRef.current || !urlView) return;
    if (viewMode !== "graph" || rendererVersion !== "v1") return;
    v1ViewportAppliedRef.current = true;
    void reactFlow.setViewport({ x: urlView.viewport.x, y: urlView.viewport.y, zoom: urlView.zoom });
  }, [urlView, viewMode, rendererVersion, reactFlow]);

  /** Reads "the current, capturable page state" fresh at call time (not a
   * memoized value — see SavedViewsMenu's getCurrentState prop doc comment
   * for why a plain prop would go stale across the two renderers' different
   * viewport-tracking mechanisms). */
  function getCurrentViewState(): SavedViewState {
    const vp = viewMode === "graph" && rendererVersion === "v1" ? reactFlow.getViewport() : viewportRef.current;
    return {
      layers: Array.from(activeLayers),
      vlanFilter,
      zoom: vp.zoom,
      viewport: { x: vp.x, y: vp.y },
      selection: selectedId,
      view: viewMode,
    };
  }

  /** SavedViewsMenu's onLoad: applies a fetched/decoded saved view to the
   * live page — the same field-by-field application the mount-time
   * urlView effect above does, plus forcing a v2 remount (mid-session load,
   * unlike the mount-time case, targets an *already-mounted* canvas whose
   * uncontrolled viewport state initialViewport can't reach after mount). */
  function handleLoadSavedView(state: SavedViewState): void {
    setActiveLayers(state.layers);
    setVlanFilter(state.vlanFilter);
    select(state.selection);
    setViewMode(state.view);
    const vp: Viewport = { x: state.viewport.x, y: state.viewport.y, zoom: state.zoom };
    setPendingV2Viewport(vp);
    setV2RemountKey((k) => k + 1);
    if (state.view === "graph" && rendererVersion === "v1") {
      void reactFlow.setViewport(vp);
    }
  }

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

  // --- T-903: command palette "Edit <entity>" verbs ----------------------
  // One verb per entity this page's own inspector already offers an Edit
  // button for (editorKindForInventoryKind — bridges, bonds, VLANs,
  // physnics), gated on the same netWrite capability the inspector and the
  // drag-drop handler below both check, so the palette never offers an
  // edit a read-only session can't actually perform.
  const openEditor = useEditorLauncherStore((s) => s.open);
  const topologyPaletteActions = useMemo<PaletteAction[]>(() => {
    if (!topology) return [];
    const actions: PaletteAction[] = [];
    for (const node of topology.nodes) {
      const editorKind = editorKindForInventoryKind(node.kind);
      if (!editorKind) continue;
      if (!capsForNode(session, node.nodeGroup).netWrite) continue;
      // Same-named entities are common across nodes (e.g. every node's own
      // mgmt bridge is conventionally "vmbr0") — the node suffix keeps
      // otherwise-identical "Edit vmbr0" rows disambiguated in the palette
      // without changing the "Edit <bridge>" verb shape the task card names.
      actions.push({
        id: `edit-${node.id}`,
        label: node.nodeGroup ? `Edit ${node.label} on ${node.nodeGroup}` : `Edit ${node.label}`,
        hint: "Topology",
        keywords: [node.kind, node.nodeGroup],
        perform: () => {
          openEditor({ kind: editorKind, node: node.nodeGroup, target: node.id });
        },
      });
    }
    return actions;
  }, [topology, session, openEditor]);
  usePaletteActions("topology", topologyPaletteActions);

  // --- T-903: roving arrow-key focus across the currently-mounted view's
  // DOM entities (Switch faceplate ports/uplinks/VLANs/VNets or Graph-view
  // EntityNodes — both tag their focusable elements with data-entity-ref).
  // Enter activates via the same handleNodeClick a pointer click uses, so
  // keyboard selection always opens the identical inspector.
  useRovingFocus({ containerRef: entityContainerRef, onActivate: handleNodeClick });

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

  // T-1007 history playback: HistoryTimeline re-queries GET /metrics/history
  // and GET /flows for a scrubbed instant and hands back data shaped exactly
  // like the two live inputs above — utilizationByRef/liveFlowRecords below
  // are overridden with the historical snapshot while scrubbing (`playback
  // .scrubbing`), and left untouched (this component's own live values,
  // unchanged) otherwise. No new rendering path: toFlowElements and
  // computeFlowEdges are the exact same calls as before this task, just fed
  // a different data source and (for flows) an explicit `now`.
  const [playback, setPlayback] = useState<HistoryPlaybackState | undefined>(undefined);
  const effectiveUtilizationByRef = playback?.scrubbing ? playback.utilizationByRef : utilizationByRef;

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
        utilizationByRef: effectiveUtilizationByRef,
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
      effectiveUtilizationByRef,
    ],
  );

  // T-1003 "Flows" layer: v2-canvas-only per this task's card (docs/
  // features/topology.md doesn't extend the switch faceplate or the v1
  // renderer with it). Only fetches/subscribes while genuinely paintable
  // (mirrors trafficMode/useLiveMetrics' "only while the mode is on"
  // convention above) — toggling it on outside Graph+v2 has nothing to
  // paint, so no network activity is started for it.
  const flowsPaintable = flowsLayerActive && viewMode === "graph" && rendererVersion === "v2";
  const { records: liveFlowRecords, isLoading: flowsLoading } = useLiveFlowRecords(flowsPaintable);
  const canvasNodeIds = useMemo(() => new Set(elements.nodes.map((n) => n.id)), [elements.nodes]);
  // T-1007: while scrubbed, paint from the historical record page/instant
  // HistoryTimeline fetched instead of the live WS-fed buffer — same
  // computeFlowEdges call as always, just a different `records` source and
  // an explicit `now` anchored at the scrubbed instant (computeFlowEdges
  // defaults `now` to the real wall clock, which would be wrong for a
  // historical window).
  const effectiveFlowRecords = playback?.scrubbing ? playback.flowRecords : liveFlowRecords;
  const flowConversationEdges = useMemo<FlowEdge[]>(
    () =>
      flowsPaintable
        ? computeFlowEdges({ records: effectiveFlowRecords, nodeIds: canvasNodeIds, now: playback?.scrubbing ? playback.at : undefined })
        : [],
    [flowsPaintable, effectiveFlowRecords, canvasNodeIds, playback],
  );
  const flowOverlayEdges = useMemo(
    () => flowConversationEdges.map((e) => ({ id: e.id, from: e.from, to: e.to, strokeWidth: flowEdgeStrokeWidth(e.bytesPerSec) })),
    [flowConversationEdges],
  );

  // T-1303 "Latency" heatmap layer: same v2-canvas-only, "only fetch while
  // genuinely paintable" scope as the Flows layer above (docs/features/
  // monitoring.md §1's new paint mode has no v1/switch-faceplate rendering
  // either).
  const latencyPaintable = latencyLayerActive && viewMode === "graph" && rendererVersion === "v2";
  const { data: latMeshLinks } = useLatMeshHeatmapQuery(latencyPaintable);
  // GET /latmesh/heatmap's fromNode/toNode are plain PVE node names, not
  // Refs — a physical cluster Node entity's own Ref is always
  // "node:<name>:<name>" (inventory.Ref.String() for KindNode, Node==ID==
  // the node name), so this is a pure string-format lookup, not a network
  // call or a graph walk.
  const nodeIdForName = useCallback((name: string) => {
    const id = `node:${name}:${name}`;
    return canvasNodeIds.has(id) ? id : undefined;
  }, [canvasNodeIds]);
  const latencyOverlayEdges = useMemo(
    () => (latencyPaintable && latMeshLinks ? computeLatencyOverlayEdges(latMeshLinks, nodeIdForName) : []),
    [latencyPaintable, latMeshLinks, nodeIdForName],
  );

  // T-1306 "Verified MTU" badge layer: same v2-canvas-only, "only fetch
  // while genuinely paintable" scope as the Latency layer above.
  const mtuPaintable = mtuLayerActive && viewMode === "graph" && rendererVersion === "v2";
  const { data: mtuProbeResults } = useMTUProbeResultsQuery(mtuPaintable);
  const mtuOverlayBadges = useMemo(
    () => (mtuPaintable && mtuProbeResults ? computeMTUOverlayEdges(mtuProbeResults, nodeIdForName) : []),
    [mtuPaintable, mtuProbeResults, nodeIdForName],
  );
  // AC4: the empty-state hint is purely data-driven (zero records
  // cluster-wide, once the initial fetch has actually completed — never
  // flashed during the brief initial loading window) and disappears the
  // moment a live record arrives via the WS bridge, no reload needed.
  // Dismissible independent of the layer toggle itself (docs/features/
  // topology.md §5's convention) — dismissing the hint doesn't turn the
  // layer back off, it just stops nagging for this session; re-toggling
  // the layer resets it so a genuinely still-empty state is shown again.
  const [flowsHintDismissed, setFlowsHintDismissed] = useState(false);
  useEffect(() => {
    if (flowsLayerActive) setFlowsHintDismissed(false);
  }, [flowsLayerActive]);
  // T-1007: while scrubbed, HistoryTimeline's own "flow history available
  // for the last N minutes only" disclosure is the relevant empty-state
  // message (a scrub can legitimately land on a quiet instant even with a
  // flow source fully configured) — this hint is specifically about "no
  // flow source configured at all", so it's suppressed while scrubbing to
  // avoid showing both at once.
  const flowsEmptyState = flowsPaintable && !playback?.scrubbing && !flowsLoading && liveFlowRecords.length === 0 && !flowsHintDismissed;
  const [selectedFlowEdgeId, setSelectedFlowEdgeId] = useState<string | undefined>(undefined);
  const selectedFlowEdge = flowConversationEdges.find((e) => e.id === selectedFlowEdgeId);
  useEffect(() => {
    // Deselect if the edge disappears (conversation went quiet, layer
    // toggled off, filter/view changed) rather than leaving a stale panel
    // open referencing an edge no longer in the current set.
    if (selectedFlowEdgeId !== undefined && !selectedFlowEdge) {
      setSelectedFlowEdgeId(undefined);
    }
  }, [selectedFlowEdgeId, selectedFlowEdge]);

  // Switch faceplate model: built from the same merged node/edge set the
  // graph view uses (base minus expanded guest-group pills, plus each
  // expansion's synthesized members — mirroring toFlowElements) so guest-
  // group expansion, WS deltas, and staleness all flow through both views
  // identically. Only recomputed for the switch view.
  const switchTopology = useMemo(() => {
    if (viewMode !== "switch") return { nodes: [] };
    const baseNodes = topology?.nodes ?? [];
    const baseEdges = topology?.edges ?? [];
    const mergedNodes = [...baseNodes.filter((n) => !expandedGroups.has(n.id)), ...extraNodes];
    const mergedEdges = [...baseEdges.filter((e) => !expandedGroups.has(e.from)), ...extraEdges];
    return buildSwitchModel(mergedNodes, mergedEdges);
  }, [viewMode, topology, extraNodes, extraEdges, expandedGroups]);

  const overCap = elements.nodes.length + elements.edges.length > RENDER_CAP;
  const navigate = useNavigate();

  // --- T-906: map export (SVG/PNG) ----------------------------------------
  // The v2 canvas renderer applies a further, zoom-driven LOD transform
  // (T-902's capsule/bundle collapse) on top of `elements` that this
  // top-level component never otherwise sees — TopologyCanvasV2 reports its
  // current LOD-transformed scene back up via onSceneChange so an export
  // triggered from this toolbar matches what the v2 canvas actually drew,
  // not the pre-LOD element set. Only consulted while that renderer is
  // mounted (viewMode === "graph" && rendererVersion === "v2"); stale values
  // from a previous mount are harmless since they're ignored otherwise.
  const [v2SceneElements, setV2SceneElements] = useState<FlowElements | undefined>(undefined);
  const graphExportElements = rendererVersion === "v2" ? (v2SceneElements ?? elements) : elements;

  const themeMode = useThemeStore((s) => s.theme);
  const exportCaptionLines = useMemo(
    () => buildCaptionLines({ viewMode, activeLayers, layerOrder: LAYER_ORDER, vlanFilter }),
    [viewMode, activeLayers, vlanFilter],
  );
  const getExportScene = useCallback((): ExportScene => {
    return viewMode === "switch"
      ? sceneFromSwitchTopology(switchTopology, { activeLayers, vlanFilter })
      : sceneFromFlowElements(graphExportElements);
  }, [viewMode, switchTopology, activeLayers, vlanFilter, graphExportElements]);

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
      void reactFlow.fitView({ nodes: [{ id: ref }], duration: motion.fitDurationMs, maxZoom: 1.25 });
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
  /**
   * The shared drop-to-op path both renderers feed. Given the dragged entity,
   * the entity it was dropped onto (already resolved — v1 does it via React
   * Flow's `getIntersectingNodes`, v2 via its own canvas hit-testing), and
   * the drop position, it drafts the op `computeDragOp` computes for that
   * pair (identical under both renderers — T-901 AC3, same function, same
   * inputs), or falls through to a plain reposition when the pair isn't a
   * recognized edit. Snap-back on validation-failure and the read-only guard
   * behave exactly as before this refactor.
   */
  function applyDrop(draggedId: string, targetId: string | undefined, pos: XYPosition): void {
    if (!topology) {
      setPosition(draggedId, pos);
      return;
    }
    const dragged = topology.nodes.find((n) => n.id === draggedId);
    if (!dragged) {
      setPosition(draggedId, pos);
      return;
    }
    const target = targetId ? topology.nodes.find((n) => n.id === targetId) : undefined;
    const op = target ? computeDragOp(dragged, target, topology) : undefined;

    if (!op) {
      setPosition(draggedId, pos);
      return;
    }
    const opNode = op.target ? refNode(op.target) : "";
    if (!op.target || !capsForNode(session, opNode === "" ? dragged.nodeGroup : opNode).netWrite) {
      toast({ title: "Read-only", description: "You don't have network-write on this node.", variant: "error" });
      setPosition(draggedId, pos);
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
    // Deliberately do not call setPosition on a drafted op: the entity hasn't
    // actually moved yet (nothing applies until Apply), so the node stays at
    // its real, computed layout position rather than wherever it was dropped.
  }

  function handleNodeDragStop(id: string, pos: XYPosition): void {
    if (!topology) {
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
    applyDrop(id, targetFlow?.id, pos);
  }

  const noLldpData = topology ? !topology.nodes.some((n) => n.kind === "lldp-neighbor") : false;

  return (
    <div className="flex h-full flex-col gap-3">
      {/* T-906 print stylesheet: this whole toolbar row is interactive chrome
       * (view/renderer toggles, filters, search, New/Export menus) with no
       * place in a printed map — hidden via Tailwind's print: variant
       * (compiles to an @media print rule), not JS, so it's inert even if
       * scripts are disabled/blocked during print. The map itself
       * (entityContainerRef below) and the print-only caption stay visible. */}
      <div className="flex flex-wrap items-center justify-between gap-2 print:hidden">
        <h1 className="text-xl font-semibold">Topology</h1>
        <div className="flex flex-wrap items-center gap-2">
          <ViewModeToggle value={viewMode} onChange={setViewMode} />
          <LayerToggleBar
            activeLayers={activeLayers}
            onToggle={toggleLayer}
            layerOrder={LAYER_ORDER}
            // T-1003: v2-canvas-only per this task's card — the toggle
            // itself stays visible outside Graph+v2 (so a user discovers
            // it exists), it simply paints nothing until both conditions
            // hold (flowsPaintable above).
            flowsLayerActive={flowsLayerActive}
            onToggleFlows={toggleFlowsLayer}
            // T-1303: same v2-canvas-only scope note as Flows above
            // (latencyPaintable).
            latencyLayerActive={latencyLayerActive}
            onToggleLatency={toggleLatencyLayer}
            // T-1306: same v2-canvas-only scope note as Latency above
            // (mtuPaintable).
            mtuLayerActive={mtuLayerActive}
            onToggleMTU={toggleMTULayer}
          />
          {viewMode === "graph" && (
            <Button
              size="sm"
              variant={trafficMode ? "primary" : "secondary"}
              aria-pressed={trafficMode}
              onClick={toggleTrafficMode}
            >
              Traffic
            </Button>
          )}
          {viewMode === "graph" && (
            // T-901 experimental feature flag: switch the Graph renderer
            // between v1 (React Flow) and v2 (canvas) at runtime. Both read
            // the same GET /topology response — flipping this re-renders from
            // the already-loaded `elements`, no refetch. Persisted per-browser
            // in localStorage (store.ts).
            <Button
              size="sm"
              variant={rendererVersion === "v2" ? "primary" : "secondary"}
              aria-pressed={rendererVersion === "v2"}
              title="Experimental canvas renderer (v2)"
              onClick={() => {
                setRendererVersion(rendererVersion === "v2" ? "v1" : "v2");
              }}
            >
              Canvas v2
            </Button>
          )}
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
          <SavedViewsMenu getCurrentState={getCurrentViewState} onLoad={handleLoadSavedView} />
          {/* T-906: present on both Graph and Switch views (getExportScene
           * switches on `viewMode` itself), unlike the Canvas v2/Traffic
           * buttons above which are Graph-only. */}
          <ExportMapMenu getScene={getExportScene} captionLines={exportCaptionLines} theme={themeMode} />
        </div>
      </div>

      {/* T-1007: history playback scrubber — only meaningful in Graph view
       * (the traffic-paint/flows layers it scrubs render there), and, like
       * every other toolbar control, absent from print output. Read-only:
       * see HistoryTimeline.tsx's own doc comment — no apply/confirm/
       * rollback affordance anywhere in it. */}
      {viewMode === "graph" && (
        <div className="print:hidden">
          <HistoryTimeline
            metricsRefs={metricsCandidateRefs}
            liveUtilizationByRef={utilizationByRef}
            liveFlowRecords={liveFlowRecords}
            onPlaybackChange={setPlayback}
          />
        </div>
      )}

      <div className="print:hidden">
        <StalenessBanner staleness={topology?.staleness} />
      </div>

      {noLldpData && (
        <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200 print:hidden">
          No LLDP data yet — the physical layer shows NICs only.{" "}
          <a href="https://man7.org/linux/man-pages/man8/lldpd.8.html" className="underline" target="_blank" rel="noreferrer">
            Set up lldpd
          </a>{" "}
          to see real switch names and ports.
        </div>
      )}

      {overCap && (
        <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200 print:hidden">
          This cluster has {elements.nodes.length + elements.edges.length} visible elements — above the ~2,000
          render cap (docs/features/topology.md §4). Use the VLAN filter or a layer toggle to narrow the view.
        </div>
      )}

      {flowsEmptyState && (
        <div className="flex items-center justify-between gap-2 rounded-md border border-cyan-300 bg-cyan-50 px-3 py-2 text-xs text-cyan-900 dark:border-cyan-700 dark:bg-cyan-950 dark:text-cyan-200 print:hidden">
          <span>
            The Flows layer is on, but vnprox has no ingested flow records cluster-wide yet — configure an sFlow/
            NetFlow/IPFIX exporter (or host-local sampling) on a node and enable it in that node&apos;s vnprox.toml{" "}
            <code>[flows]</code> section.{" "}
            <a href="https://sflow.org" target="_blank" rel="noreferrer" className="underline">
              Flow source setup reference
            </a>
          </span>
          <button
            type="button"
            aria-label="Dismiss flows setup hint"
            onClick={() => { setFlowsHintDismissed(true); }}
            className="shrink-0 rounded px-1.5 text-cyan-700 hover:bg-cyan-100 dark:text-cyan-200 dark:hover:bg-cyan-900"
          >
            ×
          </button>
        </div>
      )}

      {/* T-906 print stylesheet: the current filter/legend state as a
       * caption, visible only when printing (docs/features/topology.md §4's
       * export-gap deliverable 2) — the same lines embedded in an SVG/PNG
       * export's own caption block (see export.ts's buildCaptionLines). */}
      <div className="hidden print:block print:text-black">
        {exportCaptionLines.map((line) => (
          <p key={line} className="text-xs">
            {line}
          </p>
        ))}
      </div>

      <div
        ref={entityContainerRef}
        className="min-h-0 flex-1 rounded-lg border border-slate-200 dark:border-slate-800 print:border-none print:h-auto"
      >
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
        {!isLoading && !isError && topology && topology.nodes.length > 0 && viewMode === "switch" && (
          <SwitchView
            topology={switchTopology}
            selectedId={selectedId}
            vlanFilter={vlanFilter}
            activeLayers={activeLayers}
            staleNodeGroups={staleSummary.staleNodeGroups}
            onSelect={handleNodeClick}
            onExpandGroup={toggleExpanded}
          />
        )}
        {!isLoading && !isError && topology && topology.nodes.length > 0 && viewMode === "graph" && rendererVersion === "v1" && (
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
        {!isLoading && !isError && topology && topology.nodes.length > 0 && viewMode === "graph" && rendererVersion === "v2" && (
          <TopologyCanvasV2
            key={v2RemountKey}
            elements={elements}
            selectedId={selectedId}
            onNodeClick={handleNodeClick}
            onNodeHover={hover}
            onNodeDrop={applyDrop}
            onNodeContextMenu={handleNodeContextMenu}
            onSceneChange={setV2SceneElements}
            onPaneClick={() => {
              select(undefined);
              setContextMenu(undefined);
            }}
            initialViewport={pendingV2Viewport}
            onViewportChange={handleViewportChange}
            flowEdges={flowOverlayEdges}
            selectedFlowEdgeId={selectedFlowEdgeId}
            latencyEdges={latencyOverlayEdges}
            mtuBadges={mtuOverlayBadges}
            onFlowEdgeClick={(id) => {
              setSelectedFlowEdgeId(id);
            }}
          />
        )}
      </div>

      {selectedFlowEdge && (
        <FlowPairPanel
          edge={selectedFlowEdge}
          onClose={() => {
            setSelectedFlowEdgeId(undefined);
          }}
        />
      )}

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
      {/* T-906 print stylesheet: the inspector is a non-modal floating
       * "drawer" (InspectorStack.tsx's own doc comment) that can be open
       * alongside the map — hide it when printing along with the toolbar
       * above (AC3: "toolbar/drawer chrome is hidden"). */}
      <div className="print:hidden">
        <InspectorStack
          selectedRef={selectedId && !isGuestGroupId(selectedId) ? selectedId : undefined}
          onAllClosed={() => {
            select(undefined);
          }}
        />
      </div>

      <p className="sr-only" aria-live="polite">
        Topology last updated {new Date(dataUpdatedAt).toLocaleTimeString()}
      </p>
    </div>
  );
}
