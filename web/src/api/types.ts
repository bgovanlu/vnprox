// Hand-maintained mirror of docs/api.md's request/response shapes (a
// generation task exists in the P6 backlog — see docs/development.md's
// TypeScript standards section). Keep this file the single source of
// truth for wire types; do not redeclare API shapes elsewhere.
//
// T-005 only implements the Auth surface end-to-end (everything else in
// docs/api.md is a later task's responsibility). The remaining sections
// are stubbed out below so later tasks extend this file instead of
// creating a second, competing types module.

/** The `{"error": {...}}` envelope documented in docs/api.md's conventions
 * section, returned with the matching HTTP status on every non-2xx
 * response. `code` is a stable identifier (`validation_failed`,
 * `pve_denied`, `changeset_locked`, `peer_unreachable`, ...). */
export interface ErrorEnvelope {
  error: {
    code: string;
    message: string;
    details?: Record<string, unknown>;
  };
}

// --- Auth (docs/api.md §Auth) ---------------------------------------------

/** Per-node capability flags returned by GET /auth/me, per docs/api.md. */
export interface Capabilities {
  netRead: boolean;
  netWrite: boolean;
  sdnRead: boolean;
  sdnWrite: boolean;
  fwRead: boolean;
  fwWrite: boolean;
  guestNet: boolean;
  audit: boolean;
}

export interface AuthUser {
  username: string;
  realm: string;
}

/** POST /auth/login request body. `otp` is only present for realms that
 * require a second factor. */
export interface LoginRequest {
  username: string;
  password: string;
  realm: string;
  otp?: string;
}

/** Shared response shape for POST /auth/login and GET /auth/me. */
export interface MeResponse {
  user: AuthUser;
  caps: Record<string, Capabilities>;
}

// --- Topology & inventory (docs/api.md §Inventory & topology) --------------
// Mirrors internal/topology/types.go exactly — see that file's doc comments
// for the non-obvious bits (nodeGroup "" is the cluster-spanning SDN band;
// guest-collapse synthetic ids look like "guest-group:<node>:<targetRef>"
// and are not valid inventory Refs).

/** The four toggleable layers (docs/features/topology.md §1). These are the
 * exact short tokens the backend's `?layers=` filter and every node's
 * `layer` field use — not topology.md's prose layer names. */
export type Layer = "phys" | "l2" | "sdn" | "guest";

export const ALL_LAYERS: readonly Layer[] = ["phys", "l2", "sdn", "guest"];

/** Rendering status painting (docs/features/topology.md §2). "unknown" is a
 * legitimate, common value for peer (non-local) nodes' link/bond status —
 * those only get PVE-declared data, not live host-netlink data — not a bug
 * to work around client-side. */
export type EntityStatus = "ok" | "down" | "degraded" | "unknown";

export interface TopologyNode {
  id: string;
  kind: string;
  label: string;
  layer: Layer;
  /** "" is the sentinel for the cluster-spanning SDN band; otherwise a PVE
   * node name (the column this node renders in). */
  nodeGroup: string;
  status: EntityStatus;
  badges: string[];
  /** Present only on synthetic "guest-group" pill nodes. */
  collapsedCount?: number;
}

export interface TopologyEdge {
  from: string;
  to: string;
  kind: string;
  status: EntityStatus;
  badges: string[];
}

export interface TopologyResponse {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  layers: Layer[];
  generatedAt: number;
}

/** GET /topology's optional `?layers=&node=&vlan=` server-side filters. */
export interface TopologyFilter {
  layers?: Layer[];
  node?: string;
  vlan?: number;
}

export interface SourceValue {
  source: string;
  value: string;
}

export interface FieldSource {
  owner: string;
  conflicts?: SourceValue[];
}

export interface RelatedRef {
  ref: string;
  edgeKind: string;
  direction: "from" | "to";
}

/** GET /inventory/{ref}. "Raw source" here is resolved-field provenance
 * (which source won each field, and any dissenting values) — not the
 * original interfaces(5)/PVE JSON text, which the graph doesn't retain past
 * ingestion (a documented limitation, not something to work around). */
export interface EntityDetail {
  ref: string;
  kind: string;
  node: string;
  label: string;
  fields: Record<string, unknown>;
  provenance: Record<string, FieldSource>;
  related: RelatedRef[];
  generatedAt: number;
}

export interface SearchResult {
  ref: string;
  kind: string;
  label: string;
  node: string;
  matchedField: string;
  score: number;
}

export interface SearchResponse {
  results: SearchResult[];
}

/** The `topology.delta` WS event (docs/api.md's WebSocket section):
 * `{added, updated, removed: [Ref]}`. */
export interface TopologyDeltaEvent {
  event: "topology.delta";
  added: string[];
  updated: string[];
  removed: string[];
}

// --- Saved layouts (internal/api/layouts.go, additive — no docs/api.md
// entry existed before this task; see that file's doc comment) -----------

/** The frontend-owned canvas state a saved layout persists: manual node
 * repositioning plus active filters (docs/features/topology.md §2: "manual
 * repositioning persists per user"). Opaque to the backend, which stores it
 * as a JSON blob. */
export interface TopologyLayoutPayload {
  positions: Record<string, { x: number; y: number }>;
  activeLayers: Layer[];
  vlanFilter?: number;
}

export interface LayoutResponse {
  name: string;
  layout: TopologyLayoutPayload;
  updatedAt: number;
}

// --- Everything else in docs/api.md ---------------------------------------
// Changesets, snapshots, firewall/SDN/IPAM read views, the path simulator,
// metrics, and blueprints all have routes defined in docs/api.md but no
// frontend consumer yet — their request/response types land with the task
// that first calls them (T-2xx). Add them here, not in a parallel file.
