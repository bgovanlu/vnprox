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
}

export interface AnnotationListResponse {
  items: Annotation[];
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
  | "ipam.alloc.delete";

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
  | Record<string, unknown>;

/** One changeset operation, the wire shape internal/change/op.go's Op
 * (de)serializes. `target` is `undefined` only for "sdn.apply" (the one op
 * with no natural target entity — internal/change's `noTargetOps`). */
export interface Op {
  op: OpType;
  target?: string;
  params: OpParams;
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
}

/** WS `drift.changed` payload (docs/api.md's WebSocket section). */
export interface DriftChangedEvent {
  count: number;
}

/** T-602's unified findings-stream source producer (docs/api.md's
 * `GET /findings`, internal/findings.Source). */
export type FindingSource = "drift" | "lldp" | "ipam" | "health" | "probe";

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
  kind: "sdn_stage" | "ipam_alloc" | "stage_file" | "reload" | "fw_apply" | "fw_verify" | "sdn_apply";
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
}

/** POST /changesets body. */
export interface CreateChangesetRequest {
  title: string;
  ops: Op[];
}

/** PUT /changesets/{id} body — `title` is an accepted-but-undocumented
 * extension (T-201's report) for renaming a parked draft in place. */
export interface UpdateChangesetRequest {
  title?: string;
  ops: Op[];
}

/** POST /changesets/{id}/apply body. */
export interface ApplyChangesetRequest {
  confirmTimeoutSec: number;
  /** T-703: the typed management-path acknowledgement, recorded to the
   * audit log when the changeset touches a management path. */
  mgmtAck?: { node: string };
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
  /** "ok" | "pending" | "error" in practice (docs/features/sdn.md §1), kept
   * as a plain string since it's a server-controlled, open-ended enum
   * (mirrors this file's other `kind` fields' convention). */
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
  /** "simple" | "vlan" | "qinq" | "vxlan" | "evpn" in practice
   * (docs/features/sdn.md §2's five wizards), kept as a plain string per
   * SdnNodeStatus.status's doc comment. */
  type: string;
  bridge?: string;
  controller?: string;
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

/** GET /sdn response. */
export interface SdnTree {
  zones: SdnZone[];
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

/** One EVPN zone exit node's derived health. */
export interface EvpnExitNodeHealth {
  zone: string;
  node: string;
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

export type FwScope = "cluster" | "node" | "guest";

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
  enabled: boolean;
  defaultIn?: string;
  defaultOut?: string;
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
 * editor: `rulesetRef` + `pos` + `origin` (never DOM position), mirroring
 * `ruleDeepLinkPath`'s (web/src/fwlog/deeplink.ts) established contract. */
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
 * AlertRule.sourceFilter's element type. */
export type AlertSourceFilterValue = "drift" | "lldp" | "ipam" | "health" | "probe";

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
  status: "retrying" | "delivered" | "failed";
  error?: string;
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
  source: "sflow" | "netflow5" | "netflow9" | "ipfix" | "conntrack";
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

/** The `flow.batch` WS event (docs/api.md's WebSocket section): pushed by
 * internal/flow.Service.Ingest whenever a listener decodes new records,
 * already rate-capped per push — the same "keep the newest N, count the
 * rest" convention as `firewall.log.batch`'s `droppedTotal`. */
export interface FlowBatchEvent {
  event: "flow.batch";
  entries: FlowRecord[];
  droppedTotal: number;
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
 * cursor to resume — every request re-reads current state fresh). */
export interface ConntrackPage {
  items: ConntrackEntry[];
  partial?: boolean;
  failedNodes?: string[];
}

// --- History (GET /history/events; internal/api/history.go, T-1007) -------

/** One merged timeline-marker item — docs/api.md's `HistoryEvent` shape,
 * field-for-field. `kind: "changeset"` mirrors the relevant subset of
 * `GET /audit`'s own row for a T-205 lifecycle action (apply/confirm/
 * rollback/timer_rearm/recover/safety_override); `kind: "finding"` mirrors
 * one `finding_events` row. Exactly one of the two field groups is
 * populated, keyed on `kind` — never both, since a merged row is always
 * one or the other. */
export interface HistoryEvent {
  at: number;
  kind: "changeset" | "finding";
  action?: string;
  target?: string;
  changesetId?: string;
  result?: string;
  findingId?: string;
  transition?: "new" | "escalated" | "resolved";
}

/** GET /history/events response envelope. */
export interface HistoryEventsResponse {
  items: HistoryEvent[];
}

// --- Everything else in docs/api.md ---------------------------------------
// Snapshots and IPAM read views have routes defined in docs/api.md but no
// frontend consumer yet — their request/response types land with the task
// that first calls them. Add them here, not in a parallel file.
