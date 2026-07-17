// Pure computation for the Home dashboard's "top talkers" tile
// (docs/features/monitoring.md §3: "Per-bridge approximation from guest
// NIC counters: rank guests by throughput on a selected bridge" — here,
// the busiest bridge cluster-wide, picked automatically rather than
// user-selected). Deliberately framework-free (no hooks/React import) so
// it is directly Vitest-able against plain TopologyNode/TopologyEdge/
// LiveMetric fixtures, mirroring findings/filters.ts's own
// pure-logic-separate-from-the-component convention.
//
// This performs NO new backend aggregation (T-904's explicit constraint):
// every number here comes straight from GET /metrics/live's per-ref
// `rates`, fetched via the exact same refs `attached-to` edges already
// name in a normal GET /topology response.
import type { LiveMetric, TopologyEdge, TopologyNode } from "../api/types";

// Mirrors topology/switchModel.ts's own (unexported) BRIDGE_KINDS — kept as
// a small local literal set rather than importing a private constant.
const BRIDGE_KINDS: ReadonlySet<string> = new Set(["bridge", "ovs-bridge"]);

export interface BridgeGuestGroup {
  bridgeRef: string;
  bridgeLabel: string;
  guestRefs: string[];
}

/** Every bridge's directly-attached guest-NIC refs, from `attached-to`
 * edges (switchModel.ts's `portsOfBridge` index, filtered to just the
 * guest-nic member). A collapsed "guest-group" pill's synthetic id is
 * deliberately excluded — it names no real inventory ref GET /metrics/live
 * can be asked about. */
export function bridgeGuestGroups(nodes: TopologyNode[], edges: TopologyEdge[]): BridgeGuestGroup[] {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const guestsOf = new Map<string, string[]>();

  for (const e of edges) {
    if (e.kind !== "attached-to") continue;
    const bridge = byId.get(e.to);
    const guest = byId.get(e.from);
    if (!bridge || !BRIDGE_KINDS.has(bridge.kind)) continue;
    if (guest?.kind !== "guest-nic") continue;
    const list = guestsOf.get(e.to);
    if (list) {
      list.push(e.from);
    } else {
      guestsOf.set(e.to, [e.from]);
    }
  }

  return Array.from(guestsOf.entries()).map(([bridgeRef, guestRefs]) => ({
    bridgeRef,
    bridgeLabel: byId.get(bridgeRef)?.label ?? bridgeRef,
    guestRefs,
  }));
}

/** Every ref (bridges + their attached guests) worth asking GET
 * /metrics/live about — the exact `refs` param to fetch/subscribe to. */
export function refsToSample(groups: readonly BridgeGuestGroup[]): string[] {
  const all = new Set<string>();
  for (const g of groups) {
    for (const ref of g.guestRefs) all.add(ref);
  }
  return Array.from(all);
}

function combinedBps(m: LiveMetric | undefined): number {
  if (!m) return 0;
  return m.rates.rxBps + m.rates.txBps;
}

export interface TopTalker {
  ref: string;
  label: string;
  rxBps: number;
  txBps: number;
}

export interface TopTalkersResult {
  bridgeRef: string;
  bridgeLabel: string;
  talkers: TopTalker[];
}

/** Picks the single busiest bridge (highest combined guest rx+tx among its
 * directly-attached guest NICs) and ranks its top `limit` guest talkers by
 * combined throughput. Returns undefined when there is no measurable
 * traffic anywhere (every candidate bridge's guests are at 0bps, or there
 * are no guest-bearing bridges at all) — the tile's "all clear"/no-data
 * empty state, never a misleading zeroed table. */
export function computeTopTalkers(
  groups: readonly BridgeGuestGroup[],
  live: ReadonlyMap<string, LiveMetric>,
  labelOf: (ref: string) => string,
  limit = 5,
): TopTalkersResult | undefined {
  let best: BridgeGuestGroup | undefined;
  let bestTotal = 0;

  for (const g of groups) {
    const total = g.guestRefs.reduce((sum, ref) => sum + combinedBps(live.get(ref)), 0);
    if (total > bestTotal) {
      bestTotal = total;
      best = g;
    }
  }

  if (!best) return undefined;

  const talkers = best.guestRefs
    .map((ref) => {
      const m = live.get(ref);
      return { ref, label: labelOf(ref), rxBps: m?.rates.rxBps ?? 0, txBps: m?.rates.txBps ?? 0 };
    })
    .filter((t) => t.rxBps + t.txBps > 0)
    .sort((a, b) => b.rxBps + b.txBps - (a.rxBps + a.txBps))
    .slice(0, limit);

  if (talkers.length === 0) return undefined;

  return { bridgeRef: best.bridgeRef, bridgeLabel: best.bridgeLabel, talkers };
}
