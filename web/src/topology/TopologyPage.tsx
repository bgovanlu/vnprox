import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ReactFlowProvider, useReactFlow } from "@xyflow/react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { EmptyState } from "../components/EmptyState";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import { useSession } from "../api/useSession";
import { hasAnyCap } from "../changesets/capabilities";
import { useLldpInstallMutation } from "../onboarding/queries";
import { LldpSetupBanner } from "./LldpSetupBanner";
import { actionErrorMessage } from "../api/actionError";
import { isRateLimited, refreshCollectors } from "../api/collectors";
import { startService } from "../api/services";
import type { RemediationContext } from "../findings/remediation";
import { usePaletteActions, type PaletteAction } from "../keyboard/actions";
import { useTopologyShortcutTargetStore } from "../keyboard/topologyShortcutTarget";
import { useRovingFocus } from "../keyboard/useRovingFocus";
import type { Layer, LldpInstallNodeResult, TopologyEdge, TopologyNode } from "../api/types";
import { capsForNode } from "../changesets/capabilities";
import { computeDragOp } from "../changesets/dragDropOps";
import { editorKindForInventoryKind, useEditorLauncherStore } from "../changesets/editorLauncherStore";
import { EditorLauncher } from "../changesets/EditorLauncher";
import { CaptureDialog } from "../capture/CaptureDialog";
import { isCapturableEntityKind, useCaptureLauncherStore } from "../capture/captureLauncherStore";
import { refNode, summarizeOp } from "../changesets/opSummary";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { isTraceableEntityKind, traceFromPath, traceToExternalPath, traceToPath } from "../simulator/traceLink";
import { conntrackNodeLinkPath } from "../conntrack/urlState";
import { diagnosePath } from "../diagnose/diagnosePath";
import type { Viewport } from "./canvasScene";
import { useThemeStore } from "../store/theme";
import { ContextMenu, type ContextMenuItem } from "./ContextMenu";
import { buildCaptionLines, sceneFromFlowElements, sceneFromSwitchTopology, type ExportScene } from "./export";
import { ExportMapMenu } from "./ExportMapMenu";
import { computeFlowEdges, flowEdgeStrokeWidth, type FlowEdge } from "./flowEdges";
import { useLiveFlowRecords } from "../flows/flowsQueries";
import { computeLatencyOverlayEdges } from "./latencyMode";
import { useLatMeshHeatmapQuery } from "./latMeshQueries";
import { computeDiffOverlay, summarizeDiffOverlay } from "./diffOverlay";
import { useTopologyDiffQuery } from "./topologyDiffQuery";
import { HelpAnchor } from "../help/HelpAnchor";
import { buildPreviewScene, summarizePreviewScene, summarizeUnprojectable } from "./previewOverlay";
import { useChangesetPreviewQuery } from "./previewQuery";
import { computeMTUOverlayEdges } from "./mtuOverlay";
import { useMTUProbeResultsQuery } from "./mtuProbeQueries";
import { useWireGuardTunnelsQuery } from "../wireguard/wgTunnelsQuery";
import { computeWgTunnelOverlay } from "../wireguard/wgTunnelEdges";
import { buildNodeAnchorResolver } from "./nodeAnchor";
import { FlowPairPanel } from "./FlowPairPanel";
import { useK8sClustersQuery, useK8sOverlaysQuery } from "./layers/k8sQueries";
import { computeK8sOverlay, isK8sSyntheticId, parseK8sSyntheticId, type K8sSelection } from "./layers/k8sOverlay";
import { PodDrilldown } from "./layers/PodDrilldown";
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
import { UnrefFindingsBanner } from "./UnrefFindingsBanner";
import { summarizeStaleness } from "./staleness";
import { TopologyCanvas } from "./TopologyCanvas";
import { TopologyCanvasV2 } from "./TopologyCanvasV2";
import { useAnnotationsQuery, useMapRegionsQuery } from "./annotationsQueries";
import { SwitchView } from "./SwitchView";
import { buildSwitchModel } from "./switchModel";
import { ViewModeToggle } from "./ViewModeToggle";
import { VlanFilterInput } from "./VlanFilterInput";
import { computeLayout, type XYPosition } from "./layout";
import { useReducedMotion, motionConfig } from "../lib/useReducedMotion";
import { isGuestGroupId, isPhysGroupId } from "./projection";
import {
  TOPOLOGY_QUERY_KEY,
  useGuestGroupExpandQuery,
  useLayoutQuery,
  usePhysGroupExpandQuery,
  useSaveLayoutMutation,
  useTopologyQuery,
  useTopologyWsBridge,
} from "./queries";
import { currentLayoutPayload, useTopologyStore } from "./store";
import { toFlowElements, type FlowElements } from "./toFlowElements";

const DEFAULT_VIEWPORT: Viewport = { x: 48, y: 48, zoom: 1 };

const LAYER_ORDER: readonly Layer[] = ["phys", "l2", "sdn", "guest"];
const SAVE_DEBOUNCE_MS = 1000;

// T-1305: "view live connections" (the map's right-click entry into the
// Conntrack explorer) is offered on any entity a conntrack read is
// meaningfully scoped to — the physical/L2 kinds a connection actually
// traverses, plus guest-nic (a guest's own attachment point) and sdn-vnet
// (cluster-scoped: the link falls back to the unscoped explorer, still a
// legitimate way in). A ref's node segment (empty for a cluster-scoped
// sdn-vnet ref) is all this page has cheaply available to scope by — see
// conntrack/urlState.ts's own doc comment on why this is node-scoping, not
// exact IP/guest matching.
const CONNTRACK_ENTITY_KINDS = new Set(["bridge", "bond", "ovs-bridge", "ovs-bond", "guest-nic", "sdn-vnet"]);
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

/** T-1907's counterpart to GuestGroupExpansion, for a "phys-group:<node>"
 * per-node physical-layer summary pill: same one-query-per-expanded-pill
 * shape, but usePhysGroupExpandQuery needs the pill's own TopologyNode (its
 * `members` list lives there, not recoverable from the id alone) rather
 * than just the id — see expand.ts's doc comment. `node` is undefined for
 * one render if the pill briefly isn't in the current topology snapshot
 * (e.g. mid-poll); the query hook disables itself in that case. */
function PhysGroupExpansion({
  groupId,
  node,
  onData,
}: {
  groupId: string;
  node: TopologyNode | undefined;
  onData: (groupId: string, nodes: TopologyNode[], edges: TopologyEdge[]) => void;
}) {
  const { data } = usePhysGroupExpandQuery(node, true);
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
  // T-1202: when the global map has drilled into an attached cluster, the
  // `?cluster=<id>` param routes the same canvas to that cluster's projected
  // topology. Absent the param — a single-cluster deployment's only case —
  // this is undefined and the fetch is the unchanged local GET /topology.
  const [clusterSearchParams] = useSearchParams();
  const drilledClusterId = clusterSearchParams.get("cluster") ?? undefined;
  const { data: liveTopology, isLoading, isError, dataUpdatedAt } = useTopologyQuery(drilledClusterId);
  // T-2605 preview mode: `?previewChangeset=<id>` renders the map as it would
  // be with that changeset applied. Carried in the URL — like T-2704's diff
  // range — so the changeset drawer's "show on map" is an ordinary link and the
  // resulting view is shareable. No param means no fetch and no preview.
  const previewChangesetId = clusterSearchParams.get("previewChangeset") ?? "";
  const { data: preview, isError: previewFailed } = useChangesetPreviewQuery(previewChangesetId);
  const previewScene = useMemo(() => buildPreviewScene(preview, liveTopology), [preview, liveTopology]);
  const previewActive = preview !== undefined;
  // Everything downstream — layout, elements, the inspector, the switch view —
  // reads `topology`, so preview mode is a substitution at this one point
  // rather than a second rendering path that could drift from the real one.
  // Memoized, not a bare conditional: every downstream useMemo (layout above
  // all) keys on this value's identity, and a fresh object each render would
  // re-run the elk layout on every keystroke.
  const topology: typeof liveTopology = useMemo(
    () =>
      preview
        ? { ...preview.topology, nodes: previewScene.nodes, edges: previewScene.edges, staleness: liveTopology?.staleness }
        : liveTopology,
    [preview, previewScene, liveTopology],
  );
  useTopologyWsBridge();
  const reactFlow = useReactFlow();
  const { data: session } = useSession();
  const { toast } = useToast();
  const queryClient = useQueryClient();
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
  const wgLayerActive = useTopologyStore((s) => s.wgLayerActive);
  const toggleWGLayer = useTopologyStore((s) => s.toggleWGLayer);
  const k8sLayerActive = useTopologyStore((s) => s.k8sLayerActive);
  const toggleK8sLayer = useTopologyStore((s) => s.toggleK8sLayer);
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

  // T-2806: the map annotation layer. Both reads are the daemon's LIVE view
  // — expiry is judged server-side on each read, so nothing here (and no
  // timer) decides whether a note is still current. Their query keys are
  // their own: a layout save never invalidates them, which is half of why
  // regions survive layout changes and view switches (the other half is
  // that they live in their own shared table, not in the layout blob).
  const { data: mapNotes } = useAnnotationsQuery();
  const { data: mapRegions } = useMapRegionsQuery();

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
  const openCapture = useCaptureLauncherStore((s) => s.open);
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

  // Shared "whole node" anchor resolver for every node-to-node overlay
  // layer (Latency/T-1303, MTU/T-1306, WireGuard/T-1402): internal/
  // topology.Project never renders a KindNode entity of its own (see
  // nodeAnchor.ts's doc comment for the bug this fixes — the previous
  // per-overlay `nodeIdForName` below resolved against a
  // "node:<name>:<name>" id that no real GET /topology response ever
  // contains), so this resolves to the first rendered entity in that
  // node's own band instead. Computed from `topology.nodes` directly (not
  // the later `canvasNodeIds`, which is itself derived from `elements` —
  // using it here would be circular) since a node's set of rendered
  // entities is unaffected by guest-group expansion or these very
  // overlays.
  const nodeIdForName = useMemo(() => buildNodeAnchorResolver(topology?.nodes ?? []), [topology]);

  // T-1402 "WireGuard" layer: renders every tunnel this node can see as a
  // map edge to its far-side endpoint, painted from T-1401's live per-peer
  // status — same v2-canvas-only, "only fetch while genuinely paintable"
  // scope as Latency/MTU below. Unlike those two (which only annotate
  // existing edges via a badge overlay drawn after `elements` exists), this
  // overlay also introduces synthetic far-side endpoint nodes
  // (wgTunnelEdges.ts) that must be part of `elements` itself — merged into
  // extraNodes/extraEdges below, the same seam T-1003's expanded
  // guest-group pills already use.
  const wgPaintable = wgLayerActive && viewMode === "graph" && rendererVersion === "v2";
  const { data: wgTunnels } = useWireGuardTunnelsQuery(wgPaintable);
  const wgOverlay = useMemo(
    () => (wgPaintable && wgTunnels ? computeWgTunnelOverlay(wgTunnels, nodeIdForName) : { nodes: [], edges: [] }),
    [wgPaintable, wgTunnels, nodeIdForName],
  );

  // T-1502 "Kubernetes" layer: renders every registered cluster's pod/
  // service CIDR model (T-1501's GET /k8s/{clusterId}/overlay) as map
  // regions plus node<->guest correlation lines — same v2-canvas-only,
  // "only fetch while genuinely paintable" scope as WireGuard above, and
  // the same "synthetic nodes/edges merged into extraNodes/extraEdges"
  // shape (k8sOverlay.ts's computeK8sOverlay mirrors computeWgTunnelOverlay
  // exactly).
  const k8sPaintable = k8sLayerActive && viewMode === "graph" && rendererVersion === "v2";
  const { data: k8sClusters } = useK8sClustersQuery(k8sPaintable);
  const { overlays: k8sOverlays } = useK8sOverlaysQuery(k8sClusters, k8sPaintable);
  const k8sOverlayByCluster = useMemo(() => new Map(k8sOverlays.map((o) => [o.clusterId, o])), [k8sOverlays]);
  const k8sOverlay = useMemo(
    () =>
      k8sPaintable
        ? k8sOverlays.reduce<{ nodes: TopologyNode[]; edges: TopologyEdge[] }>(
            (acc, overlay) => {
              const computed = computeK8sOverlay(overlay);
              acc.nodes.push(...computed.nodes);
              acc.edges.push(...computed.edges);
              return acc;
            },
            { nodes: [], edges: [] },
          )
        : { nodes: [], edges: [] },
    [k8sPaintable, k8sOverlays],
  );

  const extraNodes = useMemo(
    () => [
      ...Array.from(expandedGroups).flatMap((id) => expandedData[id]?.nodes ?? []),
      ...wgOverlay.nodes,
      ...k8sOverlay.nodes,
    ],
    [expandedGroups, expandedData, wgOverlay.nodes, k8sOverlay.nodes],
  );
  const extraEdges = useMemo(
    () => [
      ...Array.from(expandedGroups).flatMap((id) => expandedData[id]?.edges ?? []),
      ...wgOverlay.edges,
      ...k8sOverlay.edges,
    ],
    [expandedGroups, expandedData, wgOverlay.edges, k8sOverlay.edges],
  );

  // Selecting a pod/pod-network/service on the Kubernetes layer opens
  // PodDrilldown below instead of the ordinary inventory inspector — a k8s
  // synthetic id is never a real inventory.Ref (isK8sSyntheticId), so
  // handleNodeClick routes it here rather than calling `select(id)` (which
  // would otherwise fire a doomed GET /inventory/{ref} the way T-1402's own
  // wg-external-endpoint nodes still do today — see k8sOverlay.ts's module
  // doc comment).
  const [k8sSelection, setK8sSelection] = useState<K8sSelection | undefined>(undefined);

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
  // Refs — resolved to a rendered map entity via the shared
  // `nodeIdForName` anchor resolver above (nodeAnchor.ts; T-1402 fixed this
  // to no longer resolve against the never-rendered "node:<name>:<name>"
  // id — see that file's doc comment).
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
  // T-2704 "diff" overlay: a selected historical range, carried in the URL
  // (`?diffFrom=&diffTo=`) so the History page's "Show on map" is an ordinary
  // link and the resulting view is shareable — the same "state lives in the
  // URL" convention T-907's saved views established. No range in the URL
  // means no fetch and no overlay at all.
  const diffFrom = searchParams.get("diffFrom") ?? "";
  const diffTo = searchParams.get("diffTo") ?? "";
  const { data: topologyDiff } = useTopologyDiffQuery(diffFrom, diffTo);
  const diffOverlay = useMemo(() => {
    const onMap = new Set(elements.nodes.map((n) => n.id));
    return computeDiffOverlay(topologyDiff, (ref) => onMap.has(ref));
  }, [topologyDiff, elements.nodes]);

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

  function conntrackItemFor(kind: string, ref: string): ContextMenuItem[] {
    if (!CONNTRACK_ENTITY_KINDS.has(kind)) return [];
    const path = conntrackNodeLinkPath("/conntrack", refNode(ref));
    return [{ label: "View live connections", onSelect: () => { void navigate(path); } }];
  }

  // T-1302: "Start capture" — the map right-click entry into the capture
  // dialog, offered on the same bridge/bond/guest-NIC/SDN-VNet kinds
  // docs/api.md's Captures section documents as valid targetRefs. Node is
  // read off nodeGroup (the column this map node renders in — "" for a
  // cluster-scoped sdn-vnet, matching CaptureDialog's own node display).
  function captureItemFor(kind: string, ref: string, nodeGroup: string, label: string): ContextMenuItem[] {
    if (!isCapturableEntityKind(kind)) return [];
    return [{ label: "Start capture", onSelect: () => { openCapture({ targetRef: ref, node: nodeGroup, label }); } }];
  }

  // T-1307: "Diagnose" — the guided diagnosis ladder's map entry point.
  // Broader than CONNTRACK_ENTITY_KINDS (also accepts a bare guest and a
  // vlan entity) since POST /diagnose's own target resolution handles
  // those, unlike GET /conntrack's own node/guest-only scoping — see
  // diagnose/diagnosePath.ts's doc comment.
  function diagnoseItemFor(kind: string, ref: string): ContextMenuItem[] {
    const path = diagnosePath(kind, ref);
    if (!path) return [];
    return [{ label: "Diagnose", onSelect: () => { void navigate(path); } }];
  }

  function contextMenuItemsFor(kind: string, ref: string, nodeGroup: string, label: string): ContextMenuItem[] {
    return [...traceItemsFor(kind, ref), ...conntrackItemFor(kind, ref), ...captureItemFor(kind, ref, nodeGroup, label), ...diagnoseItemFor(kind, ref)];
  }

  function handleNodeContextMenu(id: string, clientX: number, clientY: number): void {
    const node = topology?.nodes.find((n) => n.id === id);
    if (!node || contextMenuItemsFor(node.kind, id, node.nodeGroup, node.label).length === 0) {
      setContextMenu(undefined);
      return;
    }
    setContextMenu({ id, x: clientX, y: clientY });
  }

  function handleNodeClick(id: string): void {
    if (isGuestGroupId(id) || isPhysGroupId(id)) {
      toggleExpanded(id);
      return;
    }
    if (isK8sSyntheticId(id)) {
      const parsed = parseK8sSyntheticId(id);
      if (parsed) setK8sSelection(parsed);
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

  // T-3602. Phase 36 Tier 2: an operational action — it installs software
  // and starts a service, but changes no PVE network configuration, so
  // there is nothing to stage through internal/change. The ceremony
  // (explicit confirm naming the blast radius, capability gate, per-node
  // audit) is what stands in for a changeset's review step.
  const canInstallLldp = hasAnyCap(session, "netWrite");
  const lldpInstall = useLldpInstallMutation();
  const [lldpInstallResults, setLldpInstallResults] = useState<LldpInstallNodeResult[] | undefined>(undefined);

  // T-3604. The confirmed operational tier: this surface DOES own a
  // confirmation dialog (OperationalActionButton), so unlike the findings
  // stream it supplies a runner and can therefore offer mutating remedies.
  const [serviceStartPendingId, setServiceStartPendingId] = useState<string | undefined>(undefined);
  const [serviceStartErrors, setServiceStartErrors] = useState<Record<string, string>>({});

  const remediationCtx: RemediationContext = useMemo(
    () => ({
      netWrite: canInstallLldp,
      navigate: (to) => { void navigate(to); },
      runOperational: (remedy) => {
        if (remedy.action !== "service.start") return;
        const node = remedy.params?.node;
        const service = remedy.params?.service;
        if (node === undefined || node === "" || service === undefined || service === "") return;
        // Keyed on (node, service), the only pair that distinguishes two
        // findings that are otherwise identical — see remedyActionKey.
        const key = `${node}/${service}`;
        setServiceStartPendingId(key);
        void (async () => {
          try {
            await startService(node, service);
            // Clear this key's stale error by rebuilding without it —
            // `delete` on a computed key is banned by eslint here, and a
            // filtered rebuild says the same thing.
            setServiceStartErrors((prev) =>
              Object.fromEntries(Object.entries(prev).filter(([k]) => k !== key)),
            );
            // The finding clears on the next poll that observes the unit
            // running — no special-casing to make it disappear early, which
            // would claim success before the service had actually come up.
            void queryClient.invalidateQueries({ queryKey: TOPOLOGY_QUERY_KEY });
          } catch (err) {
            setServiceStartErrors((prev) => ({
              ...prev,
              [key]: actionErrorMessage(err, "could not start the service"),
            }));
          } finally {
            setServiceStartPendingId(undefined);
          }
        })();
      },
    }),
    [canInstallLldp, navigate, queryClient],
  );

  // T-3603. Read-only operational tier: re-runs vnprox's own poll, writes
  // nothing to any node, so no confirmation dialog — see StalenessBanner's
  // own comment. The server enforces the rate limit; this only has to
  // render the refusal honestly rather than swallowing it.
  const [refreshing, setRefreshing] = useState(false);
  const [refreshResult, setRefreshResult] = useState<{ error?: string; changed: boolean } | undefined>(undefined);
  const [refreshRateLimited, setRefreshRateLimited] = useState(false);

  async function handleRefreshCollectors(): Promise<void> {
    setRefreshing(true);
    setRefreshRateLimited(false);
    try {
      const res = await refreshCollectors();
      setRefreshResult({ error: res.error, changed: res.changed });
      if (res.error === undefined || res.error === "") {
        // The poll succeeded, so the staleness snapshot the banner reads
        // from is now out of date — re-read it rather than leaving the
        // banner asserting a failure that has just been fixed.
        void queryClient.invalidateQueries({ queryKey: TOPOLOGY_QUERY_KEY });
      }
    } catch (err) {
      if (isRateLimited(err)) {
        setRefreshRateLimited(true);
        setRefreshResult(undefined);
      } else {
        setRefreshResult({ error: actionErrorMessage(err, "refresh failed"), changed: false });
      }
    } finally {
      setRefreshing(false);
    }
  }

  async function handleInstallLldp(): Promise<void> {
    try {
      const res = await lldpInstall.mutateAsync();
      // Kept, not toasted: a partial failure has to stay legible next to
      // the retry rather than expiring after a few seconds.
      setLldpInstallResults(res.results);
    } catch {
      // The call itself failed (network, auth, 4xx) — no per-node results
      // exist to show, so this is the one case that belongs in a toast.
      setLldpInstallResults(undefined);
      toast({ title: "Could not install lldpd", variant: "error" });
    }
  }

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
            // T-1402: same v2-canvas-only scope note as MTU above
            // (wgPaintable).
            wgLayerActive={wgLayerActive}
            onToggleWG={toggleWGLayer}
            // T-1502: same v2-canvas-only scope note as WireGuard above
            // (k8sPaintable).
            k8sLayerActive={k8sLayerActive}
            onToggleK8s={toggleK8sLayer}
          />
          {viewMode === "graph" && (
            <>
              <Button
                size="sm"
                variant={trafficMode ? "primary" : "secondary"}
                aria-pressed={trafficMode}
                onClick={toggleTrafficMode}
              >
                Traffic
              </Button>
              {/* The `?` covers the paint modes as a set (traffic, latency,
               * and the simulated path) rather than sitting on one of the
               * three toggles. */}
              <HelpAnchor topic="topology-paint-modes" />
            </>
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

      <div className="print:hidden flex flex-col gap-2">
        <StalenessBanner
          staleness={topology?.staleness}
          retry={{
            canRetry: canInstallLldp,
            pending: refreshing,
            result: refreshResult,
            rateLimited: refreshRateLimited,
            onRetry: () => {
              void handleRefreshCollectors();
            },
          }}
        />
        {/* T-3501 AC5: rendered once here, identically regardless of which
            view (Switch/Graph) is active below — the two views can never
            disagree about a ref-less finding's presentation because there
            is only one place it renders. */}
        <UnrefFindingsBanner
          findings={topology?.unrefFindings}
          remediationCtx={remediationCtx}
          pendingId={serviceStartPendingId}
          results={serviceStartErrors}
        />
      </div>

      {/* T-2704: the diff overlay's own status line. The map must SAY what
          the rings mean, and it must say so even when every difference is
          off-map (a deletion has no node left to ring) — otherwise "nothing
          is highlighted" and "nothing changed" look identical. */}
      {topologyDiff && (
        <div
          className="rounded-md border border-slate-300 bg-slate-50 px-3 py-2 text-xs text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200 print:hidden"
          role="status"
        >
          <span className="font-medium">
            Diff vs {topologyDiff.from.snapshotId ?? topologyDiff.from.requested}
          </span>{" "}
          — {summarizeDiffOverlay(diffOverlay)}
          {diffOverlay.unattributedCount > 0 && (
            <span className="ml-2 rounded bg-red-100 px-1.5 py-0.5 font-medium text-red-800 dark:bg-red-950 dark:text-red-200">
              {diffOverlay.unattributedCount} unattributed
            </span>
          )}
        </div>
      )}

      {/* T-2605: preview mode's own status line. The map must SAY that it is
          showing something that has not happened, must say what would change
          even when a change is off-map, and must repeat the server's
          disclosure of what it could not project — a projected map rendered
          without that list turns a disclosed gap back into a hidden one. */}
      {previewActive && (
        <div
          className="rounded-md border border-indigo-300 bg-indigo-50 px-3 py-2 text-xs text-indigo-900 dark:border-indigo-700 dark:bg-indigo-950 dark:text-indigo-100 print:hidden"
          role="status"
        >
          <span className="inline-flex items-center gap-1.5 font-medium">
            Post-apply preview of changeset {preview.changesetId}
            <HelpAnchor topic="post-apply-preview" />
          </span>{" "}
          —{" "}
          {summarizePreviewScene(previewScene)}{" "}
          <span className="italic">Best-effort projection: nothing has been applied.</span>
          {summarizeUnprojectable(preview) !== "" && (
            <div className="mt-1">
              <span className="rounded bg-amber-100 px-1.5 py-0.5 font-medium text-amber-900 dark:bg-amber-950 dark:text-amber-100">
                {summarizeUnprojectable(preview)}
              </span>
              <ul className="mt-1 list-disc pl-5">
                {preview.unprojectable.map((op) => (
                  <li key={`${op.op}:${op.target ?? ""}:${op.opId ?? ""}`}>
                    <code>{op.op}</code> {op.target} — {op.reason}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}

      {previewChangesetId !== "" && previewFailed && (
        <div
          className="rounded-md border border-red-300 bg-red-50 px-3 py-2 text-xs text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200 print:hidden"
          role="status"
        >
          Could not project changeset {previewChangesetId}. A changeset with blocking validation errors has no
          post-apply map; the live map is shown instead.
        </div>
      )}

      <LldpSetupBanner
        show={noLldpData}
        canInstall={canInstallLldp}
        pending={lldpInstall.isPending}
        results={lldpInstallResults}
        onInstall={() => {
          void handleInstallLldp();
        }}
      />

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
        // `min-h-[22rem]`, not `min-h-0`, and the difference is the whole
        // point. This container is the only `flex-1` child of a fixed-height
        // (`h-full`) column whose other children are banners that grow with
        // the cluster — so with `min-h-0` it absorbs every shortfall and can
        // legitimately resolve to ZERO height, at which point the map is not
        // small but gone. That is not hypothetical: measured on the
        // scale-lab stack on 2026-08-20 the map was squeezed to 139px of a
        // 796px page by banners totalling 385px, and Playwright reports the
        // canvas as `hidden` when it reaches 0 — the failure quarantined as
        // T-2505-followup-01 (see planning/reports/ for the geometry dump).
        //
        // A floor turns "the map disappears" into "the page scrolls":
        // `<main>` is already `overflow-auto`, so once demand exceeds the
        // viewport the banners scroll away and the map keeps its 22rem.
        // Capping the banner lists (done separately) raised the bar; this is
        // what removes the failure mode.
        className="min-h-[22rem] flex-1 rounded-lg border border-slate-200 dark:border-slate-800 print:border-none print:h-auto print:min-h-0"
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
            diffMarks={previewActive ? previewScene.marks : diffOverlay.marks}
            onFlowEdgeClick={(id) => {
              setSelectedFlowEdgeId(id);
            }}
            // T-2806: the map annotation layer. Both come from their own
            // query caches, which no layout save or view switch touches —
            // that independence is what makes regions survive both.
            regions={mapRegions}
            notes={mapNotes}
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

      {k8sSelection && (
        <PodDrilldown
          selection={k8sSelection}
          overlay={k8sOverlayByCluster.get(k8sSelection.clusterId)}
          topologyNodes={topology?.nodes ?? []}
          topologyEdges={topology?.edges ?? []}
          onClose={() => {
            setK8sSelection(undefined);
          }}
        />
      )}

      {contextMenu && (
        <ContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          items={(() => {
            const n = topology?.nodes.find((nd) => nd.id === contextMenu.id);
            return contextMenuItemsFor(n?.kind ?? "", contextMenu.id, n?.nodeGroup ?? "", n?.label ?? contextMenu.id);
          })()}
          onClose={() => {
            setContextMenu(undefined);
          }}
        />
      )}
      <CaptureDialog />

      {Array.from(expandedGroups).map((id) =>
        isPhysGroupId(id) ? (
          <PhysGroupExpansion
            key={id}
            groupId={id}
            node={topology?.nodes.find((n) => n.id === id)}
            onData={handleExpandedData}
          />
        ) : (
          <GuestGroupExpansion key={id} groupId={id} onData={handleExpandedData} />
        ),
      )}

      <SpotlightSearch open={spotlightOpen} onOpenChange={setSpotlightOpen} onSelect={handleSearchSelect} />
      <EditorLauncher />
      {/* T-906 print stylesheet: the inspector is a non-modal floating
       * "drawer" (InspectorStack.tsx's own doc comment) that can be open
       * alongside the map — hide it when printing along with the toolbar
       * above (AC3: "toolbar/drawer chrome is hidden"). */}
      <div className="print:hidden">
        <InspectorStack
          selectedRef={selectedId && !isGuestGroupId(selectedId) && !isPhysGroupId(selectedId) ? selectedId : undefined}
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
