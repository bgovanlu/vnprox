// SPDX-License-Identifier: Apache-2.0

// T-2704's point-in-time topology diff: `GET /topology/diff?from=&to=`.
//
// Changesets record what vnprox did; the history timeline plays those back.
// Neither answers "what is different about this cluster compared to Tuesday".
// This route does — and marks each difference either with the changeset that
// explains it or, when nothing does, as UNATTRIBUTED. That marking is the
// product value: an unattributed change is one somebody made outside vnprox.
import { apiFetch } from "./client";

/** Which end of a range a resolved point is, and what it resolved to.
 * `snapshotId` is absent for the live (`to=now`) point. */
export interface TopologyDiffPoint {
  requested: string;
  snapshotId?: string;
  kind?: string;
  at: number;
  live?: boolean;
}

export interface TopologyDiffFieldChange {
  field: string;
  before: string;
  after: string;
}

/** `attributed` is never optional on the wire, deliberately: a reader must
 * not have to infer "nobody made this through vnprox" from a missing key. */
export interface TopologyDiffAttribution {
  attributed: boolean;
  changesetId?: string;
  changesetTitle?: string;
  actor?: string;
  at?: number;
}

export type TopologyDiffChange = "added" | "removed" | "modified";

export interface TopologyEntityDiff {
  ref: string;
  kind: string;
  node?: string;
  name?: string;
  change: TopologyDiffChange;
  fields: TopologyDiffFieldChange[];
  attribution: TopologyDiffAttribution;
}

/** A node captured at only one end of the range. Named rather than dropped:
 * "pve3 was not in the older capture" is not the same statement as "every
 * interface on pve3 was deleted". */
export interface TopologyDiffUnmatchedNode {
  node: string;
  presentIn: "from" | "to";
}

export interface TopologyDiffCoverage {
  nodes: string[];
  paths: string[];
  unmatchedNodes?: TopologyDiffUnmatchedNode[];
  omittedPaths?: string[];
}

export interface TopologyDiffResponse {
  from: TopologyDiffPoint;
  to: TopologyDiffPoint;
  added: TopologyEntityDiff[];
  removed: TopologyEntityDiff[];
  modified: TopologyEntityDiff[];
  coverage: TopologyDiffCoverage;
  unattributedCount: number;
}

/** The `to` sentinel for "the live cluster, read right now". */
export const TOPOLOGY_DIFF_NOW = "now";

/** GET /topology/diff — `from`/`to` are each a snapshot id, a unix-seconds
 * timestamp, or an RFC3339 timestamp; `to` also accepts `now`. */
export function fetchTopologyDiff(from: string, to: string): Promise<TopologyDiffResponse> {
  const params = new URLSearchParams({ from, to });
  return apiFetch<TopologyDiffResponse>(`/topology/diff?${params.toString()}`);
}

/** Every reported difference, in one list — the order the panel and the map
 * overlay both read. Added, then removed, then modified, each already
 * ref-ordered by the server. */
export function allTopologyDiffRows(diff: TopologyDiffResponse): TopologyEntityDiff[] {
  return [...diff.added, ...diff.removed, ...diff.modified];
}
