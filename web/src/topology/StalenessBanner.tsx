// SPDX-License-Identifier: Apache-2.0

// The docs/features/topology.md §5 staleness banner: when any collector
// source is stale (GET /topology's `staleness.stale`), the map is showing
// last-known data, and this banner says so with the timestamp of the last
// successful poll per failing source. Node-scoped staleness also greys the
// affected band (see summarizeStaleness + toFlowElements); cluster-wide
// staleness (the "pve" loop) means the banner covers the whole map.
import type { SourceStaleness, Staleness } from "../api/types";
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
      className="rounded-md border border-status-degraded bg-status-degraded-soft px-3 py-2 text-xs text-status-degraded"
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
        <p className="mt-1 text-status-degraded">
          A refresh ran moments ago — wait a few seconds before retrying.
        </p>
      )}
      {retry?.result !== undefined && (
        <p className="mt-1">
          {retry.result.error !== undefined && retry.result.error !== "" ? (
            // The same error twice is informative: it says the problem is
            // not transient, which is what the banner could never say
            // before.
            <span className="text-status-critical">Retry failed — {retry.result.error}</span>
          ) : (
            <span className="text-status-ok">
              {retry.result.changed ? "Retry succeeded — the map has been updated." : "Retry succeeded."}
            </span>
          )}
        </p>
      )}
      {/* Capped and scrolled for the same reason UnrefFindingsBanner's list
          is — see that file's comment. One stale source per node means this
          list grows with the cluster, and it sits directly above the map
          container, which only gets the height these banners leave it. */}
      {/* Capped + scrollable, so it must be keyboard-reachable (axe
          `scrollable-region-focusable`, WCAG 2.1.1). Not caught by the a11y
          sweep, which never renders this banner — it needs a stale source —
          but it is the same shape as UnrefFindingsBanner's list, which the
          sweep did catch. */}
      <ul
        className="mt-1 max-h-28 space-y-0.5 overflow-y-auto"
        tabIndex={0}
        aria-label="Stale collector sources"
      >
        {summary.staleSources.map((s) => (
          <li key={`${s.name}:${s.node ?? ""}`}>
            <span className="font-medium">{s.name}</span> ({describeScope(s)}): {describeLastSuccess(s)}
            <StaleSourceError source={s} />
          </li>
        ))}
      </ul>
    </div>
  );
}

/** T-4304 deliverable 3: the poll error, said once.
 *
 * What this replaced rendered was `lastError` verbatim, and a poll error is a
 * five-level wrap by the time it reaches here — cause, consequence, transport
 * and syscall all joined with colons. Every clause true, the whole unreadable,
 * and the operator needed one fact and one command out of it.
 *
 * The summary is computed server-side from the error's sentinels (docs/api.md's
 * `lastErrorSummary`), not by truncating the string here. Client-side
 * truncation was the tempting version and is named in T-4304's card as
 * declined: it treats the symptom, and shipping it first removes the pressure
 * to give the daemon the words.
 *
 * The chain stays reachable. `<details>` and not a tooltip, because the text
 * an operator wants to paste into a bug report has to be selectable, and
 * because it must survive a screenshot taken by someone who never hovered. */
function StaleSourceError({ source }: { source: SourceStaleness }) {
  const chain = source.lastError ?? "";
  if (chain === "") return null;

  const summary = source.lastErrorSummary ?? "";
  // No summary means the daemon did not recognise this error, not that it is
  // unimportant — show the chain rather than nothing.
  if (summary === "") {
    return <span className="text-status-degraded"> — {chain}</span>;
  }

  return (
    <>
      <span className="text-status-degraded"> — {summary}</span>
      <details className="mt-1">
        <summary className="cursor-pointer text-xs text-fg-subtle">Technical detail</summary>
        <pre className="mt-1 overflow-x-auto whitespace-pre-wrap break-words text-xs text-fg-muted">{chain}</pre>
      </details>
    </>
  );
}
