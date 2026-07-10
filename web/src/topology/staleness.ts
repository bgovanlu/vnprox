// Pure staleness-projection logic for docs/features/topology.md §5's
// degraded state: "Peer node unreachable → its band renders greyed from
// last-known data with a staleness banner and timestamp." GET /topology's
// optional `staleness` section (docs/api.md) reports per-collector-source
// freshness; this module turns it into the two things the UI renders:
// which node bands to grey (node-scoped stale sources) and what the banner
// says (every stale source, with its last-success timestamp). Deliberately
// framework-free, like projection.ts, so it's exhaustively Vitest-able.
import type { SourceStaleness, Staleness } from "../api/types";

export interface StaleSummary {
  /** True iff any source is stale — keys the banner. */
  stale: boolean;
  /** True iff a cluster-wide source (no `node` scope, e.g. the "pve" loop)
   * is stale: every band's data is last-known, so the banner covers the
   * whole map rather than naming individual bands. */
  clusterWide: boolean;
  /** Node names (TopologyNode.nodeGroup values) whose band should render
   * greyed because a source scoped to that node is stale. */
  staleNodeGroups: ReadonlySet<string>;
  /** Every stale source, for the banner's per-source detail line. */
  staleSources: SourceStaleness[];
}

const HEALTHY: StaleSummary = {
  stale: false,
  clusterWide: false,
  staleNodeGroups: new Set<string>(),
  staleSources: [],
};

/** Summarizes GET /topology's `staleness` section (absent section = no
 * collector status = nothing to report, same as fully healthy). */
export function summarizeStaleness(staleness: Staleness | undefined): StaleSummary {
  if (!staleness?.stale) {
    return HEALTHY;
  }
  const staleSources = staleness.sources.filter((s) => s.stale);
  const staleNodeGroups = new Set<string>();
  let clusterWide = false;
  for (const s of staleSources) {
    if (s.node === undefined || s.node === "") {
      clusterWide = true;
    } else {
      staleNodeGroups.add(s.node);
    }
  }
  return { stale: true, clusterWide, staleNodeGroups, staleSources };
}

/** The §5 banner's "from last-known data with a staleness banner and
 * timestamp" line for one stale source: when its data was last good, or
 * that it never was. `lastSuccess` is unix seconds (docs/api.md). */
export function describeLastSuccess(source: SourceStaleness): string {
  if (source.lastSuccess === undefined || source.lastSuccess === 0) {
    return "no successful poll yet";
  }
  return `last successful data ${new Date(source.lastSuccess * 1000).toLocaleString()}`;
}

/** Human name for a source's scope: which band(s) the stale data affects. */
export function describeScope(source: SourceStaleness): string {
  return source.node ? `node ${source.node}` : "whole cluster";
}
