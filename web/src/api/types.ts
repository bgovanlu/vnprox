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
 * frontend consumer yet — SDN/firewall/IPAM ops are not editable in T-207,
 * see this task's report) rather than an enum so unknown-to-this-file
 * values (still valid on the wire) don't need a cast. */
export type OpType =
  | "iface.update"
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
  vid: number;
  mtu?: number;
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

/** Every Params shape T-207's editors can produce. Ops this task doesn't
 * edit (SDN/firewall/IPAM) still round-trip through the drawer/review
 * screen — they just carry `Record<string, unknown>` params, since nothing
 * in this task ever needs to read a typed field off one. */
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
  kind: "stage_file" | "reload" | "sdn_apply";
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

// --- Everything else in docs/api.md ---------------------------------------
// Snapshots, IPAM read views, and the path simulator all have routes
// defined in docs/api.md but no frontend consumer yet — their
// request/response types land with the task that first calls them.
// Add them here, not in a parallel file.
