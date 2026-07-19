// Pure computation for the Home dashboard's "service-network traffic" tile
// (T-1504's deliverable: "New home-dashboard tile ... per-serviceClass
// bytes/sec breakdown over the retained flow window"). Deliberately
// framework-free (no hooks/React import) so it is directly Vitest-able
// against plain FlowRecord fixtures, mirroring topTalkers.ts's own
// "pure-logic-separate-from-the-component" convention.
//
// This performs NO new backend aggregation — every number here comes
// straight from GET /flows' already-retained `flow_samples` window
// (T-1504's additive `serviceClass` field on each record), the same
// client-side-computation stance topTalkers.ts takes over GET /metrics/live.
import type { FlowRecord, ServiceClass } from "../api/types";

/** Type guard narrowing a FlowRecord to one with a definitely-present
 * serviceClass — records with no serviceClass field at all (the daemon has
 * no FlowClassifier wired) are excluded entirely, see
 * computeServiceClassBreakdown's doc comment. */
function hasServiceClass(r: FlowRecord): r is FlowRecord & { serviceClass: ServiceClass } {
  return r.serviceClass !== undefined;
}

export interface ServiceClassBreakdownEntry {
  serviceClass: ServiceClass;
  bytes: number;
  bytesPerSec: number;
}

export interface ServiceClassBreakdownResult {
  windowSeconds: number;
  entries: ServiceClassBreakdownEntry[];
}

/** Buckets records by serviceClass and computes bytes/sec over the window
 * spanned by the records themselves (max(at) - min(at), floored at 1s to
 * avoid a divide-by-zero/inflated rate on a single-instant sample).
 * Records with no serviceClass field at all (the daemon has no
 * FlowClassifier wired — T-1504's "quietly absent" degradation) are
 * excluded entirely, so this tile has nothing to show rather than a
 * misleading all-"unclassified" bucket; a record whose serviceClass is the
 * classifier's own explicit `"unclassified"` verdict *is* included as its
 * own bucket (a real, informative "how much traffic isn't attributed yet"
 * signal). Returns undefined when there is no classified traffic at all —
 * the tile's "all clear"/no-data empty state, never a misleading zeroed
 * table. Entries are sorted by bytes, busiest first. */
export function computeServiceClassBreakdown(records: readonly FlowRecord[]): ServiceClassBreakdownResult | undefined {
  const classified = records.filter(hasServiceClass);
  if (classified.length === 0) return undefined;

  let minAt = Number.POSITIVE_INFINITY;
  let maxAt = Number.NEGATIVE_INFINITY;
  const bytesByClass = new Map<ServiceClass, number>();
  for (const r of classified) {
    minAt = Math.min(minAt, r.at);
    maxAt = Math.max(maxAt, r.at);
    bytesByClass.set(r.serviceClass, (bytesByClass.get(r.serviceClass) ?? 0) + r.bytes);
  }
  const windowSeconds = Math.max(1, maxAt - minAt);

  const entries: ServiceClassBreakdownEntry[] = Array.from(bytesByClass.entries())
    .map(([serviceClass, bytes]) => ({ serviceClass, bytes, bytesPerSec: bytes / windowSeconds }))
    .sort((a, b) => b.bytes - a.bytes);

  return { windowSeconds, entries };
}
