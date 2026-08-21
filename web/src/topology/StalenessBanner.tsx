// The docs/features/topology.md §5 staleness banner: when any collector
// source is stale (GET /topology's `staleness.stale`), the map is showing
// last-known data, and this banner says so with the timestamp of the last
// successful poll per failing source. Node-scoped staleness also greys the
// affected band (see summarizeStaleness + toFlowElements); cluster-wide
// staleness (the "pve" loop) means the banner covers the whole map.
import type { Staleness } from "../api/types";
import { Button } from "../components/Button";
import { describeLastSuccess, describeScope, summarizeStaleness } from "./staleness";

/** The outcome of the last retry, kept on screen rather than toasted: the
 * useful answer is often "it failed again, with the same error", and that
 * has to stay legible next to the button that produced it. */
export interface StalenessRetryState {
  /** Phase 36 gate. Without netWrite there is no button at all. */
  canRetry: boolean;
  pending: boolean;
  /** Undefined until a retry has been attempted. `error` empty means the
   * poll succeeded. */
  result?: { error?: string; changed: boolean };
  /** Set when the server refused the retry for coming too soon — a real
   * outcome the operator should see, not a silent no-op. */
  rateLimited?: boolean;
  onRetry: () => void;
}

export interface StalenessBannerProps {
  staleness: Staleness | undefined;
  /** Omitted entirely = no retry affordance (the pre-Phase-36 rendering). */
  retry?: StalenessRetryState;
}

export function StalenessBanner({ staleness, retry }: StalenessBannerProps) {
  const summary = summarizeStaleness(staleness);
  if (!summary.stale) {
    return null;
  }
  return (
    <div
      role="status"
      className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <p className="font-medium">
          {summary.clusterWide
            ? "This map is showing last-known data — cluster polling is failing."
            : "Parts of this map are showing last-known data (greyed bands)."}
        </p>
        {/* T-3603. Phase 36's read-only operational tier: this re-runs
            vnprox's own poll and writes nothing to any node, so — unlike
            the lldpd install — there is deliberately no confirmation
            dialog. Asking "re-read the cluster?" would only train operators
            to click through the dialogs that do matter.

            Offered even when a source has never polled successfully. The
            phase card assumed a retry was pointless there and should
            navigate to that node's connection settings instead; that was
            wrong on both counts. "No successful poll yet" means "not since
            this daemon started", which includes "the peer was unreachable
            until a moment ago" — exactly when an operator would press this
            — and there is no per-node connection-settings screen to
            navigate to anyway. */}
        {retry?.canRetry === true && (
          <Button size="sm" variant="secondary" disabled={retry.pending} onClick={retry.onRetry}>
            {retry.pending ? "Retrying…" : "Retry now"}
          </Button>
        )}
      </div>
      {retry?.rateLimited === true && (
        <p className="mt-1 text-amber-700 dark:text-amber-300">
          A refresh ran moments ago — wait a few seconds before retrying.
        </p>
      )}
      {retry?.result !== undefined && (
        <p className="mt-1">
          {retry.result.error !== undefined && retry.result.error !== "" ? (
            // The same error twice is informative: it says the problem is
            // not transient, which is what the banner could never say
            // before.
            <span className="text-red-700 dark:text-red-300">Retry failed — {retry.result.error}</span>
          ) : (
            <span className="text-emerald-700 dark:text-emerald-300">
              {retry.result.changed ? "Retry succeeded — the map has been updated." : "Retry succeeded."}
            </span>
          )}
        </p>
      )}
      {/* Capped and scrolled for the same reason UnrefFindingsBanner's list
          is — see that file's comment. One stale source per node means this
          list grows with the cluster, and it sits directly above the map
          container, which only gets the height these banners leave it. */}
      <ul className="mt-1 max-h-28 space-y-0.5 overflow-y-auto">
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
