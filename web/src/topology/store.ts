// Topology page UI state: layer visibility, VLAN filter, selection/hover,
// expanded guest-group pills, and manual node positions. Canvas state lives
// in zustand per docs/development.md's TypeScript standards ("canvas state
// in zustand"); server state (the topology data itself) stays in TanStack
// Query — see queries.ts. Not persisted via zustand's `persist` middleware
// (localStorage) like src/store/theme.ts, because positions/filters are
// meant to be a *per-user* saved layout on the server (docs/features/
// topology.md §2), not a per-browser one — see useLayoutPersistence in
// queries.ts for the load/save round trip.
import { create } from "zustand";
import { ALL_LAYERS, type Layer, type TopologyLayoutPayload } from "../api/types";

/** Which rendering of the same GET /topology data is shown: the switch
 * faceplate view (the default — docs/features/topology.md §2's virtual-
 * switch rework) or the elk pan/zoom graph. A per-session view toggle, not
 * part of the persisted saved layout (like trafficMode), so a user can flip
 * it without rewriting their saved arrangement. */
export type TopologyViewMode = "switch" | "graph";

/** Which Graph-view rendering engine draws the node-link canvas: the v1
 * React Flow (DOM/SVG) renderer or T-901's v2 canvas engine. This is an
 * *experimental feature flag*, not saved-layout or per-session view state —
 * so, unlike everything else in this store (per-user server layout) or the
 * view-mode/traffic toggles (per-session), it is persisted per-browser in
 * localStorage, the same lifetime a "Settings > Experimental" opt-in wants.
 * The Switch/Graph segmented control (viewMode) is unaffected either way:
 * this only chooses *how* the Graph view renders, never whether it shows. */
export type RendererVersion = "v1" | "v2";

const RENDERER_FLAG_KEY = "vnprox.topology.rendererV2";

/** Reads the persisted renderer flag, defaulting to the v1 fallback. Guarded
 * against a throwing/absent localStorage (SSR, privacy modes, tests) so the
 * store never fails to construct. */
function readRendererFlag(): RendererVersion {
  try {
    return globalThis.localStorage.getItem(RENDERER_FLAG_KEY) === "v2" ? "v2" : "v1";
  } catch {
    return "v1";
  }
}

function writeRendererFlag(version: RendererVersion): void {
  try {
    globalThis.localStorage.setItem(RENDERER_FLAG_KEY, version);
  } catch {
    /* best-effort: a blocked localStorage just means the flag isn't sticky. */
  }
}

export interface TopologyUIState {
  activeLayers: Set<Layer>;
  viewMode: TopologyViewMode;
  /** Graph-view renderer engine (v1 React Flow / v2 canvas). See
   * RendererVersion — persisted per-browser in localStorage, not part of the
   * server-saved layout. */
  rendererVersion: RendererVersion;
  vlanFilter: number | undefined;
  selectedId: string | undefined;
  hoveredId: string | undefined;
  spotlightOpen: boolean;
  expandedGroups: Set<string>;
  positions: Record<string, { x: number; y: number }>;
  /** "Traffic" paint mode (docs/features/monitoring.md §1: edge thickness/
   * heat by current utilization %). Not part of the persisted layout —
   * unlike activeLayers/vlanFilter/positions, it's a live-data view toggle
   * a user flips per session, not a saved arrangement. */
  trafficMode: boolean;
  /** T-1003 "Flows" layer: paints active guest-pair conversations as
   * animated/weighted edges over the v2 canvas renderer (topology/
   * flowEdges.ts) — visually distinct from trafficMode's per-entity heat
   * so both can be on at once. Same per-session (not persisted-layout)
   * lifetime as trafficMode. v2-renderer-only, per this task's card. */
  flowsLayerActive: boolean;
  /** T-1303 "Latency" heatmap layer: color-scales every node-to-node link
   * this node's own latmesh scheduler probes by rolling RTT/loss
   * (topology/latencyMode.ts) — a second, independent overlay from
   * trafficMode (per-entity utilization heat) and flowsLayerActive
   * (guest-pair conversation edges); any/all three can be on at once. Same
   * per-session (not persisted-layout) lifetime as the other two.
   * v2-renderer-only, mirroring flowsLayerActive's own scope. */
  latencyLayerActive: boolean;

  toggleLayer: (layer: Layer) => void;
  /** T-907: sets the whole active-layer set at once (as opposed to
   * toggleLayer's single-layer flip) — used when applying a saved view or a
   * shareable-URL view, whose captured `layers` is a complete replacement,
   * not a delta. An empty array is treated as "every layer" (mirrors
   * hydrateFromLayout's identical empty-array convention below), so a
   * malformed/legacy saved view never silently blanks the map. */
  setActiveLayers: (layers: Layer[]) => void;
  setViewMode: (mode: TopologyViewMode) => void;
  setRendererVersion: (version: RendererVersion) => void;
  toggleTrafficMode: () => void;
  toggleFlowsLayer: () => void;
  toggleLatencyLayer: () => void;
  setVlanFilter: (vlan: number | undefined) => void;
  select: (id: string | undefined) => void;
  hover: (id: string | undefined) => void;
  setSpotlightOpen: (open: boolean) => void;
  toggleExpanded: (groupId: string) => void;
  setPosition: (id: string, pos: { x: number; y: number }) => void;
  setPositions: (positions: Record<string, { x: number; y: number }>) => void;
  /** Applies a saved layout fetched from the server on load — replaces
   * activeLayers/vlanFilter/positions wholesale rather than merging, since
   * a saved layout is a complete snapshot of that state. */
  hydrateFromLayout: (payload: TopologyLayoutPayload) => void;
}

const DEFAULT_ACTIVE_LAYERS = new Set<Layer>(ALL_LAYERS);

export const useTopologyStore = create<TopologyUIState>((set) => ({
  activeLayers: DEFAULT_ACTIVE_LAYERS,
  viewMode: "switch",
  rendererVersion: readRendererFlag(),
  vlanFilter: undefined,
  selectedId: undefined,
  hoveredId: undefined,
  spotlightOpen: false,
  expandedGroups: new Set(),
  positions: {},
  trafficMode: false,
  flowsLayerActive: false,
  latencyLayerActive: false,

  toggleLayer: (layer) => {
    set((state) => {
      const next = new Set(state.activeLayers);
      if (next.has(layer)) next.delete(layer);
      else next.add(layer);
      return { activeLayers: next };
    });
  },
  setActiveLayers: (layers) => {
    set({ activeLayers: new Set(layers.length > 0 ? layers : ALL_LAYERS) });
  },
  setViewMode: (mode) => {
    set({ viewMode: mode });
  },
  setRendererVersion: (version) => {
    writeRendererFlag(version);
    set({ rendererVersion: version });
  },
  toggleTrafficMode: () => {
    set((state) => ({ trafficMode: !state.trafficMode }));
  },
  toggleFlowsLayer: () => {
    set((state) => ({ flowsLayerActive: !state.flowsLayerActive }));
  },
  toggleLatencyLayer: () => {
    set((state) => ({ latencyLayerActive: !state.latencyLayerActive }));
  },
  setVlanFilter: (vlan) => {
    set({ vlanFilter: vlan });
  },
  select: (id) => {
    set({ selectedId: id });
  },
  hover: (id) => {
    set({ hoveredId: id });
  },
  setSpotlightOpen: (open) => {
    set({ spotlightOpen: open });
  },
  toggleExpanded: (groupId) => {
    set((state) => {
      const next = new Set(state.expandedGroups);
      if (next.has(groupId)) next.delete(groupId);
      else next.add(groupId);
      return { expandedGroups: next };
    });
  },
  setPosition: (id, pos) => {
    set((state) => ({ positions: { ...state.positions, [id]: pos } }));
  },
  setPositions: (positions) => {
    set({ positions });
  },
  hydrateFromLayout: (payload) => {
    set({
      positions: payload.positions,
      activeLayers: new Set(payload.activeLayers.length > 0 ? payload.activeLayers : ALL_LAYERS),
      vlanFilter: payload.vlanFilter,
    });
  },
}));

/** Extracts the persistable subset of the store (docs/features/topology.md
 * §2: "manual repositioning persists per user"; deliverable #8 also
 * persists active filters) — the shape saveLayout's PUT body wants. */
export function currentLayoutPayload(state: TopologyUIState): TopologyLayoutPayload {
  return {
    positions: state.positions,
    activeLayers: Array.from(state.activeLayers),
    vlanFilter: state.vlanFilter,
  };
}
