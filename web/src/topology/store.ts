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

export interface TopologyUIState {
  activeLayers: Set<Layer>;
  vlanFilter: number | undefined;
  selectedId: string | undefined;
  hoveredId: string | undefined;
  spotlightOpen: boolean;
  expandedGroups: Set<string>;
  positions: Record<string, { x: number; y: number }>;

  toggleLayer: (layer: Layer) => void;
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
  vlanFilter: undefined,
  selectedId: undefined,
  hoveredId: undefined,
  spotlightOpen: false,
  expandedGroups: new Set(),
  positions: {},

  toggleLayer: (layer) => {
    set((state) => {
      const next = new Set(state.activeLayers);
      if (next.has(layer)) next.delete(layer);
      else next.add(layer);
      return { activeLayers: next };
    });
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
