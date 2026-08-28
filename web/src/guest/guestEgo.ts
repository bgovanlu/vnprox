// SPDX-License-Identifier: Apache-2.0

// T-3906's guest ego view: pure composition/derivation logic, kept out of
// GuestEgoView.tsx so it's directly Vitest-able (no React import) and so
// the degrade-vs-empty distinctions the card's acceptance criteria demand
// are unit-testable in isolation from any query/rendering concern.
//
// Nothing here collects new data. Every function here reshapes/filters
// data already fetched by an existing query hook (guests/queries.ts's
// useAllGuestNicsQuery, findings/queries.ts's useFindingsQuery,
// flows/flowsQueries.ts's useFlowsQuery, conntrack/conntrackQueries.ts's
// useConntrackQuery, firewall/queries.ts's useGuestRulesetQuery) — see
// GuestEgoView.tsx's own doc comment for the full reuse map.
import { refId, refNode } from "../changesets/opSummary";
import type { GuestNicRow } from "../guests/guestNics";
import type {
  FlowRecord,
  ResolvedView,
  SimEndpointSpec,
  SimulateRequest,
  StreamFinding,
} from "../api/types";

/** True iff `ref` is a bare Guest ref (`guest:<node>:<vmid>`) — NOT a
 * guest-nic ref (`guest-nic:<node>:<vmid>/<net>`), which shares the
 * "guest" prefix as a string but is a different `inventory.Ref` kind. */
export function isGuestRef(ref: string): boolean {
  return ref.split(":")[0] === "guest";
}

/** The vmid segment of a Guest or guest-nic ref — `refId` already strips
 * the `kind:node:` prefix; a guest-nic's id additionally carries `/<net>`,
 * which this drops. */
function vmidOf(ref: string): string {
  const id = refId(ref);
  const slash = id.indexOf("/");
  return slash === -1 ? id : id.slice(0, slash);
}

/** The owning guest's ref for a guest-nic ref — the inverse of
 * `nicsForGuest`'s own node+vmid derivation. Used by the Guests page's
 * per-row "Open guest view" deep link (GuestsPage.tsx lists guest-nic rows,
 * not guest rows, so it needs this to link into T-3906's guest-scoped
 * view). Returns the input unchanged if it's already a bare guest ref. */
export function guestRefFromNicRef(nicRef: string): string {
  return isGuestRef(nicRef) ? nicRef : `guest:${refNode(nicRef)}:${vmidOf(nicRef)}`;
}

/** Every guest-nic row belonging to `guestRef`, out of the cluster-wide
 * list `useAllGuestNicsQuery` already fetches (guests/queries.ts). There is
 * no direct inventory edge from a Guest to its own NICs (see
 * simulator/traceLink.ts's doc comment) — this is the same ref-string
 * derivation guestNics.ts's own row-building already relies on (node +
 * vmid segments), not a new graph traversal. */
export function nicsForGuest(rows: readonly GuestNicRow[], guestRef: string): GuestNicRow[] {
  const node = refNode(guestRef);
  const vmid = vmidOf(guestRef);
  return rows.filter((r) => refNode(r.ref) === node && vmidOf(r.ref) === vmid);
}

/** Every distinct bridge/VNet ref this guest's NICs currently resolve to —
 * the targets a flow-record query can actually be scoped to (GET /flows'
 * `guest=` filter matches a Bridge/SdnVnet ref, never a real guest ref —
 * see api/flows.ts's own doc comment). Unattached NICs (no resolved
 * target) are excluded, not represented as `undefined`. */
export function bridgeTargetsForGuest(nics: readonly GuestNicRow[]): string[] {
  return [...new Set(nics.map((n) => n.bridgeOrVnet).filter((x): x is string => x !== undefined))];
}

/** A deterministic "first" NIC to default a path evaluation from, when the
 * guest has more than one — sorted by ref so the choice is stable across
 * renders/tests rather than depending on fetch-order. */
export function primaryNic(nics: readonly GuestNicRow[]): GuestNicRow | undefined {
  return [...nics].sort((a, b) => a.ref.localeCompare(b.ref))[0];
}

/** The default outbound path evaluation this page runs automatically:
 * "can this guest reach the outside world", mirroring
 * simulator/traceLink.ts's `traceToExternalPath` pre-fill (src = this
 * guest's NIC, dst = external) — the one-click case an operator wants
 * without picking a destination first. */
export function defaultEgoSimulateRequest(nicRef: string): SimulateRequest {
  const src: SimEndpointSpec = { kind: "guest-nic", ref: nicRef };
  return { src, dst: { kind: "external" } };
}

/** Every open finding whose `refs` names this guest or one of its NICs —
 * client-side filtering over the same unfiltered `useFindingsQuery` result
 * every other findings surface in this app already fetches (findings/
 * queries.ts's own doc comment: "no server-side filter"). Matching on
 * `refs` (not `nodes`) is deliberate: a `nodes` match would pull in every
 * finding for the guest's whole node, which is not "about this guest". */
export function findingsForGuest(
  findings: readonly StreamFinding[],
  guestRef: string,
  nicRefs: readonly string[],
): StreamFinding[] {
  const scoped = new Set([guestRef, ...nicRefs]);
  return findings.filter((f) => (f.refs ?? []).some((r) => scoped.has(r)));
}

// ---------------------------------------------------------------------------
// Flows panel: distinguishing "ingestion not enabled" from "nothing for
// this guest right now" (the card's central quality bar).
// ---------------------------------------------------------------------------

export type FlowsPanelState =
  | { kind: "loading" }
  | { kind: "error" }
  | { kind: "no-targets" }
  | { kind: "ingestion-disabled" }
  | { kind: "empty" }
  | { kind: "data"; items: FlowRecord[] };

export interface FlowsPanelInput {
  /** This guest's resolved bridge/VNet targets (bridgeTargetsForGuest). An
   * empty list means there is nothing to scope a flow query to at all —
   * distinct from "scoped, but zero matched". */
  targets: readonly string[];
  /** A cheap, unfiltered `GET /flows?limit=1` probe (mirrors
   * flows/FlowExplorer.tsx's own `noFlowsAtAll` inference — "no items from
   * the initial fetch ... the exact 'no flow source configured' signal
   * this task's card describes, inferred purely from the data rather than
   * inspecting config flags this page has no route to read"). `undefined`
   * while the probe hasn't resolved yet. */
  clusterHasAnyFlows: boolean | undefined;
  clusterProbeLoading: boolean;
  clusterProbeError: boolean;
  /** The merged result of one guest-scoped `GET /flows?guest=<target>`
   * query per target. */
  guestItems: FlowRecord[];
  guestLoading: boolean;
  guestError: boolean;
}

/** AC's "graceful degradation" for the flows panel: an operator must be
 * told the difference between "flow ingestion is not enabled on this
 * node/cluster" (ingestion-disabled) and "flow ingestion is on, but this
 * guest's bridge/VNet has nothing recent" (empty) — conflating the two is
 * exactly the "empty panel that looks like 'no traffic'" failure the card
 * calls out by name. */
export function deriveFlowsPanelState(input: FlowsPanelInput): FlowsPanelState {
  if (input.targets.length === 0) return { kind: "no-targets" };
  if (input.clusterProbeLoading || input.guestLoading) return { kind: "loading" };
  if (input.clusterProbeError || input.guestError) return { kind: "error" };
  if (input.clusterHasAnyFlows === false) return { kind: "ingestion-disabled" };
  if (input.guestItems.length === 0) return { kind: "empty" };
  return { kind: "data", items: input.guestItems };
}

// ---------------------------------------------------------------------------
// Conntrack panel: distinguishing "this node can't provide conntrack" from
// "no active connections right now" — GET /conntrack's own
// `unavailableNodes` split (T-3711) already carries exactly this fact.
// ---------------------------------------------------------------------------

export interface ConntrackEntryLike {
  readonly node: string;
}

export type ConntrackPanelState<T extends ConntrackEntryLike> =
  | { kind: "loading" }
  | { kind: "error" }
  | { kind: "unavailable" }
  | { kind: "empty" }
  | { kind: "data"; items: readonly T[] };

export interface ConntrackPanelInput<T extends ConntrackEntryLike> {
  isLoading: boolean;
  isError: boolean;
  items: readonly T[] | undefined;
  /** `GET /conntrack`'s own `unavailableNodes` — names a node whose read
   * failed because the interface itself can't be provided there (no
   * CAP_NET_ADMIN / no netlink conntrack support), as opposed to an
   * ordinary transient failure (which surfaces as `isError`/`failedNodes`
   * instead — a genuinely different operator fix). */
  unavailableNodes: readonly string[] | undefined;
  /** The guest's own node — the only one this panel cares about being
   * unavailable; a different, unrelated node's outage doesn't change what
   * this guest's panel can honestly say. */
  guestNode: string;
}

export function deriveConntrackPanelState<T extends ConntrackEntryLike>(
  input: ConntrackPanelInput<T>,
): ConntrackPanelState<T> {
  if (input.isLoading) return { kind: "loading" };
  if (input.isError) return { kind: "error" };
  if (input.unavailableNodes?.includes(input.guestNode)) return { kind: "unavailable" };
  const items = input.items ?? [];
  if (items.length === 0) return { kind: "empty" };
  return { kind: "data", items };
}

// ---------------------------------------------------------------------------
// Firewall verdict summary (structural: GET /firewall/rulesets?scope=guest's
// resolved view — not a togglable data source, but "is this guest's
// firewall even active" is exactly the kind of fact that must never be
// silently folded into "zero rules").
// ---------------------------------------------------------------------------

export interface FirewallSummary {
  active: boolean;
  defaultIn: string;
  defaultOut: string;
  totalRules: number;
  enabledRules: number;
  allowCount: number;
  denyCount: number;
  /** `ResolvedView.gates` messages, verbatim — the server's own
   * explanation for why the cascade resolved the way it did (e.g. "cluster
   * firewall disabled"), reused rather than re-derived so this page can
   * never disagree with the Firewall page about why. */
  gateMessages: string[];
}

export function summarizeGuestRuleset(resolved: ResolvedView): FirewallSummary {
  const enabled = resolved.rules.filter((r) => r.rule.enabled);
  return {
    active: resolved.active,
    defaultIn: resolved.defaultIn.policy,
    defaultOut: resolved.defaultOut.policy,
    totalRules: resolved.rules.length,
    enabledRules: enabled.length,
    allowCount: enabled.filter((r) => r.rule.action.toUpperCase() === "ACCEPT").length,
    denyCount: enabled.filter((r) => r.rule.action.toUpperCase() === "DROP" || r.rule.action.toUpperCase() === "REJECT")
      .length,
    gateMessages: (resolved.gates ?? []).map((g) => g.message),
  };
}

// ---------------------------------------------------------------------------
// The guest picker (`/guest` with no `?ref=` yet) — dedupes the same
// cluster-wide NIC list down to one row per guest.
// ---------------------------------------------------------------------------

export interface GuestSummary {
  ref: string;
  label: string;
  node: string;
  nicCount: number;
}

/** `guest-nic` rows carry a label of the form "<guestname>/<net>"
 * (guests/guestNics.ts's own convention, sourced from
 * internal/topology/project.go's badgesOf) — this takes the guest-name
 * half. Falls back to the full label on the rare row that doesn't carry a
 * "/" (defensive; every real guest-nic label observed in this codebase
 * does). */
function guestLabelFromNicLabel(nicLabel: string): string {
  const slash = nicLabel.lastIndexOf("/");
  return slash === -1 ? nicLabel : nicLabel.slice(0, slash);
}

export function guestsFromNicRows(rows: readonly GuestNicRow[]): GuestSummary[] {
  const byRef = new Map<string, GuestSummary>();
  for (const r of rows) {
    const vmid = vmidOf(r.ref);
    if (vmid === "") continue;
    const guestRef = `guest:${refNode(r.ref)}:${vmid}`;
    const existing = byRef.get(guestRef);
    if (existing) {
      existing.nicCount += 1;
      continue;
    }
    byRef.set(guestRef, { ref: guestRef, label: guestLabelFromNicLabel(r.label), node: r.node, nicCount: 1 });
  }
  return [...byRef.values()].sort((a, b) => a.label.localeCompare(b.label));
}

/** Compact byte-count formatting for the flows panel (K/M/G, one decimal
 * once past the first tier) — flow records carry cumulative byte counters,
 * not a rate, so topology/metricsFormat.ts's bits-per-second formatter
 * doesn't apply here. */
export function formatByteCount(n: number): string {
  if (n < 1024) return `${String(n)} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = n / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(1)} ${units[unitIndex] ?? "TB"}`;
}
