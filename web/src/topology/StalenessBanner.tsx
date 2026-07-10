// The docs/features/topology.md §5 staleness banner: when any collector
// source is stale (GET /topology's `staleness.stale`), the map is showing
// last-known data, and this banner says so with the timestamp of the last
// successful poll per failing source. Node-scoped staleness also greys the
// affected band (see summarizeStaleness + toFlowElements); cluster-wide
// staleness (the "pve" loop) means the banner covers the whole map.
import type { Staleness } from "../api/types";
import { describeLastSuccess, describeScope, summarizeStaleness } from "./staleness";

export interface StalenessBannerProps {
  staleness: Staleness | undefined;
}

export function StalenessBanner({ staleness }: StalenessBannerProps) {
  const summary = summarizeStaleness(staleness);
  if (!summary.stale) {
    return null;
  }
  return (
    <div
      role="status"
      className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200"
    >
      <p className="font-medium">
        {summary.clusterWide
          ? "This map is showing last-known data — cluster polling is failing."
          : "Parts of this map are showing last-known data (greyed bands)."}
      </p>
      <ul className="mt-1 space-y-0.5">
        {summary.staleSources.map((s) => (
          <li key={`${s.name}:${s.node ?? ""}`}>
            <span className="font-medium">{s.name}</span> ({describeScope(s)}): {describeLastSuccess(s)}
            {s.lastError !== undefined && s.lastError !== "" && (
              <span className="text-amber-700 dark:text-amber-300"> — {s.lastError}</span>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
