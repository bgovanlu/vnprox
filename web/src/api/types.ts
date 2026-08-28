// SPDX-License-Identifier: Apache-2.0

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
  /** T-1301: the dedicated packet-capture gate (internal/auth.CapCapture) —
   * strictly stronger than netWrite (Sys.Modify AND Sys.Console); holding
   * netRead/netWrite alone never implies this. */
  capture: boolean;
  // KNOWN GAP, deliberately left open by T-3003 rather than closed silently:
  // `internal/auth.Capabilities` also carries `automation` (T-1104's
  // CapAutomation) and emits it on every `GET /auth/me` response with no
  // `omitempty`, so this mirror is one field short of the wire. It is not
  // added here because `keyof Capabilities` is consumed as an exhaustive key
  // set elsewhere (`changesets/capabilities.ts`'s CAP_LABELS), so widening it
  // is a cross-module change this card does not own. Nothing is lost in
  // practice: `automation` is never derived from a PVE privilege, so a cookie
  // session's value is always false, and `settings/WebhooksSection.tsx`
  // decides reachability from the daemon's own answer to `GET /webhooks`
  // rather than from this map. See the T-3003 report.
}

export interface AuthUser {
  username: string;
  realm: string;
}

/** One PVE privilege a denied capability still needs, per
 * internal/auth.PrivilegeRequirement — carried inside PermissionExplanation.
 * `path` is the PVE ACL path granting `privilege` there would satisfy for
 * this request's own scope: `/nodes/{node}` or cluster-wide `/`. `confirmed`
 * is false when the daemon names this privilege as required but cannot
 * confirm its current absence from already-derived data alone (T-4105:
 * `capture`'s `Sys.Console` has no dedicated capability flag) — still shown,
 * just not asserted as certain. */
export interface PermissionRequirement {
  privilege: string;
  path: string;
  confirmed: boolean;
}

/** T-4105's "why can't I?" answer, mirroring internal/auth.Explanation.
 * Present as `details.explanation` on a `403 forbidden` from a
 * capability-gated route (docs/api.md's error-envelope conventions) — never
 * a separate round trip. `missing` names the PVE privilege(s) still absent;
 * `reason` replaces it entirely for a capability that isn't PVE-privilege
 * derived at all, or for a session shape (OIDC, an API token, a
 * `read_only` daemon) where naming one privilege would leak or mislead — see
 * docs/api.md's error-envelope section for the full list. */
export interface PermissionExplanation {
  capability: string;
  granted: boolean;
  missing?: PermissionRequirement[];
  reason?: string;
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

/** One open finding naming an entity (T-3501, `internal/topology.FindingBadge`)
 * — the source-and-severity-bearing form that replaced the single bare
 * `"drift"` wire badge's "which kind, how bad" gap. `badges[]` still carries
 * a compact `"finding:<source>:<severity>"` token per distinct source (one
 * per source, worst severity) plus the legacy bare `"drift"` token for wire
 * back-compat (docs/api.md) — this is the richer form carrying the finding's
 * own `check`/`detail` text, for hover/selection (`findingBadges.ts`'s
 * `findingsFor` groups these by source). `source` is `FindingSource` except
 * for the drift-only fallback path (`paintDrift`, no `FindingsService`
 * wired), which always reports `"drift"`. */
export interface FindingBadge {
  source: FindingSource;
  severity: Severity;
  check: string;
  detail: string;
  /** Phase 36's producer-declared remedy, carried onto the map so a
   * finding offers the same action wherever it is shown. Absent for a
   * detection-only finding. Structurally identical to `StreamFinding`'s —
   * `internal/topology` mirrors `internal/findings.Remediation` because the
   * Go import direction forbids sharing the type; a test pins the two. */
  remedy?: Remediation;
}

/** A `FindingBadge` for a finding with no entity refs at all — nothing to
 * paint it on (T-3501 AC5, e.g. `health/service_down` for a bare service
 * name like dnsmasq/frr). `nodes` is the finding's own `Nodes` list, carried
 * here since there is no ref to attach it to. Surfaced via
 * `TopologyResponse.unrefFindings`, rendered by `UnrefFindingsBanner`. */
export interface UnrefFinding extends FindingBadge {
  nodes: string[];
}

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
  /** T-3501, additive to `badges`: one entry per open finding naming this
   * node, carrying its own check/detail text. Absent/empty when the entity
   * carries no open finding. */
  findings?: FindingBadge[];
  /** T-3503, physnic nodes only: the negotiated link speed. Absent — never
   * 0, never a stale last-known figure — when the kernel reports no speed,
   * which is what it does for a NIC with no carrier (Linux reports -1; see
   * planning/reports/evidence/pve-9.2.4-nic-media-and-speed.txt). The Switch
   * view silkscreens this above the port body. */
  speedMbps?: number;
  /** T-3503, physnic nodes only: the NIC's PORT_* media/connector type
   * ("tp" | "aui" | "mii" | "fibre" | "bnc" | "da" | "none" | "other"),
   * lowercased from linux/ethtool.h's PORT_* constants. Absent when
   * unreported or unrecognised — never guessed. Unlike `speedMbps`, this is
   * set independently of link carrier state: the kernel reports a NIC's
   * port type even with no carrier (see
   * planning/reports/evidence/pve-9.2.4-nic-media-and-speed.txt point 2),
   * so a down copper link still carries `mediaPort: "tp"`. The Switch
   * view's faceplate uses this to pick a port body (RJ45 for copper, SFP
   * cage for fibre/DA) independently of whether a speed is known. */
  mediaPort?: string;
  /** T-3907, physnic nodes only: the negotiated duplex mode ("full" |
   * "half"), read off the same ethtool call as `speedMbps`/`mediaPort`.
   * Absent, independently of those two, when the driver didn't answer that
   * sub-read — never guessed. The cabling plan view is the first consumer;
   * see docs/api.md's "GET /topology physical-port fields". */
  duplex?: string;
  /** Present only on synthetic "guest-group"/"phys-group" pill nodes. */
  collapsedCount?: number;
  /** Present only on synthetic "phys-group:<node>" per-node physical-layer
   * summary pills (T-1907, internal/topology/collapse_physical.go): the Ref
   * strings of every entity this pill absorbed. A guest-group pill has no
   * such field — its single shared attachment target already gives the
   * frontend a place to discover membership (see expand.ts) — so this is
   * the phys-group-only exception to that pattern. */
  members?: string[];
}

export interface TopologyEdge {
  from: string;
  to: string;
  kind: string;
  status: EntityStatus;
  badges: string[];
  /** T-3501, mirrors TopologyNode.findings — see its doc comment. Present
   * for shape symmetry; no producer currently names an edge in a finding's
   * refs (a finding names entities, and an edge has no ref of its own), so
   * this is always empty/absent from the live backend today. */
  findings?: FindingBadge[];
}

/** One collector poll loop's freshness (docs/api.md's GET /topology
 * staleness section). `name` is the loop name ("pve", "host", "lldp");
 * `node` scopes the source to one cluster node's band, absent =
 * cluster-wide (the "pve" loop). `stale` flips true after 3 consecutive
 * poll failures. `lastSuccess` (unix seconds) is absent if no poll has ever
 * succeeded; `lastError` is present only while the source is failing. */
export interface SourceStaleness {
  name: string;
  node?: string;
  lastError?: string;
  lastSuccess?: number;
  stale: boolean;
}

/** GET /topology's optional freshness summary — the data behind
 * docs/features/topology.md §5's greyed-band + staleness-banner state.
 * `stale` is true iff any source is stale. The whole section is omitted
 * when the daemon has no collector status at all. */
export interface Staleness {
  sources: SourceStaleness[];
  stale: boolean;
}

export interface TopologyResponse {
  staleness?: Staleness;
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  layers: Layer[];
  /** T-3501 AC5: every open finding whose refs are empty, so a
   * `health/service_down` finding for a bare service name is not silently
   * invisible just because it names no map entity. See `UnrefFinding`. */
  unrefFindings?: UnrefFinding[];
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

/** GET /inventory/{ref}. `rawSource` maps each contributing source name to
 * the raw text that source's contribution was derived from — the verbatim
 * interfaces(5) stanza for "host-interfaces", pretty-printed JSON of the
 * PVE API object for "pve-*" sources, compact JSON of observed state for
 * "host-netlink"/"host-lldp" (docs/api.md's response-shape note; every
 * value is a string). Omitted when no source retained raw text.
 * `provenance` stays alongside it: rawSource shows what each source said
 * verbatim, provenance shows which source won each resolved field.
 *
 * `fields` includes tri-state flags for booleans that a source may simply
 * not have reported: when `LinkUpSet`/`VlanAwareSet`/`STPSet` is false, the
 * matching `LinkUp`/`VlanAware`/`STP` value is *unknown*, not false — the
 * UI must render it as such (see InspectorPanel's fieldRows). */
export interface EntityDetail {
  ref: string;
  kind: string;
  node: string;
  label: string;
  fields: Record<string, unknown>;
  provenance: Record<string, FieldSource>;
  rawSource?: Record<string, string>;
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

// --- WireGuard tunnels (T-1401 backend, T-1402 map edges + wizard) --------
// Mirrors internal/api/wireguard.go's WireGuardTunnelView/WireGuardPeerView/
// WireGuardTunnelStatus exactly. Read-only — every mutation goes through the
// wg.* changeset op family below, never a dedicated write route.

export interface WireGuardPeer {
  publicKey: string;
  endpoint?: string;
  observedEndpoint?: string;
  allowedIps: string[];
  keepaliveSec?: number;
  /** Unix seconds; absent/0 means "never handshaked" — per T-1401's own
   * findings semantics (health_wireguard.go's checkWgHandshakeStale doc
   * comment), a peer with no handshake age at all is NOT stale — a
   * freshly-created tunnel must not immediately paint amber. */
  lastHandshakeUnix?: number;
  rxBytes: number;
  txBytes: number;
  external: boolean;
  endpointDrifted: boolean;
}

export interface WireGuardTunnelStatus {
  interfaceUp: boolean;
  peerCount: number;
}

export interface WireGuardTunnel {
  id: string;
  node: string;
  ifName: string;
  publicKey: string;
  carrier?: string;
  addresses: string[];
  peers: WireGuardPeer[];
  status: WireGuardTunnelStatus;
  listenPort: number;
  mtu: number;
}

export interface WireGuardTunnelsResponse {
  items: WireGuardTunnel[];
}

// --- Kubernetes (T-1501 backend, T-1502 map layer + flow attribution) -----
// Mirrors internal/k8s.Overlay / internal/api/k8s.go's response shapes
// exactly (docs/api.md's Kubernetes section). Read-only forever: no route
// or type in this section is ever the target of a PATCH/PUT/DELETE/POST
// beyond registering/deregistering a *local* cluster record — see
// web/src/api/k8s.ts's own doc comment.

/** GET /k8s/clusters' list item. `kubeconfig` is never echoed back by any
 * route — this shape carries only what's safe to display. */
export interface K8sCluster {
  id: string;
  name: string;
  addedBy: string;
  addedAt: number;
  /** "flannel"|"calico"|"cilium"|"unknown"|"" (never polled yet) — the last
   * poll's cached summary, not authoritative (see GET /k8s/{id}/overlay's
   * own `cni` field for a fresh read). */
  cniDetected?: string;
  /** "unpolled"|"ok"|"unreachable". */
  status: string;
}

export interface K8sClustersResponse {
  items: K8sCluster[];
}

/** One k8s node's advertised pod-network block (internal/k8s.PodCIDR). */
export interface K8sPodCidr {
  node: string;
  cidr: string;
}

export interface K8sServicePortInfo {
  name?: string;
  protocol: string;
  port: number;
  nodePort?: number;
}

/** internal/k8s.ServiceInfo — never a guessed "service CIDR" (see
 * internal/k8s/overlay.go's own doc comment); every service is carried in
 * full instead. */
export interface K8sServiceInfo {
  namespace: string;
  name: string;
  /** "ClusterIP"|"NodePort"|"LoadBalancer"|"ExternalName". */
  type: string;
  clusterIp?: string;
  ports?: K8sServicePortInfo[];
}

/** internal/k8s.PodSummary — deliberately minimal (docs/api.md's Kubernetes
 * section), carried for this task's pod-drilldown selection. */
export interface K8sPodSummary {
  namespace: string;
  name: string;
  node?: string;
  podIp?: string;
  phase?: string;
}

/** internal/k8s.NodeCorrelation. `matched: false` (never a wrong
 * `guestRef`) when nothing in the live inventory/IPAM data claims the
 * node's reported InternalIP — "observed, never guessed". */
export interface K8sNodeCorrelation {
  k8sNode: string;
  internalIp?: string;
  guestRef?: string;
  matched: boolean;
}

/** internal/k8s.NodePortFinding, also surfaced in the unified GET /findings
 * stream (`source: "k8s"`, `check: "k8s_nodeport_exposed_without_fw_rule"`) —
 * carried alongside the overlay so a single poll answers both. */
export interface K8sNodePortFinding {
  clusterId: string;
  namespace: string;
  service: string;
  port: number;
  nodePort: number;
  proto: string;
  refs: string[];
  detail: string;
}

/** GET /k8s/{clusterId}/overlay's response (internal/k8s.Overlay plus the
 * poll's own findings) — computed fresh on every call, never cached as
 * authoritative. */
export interface K8sOverlay {
  clusterId: string;
  cni: string;
  podCidrs: K8sPodCidr[];
  services: K8sServiceInfo[];
  pods: K8sPodSummary[];
  nodes: K8sNodeCorrelation[];
  generatedAt: number;
  nodePortFindings?: K8sNodePortFinding[];
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

/** GET /layouts (T-907) — every layout/saved-view the requesting user has
 * saved (docs/api.md's Saved views & annotations section), including the
 * reserved "topology"/"onboarding" auto-layout blobs alongside any named
 * saved views. `layout` is typed as `unknown` here (not
 * TopologyLayoutPayload) because a list item may be either shape — callers
 * narrow with isSavedViewPayload (savedViews.ts) to find the actual named
 * views. */
export interface LayoutListItem {
  name: string;
  layout: unknown;
  updatedAt: number;
}

export interface LayoutListResponse {
  items: LayoutListItem[];
}

/** T-907's named saved view: a preset of the topology page's own layer/
 * filter/zoom/selection/view-mode state, stored as a `layouts` row's
 * opaque `layout` blob under a user-chosen name (docs/api.md's Saved views
 * & annotations section). `kind: "view"` is the discriminator that tells a
 * saved view apart from the reserved "topology"/"onboarding" auto-layout
 * blobs when listing — see LayoutListItem above. */
export interface SavedViewPayload {
  kind: "view";
  layers: Layer[];
  vlanFilter?: number;
  zoom: number;
  viewport: { x: number; y: number };
  selection?: string;
  view: "graph" | "switch";
}

/** T-3911's composable dashboard: one entry in a saved tile grid's ordered,
 * visible tile list. `id` is either a fixed built-in tile id
 * (`web/src/dashboard/tileRegistry.ts`, e.g. `"builtin:findings"`) or a
 * plugin-provided tile's `DashboardTile.id` (GET /dashboard/tiles) prefixed
 * `"plugin:"`. Array position (in DashboardLayoutPayload.tiles) is display
 * order; a tile with no entry is hidden, not deleted from either catalog. */
export interface DashboardTileRef {
  id: string;
  kind: "builtin" | "plugin";
}

/** T-3911's reserved `"dashboard"` layout blob (docs/api.md's "Dashboard
 * tile layout shape" note): the home dashboard's tile grid, stored via the
 * same per-user `layouts` mechanism `TopologyLayoutPayload`/
 * `SavedViewPayload` above already use — no fourth persistence idiom.
 * `kind: "dashboard-tiles"` is this blob's own discriminator, the same role
 * `SavedViewPayload.kind` plays for named views. */
export interface DashboardLayoutPayload {
  kind: "dashboard-tiles";
  tiles: DashboardTileRef[];
}

// --- Annotations (internal/api/annotations.go, T-907) ---------------------

/** GET/POST /annotations' Annotation shape (docs/api.md's Saved views &
 * annotations section): a free-text sticky note pinned to a map entity's
 * Ref, shared across every user (not private per-user data). */
export interface Annotation {
  id: string;
  ref: string;
  content: string;
  createdBy: string;
  createdAt: number;
  updatedAt: number;
  /** T-2806: unix seconds, 0 = never expires. */
  expiresAt: number;
  /** T-2806: `expiresAt` judged by the daemon at the instant of this read —
   * never a stored flag, so a stopped daemon cannot leave an expired note
   * on display. The default list omits expired notes entirely; they appear
   * only via `?includeExpired=true`. */
  expired: boolean;
  /** T-2806: `ref` names no entity in the current inventory — the annotated
   * thing was deleted and this note outlived it. Derived per read, and the
   * note is deliberately retained: it may be the only record of why the
   * entity was removed. */
  orphaned: boolean;
}

export interface AnnotationListResponse {
  items: Annotation[];
}

/** GET/POST /map-regions' MapRegion shape (T-2806): a labelled rectangle
 * drawn on the topology canvas in GRAPH coordinate space — the same space
 * node positions use, so a region holds its position relative to the
 * entities it encloses under any pan/zoom. Shared across every user, and
 * stored server-side in its own table rather than inside the per-user
 * layout blob, which is what makes it survive layout auto-saves. */
export interface MapRegion {
  id: string;
  label: string;
  color: string;
  createdBy: string;
  x: number;
  y: number;
  w: number;
  h: number;
  createdAt: number;
  updatedAt: number;
  expiresAt: number;
  expired: boolean;
}

export interface MapRegionListResponse {
  items: MapRegion[];
}

// --- Changesets (docs/api.md §Changesets; internal/change's Go types) -----
// Mirrors internal/change/{op,changeset}.go's wire shapes exactly (see
// planning/reports/T-201.md/T-202.md/T-205.md for the contract this was
// built against). `Op` is a tagged union `{op, target, params}` — `target`
// is a Ref **string** ("kind:node:id", null only for "sdn.apply"), not a
// nested object (T-201's report flags this as the documented convention:
// every other Ref-typed field in this codebase's JSON is a string). Params
// shapes are typed per op family below; a plain `Record<string, unknown>`
// would defeat the "no `any`, no unchecked casts" rule for every editor
// that needs to read/write specific fields.

/** The v1 op vocabulary (docs/data-model.md §3 / internal/change/op.go's
 * OpType constants). Kept as a plain string union (not every family has a
 * frontend consumer yet — firewall/IPAM ops are not editable as of T-402,
 * see that task's report) rather than an enum so unknown-to-this-file
 * values (still valid on the wire) don't need a cast. */
export type OpType =
  | "iface.update"
  | "iface.rename"
  | "iface.raw.replace"
  | "bond.create"
  | "bond.update"
  | "bond.delete"
  | "bridge.create"
  | "bridge.update"
  | "bridge.delete"
  | "bridge.port.add"
  | "bridge.port.remove"
  | "vlan.create"
  | "vlan.update"
  | "vlan.delete"
  | "sdn.zone.create"
  | "sdn.zone.update"
  | "sdn.zone.delete"
  | "sdn.vnet.create"
  | "sdn.vnet.update"
  | "sdn.vnet.delete"
  | "sdn.subnet.create"
  | "sdn.subnet.update"
  | "sdn.subnet.delete"
  | "sdn.fabric.create"
  | "sdn.fabric.update"
  | "sdn.fabric.delete"
  | "sdn.controller.create"
  | "sdn.controller.update"
  | "sdn.controller.delete"
  | "sdn.ipam.create"
  | "sdn.ipam.update"
  | "sdn.ipam.delete"
  | "sdn.apply"
  | "guest.nic.update"
  | "fw.rule.create"
  | "fw.rule.update"
  | "fw.rule.delete"
  | "fw.rule.move"
  | "fw.options.update"
  | "fw.alias.create"
  | "fw.alias.update"
  | "fw.alias.delete"
  | "fw.ipset.create"
  | "fw.ipset.update"
  | "fw.ipset.delete"
  | "fw.group.create"
  | "fw.group.update"
  | "fw.group.delete"
  | "ipam.alloc.create"
  | "ipam.alloc.delete"
  | "qos.shape.create"
  | "qos.shape.update"
  | "qos.shape.delete"
  | "wg.tunnel.create"
  | "wg.tunnel.update"
  | "wg.tunnel.delete"
  | "wg.peer.add"
  | "wg.peer.remove";

/** internal/change's VidRange: an inclusive VLAN ID range (Low === High for
 * a single VID). */
export interface VidRange {
  low: number;
  high: number;
}

// --- Params for the op families T-207's editors emit (internal/change/
// params_{iface,bond,bridge,vlan,guest}.go). The frontend never needs to
// send an explicit `null` today (every editor either omits a field or sends
// a concrete value) so fields are plain optionals here, not the Go
// pointer-field "present-null-means-clear" tri-state — see each Params
// struct's own Go doc comment for that wire nuance if a future editor needs
// an explicit-clear affordance.

export interface IfaceUpdateParams {
  mtu?: number;
  comments?: string;
  addresses?: string[];
  gateway?: string;
  autostart?: boolean;
  /** T-703: explicitly clear the stanza's address/gateway (the
   * dedicated-management-VLAN flow takes the address+route OFF the old
   * carrier). Only honored when the value-setting sibling is absent, per
   * internal/change/ifaces.mutateIfaceUpdate's precedence. */
  removeAddress?: boolean;
  removeGateway?: boolean;
}

/** T-208's raw editor save op: params = the node's full new file content,
 * plus the sha256 read at editor-open time (the hash-conflict guard —
 * internal/change.IfaceRawReplaceParams). `target` is a `node:<n>:<n>` Ref
 * (the whole file, not one entity — see rawEditor/rawEditorOps.ts). */
export interface IfaceRawReplaceParams {
  content: string;
  baseHash?: string;
}

export interface BondCreateParams {
  mode: string;
  lacpRate?: string;
  xmitHashPolicy?: string;
  comments?: string;
  /** OVS-only: the OVS bridge this bond attaches to (rendered as
   * ovs_bridge). Required when target is an "ovs-bond:..." ref, ignored
   * for a plain "bond:..." ref (internal/change.BondCreateParams' doc
   * comment). */
  bridge?: string;
  slaves: string[];
  miimon?: number;
  mtu?: number;
}

export interface BondUpdateParams {
  mode?: string;
  slaves?: string[];
  lacpRate?: string;
  xmitHashPolicy?: string;
  miimon?: number;
  mtu?: number;
  comments?: string;
}

export type BondDeleteParams = Record<string, never>;

export interface BridgeCreateParams {
  gateway?: string;
  comments?: string;
  ports?: string[];
  vids?: VidRange[];
  addresses?: string[];
  mtu?: number;
  vlanAware?: boolean;
  stp?: boolean;
}

export interface BridgeUpdateParams {
  vlanAware?: boolean;
  vids?: VidRange[];
  addresses?: string[];
  gateway?: string;
  mtu?: number;
  stp?: boolean;
  comments?: string;
}

export type BridgeDeleteParams = Record<string, never>;

export interface BridgePortAddParams {
  port: string;
}

export interface BridgePortRemoveParams {
  port: string;
}

export interface VlanCreateParams {
  parent: string;
  addresses?: string[];
  /** T-703: the sub-interface's default gateway (rendered as the stanza's
   * `gateway` option) — a VLAN carrier taking over a node's management IP
   * needs the node's default route too (internal/change.VlanCreateParams). */
  gateway?: string;
  vid: number;
  mtu?: number;
  /** True for an OVS Int Port (ovs_type=OVSIntPort) instead of a plain
   * 802.1q VLAN sub-interface: parent then names an OVS bridge, vid becomes
   * the OVS access "tag" (0 = untagged/native), and trunks may carry an
   * additional trunked VLAN range set (internal/change.VlanCreateParams'
   * doc comment). */
  ovs?: boolean;
  /** OVS-only trunk VLAN ranges (ovs-vsctl's Port "trunks" column);
   * rejected by the backend when ovs is not true. */
  trunks?: VidRange[];
}

export interface VlanUpdateParams {
  addresses?: string[];
  mtu?: number;
}

export type VlanDeleteParams = Record<string, never>;

export interface GuestNicUpdateParams {
  bridgeOrVnet?: string;
  vid?: number;
  rateMbps?: number;
  firewall?: boolean;
  linkDown?: boolean;
}

/** internal/change.IpamAllocCreateParams (T-405's ipam.alloc.create op):
 * target is the owning SdnSubnet Ref, cidr is typically a /32 (or /128)
 * host route. */
export interface IpamAllocCreateParams {
  cidr: string;
  hostname?: string;
  mac?: string;
  comment?: string;
}

/** internal/change.IpamAllocDeleteParams (T-405's ipam.alloc.delete op). */
export interface IpamAllocDeleteParams {
  cidr: string;
}

// --- Params for the wg.* op family (T-1401's internal/change/params_wg.go,
// T-1402's frontend consumer). Target Ref conventions (not encoded in these
// param shapes themselves — see internal/change/params_wg.go's own doc
// comment, mirrored by web/src/wireguard/wizardOps.ts's target builders):
//   - wg.tunnel.* target "wg-tunnel:<node>:<tunnelId>".
//   - wg.peer.*   target "wg-peer:<node>:<tunnelId>/<peer public key>".
// A tunnel's keypair is never a param field — it is generated on the owning
// node at apply time and never rides an op, a response, or a log line.

export interface WgTunnelCreateParams {
  ifName: string;
  carrier?: string;
  addresses?: string[];
  listenPort?: number;
  mtu?: number;
}

/** internal/change.WgTunnelUpdateParams: every field is "leave unchanged if
 * omitted" on the wire (Go's pointer-field tri-state) — the frontend never
 * needs the explicit-null form (no editor here clears a field), so these
 * stay plain optionals like every other *UpdateParams in this file. */
export interface WgTunnelUpdateParams {
  listenPort?: number;
  addresses?: string[];
  mtu?: number;
  carrier?: string;
}

export type WgTunnelDeleteParams = Record<string, never>;

/** internal/change.WgPeerAddParams. `presharedKey` is a WRITE-ONLY ingest
 * field — the change service seals it into `presharedKeyEnc` at stage time
 * and strips the plaintext from every read response (T-1401's review fix
 * pass); this wizard only ever sets `presharedKey` (plaintext, at draft
 * time), never `presharedKeyEnc` directly. */
export interface WgPeerAddParams {
  publicKey: string;
  endpoint?: string;
  presharedKey?: string;
  clusterId?: string;
  allowedIps?: string[];
  keepaliveSec?: number;
  external?: boolean;
}

export interface WgPeerRemoveParams {
  publicKey: string;
}

// --- Params for the sdn.zone/vnet/subnet.* op family (T-402's editors,
// internal/change/params_sdn.go). Field names/shapes mirror that file's
// JSON tags exactly — note SdnSubnet*Params' `dhcpRanges` is an array of
// "start-end" strings (the op's own wire shape), distinct from GET /sdn's
// read-side SdnSubnet.dhcpRangeStart/dhcpRangeEnd (a single pair, PVE's own
// read wire shape T-401 passed through as-is) — two different shapes for
// two different purposes, both pre-existing.

export interface SdnZoneCreateParams {
  type: string;
  bridge?: string;
  controller?: string;
  ipam?: string;
  nodes?: string[];
  /** EVPN zones' egress path (T-403, docs/features/sdn.md §2: "exit nodes,
   * primary exit") — must name real cluster nodes. */
  exitNodes?: string[];
  /** VXLAN/EVPN zones' VTEP mesh peer addresses (T-403, docs/features/
   * sdn.md §2: "peer address list auto-suggested from cluster node IPs") —
   * underlay IP addresses, not node names; unlike nodes/exitNodes this is
   * not validated against the cluster's node list. */
  peers?: string[];
  vrfVxlan?: number;
  mtu?: number;
}

export interface SdnZoneUpdateParams {
  bridge?: string;
  controller?: string;
  ipam?: string;
  nodes?: string[];
  exitNodes?: string[];
  peers?: string[];
  vrfVxlan?: number;
  mtu?: number;
}

export type SdnZoneDeleteParams = Record<string, never>;

export interface SdnVnetCreateParams {
  zone: string;
  alias?: string;
  tag?: number;
  vlanAware?: boolean;
}

export interface SdnVnetUpdateParams {
  alias?: string;
  tag?: number;
  vlanAware?: boolean;
}

export type SdnVnetDeleteParams = Record<string, never>;

export interface SdnSubnetCreateParams {
  vnet: string;
  cidr: string;
  gateway?: string;
  dnsZonePrefix?: string;
  dhcpRanges?: string[];
  snat?: boolean;
}

export interface SdnSubnetUpdateParams {
  gateway?: string;
  dnsZonePrefix?: string;
  dhcpRanges?: string[];
  snat?: boolean;
}

export type SdnSubnetDeleteParams = Record<string, never>;

// --- SDN Fabric op params (T-3101; internal/change/params_sdn_fabric.go) --
// Mirrors that file field-for-field. `protocol` is conditional-schema: only
// the fields listed for the chosen protocol are meaningful (see
// FabricsView.tsx's protocol-conditional form and docs/api.md's `GET /sdn`
// section). `protocol` is real WireGuard when `"wireguard"` — a different
// management plane from `WgTunnelCreateParams` above (T-1401); the two
// never share a shape.

export interface SdnFabricCreateParams {
  protocol: string; // "bgp" | "openfabric" | "ospf" | "wireguard"
  ipPrefix?: string;
  ip6Prefix?: string;
  /** openfabric-only. */
  csnpInterval?: number;
  /** openfabric-only. */
  helloInterval?: number;
  /** openfabric and ospf both carry this. */
  routeFilter?: string;
  /** ospf-only. */
  area?: string;
  /** bgp and ospf both carry this. */
  redistribute?: string[];
  /** wireguard-only — real WireGuard's persistent-keepalive interval. */
  persistentKeepalive?: number;
}

export interface SdnFabricUpdateParams {
  ipPrefix?: string;
  ip6Prefix?: string;
  csnpInterval?: number;
  helloInterval?: number;
  routeFilter?: string;
  area?: string;
  redistribute?: string[];
  persistentKeepalive?: number;
}

export type SdnFabricDeleteParams = Record<string, never>;

// --- SDN Controller op params (T-3102; internal/change/params_sdn_controller.go) --
// Mirrors that file field-for-field. `type` is conditional-schema, the same
// "one struct, schema-validated combination" choice SdnFabricCreateParams
// documents: bgp gets asn/bgpMode/bgpMultipathAsPathRelax/ebgp/
// ebgpMultihop/peers; evpn gets fabric/peerGroupName/routeMapIn/
// routeMapOut; isis gets isisDomain/isisIfaces/isisNet; faucet gets none of
// the above (node/nodes/loopback are general — every type may set them).

export interface SdnControllerCreateParams {
  type: string; // "bgp" | "evpn" | "faucet" | "isis"
  bgpMode?: string; // bgp-only: "auto" | "external" | "internal"
  fabric?: string; // evpn-only
  isisDomain?: string; // isis-only
  isisNet?: string; // isis-only
  loopback?: string;
  node?: string;
  peerGroupName?: string; // evpn-only
  routeMapIn?: string; // evpn-only
  routeMapOut?: string; // evpn-only
  nodes?: string[];
  peers?: string[]; // bgp-only
  isisIfaces?: string[]; // isis-only
  asn?: number; // bgp-only
  ebgpMultihop?: number; // bgp-only
  ebgp?: boolean; // bgp-only
  bgpMultipathAsPathRelax?: boolean; // bgp-only
}

export interface SdnControllerUpdateParams {
  bgpMode?: string;
  fabric?: string;
  isisDomain?: string;
  isisNet?: string;
  loopback?: string;
  node?: string;
  peerGroupName?: string;
  routeMapIn?: string;
  routeMapOut?: string;
  nodes?: string[];
  peers?: string[];
  isisIfaces?: string[];
  asn?: number;
  ebgpMultihop?: number;
  ebgp?: boolean;
  bgpMultipathAsPathRelax?: boolean;
}

export type SdnControllerDeleteParams = Record<string, never>;

// --- SDN IPAM plugin-instance op params (T-3104;
// internal/change/params_sdn_ipam.go) --- Mirrors that file field-for-field.
// This is the configured IPAM *plugin object* itself (its connection
// config), not an allocation — see IpamAllocCreateParams below, unchanged.
// `type` is conditional-schema, the same "one struct, schema-validated
// combination" choice SdnFabricCreateParams/SdnControllerCreateParams
// document — except (params_sdn_ipam.go's own doc comment) the capture
// gives no per-type field breakdown for this family at all, so which
// fields apply to which type is this task's own documented inference:
// url/token/section/fingerprint apply to netbox/phpipam (url+token
// required); none apply to pve.
export interface SdnIpamCreateParams {
  type: string; // "netbox" | "phpipam" | "pve"
  url?: string; // required for netbox/phpipam
  token?: string; // required for netbox/phpipam; write-only, never read back
  fingerprint?: string;
  section?: number;
}

export interface SdnIpamUpdateParams {
  url?: string;
  token?: string;
  fingerprint?: string;
  section?: number;
}

export type SdnIpamDeleteParams = Record<string, never>;

export type SdnApplyParams = Record<string, never>;

// --- Firewall op params (T-502; internal/change/params_fw.go) -------------
// Mirrors the Go param structs field-for-field. Target is always a
// FwRuleset scope Ref ("fw-ruleset:<node>:<cluster|node|guest/kind/vmid>")
// per params_fw.go's doc comment — including for the alias/ipset/group
// ops, which carry their own `name` to identify which object within that
// scope's ruleset they operate on.

export interface FwRuleCreateParams {
  direction: string;
  action: string;
  proto?: string;
  source?: string;
  dest?: string;
  sport?: string;
  dport?: string;
  iface?: string;
  macro?: string;
  log?: string;
  comment?: string;
  pos: number;
  enabled: boolean;
}

export interface FwRuleUpdateParams {
  direction?: string;
  action?: string;
  proto?: string;
  source?: string;
  dest?: string;
  sport?: string;
  dport?: string;
  iface?: string;
  macro?: string;
  log?: string;
  comment?: string;
  enabled?: boolean;
  pos: number;
}

export interface FwRuleDeleteParams {
  pos: number;
}

/** The rule content the client observed at `fromPos` when the move was
 * drafted (internal/change.FwRuleFields) — the apply-time executor
 * re-fetches the live rule at `fromPos` and refuses the move if it no
 * longer matches (acceptance criterion 3's move-race guard). */
export interface FwRuleFields {
  direction: string;
  action: string;
  proto?: string;
  source?: string;
  dest?: string;
  sport?: string;
  dport?: string;
  iface?: string;
  macro?: string;
  log?: string;
  comment?: string;
  enabled: boolean;
}

export interface FwRuleMoveParams {
  fromPos: number;
  toPos: number;
  expect?: FwRuleFields;
}

export interface FwOptionsUpdateParams {
  defaultIn?: string;
  defaultOut?: string;
  /** The forward chain's own fallthrough policy/log level (T-3103).
   * defaultForward is valid at cluster/node/vnet scope (ACCEPT|DROP only —
   * no REJECT); logLevelForward is only hardware-confirmed at vnet scope.
   * See internal/change's schemaFwOptionsForScope — the server rejects
   * either at a scope it isn't valid for, so the UI should only ever send
   * defaultForward outside guest scope and logLevelForward at vnet scope. */
  defaultForward?: string;
  logLevelForward?: string;
  enabled?: boolean;
}

export interface FwAliasCreateParams {
  name: string;
  cidr: string;
  comment?: string;
}

export interface FwAliasUpdateParams {
  name: string;
  cidr?: string;
  comment?: string;
}

export interface FwAliasDeleteParams {
  name: string;
}

export interface FwIpsetCreateParams {
  name: string;
  comment?: string;
  cidrs?: string[];
}

export interface FwIpsetUpdateParams {
  name: string;
  cidrs?: string[];
  comment?: string;
}

export interface FwIpsetDeleteParams {
  name: string;
}

/** One rule inside a security group's `rules` array — no independent
 * `pos` on the wire (array order carries it), per FwGroupCreateParams'
 * Go doc comment. */
export interface FwRuleSpec {
  direction: string;
  action: string;
  proto?: string;
  source?: string;
  dest?: string;
  sport?: string;
  dport?: string;
  macro?: string;
  comment?: string;
  enabled: boolean;
}

export interface FwGroupCreateParams {
  name: string;
  comment?: string;
  rules?: FwRuleSpec[];
}

export interface FwGroupUpdateParams {
  name: string;
  comment?: string;
  rules?: FwRuleSpec[];
}

export interface FwGroupDeleteParams {
  name: string;
}

/** Every Params shape editors in this codebase can produce (T-207's
 * node-file editors, T-402's SDN editors, T-502's firewall editors, and
 * T-405's ipam.alloc.* editor). Ops nothing here edits yet still round-trip
 * through the drawer/review screen — they just carry
 * `Record<string, unknown>` params, since nothing needs to read a typed
 * field off one. */
export type OpParams =
  | IfaceUpdateParams
  | IfaceRawReplaceParams
  | BondCreateParams
  | BondUpdateParams
  | BondDeleteParams
  | BridgeCreateParams
  | BridgeUpdateParams
  | BridgeDeleteParams
  | BridgePortAddParams
  | BridgePortRemoveParams
  | VlanCreateParams
  | VlanUpdateParams
  | VlanDeleteParams
  | GuestNicUpdateParams
  | IpamAllocCreateParams
  | IpamAllocDeleteParams
  | SdnZoneCreateParams
  | SdnZoneUpdateParams
  | SdnZoneDeleteParams
  | SdnVnetCreateParams
  | SdnVnetUpdateParams
  | SdnVnetDeleteParams
  | SdnSubnetCreateParams
  | SdnSubnetUpdateParams
  | SdnSubnetDeleteParams
  | SdnFabricCreateParams
  | SdnFabricUpdateParams
  | SdnFabricDeleteParams
  | SdnControllerCreateParams
  | SdnControllerUpdateParams
  | SdnControllerDeleteParams
  | SdnIpamCreateParams
  | SdnIpamUpdateParams
  | SdnIpamDeleteParams
  | SdnApplyParams
  | FwRuleCreateParams
  | FwRuleUpdateParams
  | FwRuleDeleteParams
  | FwRuleMoveParams
  | FwOptionsUpdateParams
  | FwAliasCreateParams
  | FwAliasUpdateParams
  | FwAliasDeleteParams
  | FwIpsetCreateParams
  | FwIpsetUpdateParams
  | FwIpsetDeleteParams
  | FwGroupCreateParams
  | FwGroupUpdateParams
  | FwGroupDeleteParams
  | WgTunnelCreateParams
  | WgTunnelUpdateParams
  | WgTunnelDeleteParams
  | WgPeerAddParams
  | WgPeerRemoveParams
  // T-3004: QoS shapes are edited exclusively through these three ops (see
  // their declarations near the bottom of this file) — GET /qos/shapes is
  // the only QoS route, and it is a read.
  | QosShapeCreateParams
  | QosShapeUpdateParams
  | QosShapeDeleteParams
  | Record<string, unknown>;

/** One changeset operation, the wire shape internal/change/op.go's Op
 * (de)serializes. `target` is `undefined` only for "sdn.apply" (the one op
 * with no natural target entity — internal/change's `noTargetOps`). */
export interface Op {
  op: OpType;
  target?: string;
  params: OpParams;
  /** T-2003: a stable, server-assigned id (empty/absent for an op not yet
   * persisted — e.g. one just built by an editor and not yet saved). A
   * review Comment attaches to this, not to the op's position in the array
   * (which reordering/editing can change). Round-trips automatically as
   * long as an unedited op's object is spread back in on save (every
   * op-accumulating path in useDrawerActions.ts already does this). */
  id?: string;
}

export type Severity = "error" | "warning" | "info";

/** A validation result (docs/api.md: `{severity, code, message, ref?,
 * fix?}`). `fix` is a machine-applicable one-op patch sharing the
 * offending op's own target (internal/change/validate_fix.go). */
export interface Finding {
  severity: Severity;
  code: string;
  message: string;
  ref?: string;
  fix?: Op[];
}

/** GET /drift item (docs/api.md: `[{check, severity, nodes, detail}]`, plus
 * T-305's additive id/refs/fixable fields — internal/drift.Finding). `check`
 * is one of "bridge_divergence"|"mtu_consistency"|"sdn_realization"|
 * "pending_interfaces"|"file_runtime_divergence" (docs/features/topology.md
 * §6's five check families), left as a plain string here since the frontend
 * only ever displays it, never branches on a closed set of values. */
export interface DriftFinding {
  id: string;
  check: string;
  severity: Severity;
  detail: string;
  nodes: string[];
  refs?: string[];
  fixable: boolean;
  /** T-2703's three-position report, set only by the `spec_reconciliation`
   * check family and omitted by every other one — a finding with no spec
   * position has no third position to report. */
  reconciliation?: Reconciliation;
}

/** One of the three positions a reconciliation compares (internal/drift.Position):
 * `spec` is the declarative document, `config` is /etc/network/interfaces as
 * PVE reports it, `live` is the running kernel. */
export type SpecPosition = "spec" | "config" | "live";

/** One position's rendering of one field. `known: false` means that position
 * never reported the field — which is NOT the same as reporting it empty, and
 * must never be rendered as a value. */
export interface PositionValue {
  position: SpecPosition;
  value: string;
  known: boolean;
}

/** One field's value at all three positions, plus the pairs that disagree
 * about it (`"spec/config"`, `"config/live"`, `"spec/live"`). */
export interface FieldPositions {
  field: string;
  values: PositionValue[];
  differs: string[];
}

/** One of the three pairwise comparisons. All three are always present,
 * including the ones that agree — a three-way divergence must not read as a
 * two-way diff. `comparable: false` means the two positions shared no field
 * either of them reported: "they agree" and "there was nothing to compare"
 * are different statements. */
export interface PairDiff {
  a: SpecPosition;
  b: SpecPosition;
  fields: string[];
  comparable: boolean;
}

/** What a finding OFFERS — never what was done. Each is true if and only if
 * performing it would produce a non-empty artifact, so a finding offering
 * neither is ordinary and honest (docs/api.md's `spec_reconciliation`
 * paragraph). */
export interface ReconciliationActions {
  /** Propose a spec commit describing the cluster as it is. */
  adoptReality: boolean;
  /** Stage a changeset bringing the cluster back to the document. */
  restoreIntent: boolean;
}

/** T-2703's three-position report attached to a `spec_reconciliation`
 * finding (internal/drift.Reconciliation). */
export interface Reconciliation {
  ref: string;
  inSpec: boolean;
  inConfig: boolean;
  inLive: boolean;
  fields: FieldPositions[];
  pairs: PairDiff[];
  actions: ReconciliationActions;
}

/** WS `drift.changed` payload (docs/api.md's WebSocket section). */
export interface DriftChangedEvent {
  count: number;
}

/** T-602's unified findings-stream source producer (docs/api.md's
 * `GET /findings`, internal/findings.Source).
 *
 * Debt sweep item 9 / `T-3004-followup-01` (2026-08-19): this union and
 * `SOURCE_LABELS` (web/src/findings/FindingsStreamPanel.tsx) drifted apart —
 * this union named 5 of `internal/findings.Source`'s 17 constants
 * (`internal/findings/types.go`, the authoritative list per CLAUDE.md: read
 * the Go source, not a doc that copies it), so `SOURCE_LABELS[f.source]`
 * evaluated to `undefined` for the other 11 and those findings were neither
 * labeled nor filterable. Kept exhaustive with that Go file on purpose: a
 * source added to one and not the other must fail to compile, not render
 * `undefined · <check>` at runtime — `SOURCE_LABELS: Record<FindingSource,
 * string>`'s object-literal excess/missing-property check is what enforces
 * that, and it only works if this union lists every real value. */
export type FindingSource =
  | "drift"
  | "lldp"
  | "ipam"
  | "health"
  | "probe"
  | "wireguard"
  | "wan"
  | "flow"
  | "k8s"
  | "rogue"
  | "capacity"
  | "baseline"
  | "federation"
  | "peer"
  | "store"
  | "cert"
  | "gitsync";

/** GET /findings item (docs/api.md's `GET /findings` section —
 * internal/findings.Finding): the superset of DriftFinding's shape plus a
 * `source` tag and an optional `docsLink` remediation pointer. Named
 * `StreamFinding` (not `Finding`) to avoid colliding with this file's
 * existing `Finding` (a changeset validation result — an unrelated, older
 * concept that happens to share the English word). */
export interface StreamFinding {
  id: string;
  source: FindingSource;
  check: string;
  severity: Severity;
  detail: string;
  nodes: string[];
  refs?: string[];
  fixable: boolean;
  docsLink?: string;
  /** Phase 36: the remedy this finding's producer offers, or absent when
   * the finding is detection-only. Never a network-configuration change —
   * those are `fixable` above, and they stage a changeset. */
  remedy?: Remediation;
  /** T-2402: this finding's currently-ACTIVE acknowledgement, or absent.
   * The server evaluates expiry, so an expired acknowledgement arrives as
   * `undefined` and the client never has to reason about a clock. */
  ack?: FindingAck;
}

/** Phase 36's producer-declared remedy (`internal/findings.Remediation`).
 *
 * `action` is the stable identifier a renderer resolves through
 * `remediationAction()` (web/src/findings/remediation.ts) — deliberately
 * NOT `check`, so that adding a remedy never means editing every component
 * that displays findings. An `action` this client does not recognise
 * renders no button at all, which is what lets a newer daemon add one
 * without breaking an older SPA.
 *
 * `kind` is the tier: `"operational"` mutates something and therefore
 * always confirms first; `"navigate"` only moves the operator to the screen
 * where the decision gets made. A computed changeset fix is neither — it is
 * `fixable`, and it goes through `POST /findings/{id}/fix`. */
export interface Remediation {
  action: string;
  kind: "operational" | "navigate";
  label: string;
  params?: Record<string, string>;
}

/** T-2402's acknowledgement (`internal/findings.Ack`). `expiresAt` is unix
 * seconds; absent or 0 means "until explicitly un-acknowledged". */
export interface FindingAck {
  reason: string;
  ackedBy: string;
  ackedAt: number;
  expiresAt?: number;
}

/** T-2403: one row of an entity's change history (`GET /inventory/history`,
 * `internal/change.EntityHistoryEntry`) — the audit trail, changesets, and
 * snapshots merged and re-sliced by entity. */
export interface EntityHistoryEntry {
  kind: "changeset" | "audit" | "snapshot";
  at: number;
  actor?: string;
  summary: string;
  changesetId?: string;
  snapshotId?: string;
  opId?: string;
  result?: string;
}

/** `truncated` is part of the contract, not an implementation detail: the
 * changeset scan is bounded, and a silently short history is
 * indistinguishable from a genuinely short one. */
export interface EntityHistoryPage {
  items: EntityHistoryEntry[];
  truncated: boolean;
}

/** T-2404: what an operator would NOTICE if a changeset were applied
 * (`GET /changesets/{id}/impact`, `internal/change.Impact`). Computed
 * server-side; there is no request field a client could use to influence it. */
export interface ChangesetImpact {
  nodes: string[];
  carriers: string[];
  guests: GuestImpact[];
  ops: OpImpact[];
  disruption: DisruptionClass;
  touchesMgmtPath: boolean;
}

export type DisruptionClass = "none" | "brief" | "outage";

export interface GuestImpact {
  ref: string;
  name: string;
  node: string;
  vmid: number;
  nic: string;
  carrier: string;
}

/** One op's contribution. `reason` is never empty: the server has no way to
 * express a verdict without one, so the UI never has to render an unexplained
 * warning. */
export interface OpImpact {
  opId?: string;
  op: string;
  target?: string;
  disruption: DisruptionClass;
  reason: string;
}

/** WS `findings.changed` payload (docs/api.md's WebSocket section). */
export interface FindingsChangedEvent {
  count: number;
}

export type ChangesetStatus =
  | "draft"
  | "validated"
  | "applying"
  | "awaiting_confirm"
  | "committed"
  | "rolled_back"
  | "failed"
  | "discarded";

/** One apply-plan step (internal/change/apply_plan.go's Step) — the Plan
 * tab's row shape. `opIdx` indexes into the changeset's own `ops` array. */
export interface PlanStep {
  // `switch_apply` AND `qos_apply` were both missing here until 2026-08-16,
  // though change.StepSwitchApply and change.StepQosApply have always
  // existed — so two real plan step kinds were silently mistyped. T-3005
  // found the first (it needed the kind to decide canary eligibility, since
  // switch_apply is one of internal/change's canaryUnstageableKinds, and had
  // to key off a raw string instead); writing the drift guard below found the
  // second. internal/change's TestPlanStepKindsMatchTheTypeScriptUnion now
  // fails if this list and change.StepKind ever disagree again.
  kind:
    | "sdn_stage"
    | "ipam_alloc"
    | "stage_file"
    | "reload"
    | "wg_apply"
    | "fw_apply"
    | "fw_verify"
    | "sdn_apply"
    | "switch_apply"
    | "qos_apply"
    | "tc_mirror_apply";
  node?: string;
  summary: string;
  opIdx?: number[];
}

export interface Plan {
  steps: PlanStep[];
}

export type StepStatus = "pending" | "ok" | "failed" | "skipped" | "rolled_back";

export interface StepLog {
  kind: PlanStep["kind"];
  node?: string;
  summary: string;
  status: StepStatus;
  error?: string;
  index: number;
  startedAt?: number;
  endedAt?: number;
}

export interface RollbackLog {
  node?: string;
  summary: string;
  status: StepStatus;
  error?: string;
  at?: number;
}

export interface ApplyLog {
  failedStep?: number;
  rolledBackBy?: string;
  steps: StepLog[];
  rollback?: RollbackLog[];
}

/** GET/POST/PUT `/changesets*`'s response shape (internal/api/changesets.go's
 * changesetResponse — `ops`/`findings` are always arrays, never null). */
export interface Changeset {
  id: string;
  title: string;
  author: string;
  status: ChangesetStatus;
  ops: Op[];
  findings: Finding[];
  plan?: Plan;
  applyLog?: ApplyLog;
  confirmDeadline?: number;
  createdAt: number;
  updatedAt: number;
  /** T-703: server-computed — the ops intersect a node's resolved
   * management path (change.TouchesMgmtPath). The review screen turns this
   * into a mandatory acknowledgement block and the apply enforces a
   * confirm-window floor. Absent on older responses (treated as false). */
  touchesMgmtPath?: boolean;
  /** T-1805: whether this changeset reverts itself with no live session, and
   * until when. Present on the apply response and on a read of an
   * `awaiting_confirm` changeset; absent otherwise (and on older responses,
   * which the UI treats as "unknown" and says nothing about). It carries a
   * coverage bound only — never the sealed PVE ticket the coverage rests on. */
  unattendedRevert?: UnattendedRevert;
  /** T-2003: per-op/changeset review comments and the current review-
   * approval decision. Present only on the canonical `GET /changesets/{id}`
   * read (internal/api's handleGetChangeset) — every other response
   * (create/update/validate/apply/list/...) omits both, exactly like
   * `touchesMgmtPath`'s own precedent for a field only the canonical read
   * computes. */
  comments?: ChangesetComment[];
  approval?: ApprovalState;
  /** T-2805: the advisory-lock warning for a staging request. Present only
   * on `POST /changesets` and `PUT /changesets/{id}`, and only when there is
   * something to warn about — an uncontended staging response omits it
   * entirely. It is a WARNING and nothing else: the changeset it arrives on
   * was already created or updated, and no route refuses anything because of
   * a lock. */
  locks?: ChangesetLocks;
  /** T-2602: the paused staged (canary) apply, if this changeset is
   * currently between stages. Present ONLY while a pause exists — every
   * ordinary apply, and every changeset not mid-hold, omits it entirely.
   * Persisted server-side (`changeset_apply_stages`), so it is the thing to
   * re-derive the rollout view from on load rather than any client state. */
  applyStage?: StagedApplyState;
}

/** T-2602 apply modes. `all` is what apply has always done: fan out to every
 * affected node at once. */
export type ApplyMode = "all" | "canary";

/** T-2602 canary gates. `manual` waits for `POST /changesets/{id}/continue`;
 * `auto` promotes at the hold deadline only on clean evidence. */
export type ApplyGate = "manual" | "auto";

/** `applyStrategy` on the apply body (docs/api.md's "Canary / staged
 * multi-node apply"). Omitting the whole object is `mode: "all"`, and
 * `mode: "all"` may NOT carry `canaryNodes`, `holdForSec` or `gate` — the
 * server refuses such a body rather than ignoring the fields. */
export interface ApplyStrategy {
  mode: ApplyMode;
  gate?: ApplyGate;
  canaryNodes?: string[];
  holdForSec?: number;
}

/** `applyStage` on a changeset response (internal/change.StagedApplyState).
 *
 * `appliedNodes`/`pendingNodes` are documented as always-present arrays, but
 * they are typed optional here deliberately: a response that omits one is an
 * ABSENT answer, not an empty one, and rolloutState.ts renders the
 * difference rather than collapsing it into "nothing pending". */
export interface StagedApplyState {
  /** `canary_hold` (paused between stages) or `promoting` (the remaining
   * stage is executing right now). Typed as a plain string because the
   * server's set is closed but versioned independently of this client — an
   * unrecognised state must render as unrecognised, never as either known
   * one. */
  state: string;
  author?: string;
  strategy: ApplyStrategy;
  appliedNodes?: string[];
  pendingNodes?: string[];
  holdStartedAt?: number;
  holdDeadline?: number;
  confirmDeadline?: number;
}

/** One advisory lock a staged draft holds on an entity (docs/api.md's
 * "Advisory locks and presence"). `holder` is absent for a caller without
 * the `audit` capability — that an entity is spoken for is an ordinary read;
 * WHO spoke for it is an attribution. */
export interface EntityLock {
  ref: string;
  changesetId: string;
  holder?: string;
  acquiredAt: number;
  expiresAt: number;
  /** Whether the requesting session holds it. Always answerable — "is this
   * me?" attributes nothing to anyone else. */
  mine: boolean;
}

/** The `locks` object on a staging response (T-2805). `held` are other
 * operators' claims this staging stepped around; `overridden` are the ones
 * it deliberately took over (each with a `changeset.lock_override` audit
 * row). */
export interface ChangesetLocks {
  held?: EntityLock[];
  overridden?: EntityLock[];
}

/** `GET /locks` (T-2805). */
export interface LocksResponse {
  locks: EntityLock[];
}

/** One person present on a presence scope. `sessions` counts that person's
 * distinct sessions, so a second browser tab is not a second colleague. */
export interface PresenceViewer {
  user: string;
  since: number;
  sessions: number;
}

/** One scope's presence (T-2805). `viewers` is absent for a caller without
 * the `audit` capability; `count` is always present, because a count is not
 * an identity. */
export interface PresenceScope {
  scope: string;
  viewers?: PresenceViewer[];
  count: number;
}

/** `GET /presence` (T-2805). */
export interface PresenceResponse {
  scopes: PresenceScope[];
}

/** The `presence.changed` WS event (T-2805). It carries a count and never an
 * identity — see docs/api.md's WebSocket section for why that is structural
 * rather than a policy the client could work around. */
export interface PresenceChangedEvent {
  scope: string;
  count: number;
}

/** One review comment (docs/api.md's changesets section, T-2003):
 * `{id, opId?, author, body, createdAt}`. `opId` absent means a
 * changeset-level comment; present, it names the commented `Op.id`. */
export interface ChangesetComment {
  id: string;
  opId?: string;
  author: string;
  body: string;
  createdAt: number;
}

export type ApprovalStatus = "none" | "approved" | "rejected";

/** A changeset's review-approval state (T-2003): whether this deployment's
 * policy currently requires approval before apply (`required`), and — once
 * a decision exists — who made it, when, and (for a rejection) why.
 * `required` is never inferred from the absence of an apply button in this
 * UI: it is what the server tells us, and the server's own Apply refusal
 * (the `approval_required` error code) is the actual enforcement. */
export interface ApprovalState {
  status: ApprovalStatus;
  decidedBy?: string;
  reason?: string;
  decidedAt?: number;
  required: boolean;
  /** T-2604's enforced two-person rule, present only when this changeset
   * falls in at least one protected op class. Like `required` above, it is a
   * READ of the server's gate and never the gate itself: the server's own
   * `two_person_required` refusal at apply is the enforcement. */
  twoPerson?: TwoPersonState;
}

/** One protected op class a changeset falls into (T-2604): the declared
 * class name, how many DISTINCT principals it requires, and how many of the
 * changeset's ops put it in that class. */
export interface ProtectedClassMatch {
  class: string;
  approvals: number;
  ops: number;
}

/** An emergency break-glass override on record for a changeset (T-2604).
 * `ackableAt` is the unix instant the error finding it raised becomes
 * acknowledgeable — 24 hours after it was invoked. */
export interface BreakGlassRecord {
  changesetId: string;
  reason: string;
  invokedBy: string;
  invokedAt: number;
  ackableAt: number;
}

/** A changeset's two-person-rule state (T-2604). */
export interface TwoPersonState {
  classes?: ProtectedClassMatch[];
  approvers?: string[];
  breakGlass?: BreakGlassRecord;
  required: number;
  satisfied: boolean;
}

/** T-4006's audited override of a declared freeze-window policy rule, on
 * record for a changeset (`POST /changesets/{id}/freeze-override`'s response
 * shape). Unlike `BreakGlassRecord` above there is no `ackableAt` — the
 * escape hatch here downgrades a VALIDATE-time finding to a visible warning
 * rather than gating an authorization check, so there is no separate
 * unacknowledgeable finding tied to it; the override itself, and the
 * `[overridden: ...]`-annotated finding it produces, are the visible trail. */
export interface FreezeOverrideRecord {
  changesetId: string;
  reason: string;
  invokedBy: string;
  invokedAt: number;
}

/** One zone/vnet/subnet real PVE currently reports staged-but-not-yet-
 * applied outside this changeset's own ops (T-3101-followup-01): the
 * foreign-SDN-pending "surface and confirm" gate's review-screen content —
 * `docs/api.md`'s `GET/POST .../sdn-foreign-pending(/ack)`. `fields`
 * carries PVE's own current field values for that object, for an "this
 * apply will also commit ..." listing that shows precisely what changed. */
export interface SdnPendingEntry {
  kind: "zone" | "vnet" | "subnet";
  id: string;
  state: "new" | "changed" | "deleted";
  fields?: Record<string, unknown>;
}

/** `GET`/`POST .../sdn-foreign-pending(/ack)`'s response body
 * (T-3101-followup-01). `acknowledgedBy`/`acknowledgedAt` are present only
 * on the ack route's response. */
export interface SdnForeignPendingResponse {
  entries: SdnPendingEntry[];
  acknowledgedBy?: string;
  acknowledgedAt?: number;
}

/** T-1805 (`unattendedRevert` on a changeset response): the server's answer to
 * "if this change locks me out, will it revert itself — and for how long?".
 *
 * PVE firewall and SDN writes are performed with the *user's* ticket, so
 * reverting them without a session requires the ticket vnprox sealed at apply
 * time. `required` is false for a changeset with no such op, in which case the
 * daemon-level rollback machinery covers the whole window on its own. */
export interface UnattendedRevert {
  /** Does anything in this changeset need the user's PVE ticket to revert? */
  required: boolean;
  /** Is unattended revert of that portion possible at all? */
  available: boolean;
  /** Unix seconds past which the ticket-scoped portion can no longer revert
   * unattended: min(confirm deadline, PVE ticket expiry). */
  coversUntil?: number;
  /** Does `coversUntil` reach the confirm deadline? false is the reduced-
   * coverage case the operator must be told about at apply time. */
  fullWindow: boolean;
  /** Operator-facing explanation whenever coverage is absent or partial. */
  reason?: string;
}

/** POST /changesets body. */
export interface CreateChangesetRequest {
  title: string;
  ops: Op[];
  /** T-2805: take over another operator's advisory lock on an entity this
   * changeset touches. Omitting it leaves their claim alone and returns it
   * in the response's `locks.held`; the changeset is staged either way. Each
   * takeover is audited server-side. */
  lockOverride?: boolean;
}

/** PUT /changesets/{id} body — `title` is an accepted-but-undocumented
 * extension (T-201's report) for renaming a parked draft in place. */
export interface UpdateChangesetRequest {
  title?: string;
  ops: Op[];
  /** T-2805: see CreateChangesetRequest.lockOverride. */
  lockOverride?: boolean;
}

/** POST /changesets/{id}/apply body. */
export interface ApplyChangesetRequest {
  confirmTimeoutSec: number;
  /** T-703: the typed management-path acknowledgement, recorded to the
   * audit log when the changeset touches a management path. */
  mgmtAck?: { node: string };
  /** T-2602: how the apply fans out. OMITTED for the default all-at-once
   * apply — the server treats an absent object and `{mode:"all"}` the same,
   * but sending nothing keeps the historical request body byte-for-byte
   * unchanged, which is what the regression assertion in
   * ReviewApplyScreen.canary.test.tsx pins. */
  applyStrategy?: ApplyStrategy;
  /** T-2603: arm finding-triggered rollback for this apply. Omitted means
   * "the cluster default" (itself false), so an apply that says nothing
   * behaves exactly as it always did. */
  autoRollbackOnError?: boolean;
}

/** One file's rendered diff for one node (internal/change/ifaces.FileDiff). */
export interface FileDiff {
  node: string;
  path: string;
  unified: string;
  changed: boolean;
}

/** One op's Summary-tab card (internal/change/ifaces.OpSummary). */
export interface OpSummary {
  op: string;
  target: string;
  node: string;
  summary: string;
}

/** GET /changesets/{id}/diff response (internal/change/ifaces.ChangesetDiff). */
export interface ChangesetDiff {
  files: FileDiff[];
  ops: OpSummary[];
}

/** The `changeset.status` WS event (docs/api.md's WebSocket section):
 * `{id, status, confirmDeadline?}`. */
export interface ChangesetStatusEvent {
  event: "changeset.status";
  id: string;
  status: ChangesetStatus;
  confirmDeadline?: number;
}

// --- MAC/FDB browser (GET /fdb; internal/topology/types.go's FDBRow,
// docs/features/lldp-discovery.md §4) -----------------------------------

/** Which known thing (if any) an FDBRow's `mac` resolves to elsewhere in
 * the cluster-wide inventory: a guest's own NIC ("guest" — `ownerRef` is
 * the owning Guest's ref, a valid GET /inventory/{ref} deep link), a MAC
 * vnprox otherwise recognizes (a physical NIC on any node — "vnprox-known"),
 * or neither ("unknown" — most often exactly what shows up on an
 * uplink/trunk port: a real switch/device the FDB learned, not one of
 * vnprox's own entities). */
export type FDBOwner = "guest" | "vnprox-known" | "unknown";

/** One bridge forwarding-database entry, cluster-wide and
 * ownership-labeled. `score` is only meaningful (nonzero) on GET
 * /fdb?mac=-search results — omitted (0) on the plain "browse everything"
 * listing. */
export interface FDBRow {
  node: string;
  bridge: string;
  bridgeRef: string;
  mac: string;
  port?: string;
  owner: FDBOwner;
  ownerRef?: string;
  ownerLabel?: string;
  vlan?: number;
  score?: number;
  master?: boolean;
  permanent?: boolean;
  stale: boolean;
}

export interface FDBResponse {
  items: FDBRow[];
}

// --- Multicast / MDB browser (GET /mdb; internal/api/mdb.go, T-3902) -------
// Sibling of the MAC/FDB browser above, but a *live* cluster-wide fan-out
// (like GET /conntrack) rather than an inventory-backed listing: there is
// no netlink MDB dump this codebase's vendored netlink library supports,
// so nothing feeds MDB state into the collected inventory graph the way
// FDB is. Field shapes are grounded in
// planning/reports/evidence/pve-9.2.4-bridge-mdb-2026-08-27.txt — the real
// PVE 9.2.4 host observed carried only IPv6 mDNS ("ff02::fb") entries,
// state "temp", protocol "kernel"; a "permanent" state, an IPv4 group, and
// a VLAN-tagged row are all real `bridge` vocabulary but unverified
// against actual output.

/** One bridge multicast forwarding-database entry, node/bridge-tagged. */
export interface MDBEntry {
  node: string;
  bridge: string;
  group: string;
  port?: string;
  state?: string;
  protocol?: string;
  vlan?: number;
}

/** One bridge's IGMP/MLD-snooping configuration (docs/api.md's Multicast/
 * MDB section). routerMode is the kernel's raw multicast_router sysfs
 * value: 0 (never), 1 (learn/auto — the only value observed on a real PVE
 * 9.2.4 host), 2 (permanently enabled). */
export interface MDBBridge {
  node: string;
  bridge: string;
  snooping: boolean;
  querier: boolean;
  routerMode: number;
}

/** GET /mdb's response envelope — the same cluster-fan-out shape GET
 * /conntrack uses (partial/failedNodes), minus an unavailableNodes split:
 * the `bridge` binary being missing is treated as an ordinary read
 * failure, not a distinct expected-degraded state. */
export interface MDBResponse {
  entries: MDBEntry[];
  bridges: MDBBridge[];
  partial?: boolean;
  failedNodes?: string[];
}

// --- SDN (docs/api.md §"Firewall, SDN, IPAM"; GET /sdn) --------------------
// Mirrors internal/sdn/service.go's Tree/Zone/Vnet/Subnet/NodeStatus/
// PendingDiff exactly — see that file's doc comments for the non-obvious
// bits (Diff is entirely omitted, not present-but-empty, for an in-sync
// object; ChangedFields/Running/Staged are only populated for state
// "changed"). Added by T-401 (not in the original docs/api.md contract
// beyond the bare route + one-line purpose; the full response shape is
// documented in docs/api.md's GET /sdn row in this same change per
// docs/development.md's definition-of-done #4).

/** One node's realization status for a zone
 * (docs/features/sdn.md §1: "applied / pending / error"). */
export interface SdnNodeStatus {
  node: string;
  /** "ok" | "pending" | "error" in practice (docs/features/sdn.md §1), plus
   * the server-synthesized "unknown" (T-3701: a declared member node PVE's
   * own per-node status read had nothing to report for at all — not the
   * same fact as that node being healthy). Kept as a plain string since
   * it's a server-controlled, open-ended enum (mirrors this file's other
   * `kind` fields' convention). */
  status: string;
  detail?: string;
}

/** The staged-vs-running delta for one zone/vnet/subnet
 * (docs/features/sdn.md §1: "vnprox surfaces staged-vs-running as a
 * first-class diff instead of a mystery 'pending' flag"). Absent entirely
 * (the entity's `pendingDiff` field is undefined) for an in-sync object. */
export interface SdnPendingDiff {
  state: "new" | "changed" | "deleted";
  changedFields?: string[];
  running?: Record<string, unknown>;
  staged?: Record<string, unknown>;
}

export interface SdnSubnet {
  id: string;
  vnet: string;
  cidr: string;
  gateway?: string;
  dhcpRangeStart?: string;
  dhcpRangeEnd?: string;
  snat?: boolean;
  pending?: string;
  pendingDiff?: SdnPendingDiff;
}

export interface SdnVnet {
  id: string;
  zone: string;
  alias?: string;
  tag?: number;
  vlanAware?: boolean;
  pending?: string;
  pendingDiff?: SdnPendingDiff;
  subnets: SdnSubnet[];
}

export interface SdnZone {
  id: string;
  /** Real PVE 9.2's enum is
   * "evpn" | "faucet" | "qinq" | "simple" | "vlan" | "vxlan"
   * (captured: planning/reports/evidence/pve-9.2.4-sdn-schema.txt).
   * Kept as a plain string per SdnNodeStatus.status's doc comment.
   *
   * Note this is wider than the set the UI offers a wizard for
   * (docs/features/sdn.md §2's five): a faucet zone needs a faucet
   * controller, so vnprox accepts and displays one without offering to
   * create one. Do not narrow this comment back to the wizard list — the
   * gap between "types PVE has" and "types we can create" is real and
   * load-bearing. */
  type: string;
  bridge?: string;
  controller?: string;
  /** Reference by id to an SdnIpam entry in this same SdnTree's `ipams`
   * array — mirrors `controller`'s own "reference by id to a sibling
   * object" shape. Real, captured PVE zone parameter (planning/reports/
   * evidence/pve-9.2.4-sdn-schema.txt's `--ipam`), only wired end to end
   * by T-3104 — this field existed on SdnZoneCreateParams/
   * SdnZoneUpdateParams before that but was never actually populated on a
   * read (internal/pve.SDNZone.IPAM's doc comment). */
  ipam?: string;
  nodes?: string[];
  exitNodes?: string[];
  peers?: string[];
  mtu?: number;
  vrfVxlan?: number;
  pending?: string;
  nodeStatus: SdnNodeStatus[];
  pendingDiff?: SdnPendingDiff;
  vnets: SdnVnet[];
}

/** One SDN fabric (T-3101), mirroring internal/sdn/service.go's Fabric
 * exactly. A sibling top-level collection on SdnTree, not nested under
 * SdnZone — a fabric is cluster underlay a zone may ride on, not a zone's
 * child the way SdnVnet is. `protocol` is real PVE 9.2's captured enum
 * (planning/reports/evidence/pve-9.2.4-sdn-schema.txt): "bgp" | "openfabric"
 * | "ospf" | "wireguard" — the last one is genuinely WireGuard, but PVE-
 * managed underlay transport, a different management plane from this file's
 * WireGuardTunnel above (T-1401) — the two never share a shape or a map
 * badge, see FabricsView.tsx. */
export interface SdnFabric {
  id: string;
  protocol: string;
  pending?: string;
  ipPrefix?: string;
  ip6Prefix?: string;
  csnpInterval?: number;
  helloInterval?: number;
  routeFilter?: string;
  area?: string;
  redistribute?: string[];
  persistentKeepalive?: number;
  /** Configured membership, not verified realization health — the captured
   * fabrics API has no per-fabric status route the way a zone has one, so
   * every entry here is "ok" unconditionally (see docs/api.md's `GET /sdn`
   * section). */
  nodeStatus: SdnNodeStatus[];
}

/** One SDN controller (T-3102), mirroring internal/sdn/service.go's
 * Controller exactly. A sibling top-level collection on SdnTree, not nested
 * under SdnZone — a controller is infrastructure a zone may ride on
 * (SdnZone.controller is a *reference* by id), not a zone's child the way
 * SdnVnet is; deleting a zone must never delete the controller it named.
 * `type` is real PVE 9.2's captured enum (planning/reports/evidence/
 * pve-9.2.4-sdn-schema.txt): "bgp" | "evpn" | "faucet" | "isis". Unlike
 * SdnFabric it carries no nodeStatus — the captured API has no per-
 * controller status route AND no separate per-node-membership collection
 * the way fabrics have (see ControllersView.tsx). EVPN/BGP session health
 * is reported separately (EvpnStatus.controllers) and re-attached by id,
 * not inferred here. */
export interface SdnController {
  id: string;
  type: string;
  pending?: string;
  bgpMode?: string;
  fabric?: string;
  isisDomain?: string;
  isisNet?: string;
  loopback?: string;
  node?: string;
  peerGroupName?: string;
  routeMapIn?: string;
  routeMapOut?: string;
  nodes?: string[];
  peers?: string[];
  isisIfaces?: string[];
  asn?: number;
  ebgpMultihop?: number;
  ebgp?: boolean;
  bgpMultipathAsPathRelax?: boolean;
}

/** One configured SDN ipam plugin instance (T-3104), mirroring
 * internal/sdn/service.go's Ipam exactly. A sibling top-level collection on
 * SdnTree, not nested under SdnZone — an ipam instance is infrastructure a
 * zone may ride on (SdnZone.ipam is a *reference* by id), not a zone's
 * child. `type` is real PVE 9.2's captured enum (planning/reports/evidence/
 * pve-9.2.4-sdn-schema.txt): "netbox" | "phpipam" | "pve" — vnprox already
 * modeled this correctly (no capture-vs-model mismatch here, unlike every
 * other SDN family this phase touched). `token` is deliberately absent:
 * real PVE never echoes a configured secret back on a read (see
 * internal/pve/sdn_ipam.go's package doc comment) — a create/update form
 * takes it as write-only input (SdnIpamCreateParams.token above), but
 * nothing in this response shape carries it back out. */
export interface SdnIpam {
  id: string;
  type: string;
  pending?: string;
  url?: string;
  fingerprint?: string;
  section?: number;
}

/** A read-only BGP prefix-list object (T-3101) — no changeset op exists for
 * either this or SdnRouteMap; field shape beyond `id` is unconfirmed
 * against hardware (planning/reports/needs-hardware-validation.md's T-3101
 * entry). */
export interface SdnPrefixList {
  id: string;
}

/** A read-only BGP route-map object (T-3101). See SdnPrefixList's doc
 * comment. */
export interface SdnRouteMap {
  id: string;
}

/** GET /sdn response. */
export interface SdnTree {
  zones: SdnZone[];
  fabrics: SdnFabric[];
  controllers: SdnController[];
  ipams: SdnIpam[];
  prefixLists: SdnPrefixList[];
  routeMaps: SdnRouteMap[];
  generatedAt: number;
}

// --- IPAM (docs/api.md's /ipam routes; internal/ipam's Go types) ----------
// Mirrors internal/ipam/types.go exactly — see that file's doc comments for
// the non-obvious bits (Cell.state vs. Cell.confidence are related but
// distinct axes; AllocationList carries the occupied addresses as entries
// and the contiguous free gaps between them as freeRanges, sparse at any
// subnet size). Added by T-405; the address-list shape supersedes the
// original allocation-grid/paged-block response.

/** One row of GET /ipam/subnets. */
export interface IpamSubnet {
  cidr: string;
  zone?: string;
  vnet?: string;
  gateway?: string;
  /** Only set for a bridge-derived (non-SDN) subnet. */
  node?: string;
  source: "sdn" | "bridge";
  readOnly?: boolean;
  dhcpEnabled?: boolean;
  total: number;
  allocated: number;
  observed: number;
  conflicts: number;
  utilization: number;
}

export interface IpamSubnetsResponse {
  items: IpamSubnet[];
  generatedAt: number;
}

/** One allocation-grid cell's render state
 * (docs/features/ipam.md §2: "free / allocated / observed-unallocated /
 * reserved / gateway / conflict"). */
export type IpamCellState = "free" | "allocated" | "reserved" | "observed" | "gateway" | "conflict";

/** The multi-source-merge confidence label
 * (docs/features/ipam.md §1), independent of (but related to)
 * IpamCellState — see internal/ipam/types.go's Cell doc comment. */
export type IpamConfidence = "allocated" | "observed" | "both" | "conflict" | "";

export interface IpamCell {
  ip: string;
  state: IpamCellState;
  confidence?: IpamConfidence;
  hostname?: string;
  mac?: string;
  vmid?: number;
  guestRef?: string;
  sources?: string[];
}

/** A contiguous run of unallocated host addresses, collapsed into one row of
 * the address list (docs/features/ipam.md §2). Start/End inclusive; count is
 * the number of addresses in the run. */
export interface IpamFreeRange {
  start: string;
  end: string;
  count: number;
}

/** The address-list summary strip's per-state tally. The buckets are
 * mutually exclusive and sum to the subnet's usable-host count. */
export interface IpamCounts {
  allocated: number;
  reserved: number;
  observed: number;
  gateway: number;
  conflict: number;
  free: number;
}

/** One conflict-detection health finding
 * (docs/features/ipam.md §2: "Each conflict is a health finding with
 * suggested resolution"). */
export interface IpamConflict {
  type: "duplicate_ip" | "observed_unallocated" | "allocated_dark";
  severity: Severity;
  ips: string[];
  message: string;
  suggestion: string;
}

/** GET /ipam/subnets/{cidr}/allocations response: the NetBox-style address
 * list — every occupied address (entries, sorted ascending) plus the
 * collapsed free gaps between them (freeRanges). Sparse by construction, so
 * it renders identically for a /30 or a /16. */
export interface IpamAllocationList {
  cidr: string;
  gateway?: string;
  entries: IpamCell[];
  freeRanges: IpamFreeRange[];
  conflicts: IpamConflict[];
  counts: IpamCounts;
  prefix: number;
  total: number;
  readOnly?: boolean;
  generatedAt: number;
}

// --- DHCP (docs/api.md; GET /sdn/dhcp) ------------------------------------
// Mirrors internal/ipam/dhcp.go's Reservation/Lease/DHCPView exactly.
// Added by T-406 (docs/features/sdn.md §5).

/** One DHCP-eligible subnet's MAC-bound static reservation — a filtered,
 * derived view over the exact same PVE-IPAM allocation record the IPAM
 * grid renders as an allocated cell (see internal/ipam/dhcp.go's doc
 * comment: "one dataset", never a second stored copy). */
export interface DhcpReservation {
  cidr: string;
  zone: string;
  vnet: string;
  ip: string;
  mac: string;
  hostname?: string;
  vmid?: number;
  guestRef?: string;
}

/** One live dnsmasq-observed DHCP lease, correlated to a known guest by
 * MAC when one matches. */
export interface DhcpLease {
  cidr: string;
  zone: string;
  vnet: string;
  ip: string;
  mac: string;
  hostname?: string;
  guestRef?: string;
}

/** GET /sdn/dhcp response. */
export interface DhcpView {
  reservations: DhcpReservation[];
  leases: DhcpLease[];
  generatedAt: number;
}

// --- EVPN/BGP observability (docs/api.md; GET /sdn/evpn/status) -----------
// Mirrors internal/evpn/types.go's Status/NodeStatus/Peer/VNI/
// ExitNodeHealth/Finding exactly — see that file's doc comments and
// docs/api.md's `GET /sdn/evpn/status` row for the non-obvious bits
// (frrInstalled=false is the clean "no EVPN" case, distinct from `error`
// being set). Added by T-404.

/** One BGP/EVPN peering session's detail — the peering matrix's per-cell
 * data and the session detail panel's content. */
export interface EvpnPeer {
  peerAddr: string;
  peerNode?: string;
  addressFamily?: string;
  /** FRR's own FSM vocabulary: "Idle" | "Connect" | "Active" | "OpenSent" |
   * "OpenConfirm" | "Established", kept as a plain string per this file's
   * open-ended-server-enum convention (see SdnNodeStatus.status). */
  state: string;
  /** FRR's parenthetical qualifier for a down session when present (e.g.
   * "Idle (Admin)" -> state:"Idle", stateReason:"Admin") — the closest
   * thing FRR's summary JSON has to a "last error". */
  stateReason?: string;
  remoteAs?: number;
  pfxRcd?: number;
  pfxSnt?: number;
  uptimeSecs?: number;
  flapTransitions?: number;
}

/** One EVPN VNI observed on a node. */
export interface EvpnVni {
  vni: number;
  type: string; // "L2" | "L3"
  vxlanIf?: string;
  tenantVrf?: string;
  numMacs?: number;
  numArpNd?: number;
}

/** One cluster node's FRR observation. frrInstalled=false (peers/vnis
 * empty, error unset) is the documented clean "no EVPN" case
 * (docs/features/sdn.md §3) — distinct from `error` being set, a real
 * read/parse failure on a node that does run FRR. */
export interface EvpnNodeStatus {
  node: string;
  frrInstalled: boolean;
  routerId?: string;
  asn?: number;
  peers: EvpnPeer[];
  vnis: EvpnVni[];
  error?: string;
}

/** One EVPN zone exit node's derived health. `controller` (T-3102,
 * additive) names the zone's own controller reference when it resolves to
 * a real SdnController — omitted when the zone has none set or the id
 * doesn't resolve. */
export interface EvpnExitNodeHealth {
  zone: string;
  node: string;
  controller?: string;
  healthy: boolean;
  detail?: string;
}

/** One SDN controller's BGP/EVPN peering health (T-3102 acceptance
 * criterion 3: EVPN/BGP status attaches to the controller rather than
 * being inferred). `zones` lists every zone whose own `controller` field
 * names this controller. `peers`/`healthy` are computed only for bgp/evpn
 * controllers, by matching the controller's configured peer address list
 * against observed sessions across the cluster fan-out (EvpnNodeStatus.
 * peers) — a faucet/isis controller, or a bgp/evpn one with no peers
 * configured, still appears with `healthy: true` and a `detail` explaining
 * why, rather than being omitted. */
export interface EvpnControllerHealth {
  id: string;
  type: string;
  zones?: string[];
  peers?: string[];
  healthy: boolean;
  detail?: string;
}

/** A flapping-session health finding (docs/features/sdn.md §3: "Flapping
 * sessions raise a health finding"). */
export interface EvpnFinding {
  id: string;
  code: string;
  severity: Severity;
  node: string;
  peerAddr: string;
  detail: string;
}

/** GET /sdn/evpn/status response. */
export interface EvpnStatus {
  nodes: EvpnNodeStatus[];
  exitNodes: EvpnExitNodeHealth[];
  /** T-3102 acceptance criterion 3's re-attachment — the same route, same
   * envelope, one additive field. */
  controllers: EvpnControllerHealth[];
  findings: EvpnFinding[];
  generatedAt: number;
  partial?: boolean;
  failedNodes?: string[];
}

// --- Firewall (docs/api.md §"Firewall, SDN, IPAM"; internal/api/firewall.go,
// internal/fw's pure resolver) ----------------------------------------------
// GET /firewall/rulesets?scope= and GET /firewall/objects — T-501's read
// views: per-scope raw rulesets, the guest resolved (group-expanded)
// evaluation order with origin labels, enablement banners (the
// "Datacenter firewall is OFF" footgun, docs/features/firewall.md §2), and
// alias/ipset/security-group usage tracking with macro expansion previews.

export type FwScope = "cluster" | "node" | "guest" | "vnet";

/** One documented evaluation step's origin label
 * (docs/features/firewall.md §1: "cluster rules → security groups → guest
 * rules → default policies"). "default" only ever appears on a
 * DefaultPolicy, never inside a ResolvedRuleView. */
export type FwOrigin = "cluster" | "group" | "guest" | "default";

/** A macro's proto/port expansion preview (docs/features/firewall.md §2's
 * "macro picker ... with expansion preview"). */
export interface MacroPortView {
  proto?: string;
  dport?: string;
}

export interface MacroView {
  name: string;
  comment?: string;
  ports: MacroPortView[];
}

/** One firewall rule, as configured (internal/inventory.FwRule mirrored by
 * internal/api/firewall.go's ruleView). `macroExpansion` is populated
 * server-side whenever `macro` names a macro this build knows. */
export interface RuleView {
  pos: number;
  enabled: boolean;
  direction: string;
  action: string;
  proto?: string;
  source?: string;
  dest?: string;
  sport?: string;
  dport?: string;
  iface?: string;
  macro?: string;
  macroExpansion?: MacroPortView[];
  log?: string;
  comment?: string;
}

/** An enablement banner (docs/features/firewall.md §2's "Datacenter
 * firewall is OFF: none of these rules are active" example) — cascades
 * from the datacenter scope down through node and guest scopes even when
 * that scope's own toggle is nominally on. */
export interface BannerView {
  scope: FwScope;
  message: string;
}

/** One scope's raw ruleset (the read-only per-scope rule table). */
export interface RulesetView {
  ref: string;
  scope: FwScope;
  node?: string;
  /** The owning SDN vnet's own ref ("sdn-vnet::<zone>/<vnet>"), populated
   * only for scope="vnet" — the vnet-scope counterpart of `node` (T-3103). */
  vnet?: string;
  enabled: boolean;
  defaultIn?: string;
  defaultOut?: string;
  /** The forward chain's own fallthrough policy/log level (T-3103).
   * defaultForward is set at cluster/node/vnet scope; logLevelForward only
   * at vnet scope — see internal/inventory.FwRuleset's Go doc comments for
   * why that asymmetry is real (hardware-captured), not an oversight. */
  defaultForward?: string;
  logLevelForward?: string;
  rules: RuleView[];
  banners?: BannerView[];
}

/** One entry in a guest's effective, ordered evaluation
 * (docs/features/firewall.md §1). `groupName` is set when this rule came
 * from (or is itself a reference to) a security group. */
export interface ResolvedRuleView {
  origin: FwOrigin;
  groupName?: string;
  rule: RuleView;
  pos: number;
}

export interface DefaultPolicyView {
  direction: "in" | "out";
  policy: string;
  origin: FwOrigin;
}

/** A guest's full resolved view: the ordered rule list plus the two
 * directions' fallthrough default policies and every enablement gate
 * making some or all of it inert. */
export interface ResolvedView {
  guest: string;
  active: boolean;
  gates?: BannerView[];
  rules: ResolvedRuleView[];
  defaultIn: DefaultPolicyView;
  defaultOut: DefaultPolicyView;
}

/** GET /firewall/rulesets?scope=guest&ref=... response: the guest's own
 * raw ruleset plus its resolved view in one payload. */
export interface GuestRulesetResponse {
  ruleset: RulesetView;
  resolved: ResolvedView;
}

/** `?scope=node`/`?scope=guest` with no `ref`/`node` — the hierarchy list
 * view. */
export interface RulesetListResponse {
  items: RulesetView[];
}

/** GET /firewall/rulesets?scope=group&name=... response (T-2002): a
 * security group's own name/comment/rule list — the group inspector's read
 * side. Distinct shape from RulesetView: a security group has no ref/
 * scope/enabled/defaultIn/defaultOut of its own (it's a named rule list
 * referenced by a `direction: "group"` rule elsewhere, never a ruleset in
 * its own right). */
export interface GroupRulesetResponse {
  name: string;
  comment?: string;
  rules: RuleView[];
}

export interface RuleRefView {
  scope: FwScope;
  ref: string;
  pos: number;
}

/** One alias/ipset/security-group's "referenced by N rules" usage summary
 * (docs/features/firewall.md §2). `kind` names which object kind this is;
 * `scope` is the scope the object is *defined* in (cluster-scope objects
 * are visible everywhere; node-/guest-scope ones only within their own
 * ruleset). */
export interface ObjectUsageView {
  kind: "alias" | "ipset" | "group";
  scope: FwScope;
  name: string;
  comment?: string;
  count: number;
  referencedBy?: RuleRefView[];
}

/** GET /firewall/objects response. */
export interface FirewallObjectsResponse {
  aliases: ObjectUsageView[];
  ipsets: ObjectUsageView[];
  groups: ObjectUsageView[];
  macros: MacroView[];
}

// --- Metrics (GET /metrics/live, GET /metrics/history, `metrics.sample` WS
// event; internal/metrics.Rates/LiveMetric/HistoryPoint/SlaveRate,
// docs/features/monitoring.md §1-2) ----------------------------------------

/** Per-second rate set for one entity, mirroring internal/metrics.Rates.
 * Bps fields are bits/sec (link-utilization math's conventional unit); all
 * others are events/sec. */
export interface Rates {
  rxBps: number;
  txBps: number;
  rxPps: number;
  txPps: number;
  rxErrsPerSec: number;
  txErrsPerSec: number;
  rxDropPerSec: number;
  txDropPerSec: number;
}

/** One bond slave's own current rate + LACP/MII active state
 * (docs/features/monitoring.md §1: "Bond member balance shown per-slave"). */
export interface SlaveRate {
  ref: string;
  active: boolean;
  rates: Rates;
}

/** One entity's current rate snapshot — GET /metrics/live's per-item shape
 * and the traffic paint mode's data source (utilizationPct drives edge
 * heat/thickness). `slaves` is only present for a Bond ref. */
export interface LiveMetric {
  ref: string;
  at: number;
  rates: Rates;
  speedMbps?: number;
  rxUtilPct?: number;
  txUtilPct?: number;
  utilizationPct?: number;
  slaves?: SlaveRate[];
}

export interface MetricsLiveResponse {
  items: LiveMetric[];
}

/** One 24h-ring history point — GET /metrics/history's per-item shape
 * (rate derived between two consecutive 30s-downsampled stored samples). */
export interface HistoryPoint {
  at: number;
  rates: Rates;
}

export interface MetricsHistoryResponse {
  ref: string;
  items: HistoryPoint[];
}

/** The `metrics.sample` WS push (docs/api.md: `{ref, at, rates}`). */
export interface MetricsSampleEvent {
  event: "metrics.sample";
  ref: string;
  at: number;
  rates: Rates;
}

// --- Blueprints (GET/POST /blueprints, POST /blueprints/{id}/instantiate;
// docs/api.md's Blueprints section; internal/blueprint's Go types) --------

export type BlueprintParamType = "string" | "int" | "bool" | "cidr" | "ip" | "vid" | "vidList" | "iface" | "nodeList";

/** JSON value a param's default/a form's submitted value can take —
 * deliberately not `unknown` (every ParamDef.type above maps to exactly
 * one of these shapes, and the param form/validators branch on `type` to
 * narrow it, never on structural inspection). */
export type BlueprintParamValue = string | number | boolean | string[] | number[];

/** One parameter a blueprint's param form collects (docs/api.md's
 * Blueprints section: "ParamDef"). `addressSuggest` (only ever true on a
 * "cidr"/"ip" param) drives the param form's "suggest" button, calling
 * `GET /blueprints/{id}/suggest?param=`. */
export interface BlueprintParamDef {
  name: string;
  type: BlueprintParamType;
  label?: string;
  description?: string;
  default?: BlueprintParamValue;
  required?: boolean;
  addressSuggest?: boolean;
  subnet?: string;
}

export type BlueprintNodeSelectorMode = "all" | "single";

export interface BlueprintNodeSelector {
  mode: BlueprintNodeSelectorMode;
}

/** One entity a blueprint creates. `fields` keys mirror the corresponding
 * change op's Create-params JSON field names (docs/api.md's Blueprints
 * section) — the frontend never interprets them beyond rendering the
 * preview diagram and passing them through untouched on save/import. */
export interface BlueprintEntityTemplate {
  kind: "bridge" | "bond" | "vlan" | "sdn-zone" | "sdn-vnet" | "sdn-subnet";
  idTemplate: string;
  nodeSelector?: BlueprintNodeSelector;
  fields: Record<string, unknown>;
}

/** A parameterized topology template (docs/api.md's Blueprints section;
 * docs/data-model.md §4). `readOnly` marks the five bundled starters. */
export interface Blueprint {
  blueprintVersion: number;
  id: string;
  name: string;
  description?: string;
  readOnly?: boolean;
  nodeSelector: BlueprintNodeSelector;
  params: BlueprintParamDef[];
  entities: BlueprintEntityTemplate[];
  createdBy?: string;
  createdAt?: number;
  updatedAt?: number;
}

export interface BlueprintsListResponse {
  items: Blueprint[];
}

/** POST /blueprints/{id}/instantiate body (docs/api.md: `nodes`/`title`
 * are additive to the documented `{params}` shape). */
export interface InstantiateBlueprintRequest {
  params: Record<string, BlueprintParamValue>;
  nodes?: string[];
  title?: string;
}

/** POST /blueprints/capture body (T-603 additive route). */
export interface CaptureBlueprintRequest {
  node: string;
}

/** GET /blueprints/{id}/suggest response. */
export interface SuggestAddressResponse {
  address: string;
}

// --- Blueprint sharing bundles (T-1107, docs/features/blueprints.md §5;
// GET /blueprints/{id}/bundle, GET /blueprints/signing-key,
// POST /blueprints/import, GET/POST/DELETE /blueprint-signers) -----------

/** An Ed25519 signature over a bundle's `blueprint` field
 * (internal/blueprint.BundleSignature). `publicKey` (base64) travels
 * alongside the fingerprint so a receiving install can verify the
 * signature standalone, before deciding whether it trusts the signer. */
export interface BundleSignature {
  alg: string;
  publicKeyFingerprint: string;
  publicKey: string;
  sig: string;
}

/** The sharable envelope `{bundleVersion, blueprint, signature?}`
 * (docs/features/blueprints.md §5). `signature` is absent for an unsigned
 * bundle. */
export interface BlueprintBundle {
  bundleVersion: number;
  blueprint: Blueprint;
  signature?: BundleSignature;
}

/** POST /blueprints/import's body: the bundle plus the two explicit-trust
 * flags — at most one is ever meaningfully set for a given bundle (an
 * unsigned bundle only reads trustUnsigned; a signed one only reads
 * trustNewKey). */
export interface ImportBundleRequest extends BlueprintBundle {
  trustUnsigned?: boolean;
  trustNewKey?: boolean;
}

/** The four distinct outcomes POST /blueprints/import can report
 * (docs/api.md's Blueprint bundles section). */
export type BundleImportStatus = "imported" | "unsigned" | "untrustedSignature" | "invalidSignature";

/** A signer identified in an import response (untrusted case) or returned
 * from the trust-store CRUD routes. */
export interface BlueprintSigner {
  fingerprint: string;
  publicKey: string;
  label?: string;
  addedBy?: string;
  addedAt?: number;
}

export interface ImportBundleResponse {
  status: BundleImportStatus;
  blueprint?: Blueprint;
  signer?: BlueprintSigner;
}

export interface BlueprintSignersListResponse {
  items: BlueprintSigner[];
}

/** GET /blueprints/signing-key response: this installation's own bundle-
 * signing public key, for a receiving admin to share out-of-band and pin
 * via POST /blueprint-signers. */
export interface BlueprintSigningKeyResponse {
  alg: string;
  publicKey: string;
  fingerprint: string;
}

// --- Firewall log viewer (GET /firewall/log; `firewall.log.batch` WS
// event; docs/features/firewall.md §4, internal/fwlog) -------------------

/** Honest correlation outcome for one log line (internal/fwlog.Correlation
 * — see docs/api.md's `GET /firewall/log` section for what each value
 * means). Never a silent guess: `"ambiguous"`/`"unmatched"`/
 * `"unknown_chain"`/`"no_guest_data"` are all first-class, always-labeled
 * outcomes, not error states. */
export type FwLogCorrelationStatus = "rule" | "default_policy" | "ambiguous" | "unmatched" | "unknown_chain" | "no_guest_data";

/** A correlated line's deep-link target: enough to navigate to
 * `/firewall` and locate the exact rule by identity (guestRef + pos +
 * origin), never by DOM position — see this task's report on why that
 * matters. */
export interface FwLogRuleRef {
  guestRef: string;
  origin: "cluster" | "group" | "guest";
  groupName?: string;
  pos: number;
}

export interface FwLogCorrelation {
  status: FwLogCorrelationStatus;
  rule?: FwLogRuleRef;
  candidatePositions?: number[];
  reason?: string;
}

/** One parsed (and, where possible, correlated) pve-firewall log line. */
export interface FwLogEntry {
  seq: number;
  node: string;
  vmid: number;
  guestRef?: string;
  direction?: "in" | "out" | "";
  action?: string;
  proto?: string;
  source?: string;
  dest?: string;
  sport?: string;
  dport?: string;
  at?: number;
  raw: string;
  correlation: FwLogCorrelation;
}

/** GET /firewall/log response. */
export interface FwLogPage {
  items: FwLogEntry[];
  droppedTotal: number;
  unavailableNodes?: string[];
}

/** The `firewall.log.batch` WS event (docs/api.md's WebSocket section). */
export interface FwLogBatchEvent {
  event: "firewall.log.batch";
  entries: FwLogEntry[];
  droppedTotal: number;
}

/** GET /firewall/effects?group= response (T-502 acceptance criterion 4's
 * rule-effects preview for a security-group reference). */
export interface FirewallEffectsResponse {
  group: string;
  guests: string[];
}

// --- Firewall log analytics (T-1006, docs/api.md's `GET
// /firewall/analytics` section; internal/fwlog.Analyze) -----------------

/** One rule's observed hit count within the query's window, plus its most
 * recent hit's timestamp (unix seconds, omitted iff `hits` is 0). `rule`
 * reuses `FwLogRuleRef` — the exact same deep-link identity `GET
 * /firewall/log`'s correlated lines carry. */
export interface FwRuleHitCount {
  rule: FwLogRuleRef;
  hits: number;
  lastSeenAt?: number;
}

/** One source/destination address's occurrence count among DROP/REJECT
 * lines within the window. */
export interface FwEndpointCount {
  value: string;
  count: number;
}

export interface FwTopBlocked {
  sources: FwEndpointCount[];
  destinations: FwEndpointCount[];
}

/** One enabled rule with zero hits within the window. `daysSinceLastHit`
 * is `-1` when the rule has no observed hit anywhere in the currently-
 * retained log buffer at all — an honest "don't know" (its true history
 * may simply have rotated out of the bounded ring), never fabricated as 0
 * or the window length. */
export interface FwUnusedRule {
  rule: FwLogRuleRef;
  daysSinceLastHit: number;
}

/** GET /firewall/analytics?scope=&ref=&windowHours= response. */
export interface FwAnalyticsResponse {
  hitCounts: FwRuleHitCount[];
  topBlocked: FwTopBlocked;
  unusedRules: FwUnusedRule[];
}

// --- Path simulator (docs/api.md §"Path simulator"; internal/sim + T-503's
// `internal/api/simulate.go`) ------------------------------------------
// Mirrors the server's `simulateRequest`/`simulateResponse` wire shapes
// exactly (camelCase, macro-expanded `RuleView` reused for the blocking
// rule's deep link). See planning/reports/T-503.md for the honesty
// contract this type set exists to uphold: `indeterminate` is a fourth,
// first-class verdict (never squeezed into allow/deny/unreachable), and
// `caveats` is always non-empty and must always be rendered (T-504 AC3).

export type SimEndpointKind = "guest-nic" | "ip" | "external";

/** One end of a simulated flow, as sent to `POST /simulate/path`. `ref` (a
 * `guest-nic:<node>:<vmid>/<key>` Ref triplet) is required for
 * `guest-nic`; `ip` is required for `ip`; `external` needs neither. */
export interface SimEndpointSpec {
  kind: SimEndpointKind;
  ref?: string;
  ip?: string;
}

export interface SimulateRequest {
  src: SimEndpointSpec;
  dst: SimEndpointSpec;
  proto?: string;
  port?: number;
}

/** The four-value verdict (docs/api.md's flagged deviation note: the
 * original one-liner pinned `allow|deny|unreachable` — T-503 added
 * `indeterminate` because the honesty contract forbids a confident
 * verdict when the engine could not fully evaluate the path). Never
 * render `indeterminate` as if it were a pass or a fail. */
export type SimVerdict = "allow" | "deny" | "unreachable" | "indeterminate";

/** Where a resolved endpoint's IP came from, ordered by confidence
 * (docs/api.md: `ipSource` is `literal|static|ipam|guest-agent|omitted`). */
export type SimIpSource = "literal" | "static" | "ipam" | "guest-agent";

/** How the engine understood one endpoint. */
export interface SimResolvedEndpoint {
  kind: SimEndpointKind;
  ref?: string;
  guest?: string;
  node?: string;
  ip?: string;
  ipSource?: SimIpSource;
  attachment?: string;
  zone?: string;
  vnet?: string;
  subnet?: string;
  description?: string;
  vid?: number;
}

/** One step of the traced path, for map rendering (T-504). `ref` is a
 * real inventory Ref when the hop is a real entity, else a synthetic id
 * (`"external"`, a fabric segment) that will never match a topology node. */
export interface SimHop {
  ref?: string;
  kind: string;
  node?: string;
  label: string;
  detail?: string;
}

/** The evaluation-order origin of a blocking rule — mirrors
 * `FwLogRuleRef.origin` above (never "default": a default-policy
 * fallthrough is never reported as a `blockingRule`, only an explicit
 * ACCEPT/DROP/REJECT rule is). */
export type SimRuleOrigin = "cluster" | "group" | "guest";

/** Present only for `verdict: "deny"` — the exact rule that produced it,
 * with enough identity for the one-click deep link into the firewall
 * editor: `pos` + `origin` + `groupName?` (never DOM position), mirroring
 * `ruleDeepLinkPath`'s (web/src/fwlog/deeplink.ts) established contract.
 * `rulesetRef` (populated for every origin as of T-2002 — the ruleset the
 * matched rule is literally defined in) is part of this frozen shape too
 * (this type also serializes verbatim as the `simulate.path` MCP tool's
 * payload, docs/architecture.md §13.1 decision D10 — additive-only, never
 * removed) but is deliberately NOT what the deep link itself uses: see
 * web/src/simulator/deeplink.ts's doc comment for why the deep link always
 * derives the guest to open from the endpoint instead. */
export interface SimBlockingRule {
  enforcementPoint: "source-guest-out" | "dest-guest-in";
  rulesetRef: string;
  origin: SimRuleOrigin;
  groupName?: string;
  direction: string;
  action: string;
  rule: RuleView;
  pos: number;
}

/** Present only for `verdict: "unreachable"` — the missing-link
 * explanation (docs/features/firewall.md §5's exact operator-facing
 * messages, e.g. "VLAN 30 is not trunked on bond0 of node pve2").
 * `atRef`/`atNode` mark the break for T-504's map rendering (AC2). */
export interface SimMissing {
  code: string;
  message: string;
  atRef?: string;
  atNode?: string;
}

export type SimCaveatSeverity = "info" | "warning" | "blocker";

/** One honesty-contract disclosure. `caveats` on a `SimulateResult` is
 * always non-empty (every result carries at least the standing
 * "simulated" note) and must always be rendered, never collapsed by
 * default (T-504 AC3) — `severity: "blocker"` caveats are what explain an
 * `indeterminate` verdict and must be surfaced prominently. */
export interface SimCaveat {
  code: string;
  severity: SimCaveatSeverity;
  message: string;
  feature?: string;
}

/** `POST /simulate/path` response. */
export interface SimulateResult {
  verdict: SimVerdict;
  src: SimResolvedEndpoint;
  dst: SimResolvedEndpoint;
  proto?: string;
  port?: number;
  hops: SimHop[];
  blockingRule?: SimBlockingRule;
  missing?: SimMissing;
  caveats: SimCaveat[];
}

// --- Live path probe (docs/api.md §"Live path probe (T-802)"; T-806's
// "Verify live" UI) ---------------------------------------------------

/** `internal/probe.Outcome` (docs/api.md's `observed.outcome`).
 * `reachable`/`unreachable` are a completed probe's genuine result;
 * `timeout` means the probe did not complete within the bounded deadline;
 * `error` means the probe could not be attempted/classified at all (guest
 * agent unreachable, transport failure, unresolvable dst) — the
 * honesty-contract "no claim" bucket, never conflated with a genuine
 * `unreachable`. */
export type VerifyOutcome = "reachable" | "unreachable" | "timeout" | "error";

/** `POST /simulate/verify`'s `observed` field. `execError` is set iff
 * `outcome === "error"`; `detail` is populated for every other outcome. */
export interface VerifyObserved {
  outcome: VerifyOutcome;
  detail?: string;
  execError?: string;
}

/** `POST /simulate/verify` response: the identical src->dst/proto/port
 * tuple run through both the static simulator (`simulated`, byte-identical
 * to `POST /simulate/path`'s own result) and a real live guest-agent probe
 * (`observed`), plus whether the two disagree. */
export interface VerifyResult {
  simulated: SimulateResult;
  observed: VerifyObserved;
  diverges: boolean;
}

/** `GET /simulate/verify/eligibility`'s machine-readable `reason` values
 * (internal/api/simulate.go's `eligibilityReason*` constants) — the
 * frontend maps each to its own plain-English grey-out copy
 * (verifyEligibility.ts) rather than rendering server text directly. */
export type VerifyEligibilityReason = "not-qemu" | "agent-unreachable";

/** `GET /simulate/verify/eligibility?ref=` response: whether the named
 * guest-nic ref can currently host a live probe. `reason` is omitted when
 * `eligible` is true. */
export interface VerifyEligibility {
  eligible: boolean;
  reason?: VerifyEligibilityReason;
}

// --- Guest network interior inspector (docs/api.md §"Guest interior",
// T-1304) ---------------------------------------------------------------

/** `GET/PUT /guests/{ref}/interior-toggle` response: whether the
 * interior-inspector opt-in is currently enabled for this guest (off by
 * default). */
export interface GuestInteriorToggle {
  ref: string;
  enabled: boolean;
}

/** One network interface reported inside the guest. */
export interface GuestInteriorInterface {
  name: string;
  mac?: string;
  mtu?: number;
  up: boolean;
}

/** One address claimed on one of the guest's interfaces. */
export interface GuestInteriorAddress {
  interface: string;
  ip: string;
  family: "ipv4" | "ipv6";
  prefix?: number;
}

/** One routing-table entry reported inside the guest. `destination` is
 * `"default"` or a CIDR. */
export interface GuestInteriorRoute {
  destination: string;
  gateway?: string;
  dev?: string;
  metric?: number;
}

/** The guest's resolver configuration. */
export interface GuestInteriorDNS {
  nameservers?: string[];
  searchDomains?: string[];
}

/** One listening TCP/UDP socket reported inside the guest. */
export interface GuestInteriorListeningSocket {
  proto: "tcp" | "udp";
  localAddr: string;
  localPort: number;
}

/** `GET /guests/{ref}/interior`'s per-address IPAM cross-check annotation
 * — `claimed` is always true (this list only ever holds addresses the
 * guest itself claimed); `allocated` reports whether IPAM has a matching
 * allocation record; `matches` is `claimed && allocated`. Never a write to
 * IPAM — this is an observed-vs-allocated comparison only
 * (docs/features/ipam.md §1's "observed, never authoritative" confidence
 * labeling applied to the guest's own self-report). */
export interface GuestInteriorIPAMDiffEntry {
  ip: string;
  claimed: boolean;
  allocated: boolean;
  matches: boolean;
}

/** Which read path produced a `GuestInterior` view. */
export type GuestInteriorSource = "qemu-ga" | "lxc-host";

/** `GET /guests/{ref}/interior` response: the guest's own inside view of
 * its network, plus the IPAM cross-check annotation. */
export interface GuestInterior {
  interfaces: GuestInteriorInterface[];
  addresses: GuestInteriorAddress[];
  routes: GuestInteriorRoute[];
  dns: GuestInteriorDNS;
  listeningSockets: GuestInteriorListeningSocket[];
  defaultGatewayReachable: boolean;
  source: GuestInteriorSource;
  ipamDiff: GuestInteriorIPAMDiffEntry[];
}

// --- Protected interfaces (docs/api.md §"Protected interfaces"; T-203,
// consumed by T-605's onboarding walkthrough step 2) -----------------------

/** GET/PUT `/protected-interfaces` wire shape: node name -> the refs
 * protected on that node. `updatedBy`/`updatedAt`/`version` are only ever
 * populated by GET (internal/api/protected.go's protectedResponse); PUT's
 * response carries them too (same handler builds both), but a freshly
 * confirmed set from `/suggest` has none of them yet. */
export interface ProtectedInterfacesResponse {
  nodes: Record<string, string[]>;
  updatedBy?: string;
  updatedAt: number;
  version: number;
}

/** GET `/protected-interfaces/suggest` response: same `{nodes}` shape the
 * PUT accepts, so the onboarding UI can present it for confirmation and
 * submit the (possibly corrected) result straight back. */
export interface ProtectedInterfacesSuggestResponse {
  nodes: Record<string, string[]>;
}

/** PUT `/protected-interfaces` request body. */
export interface ProtectedInterfacesPutRequest {
  nodes: Record<string, string[]>;
}

// --- Management-path status (docs/api.md §"Protected interfaces"; T-702,
// internal/api/protected.go's handleMgmtStatus) --------------------------

/** One role a resolved management-path ref serves for its node — the exact
 * strings GET /topology's node badges also use (docs/features/topology.md
 * §3), so a badge value and a ManagementPathRef role are always the same
 * vocabulary. */
export type MgmtRole = "mgmt" | "corosync";

/** One resolved protected ref: docs/features/topology.md §3's "each
 * protected ref with roles..., the resolved physical path..., and a
 * redundant bool". `path` is every entity ref physically carrying `ref`
 * (bridge ports / bond slaves / the parent bridge for a VLAN carrier),
 * excluding `ref` itself — these are exactly the refs GET /topology badges
 * "mgmt-path". */
export interface ManagementPathRef {
  ref: string;
  roles: MgmtRole[];
  path: string[];
  redundant: boolean;
}

/** GET `/protected-interfaces/status` response. `source` is "confirmed"
 * when protected.json has at least one node entry (the onboarding-
 * confirmed set), or "detected" when it's empty and this falls back to
 * live detection — an unconfirmed cluster still gets a display answer,
 * just an explicitly provisional one (docs/features/topology.md §3's
 * "source: detected caveat with a link to the onboarding protected step"). */
/** GET `/config` (internal/api's InstanceInfo): the daemon's non-secret
 * operational configuration, surfaced read-only in the Settings page's
 * Instance section. Never carries a secret (token/key/password) — see
 * internal/api/config.go. */
export interface InstanceConfigResponse {
  version: string;
  listen: string;
  pveApiUrl: string;
  protectedPath: string;
  pveInterval: string;
  hostInterval: string;
  lldpInterval: string;
  confirmTimeoutDefaultSec: number;
  snapshotKeepDays: number;
  snapshotPinDays: number;
  readOnly: boolean;
  allowDangerousOps: boolean;
  /** T-2801: this daemon runs against the embedded synthetic cluster. */
  demo: boolean;
}

/** GET `/health` (internal/api's healthResponse). The API's only
 * unauthenticated route. */
export interface HealthResponse {
  status: string;
  version: string;
  /** T-2801. Absent (not false) on a normal daemon — the Go field is
   * `omitempty`, so an existing consumer sees a byte-identical response. */
  demo?: boolean;
}

export interface ProtectedInterfacesStatusResponse {
  source: "confirmed" | "detected";
  nodes: Record<string, ManagementPathRef[]>;
  badRefs?: string[];
  /** T-703: the confirmed protected set no longer contains a carrier live
   * detection currently finds — the management path moved since onboarding
   * confirmed it (e.g. flow C committed, the refresh prompt declined).
   * Absent while false. */
  staleProtected?: boolean;
}

// --- LLDP (docs/api.md §"Inventory & topology"'s GET /lldp row, and
// §"LLDP guided install (T-605)") -------------------------------------------

/** One GET /lldp item (internal/api/lldp.go's lldpNeighborResponse,
 * docs/data-model.md §1's LldpNeighbor contract). An empty `items` array is
 * the onboarding walkthrough's signal that lldpd may not be running on this
 * node yet (docs/user-guide.md §1.3). */
export interface LldpNeighbor {
  ref: string;
  node: string;
  localIface: string;
  protocol: string;
  chassisName: string;
  chassisId: string;
  chassisIdType?: string;
  portId: string;
  portIdType?: string;
  portDescr?: string;
  mgmtIps?: string[];
  pvid?: number;
  taggedVlans?: number[];
  speedMbps?: number;
  speedDescr?: string;
  ttl?: number;
  lastSeen?: number;
}

export interface LldpResponse {
  items: LldpNeighbor[];
}

/** One row of GET /ports (internal/topology.PortRow) — the flat ports table
 * (docs/features/lldp-discovery.md §2): each of this cluster's physical NICs
 * paired with the switch/port LLDP says it connects to. `stale` is true once
 * a neighbor has greyed (2×TTL) or aged past 10 minutes; stale rows are kept
 * (unlike the map, which drops them) for troubleshooting unplugged links. */
export interface PortRow {
  node: string;
  nic: string;
  switch: string;
  port: string;
  speedDescr?: string;
  taggedVlans?: number[];
  speedMbps?: number;
  pvid?: number;
  lastSeen?: number;
  stale: boolean;
}

export interface PortsResponse {
  items: PortRow[];
}

/** POST /lldp/install request body — `confirm` must literally be `true` or
 * the server rejects with 400 `validation_failed`. */
export interface LldpInstallRequest {
  confirm: true;
}

export interface LldpInstallNodeResult {
  node: string;
  ok: boolean;
  error?: string;
}

export interface LldpInstallResponse {
  results: LldpInstallNodeResult[];
}

/** POST /collectors/refresh response (T-3603, internal/api's
 * collectorRefreshResponse). A poll that failed is still a 200 with
 * `error` set — the request was understood and performed, and "it failed
 * again, with this message" is the useful answer rather than a transport
 * error. `changed` distinguishes "worked but nothing moved" from "nothing
 * happened", which otherwise look identical. */
/** POST /services/start response (T-3604). A failed start is an HTTP
 * error carrying systemd's own message, not a 200 with a flag — unlike a
 * collector refresh, "it did not start" is a failure of the thing the
 * operator asked for, not a reported observation. */
export interface ServiceStartResponse {
  node: string;
  unit: string;
  ok: boolean;
}

export interface CollectorRefreshResponse {
  node?: string;
  error?: string;
  changed: boolean;
}

// --- Onboarding walkthrough (T-605; docs/user-guide.md §1) -----------------
// Client-owned progress state, opaque to the backend, persisted via the
// existing GET/PUT /layouts/{name} mechanism under name "onboarding" (see
// api/onboarding.ts) — the same "frontend-owned payload, backend stores it
// as a JSON blob" pattern TopologyLayoutPayload above already established.

/** The walkthrough's four steps, in the fixed order docs/user-guide.md §1
 * documents, plus the terminal "done" state once step 4 is finished. */
export type OnboardingStep = "found-summary" | "protected" | "lldp" | "health" | "done";

export interface OnboardingProgress {
  version: 1;
  /** Unix-ms timestamp of the last "minimize" click, or null while the
   * panel is (or should be) fully open. Distinct from completion — a
   * dismissed-but-not-done walkthrough resumes at `currentStep` when
   * reopened via the AppShell's reopen pill. */
  dismissedAt: number | null;
  currentStep: OnboardingStep;
  skippedSteps: OnboardingStep[];
  completedSteps: OnboardingStep[];
}

// --- Alert Rules (T-1005; docs/api.md's "Alert Rules" section) ------------

/** `targetKind` vocabulary — internal/findings/webhook.go's `PayloadFor`
 * shapes the outbound request differently per kind (docs/api.md's
 * "Delivery shapes per targetKind" paragraph). */
export type AlertTargetKind = "generic" | "gotify" | "ntfy" | "slack";

/** `GET /findings`'s own `source` vocabulary — reused verbatim as
 * AlertRule.sourceFilter's element type.
 *
 * Debt sweep "found during the sweep, not yet carded" (2026-08-19):
 * this used to be its own 5-of-17 union (the same drift `FindingSource`
 * itself had before `T-3004-followup-01` fixed it — see that type's doc
 * comment). There is no genuine reason `sourceFilter` needs a narrower
 * type than `source` — an alert rule can filter on any producer a finding
 * can carry, and an empty/omitted list already means "match every value"
 * (docs/api.md's Alert Rules section), so there is no "all" sentinel that
 * would justify a separate union. Aliased to `FindingSource` directly so
 * the two vocabularies cannot drift apart again; kept as a distinct name
 * only for readability at `AlertRule`/`AlertRuleRequest`'s call sites. */
export type AlertSourceFilterValue = FindingSource;

/** One `AlertRule` (internal/api/alertrules.go's alertRuleResponse) — never
 * carries the target secret, plaintext or encrypted; `hasSecret` is the
 * only signal a client gets that one is configured (matching GET /config's
 * "deliberately excludes every secret" contract). */
export interface AlertRule {
  id: string;
  name: string;
  enabled: boolean;
  sourceFilter?: AlertSourceFilterValue[];
  severityFilter?: Severity[];
  targetKind: AlertTargetKind;
  targetUrl: string;
  hasSecret: boolean;
  createdAt: number;
  updatedAt: number;
  /** T-2407 delivery scheduling. `quietStart`/`quietEnd` are "HH:MM" local
   * wall clock in `quietTz` (empty = the daemon's own zone); both absent
   * means no quiet hours, and `quietStart > quietEnd` is a window crossing
   * midnight. `digestWindowSec` 0 delivers each event on its own.
   *
   * `bypassQuietHoursOnError` delivers `error`-severity findings during
   * quiet hours anyway, and defaults to true — vnprox has no `critical`
   * severity, so `error` is the top tier this applies to. */
  quietStart?: string;
  quietEnd?: string;
  quietTz?: string;
  digestWindowSec: number;
  bypassQuietHoursOnError: boolean;
}

export interface AlertRulesListResponse {
  items: AlertRule[];
}

/** POST /alert-rules and PUT /alert-rules/{id}'s shared request body.
 * `targetSecret` is a three-way-nullable field: omitted (PUT: leave the
 * existing secret untouched; POST: no secret), `""` (clear it), non-empty
 * (set/replace it) — see docs/api.md's "Create/update request body"
 * paragraph. */
export interface AlertRuleRequest {
  name: string;
  enabled: boolean;
  sourceFilter?: AlertSourceFilterValue[];
  severityFilter?: Severity[];
  targetKind: AlertTargetKind;
  targetUrl: string;
  targetSecret?: string;
  /** T-2407; see AlertRule. `bypassQuietHoursOnError` is optional so that
   * omitting it means "use the default" (true) rather than "false" — the
   * difference between being paged for an outage at 3 a.m. and not being. */
  quietStart?: string;
  quietEnd?: string;
  quietTz?: string;
  digestWindowSec?: number;
  bypassQuietHoursOnError?: boolean;
}

/** One row of the delivery log (internal/api/alertrules.go's
 * alertDeliveryResponse) — one per HTTP delivery *attempt*, not one per
 * logical delivery; see docs/api.md's "Delivery/retry" paragraph for the
 * status vocabulary. */
export interface AlertDelivery {
  id: string;
  ruleId: string;
  findingId: string;
  at: number;
  attempt: number;
  status: "retrying" | "delivered" | "failed" | "deferred";
  error?: string;
  /** T-2407: why a delivery was deferred, or what a coalesced one contained.
   * Distinct from `error` — a deferral is not a failure. */
  detail?: string;
}

export interface AlertDeliveriesListResponse {
  items: AlertDelivery[];
}

/** POST /alert-rules/{id}/test's response — always HTTP 200 once the rule
 * is found; a failed test delivery is a reportable outcome, not an API
 * error. */
export interface AlertRuleTestResponse {
  status: "delivered" | "failed";
  error?: string;
}

// --- Flows (T-1002 backend / T-1003 frontend; docs/api.md's "Flows"
// section + `internal/api/flows.go`'s flowRecordResponse) -----------------

/** T-1504's flow-metadata service-network attribution
 * (internal/flow.Classifier) — never payload inspection, see that
 * package's doc comment. `"unclassified"` means no registered NetworkSource
 * matched; `serviceClass` is omitted entirely (not `"unclassified"`) when
 * the daemon has no FlowClassifier wired at all. */
export type ServiceClass = "migration" | "backup" | "ceph-public" | "ceph-cluster" | "corosync" | "unclassified";

/** One ingested flow sample — the shape both `GET /flows`'s `items` and the
 * `flow.batch` WS event's `entries` carry, field-for-field per docs/api.md.
 * `srcRef`/`dstRef` are inventory Ref strings, populated only when the IP
 * resolved against a known bridge/SDN-subnet — **always a Bridge or
 * SdnVnet ref, never a GuestNic ref** (internal/flow.GraphResolver's
 * documented "never guessed" gap: the inventory graph carries no guest IP
 * addresses at all). `proto` is the raw IP protocol number (6=tcp,
 * 17=udp, 1=icmp, ...); `source` names which listener/sampler produced the
 * record. */
export interface FlowRecord {
  at: number;
  node: string;
  srcIp: string;
  dstIp: string;
  srcPort?: number;
  dstPort?: number;
  proto: number;
  bytes: number;
  packets: number;
  vlan?: number;
  srcRef?: string;
  dstRef?: string;
  ingressIfIndex?: number;
  egressIfIndex?: number;
  source: "sflow" | "netflow5" | "netflow9" | "ipfix" | "conntrack" | "fixture";
  serviceClass?: ServiceClass;
}

/** GET /flows response envelope — the same cluster-fan-out shape GET
 * /audit / GET /snapshots use (`partial`/`failedNodes` only present when at
 * least one peer's page couldn't be fetched). */
export interface FlowsPage {
  items: FlowRecord[];
  nextCursor?: string;
  partial?: boolean;
  failedNodes?: string[];
}

/** GET /neighbors/history's `items` shape (docs/api.md's "Neighbor binding
 * history" section, T-3905): one IP<->MAC binding transition. `prevMac` is
 * omitted and `firstSeen` true when this is the (node, ip) pair's
 * first-ever recorded row (a discovery, not a rebind). `state` mirrors
 * internal/host.Neighbor's kernel neighbor-cache-state vocabulary. */
export interface NeighborBinding {
  node: string;
  ip: string;
  mac: string;
  prevMac?: string;
  iface?: string;
  state?: "REACHABLE" | "STALE" | "PERMANENT";
  at: number;
  firstSeen: boolean;
}

/** GET /neighbors/history response envelope — the same cluster-fan-out
 * shape GET /flows uses. */
export interface NeighborHistoryPage {
  items: NeighborBinding[];
  nextCursor?: string;
  partial?: boolean;
  failedNodes?: string[];
}

/** The `flow.batch` WS event (docs/api.md's WebSocket section): pushed by
 * internal/flow.Service.Ingest whenever a listener decodes new records,
 * already rate-capped per push — the same "keep the newest N, count the
 * rest" convention as `firewall.log.batch`'s `droppedTotal`. */
export interface FlowBatchEvent {
  event: "flow.batch";
  entries: FlowRecord[];
  droppedTotal: number;
}

// --- Latency mesh (GET /latmesh/*; internal/api/latmesh.go, T-1303) -------

/** GET /latmesh/heatmap's per-item shape (docs/api.md's Latency mesh
 * section). `linkId` is `internal/latmesh.Pair.LinkID`'s stable,
 * content-derived key (`"<fabric>[:<label>]|<fromNode>-><toNode>"`),
 * globally unique per directed link. `at`/`rttMs`/`lossPct` are the most
 * recent probe tick's own values; `rollingRttMs`/`rollingLossPct` are the
 * mean over the server's rolling window — what the map's latency heatmap
 * paint mode and the path_latency_degraded/path_loss findings both key on,
 * never the single noisy `rttMs`/`lossPct` reading. Node-local only (no
 * cluster fan-out yet — see docs/api.md's Latency mesh section). */
export interface LatMeshLink {
  linkId: string;
  fabric: "corosync" | "guest";
  fromNode: string;
  toNode: string;
  at: number;
  rttMs: number;
  lossPct: number;
  rollingRttMs: number;
  rollingLossPct: number;
  sampleCount: number;
}

/** GET /latmesh/heatmap response envelope. */
export interface LatMeshHeatmap {
  items: LatMeshLink[];
}

/** GET /latmesh/history's per-item shape. */
export interface LatMeshSample {
  at: number;
  rttMs: number;
  lossPct: number;
}

/** GET /latmesh/history response envelope. */
export interface LatMeshHistory {
  linkId: string;
  items: LatMeshSample[];
}

// --- Path MTU prober (GET /mtuprobe/results; internal/api/mtuprobe.go, T-1306) --

/** One GET /mtuprobe/results item — docs/api.md's `MTUProbeResult` shape
 * (internal/mtuprobe.Result). `linkId`/`fabric`/`fromNode`/`toNode` are the
 * exact same internal/latmesh.Pair.LinkID-keyed identity `LatMeshLink` uses
 * for the same path, so a link's latency reading and its verified MTU
 * reading correlate by `linkId`. `mtu` is the binary search's converged
 * path MTU; `at` is the unix-seconds timestamp of the probe that produced
 * it; `probeCount` is how many DF-probes that convergence took. A link the
 * prober hasn't reached yet simply has no item — never a stale/zero entry. */
export interface MTUProbeResult {
  linkId: string;
  fabric: "corosync" | "guest";
  fromNode: string;
  toNode: string;
  mtu: number;
  at: number;
  probeCount: number;
}

/** GET /mtuprobe/results response envelope. */
export interface MTUProbeResults {
  items: MTUProbeResult[];
}

// --- Ceph network awareness (GET /ceph/status; internal/api/ceph.go, T-1503) --

/** One OSD-hosting node's resolved physical path for Ceph's public/cluster
 * networks — docs/api.md's Ceph section, `internal/ceph.NodeAttribution`.
 * `*Carrier`/`*RidingOn`/`*Path` entries are `inventory.Ref` strings,
 * omitted (never a guess) when that network's CIDR isn't declared or no
 * interface on this node carries a matching address. `*RidingOn` is the
 * single bond ref this network's traffic rides if bonded, or the sole bare
 * terminal NIC ref if not — the badge target the Ceph map overlay paints. */
export interface CephNodeAttribution {
  node: string;
  publicCarrier?: string;
  publicPath?: string[];
  publicRidingOn?: string;
  publicMtu?: number;
  clusterCarrier?: string;
  clusterPath?: string[];
  clusterRidingOn?: string;
  clusterMtu?: number;
}

/** One Ceph OSD plus the bond/NIC ref its node's public/cluster traffic
 * rides (denormalized from the owning `CephNodeAttribution`, "which OSDs
 * ride which bonds") — `internal/ceph.OSDAttribution`. */
export interface CephOSD {
  ref: string;
  id: number;
  node: string;
  device?: string;
  up: boolean;
  in: boolean;
  publicBond?: string;
  clusterBond?: string;
}

/** GET /ceph/status response — `internal/ceph.Overlay`. Both network CIDRs
 * are omitted on a cluster with no Ceph installed at all (never an error;
 * `nodes`/`osds` are then both empty). */
export interface CephOverlay {
  publicNetwork?: string;
  clusterNetwork?: string;
  nodes: CephNodeAttribution[];
  osds: CephOSD[];
}

// --- Conntrack (T-1305 backend / frontend; docs/api.md's "Conntrack"
// section + internal/api/conntrack.go's conntrackEntryResponse) -----------

/** One NAT-translated endpoint — docs/api.md's Conntrack section's
 * `natSrc`/`natDst` shape. */
export interface NatAddr {
  ip: string;
  port?: number;
}

/** One live conntrack table entry — the shape `GET /conntrack`'s `items`
 * carries, field-for-field per docs/api.md. `node` is which cluster node
 * this connection was observed on (a live, per-node kernel read, never
 * cached/merged server-side beyond this one response). `natSrc`/`natDst`
 * are present only when that side of the connection is NAT'd (SNAT/DNAT
 * respectively) — absent for a plain, untranslated connection. */
export interface ConntrackEntry {
  node: string;
  srcIp: string;
  dstIp: string;
  state?: string;
  natSrc?: NatAddr;
  natDst?: NatAddr;
  proto: number;
  srcPort?: number;
  dstPort?: number;
  timeoutSec?: number;
}

/** GET /conntrack response envelope — the same cluster-fan-out shape GET
 * /audit / GET /flows use, minus pagination (a live table snapshot has no
 * cursor to resume — every request re-reads current state fresh).
 * `unavailableNodes` (T-3711, additive) separately names a node whose
 * conntrack interface itself cannot be provided (no CAP_NET_ADMIN, or no
 * netlink conntrack support at all) — distinct from `failedNodes`' ordinary
 * read failure; a node is never listed in both. */
export interface ConntrackPage {
  items: ConntrackEntry[];
  partial?: boolean;
  failedNodes?: string[];
  unavailableNodes?: string[];
}

// --- Diagnosis (docs/api.md's "Diagnosis" section; internal/diagnose,
// internal/api/diagnose.go, T-1307) ----------------------------------------

/** One ladder step's outcome classification (docs/api.md's Diagnosis
 * section). `"ran"` covers both a genuine result AND an honest "could not
 * attempt this" outcome (e.g. a live probe against an unreachable guest
 * agent) — never conflated with `"skipped"` (the step does not apply to
 * this target at all) or `"error"` (a genuine ladder-level failure). */
export type DiagnoseStepStatus = "ran" | "skipped" | "error";

/** One entry of `DiagnoseResult.steps`. `name` is always one of the five
 * registered steps, in this fixed order: `"config-check"`, `"live-probe"`,
 * `"guest-interior"`, `"conntrack"`, `"capture"`. `detail` (absent for a
 * skipped/errored step) is that step's own underlying response shape
 * verbatim — deliberately typed `unknown` here rather than a discriminated
 * union of every possible step response: this page renders it as
 * read-only formatted JSON, never destructures it, so a stricter type
 * would buy nothing and would drift the moment a composed route's own
 * response shape changes. */
export interface DiagnoseStep {
  name: string;
  status: DiagnoseStepStatus;
  summary: string;
  detail?: unknown;
  ranAt: number;
}

/** How much the ladder's overall run actually established about the
 * target — a one-line orientation, not a scored diagnosis (see each
 * step's own `summary`/`detail` for the real content). */
export type DiagnoseConfidence = "high" | "medium" | "low" | "none";

/** The ladder's single readable, advisory-only conclusion.
 * `suggestedFixRef` (omitted when none applies), when present, is an
 * existing fixable finding's own id — resolved through the same
 * `POST /findings/{id}/fix` route `GET /findings` already uses, never a
 * new auto-apply mechanism. */
export interface DiagnoseVerdict {
  summary: string;
  confidence: DiagnoseConfidence;
  linkedFindingIds: string[];
  suggestedFixRef?: string;
}

/** `POST /diagnose` response — the stable, machine-consumable ladder
 * result T-1701's MCP AI operator drives next arc (docs/api.md's Diagnosis
 * section: "treat field names/the status vocabulary as a versioned
 * contract, not an internal detail"). */
export interface DiagnoseResult {
  target: string;
  steps: DiagnoseStep[];
  verdict: DiagnoseVerdict;
}

// --- History (GET /history/events; internal/api/history.go, T-1007) -------

/** One merged timeline-marker item — docs/api.md's `HistoryEvent` shape,
 * field-for-field. `kind: "changeset"` mirrors the relevant subset of
 * `GET /audit`'s own row for a T-205 lifecycle action (apply/confirm/
 * rollback/timer_rearm/recover/safety_override); `kind: "finding"` mirrors
 * one `finding_events` row. Exactly one of the two field groups is
 * populated, keyed on `kind` — never both, since a merged row is always
 * one or the other. `serviceClass` (T-1504) is additionally present on a
 * `kind: "finding"` entry only when that finding is a
 * `service_traffic_on_wrong_network` finding — parsed server-side from the
 * finding's own content-derived id, never present on any other finding. */
export interface HistoryEvent {
  at: number;
  kind: "changeset" | "finding";
  action?: string;
  target?: string;
  changesetId?: string;
  result?: string;
  findingId?: string;
  transition?: "new" | "escalated" | "resolved";
  serviceClass?: ServiceClass;
}

/** GET /history/events response envelope. */
export interface HistoryEventsResponse {
  items: HistoryEvent[];
}

// --- Captures (docs/api.md's Captures section; T-1301, T-1302) ------------

/** The server-enforced, un-overridable cap set effective for a capture
 * session/group — docs/api.md: "a request may ask for a lower value, never
 * a higher one". The UI only ever renders these (server-reported) values,
 * never the request it sent (T-1302 AC1's "server's actual (lower) value,
 * never the requested one"). */
export interface CaptureCaps {
  maxDurationSec: number;
  maxBytes: number;
  maxPackets: number;
  retentionHours: number;
}

export type CaptureStatus = "running" | "completed" | "stopped" | "error" | "purged";

/** One node-local capture session — docs/api.md's `captureSession` shape.
 * There is deliberately no `filePath` field: the on-disk path is never
 * serialized to API clients. */
export interface CaptureSession {
  id: string;
  groupId: string;
  targetRef: string;
  node: string;
  filter: string;
  caps: CaptureCaps;
  status: CaptureStatus;
  startedBy: string;
  startedAt: number;
  stoppedAt: number;
  fileBytes: number;
  packets: number;
  nodes?: string[];
}

/** POST /captures' response, and GET /captures/{id}'s shape — a "capture
 * group": one session for a single-point capture, ≥2 correlated sessions
 * (sharing this `id`) for a multi-point one. */
export interface CaptureGroup {
  id: string;
  status: CaptureStatus;
  startedBy: string;
  startedAt: number;
  caps: CaptureCaps;
  sessions: CaptureSession[];
}

/** GET /captures response envelope. */
export interface CaptureListResponse {
  items: CaptureGroup[];
}

/** POST /captures' body. durationSec/maxBytes/maxPackets are *requests*
 * only — see CaptureCaps' doc comment. */
export interface CaptureStartRequest {
  targetRef: string;
  filter?: string;
  durationSec?: number;
  maxBytes?: number;
  maxPackets?: number;
  peerTargets?: string[];
}

// --- Edge & NAT cockpit (T-1403; docs/api.md's "Edge & NAT cockpit"
// section, internal/api/edge.go) ------------------------------------------
// Read-only: no request body on either route, no mutation type here on
// purpose — every nat.*/route.static.* write is an ordinary changeset op
// (see the ChangesetOp union below), never a dedicated call from this page.

/** One node's default gateway — the same field iface.update's `gateway`
 * param writes; GET /edge/routes never invents a second representation. */
export interface EdgeDefaultRoute {
  node: string;
  iface: string;
  gateway: string;
}

/** One route.static.* rule. */
export interface EdgeStaticRoute {
  id: string;
  node: string;
  iface: string;
  destCidr: string;
  gateway: string;
  metric?: number;
  comment?: string;
}

/** GET /edge/routes response. */
export interface EdgeRoutesView {
  defaultRoutes: EdgeDefaultRoute[];
  staticRoutes: EdgeStaticRoute[];
  generatedAt: number;
}

/** One nat.masquerade.* rule. */
export interface EdgeMasquerade {
  id: string;
  node: string;
  iface: string;
  sourceCidr: string;
  comment?: string;
}

/** One nat.portforward.* rule. `targetGuestRef`/`targetGuestPoweredOff` are
 * populated only when `intIp` correlates to a currently-known guest (live
 * PVE-IPAM data) — absent/false otherwise, never guessed.
 * `targetGuestPoweredOff: true` is this card's own exit-demo scenario: a
 * port-forward exposing a guest that is not actually running. */
export interface EdgePortForward {
  id: string;
  node: string;
  iface: string;
  proto: string;
  extPort: number;
  intIp: string;
  intPort: number;
  comment?: string;
  targetGuestRef?: string;
  targetGuestPoweredOff?: boolean;
}

/** One PVE SDN simple-zone subnet with SNAT enabled — already read-only via
 * GET /sdn's own Subnet.snat (docs/features/sdn.md §2); this shape only
 * re-presents it in the Edge layer's own terms. */
export interface EdgeSDNSimpleZoneNAT {
  zone: string;
  vnet: string;
  subnet: string;
  gateway?: string;
}

/** GET /edge/nat response. */
export interface EdgeNATView {
  masquerade: EdgeMasquerade[];
  portForwards: EdgePortForward[];
  sdnSimpleZoneNat: EdgeSDNSimpleZoneNAT[];
  generatedAt: number;
}

// --- Ingress visibility (T-1406; docs/api.md's "Ingress visibility"
// section, internal/api/ingress.go) ----------------------------------------
// Read-only discovery of the reverse-proxy layer, only for operator-added
// targets. GET /ingress/status issues no mutation of its own; the only
// writes here are POST/DELETE /ingress/targets, which never touch the
// discovered proxy itself — they only add/remove a row this daemon polls.

/** `kind` vocabulary for an ingress discovery target — the four vendors
 * internal/ingress ships a discoverer for. */
export type IngressTargetKind = "haproxy" | "nginx" | "caddy" | "traefik";

/** One configured reverse-proxy discovery target. `hasCredential` is the
 * only signal a client gets that one is set (never returned itself). */
export interface IngressTarget {
  id: string;
  kind: IngressTargetKind;
  address: string;
  addedBy: string;
  addedAt: number;
  hasCredential: boolean;
}

/** GET /ingress/targets response. */
export interface IngressTargetsListResponse {
  items: IngressTarget[];
}

/** POST /ingress/targets request body. */
export interface IngressTargetCreateRequest {
  kind: IngressTargetKind;
  address: string;
  credential?: string;
}

/** One backend/upstream server a target reported, with guest correlation
 * applied where resolvable (never guessed). */
export interface IngressBackend {
  route?: string;
  address: string;
  guestRef?: string;
  healthy: boolean;
}

/** One target's freshly discovered state. `reachable: false` with an
 * `error` is a normal, expected outcome (a down/misconfigured proxy). */
export interface IngressTargetStatus {
  id: string;
  kind: IngressTargetKind;
  address: string;
  reachable: boolean;
  error?: string;
  backends: IngressBackend[];
}

/** One WAN -> port-forward -> proxy guest -> backend guest chain — drawn
 * only when a GET /edge/nat port-forward's `intIp` matches a configured
 * ingress target's own address (never an inferred/guessed chain). */
export interface IngressChain {
  portForwardId: string;
  node: string;
  proto: string;
  extPort: number;
  proxyGuestRef?: string;
  targetId: string;
  targetKind: IngressTargetKind;
  backends: IngressBackend[];
}

/** GET /ingress/status response. */
export interface IngressStatusView {
  targets: IngressTargetStatus[];
  chains: IngressChain[];
  generatedAt: number;
}

// --- Migration planner (docs/api.md's "Migration planner" section;
// internal/migration, internal/api/migration.go, T-1507) -------------------

/** `POST /migration/preflight`'s advisory verdict — never anything but a
 * warning: this route never triggers or blocks a migration itself
 * (docs/api.md's Migration planner section). */
export type MigrationVerdict = "ok" | "tight" | "insufficient";

/** `POST /migration/preflight` response — the pinned shape
 * docs/api.md documents (also Phase 16's failure-impact simulator's own
 * input contract). `estimatedTransferSec` is `-1` when `headroomMbps` is
 * `0` (no finite estimate is possible) — render that as "unknown", not a
 * literal "-1 seconds". `bestEffort` is always `true` this arc (no live
 * guest instrumentation) but is still rendered from the response, not
 * assumed, in case a future arc narrows it. */
export interface MigrationAssessment {
  headroomMbps: number;
  estimatedTransferSec: number;
  verdict: MigrationVerdict;
  bestEffort: boolean;
  caveats: string[];
}

// --- Microsegmentation (POST /microseg/*; internal/api/microseg.go, T-1602;
//     consumed by web/src/microseg/, T-1603) ------------------------------

/** One classified flow in a `MicrosegDryRunReport` bucket (docs/api.md's
 * Microsegmentation section). Enough to trace a would-have-blocked (or
 * cannot-determine) flow back to the exact observed conversation and the
 * rule tail it fell into. `reason` is set only for `cannotDetermine`
 * entries (the undecidable rule's own reason string). `at` is a unix
 * seconds timestamp; `proto` is the raw IP protocol number (6=tcp, ...),
 * the same encoding FlowRecord uses. */
export interface MicrosegFlowRef {
  direction: string;
  peerIp: string;
  peerSubnet: string;
  reason?: string;
  proto: number;
  port: number;
  at: number;
  bytes: number;
}

/** `POST /microseg/propose` response: the minimal covering-set firewall
 * policy for a guest, its honesty fields, and the ready-to-stage changeset
 * ops. `coveragePct`/`uncoveredFlowCount` are surfaced verbatim and **never
 * rounded** to "covers everything" (T-1602's coverage contract). `rules`
 * is the ordered ACCEPT allow-list plus one trailing match-all deny per
 * governed direction; `stagedOps` are the `fw.rule.create` ops the review
 * UI hands into the ordinary ChangesetDrawer — the planner never applies. */
export interface MicrosegProposal {
  guestRef: string;
  rulesetRef: string;
  directions: string[];
  rules: RuleView[];
  stagedOps: Op[];
  coveragePct: number;
  observedGoodBytes: number;
  coveredBytes: number;
  observedGoodFlowCount: number;
  uncoveredFlowCount: number;
  excludedAnomalyFlows: number;
  alreadyCoveredGroups: number;
}

/** `POST /microseg/dry-run` response: every replayed flow in exactly one of
 * four honest buckets (docs/api.md's Microsegmentation section). A
 * `wouldBlock` flow that was observed-good is the **would-have-blocked**
 * signal a reviewer must see before enforcing; `cannotDetermine` holds
 * flows the shared evaluator could not prove permitted (never folded into
 * `wouldAllow`). Both are surfaced prominently by the review UI. All four
 * arrays are always present (`[]`, never null). */
export interface MicrosegDryRunReport {
  guestRef: string;
  wouldAllow: MicrosegFlowRef[];
  wouldBlock: MicrosegFlowRef[];
  cannotDetermine: MicrosegFlowRef[];
  ungoverned: MicrosegFlowRef[];
  coveragePct: number;
}

// --- Everything else in docs/api.md ---------------------------------------
// Snapshots and IPAM read views have routes defined in docs/api.md but no
// frontend consumer yet — their request/response types land with the task
// that first calls them. Add them here, not in a parallel file.

// --- Blueprint & plugin hub (T-1705) --------------------------------------
// The browse/install surface over T-1107 signed blueprint bundles and
// T-1702 SDK plugins (docs/api.md's Hub section). The hub is a
// catalog/install-orchestration client; the signature + trust gates are
// inherited wholesale from T-1107 / T-1702, never re-implemented here.

export type HubEntryType = "blueprint" | "plugin";

/** One catalog entry from GET /hub/index. `vetted` (T-3709) means the
 * signer is in the operator's own allowlist AND the artifact passed
 * automated hygiene checks at publish time (capability manifest
 * well-formed, catalog/manifest capability agreement, strict decoding —
 * never a reproducible-build claim). It is never a human's endorsement and
 * never bypasses the per-installation trust decision an install still
 * enforces — see docs/hub-registry.md's "Automated vetting" section for the
 * exact checks. capabilities/extensionPoints/transport are populated only
 * for plugins so a browse UI can surface a plugin's declared capability
 * scope for review before an install is confirmed. */
export interface HubEntry {
  type: HubEntryType;
  id: string;
  name: string;
  version: string;
  publisher?: string;
  description?: string;
  artifactUrl: string;
  signerFingerprint?: string;
  transport?: string;
  capabilities?: string[];
  extensionPoints?: string[];
  signed: boolean;
  vetted: boolean;
}

export interface HubIndexResponse {
  items: HubEntry[];
}

/** POST /hub/install body. trustUnsigned/trustNewKey are the identical
 * explicit-trust flags POST /blueprints/import uses — a hub install of an
 * unsigned or untrusted-signer artifact requires the same explicit step. */
export interface HubInstallRequest {
  type: HubEntryType;
  id: string;
  version?: string;
  trustUnsigned?: boolean;
  trustNewKey?: boolean;
}

/** An installed plugin's identity + authoritative (signed) capability scope,
 * echoed back on a successful plugin install. */
export interface HubPluginInstalled {
  id: string;
  name: string;
  version: string;
  capabilities: string[];
  extensionPoints: string[];
}

/** POST /hub/install response. `status` reuses the blueprint-bundle
 * signature-gate vocabulary (unsigned / untrustedSignature /
 * invalidSignature) plus "imported" (blueprint success) / "installed"
 * (plugin success), so the same trust-status dialog covers both kinds.
 * "capabilityMismatch" (T-2104 AC2) is plugin-only: the catalog entry's
 * advertised capabilities/extensionPoints disagreed with the artifact's own
 * manifest, so the install was refused unconditionally — no trust flag can
 * make it proceed, because an operator can only consent to what the catalog
 * showed them. */
export type HubInstallStatus = BundleImportStatus | "installed" | "capabilityMismatch";

export interface HubInstallResponse {
  type: HubEntryType;
  status: HubInstallStatus;
  blueprint?: Blueprint;
  signer?: BlueprintSigner;
  plugin?: HubPluginInstalled;
}

// --- T-3004's analysis surfaces (failure simulation, WAN health, capacity
// export, PBS backup paths, QoS shapes, IPv6 segments). Every one of these
// is a read shape; the only write in the set is PUT /wan/targets, and QoS
// editing is an ordinary qos.shape.* changeset op (below), never a route.

/** `internal/failsim.Impact.severity` (docs/api.md's Failure-impact
 * simulation section): a coarse rollup, explicitly "never a substitute for
 * the structured fields". `info` means nothing known-broken *and* at least
 * one dimension could not be assessed — which is an unknown, not a pass. */
export type SpofSeverity = "none" | "info" | "warning" | "critical";

/** One `internal/failsim.Impact`. `target` and the ref arrays are
 * `inventory.Ref` strings; `mgmtPathLoss` is a list of node names. Every
 * array field is always emitted by the server (never omitted), so a caller
 * can tell "checked, nothing there" from "not checked" — the latter is what
 * `notEvaluated` names. `severity` is widened to `string` on the wire on
 * purpose: an unrecognised value must render as indeterminate rather than
 * being cast into one of the four known ones. */
export interface FailsimImpact {
  target: string;
  severity: string;
  disconnectedGuests: string[];
  strandedVlans: string[];
  mgmtPathLoss: string[];
  /** The load-bearing honesty channel: dimension codes (`quorum`, `ceph`,
   * `tunnels`, `guest-connectivity`) the simulator could not assess. A
   * dimension named here is unknown, never safe. */
  notEvaluated: string[];
  quorumRisk: boolean;
  cephRisk: boolean;
}

/** One `GET /failsim/spof-score` entry: the entity whose removal was
 * simulated, and what that removal costs. */
export interface SpofEntry {
  ref: string;
  impact: FailsimImpact;
}

/** `GET /failsim/spof-score` — `score` is 100 minus each SPOF's severity
 * weight, floored at 0 (higher is better). `generatedAt` is RFC 3339, not
 * unix seconds: this is the one route in the set that stamps a string. */
export interface SpofScore {
  score: number;
  entries: SpofEntry[];
  generatedAt: string;
}

/** `GET /wan/status` per-target reading. `at` is unix seconds. */
export interface WanTargetStatus {
  host: string;
  at: number;
  rttMs: number;
  lossPct: number;
  rollingRttMs: number;
  rollingLossPct: number;
  reachable: boolean;
}

/** One uplink's rollup. `status` is `healthy`|`degraded`|`unreachable` on
 * the wire; widened to `string` so an unknown value renders as unknown. */
export interface WanUplinkStatus {
  node: string;
  uplink: string;
  status: string;
  targets?: WanTargetStatus[];
  availabilityPct: number;
  rttMs: number;
  lossPct: number;
}

/** `GET /wan/status`. `verdict` is `healthy`|`wan_degraded`|`likely_isp`|
 * `no_targets`; `summary` is the daemon's own operator-facing sentence for
 * it, which the UI renders verbatim rather than re-deriving. */
export interface WanStatus {
  verdict: string;
  summary: string;
  uplinks?: WanUplinkStatus[];
  generatedAt: number;
}

/** One `GET`/`PUT /wan/targets` item. `uplink` is a caller-chosen label —
 * in practice a `GET /edge/routes` default-route interface name. */
export interface WanTarget {
  uplink: string;
  host: string;
}

/** `GET`/`PUT /wan/targets`. `node` is response-only (a computed field —
 * both verbs act on the requesting session's own node). */
export interface WanTargetsView {
  node: string;
  targets: WanTarget[];
}

/** `GET /capacity/export`'s two `kind` values (`store.CapacityKindLink` /
 * `CapacityKindIPAMPool`). A link ref is a `physnic:<node>:<iface>` Ref
 * string; an IPAM pool ref is the subnet's CIDR, not a Ref. */
export type CapacityKind = "link" | "ipam_pool";

/** One daily bucket. `bucketAt`/`createdAt` are unix seconds; the
 * utilizations are percentages. */
export interface CapacityAggregate {
  bucketAt: number;
  avgUtilization: number;
  maxUtilization: number;
  createdAt: number;
}

/** `GET /capacity/export?...&format=json`. The CSV form of the same data is
 * a file download, not this shape. */
export interface CapacityExport {
  ref: string;
  kind: CapacityKind;
  aggregates: CapacityAggregate[];
}

/** One discovered PBS host (`GET /pbs`). */
export interface PbsHost {
  ref: string;
  address: string;
  fingerprint?: string;
  datastores?: string[];
  storageIds?: string[];
  port?: number;
}

/** One backup job riding a resolved path. */
export interface PbsJob {
  id: string;
  storage: string;
  schedule?: string;
  guests: number;
  all: boolean;
}

/** One resolved node -> PBS host backup path. `carrier`/`ridingOn` are Ref
 * strings, omitted when vnprox could not resolve them ("never a guess").
 * `linkSpeedKnown` is always emitted: `linkMbps` without it is meaningless,
 * and an unknown link speed must not render as a number. */
export interface PbsPath {
  node: string;
  host: string;
  carrier?: string;
  ridingOn?: string;
  sizingHint: string;
  path?: string[];
  storageIds?: string[];
  jobs?: PbsJob[];
  linkMbps?: number;
  linkSpeedKnown: boolean;
}

/** `GET /pbs` — the read-only PBS overlay. */
export interface PbsOverlay {
  hosts: PbsHost[];
  paths: PbsPath[];
}

/** One `GET /qos/shapes` row (docs/api.md's QoS section) — a read view onto
 * the app-owned `qos_shapes` table, never a live `tc` read. */
export interface QosShape {
  id: string;
  node: string;
  bridge: string;
  matchCidr?: string;
  matchVlan?: number;
  rateMbit: number;
  ceilMbit?: number;
  priority?: number;
}

/** `GET /qos/shapes`. */
export interface QosShapesView {
  shapes: QosShape[];
}

/** op "qos.shape.create" (internal/change/params_qos.go). `bridge` is the
 * plain interface name — the op target's own Node already supplies the
 * node. Both match fields empty means the shape governs the bridge's whole
 * otherwise-unclassified egress. */
export interface QosShapeCreateParams {
  bridge: string;
  rateMbit: number;
  matchCidr?: string;
  matchVlan?: number;
  ceilMbit?: number;
  priority?: number;
}

/** op "qos.shape.update": absent means unchanged, the same partial-patch
 * convention every other *UpdateParams uses. */
export interface QosShapeUpdateParams {
  bridge?: string;
  matchCidr?: string;
  matchVlan?: number;
  rateMbit?: number;
  ceilMbit?: number;
  priority?: number;
}

/** op "qos.shape.delete" — no params; the target Ref is the whole input. */
export type QosShapeDeleteParams = Record<string, never>;

/** One node's per-interface IPv6 RA/DHCPv6 observation (`GET
 * /ipv6/segments`). `raPresent` is always emitted; every other flag is
 * omitted when false, so absence means "not observed", never "off". */
export interface IPv6Segment {
  ref?: string;
  node: string;
  iface: string;
  /** "bridge" | "vnet" | "" — "" when the observation could not be
   * correlated to a known inventory entity. */
  kind?: string;
  vnet?: string;
  zone?: string;
  prefixes?: string[];
  vid?: number;
  routerLifetimeSec?: number;
  raPresent: boolean;
  managedFlag?: boolean;
  otherFlag?: boolean;
  dhcpv6ServerPresent?: boolean;
  dhcpv6InferredFromRA?: boolean;
}

/** `GET /ipv6/segments` — the same `partial`/`failedNodes` cluster-fan-out
 * convention `GET /sdn/evpn/status` uses: one node's RA read failing never
 * blanks every other node's. */
export interface IPv6SegmentsView {
  items: IPv6Segment[];
  generatedAt: number;
  partial?: boolean;
  failedNodes?: string[];
}

// --- Tokens (docs/api.md §"Tokens & Webhooks (T-1104, automation)") --------
// Mirrors internal/api/tokens.go's tokenResponse / tokenCreateRequest /
// tokenCreateResponse exactly. Every optional field below is `omitempty` on
// the Go side, so "absent" is a real, distinct state — never a zero.

/** One row of `GET /tokens` (the caller's own tokens only — the route filters
 * by `created_by`). The raw bearer value is never part of this shape.
 *
 * `expiresAt` absent is **not** "unknown": it is the documented, deliberate
 * non-expiring token — either minted before v4.1, or minted with an explicit
 * JSON `null` (docs/api.md: "an explicit JSON `null` → a non-expiring token
 * ... now an explicit ceremony rather than the silent default"). Rendering it
 * must say so rather than leaving a blank cell. */
export interface ApiToken {
  id: string;
  name: string;
  /** The token's *stored* scope list. Not necessarily what a request
   * authenticated by it actually gets — see `effectiveTokenScopes` in
   * `settings/tokenScope.ts` for the `[server] read_only` narrowing. */
  scopes: string[];
  createdBy: string;
  createdAt: number;
  lastUsedAt?: number;
  revokedAt?: number;
  expiresAt?: number;
}

export interface ApiTokensListResponse {
  items: ApiToken[];
}

/** `POST /tokens` body. The three-way `expiresAt` contract is load-bearing and
 * is why this is `number | null | undefined` rather than `number | undefined`:
 *
 *   - field omitted  → the daemon's 90-day default (T-2903)
 *   - `null`         → a non-expiring token (explicit opt-out)
 *   - unix seconds   → that instant; must be in the future, else 400
 *
 * `JSON.stringify` drops `undefined` properties and keeps `null`, so the three
 * cases survive the wire exactly as `internal/api/tokens.go`'s
 * `json.RawMessage` field distinguishes them. */
export interface ApiTokenCreateRequest {
  name: string;
  scopes: string[];
  expiresAt?: number | null;
}

/** `POST /tokens`' 201 body: the token row **plus** the one-time reveal of the
 * raw bearer value. `token` is the only time this string ever crosses the
 * wire; it is not retrievable afterwards from any route. */
export interface ApiTokenCreateResponse extends ApiToken {
  token: string;
}

// --- Webhooks (docs/api.md §"Tokens & Webhooks (T-1104, automation)") ------

/** One row of `GET /webhooks` (internal/api/webhooks.go's webhookResponse).
 * The HMAC signing secret is never echoed back by any route. */
export interface Webhook {
  id: string;
  url: string;
  events?: string[];
  createdBy: string;
  createdAt: number;
  consecutiveFailures: number;
  lastAttemptAt?: number;
  lastSuccessAt?: number;
  lastError?: string;
}

export interface WebhooksListResponse {
  items: Webhook[];
}

/** `POST /webhooks` body. `secret` is create-only and required — there is no
 * update route, so there is no "leave unchanged" case to model. */
export interface WebhookCreateRequest {
  url: string;
  secret: string;
  events?: string[];
}

// --- Plugins (docs/api.md §"Plugins (T-1702)") -----------------------------

/** One installed plugin (internal/api/plugins.go's pluginResponse). The
 * internal launch `endpoint` is deliberately absent from the wire shape.
 *
 * `capabilities` is the plugin's **declared capability ceiling**, not a
 * grant this UI can widen: `Registry.Install` re-validates it, and
 * `POST /hub/install` refuses a manifest that disagrees with the listing
 * (`capabilityMismatch`, T-2904) before any signature or trust decision. */
export interface Plugin {
  id: string;
  name: string;
  version: string;
  apiVersion: string;
  transport: string;
  extensionPoints: string[];
  capabilities: string[];
  installedBy: string;
  installedAt: number;
  enabled: boolean;
}

export interface PluginsListResponse {
  items: Plugin[];
}

// --- Dashboard tiles (T-3911, docs/api.md "Dashboard tiles" section) ------

/** One tile a `dashboardTile` plugin contributes (internal/api/dashboard.go's
 * dashboardTileResponse — a decoupled mirror of `plugin.Tile`'s exact wire
 * shape, docs/plugins/dashboard-tile.md). Display-only: there is
 * deliberately no action/mutation field. `severity` is advisory tile
 * coloring only; an empty/absent value means neutral. */
export interface DashboardTile {
  id: string;
  title: string;
  value: string;
  detail?: string;
  link?: string;
  severity?: "info" | "warn" | "critical";
}

export interface DashboardTilesResponse {
  items: DashboardTile[];
}

// --- Doctor (GET /doctor/live, T-2406) -------------------------------------

/** The four statuses `internal/doctor.Status` defines. `warn` exists and is
 * NOT a failure (it does not drive `vnproxctl doctor`'s exit code); `skip`
 * means "could not be checked, with a reason" and is explicitly not a pass. */
export type DoctorStatus = "pass" | "warn" | "fail" | "skip";

/** One check result. `status` is typed as the raw wire `string` on purpose:
 * a status this build does not recognise must render as an explicit unknown,
 * never fall through to a definite verdict. Narrow it with
 * `asDoctorStatus()` from `api/doctor.ts` rather than casting. */
export interface DoctorResult {
  check: string;
  status: string;
  detail: string;
  remediation?: string;
}

/** `GET /doctor/live`'s envelope (internal/api/doctor.go's
 * doctorLiveResponse). Carries exactly `internal/doctor.LiveChecks` — the
 * four daemon-credentialed checks — never the full ten-check `vnproxctl
 * doctor` suite. */
export interface DoctorLiveResponse {
  results: DoctorResult[];
}

// --- Route explorer (T-3903, docs/api.md "Route explorer" section) --------

/** `GET /route/nodes`'s envelope. */
export interface RouteNodesResponse {
  nodes: string[];
}

/** One kernel FIB entry (internal/route.FIBRoute's wire shape). `dst` is
 * always a normalized CIDR — iproute2's `"default"` keyword is expanded to
 * `"0.0.0.0/0"`/`"::/0"`, and a bare host address (every `"local"`-table
 * entry) is given the family's full prefix length. `pref` (IPv6's RFC 4191
 * route preference) is present only on `afi: "ipv6"` entries. */
export interface FIBRoute {
  afi: "ipv4" | "ipv6";
  table: string;
  type: string;
  dst: string;
  gateway?: string;
  dev: string;
  protocol?: string;
  scope?: string;
  prefSrc?: string;
  pref?: string;
  metric?: number;
}

/** One `ip rule` policy-routing rule (internal/route.PolicyRule). */
export interface PolicyRule {
  afi: "ipv4" | "ipv6";
  src: string;
  table: string;
  priority: number;
}

/** One FRR RIB next hop (internal/route.RIBNextHop). */
export interface RIBNextHop {
  ip?: string;
  interface: string;
  directlyConnected?: boolean;
  active: boolean;
  fib: boolean;
  weight?: number;
}

/** One FRR RIB entry (internal/route.RIBRoute). `protocol` uses FRR's own
 * vocabulary ("connected"/"local"/"kernel"/"bgp"/...), distinct from
 * `FIBRoute.protocol`'s kernel vocabulary. `selected`/`installed`
 * distinguish "the route FRR chose" from a candidate it knows about but
 * did not install — FRR can hold more than one candidate per prefix. */
export interface RIBRoute {
  afi: "ipv4" | "ipv6";
  vrf: string;
  prefix: string;
  protocol: string;
  uptime?: string;
  nexthops: RIBNextHop[];
  distance?: number;
  metric?: number;
  selected?: boolean;
  installed?: boolean;
}

/** `GET /route/snapshot`'s response. `rib` is omitted (never an empty
 * array) exactly when `frrUnavailable` is true — the node runs no FRR at
 * all (no SDN EVPN zone configured, the common case), not a fetch
 * failure. */
export interface RouteSnapshot {
  node: string;
  fib: FIBRoute[];
  rules: PolicyRule[];
  rib?: RIBRoute[];
  frrUnavailable: boolean;
}

/** `GET /route/lookup`'s response — T-3903's core operator question,
 * "which path would this address take." `ambiguous` (candidate device
 * names) is populated exactly when `reachable` is false because more than
 * one equally-specific route matched and no `iface` hint was given to
 * disambiguate (the same situation `ip route get` itself resolves by
 * requiring an explicit `dev`). `rulesSkipped` names any policy rule the
 * lookup could not evaluate (a source-address-scoped rule — this lookup
 * answers "which path does a destination take," not "...from this
 * specific source"). `trace` is a human-readable, ordered account of how
 * the lookup reached its answer. */
export interface RouteLookupResult {
  dst: string;
  reachable: boolean;
  matchedRoute?: FIBRoute;
  matchedRule?: PolicyRule;
  trace?: string[];
  ambiguous?: string[];
  rulesSkipped?: string[];
}

// --- Compiled ruleset (nftables), T-3904 ------------------------------------
//
// GET /firewall/compiled: a read-only view of the nftables ruleset PVE
// actually compiled and installed on one node. Never an editor — no
// mutation call exists anywhere in web/src/api/nftables.ts, matching the
// permanent boundary docs/features.md documents ("vnprox ... never
// installs its own nftables ruleset").

/** One nftables table (internal/host.NftTable). `pveAuthored` is true only
 * for the two table names planning/reports/evidence/
 * pve-9.2.4-nftables-firewall-engine-2026-08-28.txt confirms
 * proxmox-firewall itself creates — any other table is still listed but
 * never treated as PVE's compiled output. */
export interface NftTable {
  family: string;
  name: string;
  pveAuthored: boolean;
}

/** One chain within an NftTable (internal/host.NftChain). `hook`/
 * `priority`/`policy` are set only for a base chain (attached to a
 * netfilter hook). `builtin` is true for one of proxmox-firewall's fixed
 * protection/plumbing chains — present whether or not the operator
 * authored any rule at all. */
export interface NftChain {
  name: string;
  table: NftTable;
  builtin: boolean;
  type?: string;
  hook?: string;
  priority?: string;
  policy?: string;
}

/** A compiled rule's best-effort link back to the vnprox-authored FwRule
 * that produced it, or an honest statement that none could be determined
 * (T-3904 AC2). `determined: false` always carries a specific `reason` —
 * never render a link when `determined` is false, and always show
 * `reason` to the operator rather than a bare placeholder. `scope`/`ref`/
 * `pos`/`origin` (set only when `determined`) identify the matched rule
 * using the same identity triple web/src/firewall/focusRule.ts's deep-link
 * contract already uses. */
export interface NftRuleAttribution {
  determined: boolean;
  scope?: string;
  ref?: string;
  origin?: string;
  pos?: number;
  reason?: string;
}

/** One compiled nftables rule (internal/host.NftRule). Match fields other
 * than `table`/`chain`/`handle`/`attribution` are best-effort extractions
 * from the rule's nft JSON expression (upstream nft's own generic wire
 * format) — an omitted field means this reader did not recognize a
 * corresponding match, not necessarily that the rule has none. */
export interface NftRule {
  table: NftTable;
  chain: string;
  handle: number;
  attribution: NftRuleAttribution;
  comment?: string;
  verdict?: string;
  proto?: string;
  srcAddr?: string;
  dstAddr?: string;
  srcPort?: string;
  dstPort?: string;
  iifname?: string;
  oifname?: string;
  log?: boolean;
}

/** `GET /firewall/compiled`'s response. `empty` is true when no
 * PVE-authored table was found at all — genuinely ambiguous between "this
 * scope's firewall is disabled" and "this node compiles the legacy
 * iptables engine instead of nftables" (see the evidence file); render
 * both possibilities, never guess which. */
export interface NftRulesetResponse {
  node: string;
  tables: NftTable[];
  chains: NftChain[];
  rules: NftRule[];
  empty: boolean;
}

// --- Switch counters / SNMP (GET /snmp/counters, /snmp/targets; internal/api/ifcounters.go, T-4013) --

/** One polled port's honest current state (docs/api.md's "Switch counters
 * (SNMP)" section) — never collapsed into a single "no data" signal.
 * `notConfigured`: no enabled SNMP target for this switch's chassis (the
 * common default). `unreachable`: a poll was attempted and the transport
 * failed. `noCounters`: the switch answered, but this port's counters
 * could not be obtained. `ok`: real counters this tick. */
export type SNMPCounterState = "not_configured" | "unreachable" | "no_counters" | "ok";

/** One GET /snmp/counters item (internal/ifcounters.Result). The six
 * counter fields and `operUp` are only meaningful (non-zero-valued) when
 * `state` is `"ok"` — a caller must switch on `state` before reading them,
 * never infer "no data" from a zero value alone. */
export interface SNMPCounterResult {
  chassisId: string;
  switchName: string;
  node: string;
  localIface: string;
  switchPort: string;
  state: SNMPCounterState;
  inErrors?: number;
  outErrors?: number;
  inDiscards?: number;
  outDiscards?: number;
  inOctets?: number;
  outOctets?: number;
  operUp?: boolean;
  at: number;
}

/** GET /snmp/counters response envelope. */
export interface SNMPCounterResults {
  items: SNMPCounterResult[];
}

/** One GET/PUT /snmp/targets item (store.SwitchSNMPTarget, community never
 * included — `hasCommunity` only, matching `AlertRule`'s `hasSecret`
 * convention). `mgmtAddr` empty means "use whichever address this
 * chassis's LLDP neighbor(s) currently advertise". */
export interface SNMPTarget {
  chassisId: string;
  chassisIdType?: string;
  mgmtAddr?: string;
  port: number;
  enabled: boolean;
  hasCommunity: boolean;
  addedBy: string;
  addedAt: number;
}

/** GET /snmp/targets response envelope. */
export interface SNMPTargetsResponse {
  items: SNMPTarget[];
}

/** PUT /snmp/targets/{chassisId} request body. `community` follows the
 * same three-state contract `AlertRuleRequest.targetSecret` uses: omitted
 * leaves the stored community untouched, `""` clears it, non-empty
 * replaces it. */
export interface SNMPTargetRequest {
  chassisIdType?: string;
  mgmtAddr?: string;
  port?: number;
  enabled: boolean;
  community?: string;
}
