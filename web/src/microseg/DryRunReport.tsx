// SPDX-License-Identifier: Apache-2.0

// The reviewer-facing proof surface: renders a monitor-only dry-run's
// four-bucket honest report (T-1602's DryRun) before anyone enforces a
// proposed policy. The two buckets a reviewer MUST see — `wouldBlock`
// (observed-good traffic the policy would have blocked) and
// `cannotDetermine` (flows the shared evaluator could not prove permitted) —
// are surfaced FIRST and PROMINENTLY, never folded into a rosy "looks good"
// summary: a reviewer relies on seeing exactly what a proposal would break.
// `wouldAllow`/`ungoverned` are the reassuring buckets and sit below,
// collapsible detail. The flow tables reuse the same column shape as the
// Flow Explorer (FlowRefTable) so a would-have-blocked flow reads exactly
// like the flows the reviewer already knows.
import { useState } from "react";
import type { MicrosegDryRunReport } from "../api/types";
import { FlowRefTable } from "./FlowRefTable";
import { formatCoveragePct } from "./format";

interface DryRunReportProps {
  report: MicrosegDryRunReport;
  /** True when this report replayed a held-out window rather than the
   * training corpus itself — an independent soundness proof point, so its
   * would-block count is expected to be a bounded nonzero tail (labelled
   * as such rather than alarming as a synthesis bug). */
  heldOut?: boolean;
}

function CountTile({
  label,
  count,
  tone,
}: {
  label: string;
  count: number;
  tone: "danger" | "warn" | "ok" | "muted";
}) {
  const toneClass =
    tone === "danger"
      ? "border-red-300 bg-red-50 text-red-800 dark:border-red-800 dark:bg-red-950/40 dark:text-red-300"
      : tone === "warn"
        ? "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-300"
        : tone === "ok"
          ? "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300"
          : "border-slate-300 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-900/40 dark:text-slate-300";
  return (
    <div className={`flex min-w-[9rem] flex-col rounded-lg border px-3 py-2 ${toneClass}`}>
      <span className="text-2xl font-semibold tabular-nums">{count.toLocaleString()}</span>
      <span className="text-xs font-medium">{label}</span>
    </div>
  );
}

export function DryRunReport({ report, heldOut = false }: DryRunReportProps) {
  const [showAllowed, setShowAllowed] = useState(false);
  const wouldBlockCount = report.wouldBlock.length;
  const cannotDetermineCount = report.cannotDetermine.length;
  const hasBlock = wouldBlockCount > 0;
  const hasCannot = cannotDetermineCount > 0;

  return (
    <div className="flex flex-col gap-4" data-testid="dry-run-report">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">
          Dry-run{heldOut ? " (held-out window)" : " (training window)"} — monitor only, nothing is enforced
        </h3>
        <span className="text-xs text-slate-500 dark:text-slate-400">
          Coverage {formatCoveragePct(report.coveragePct)}
        </span>
      </div>

      {/* Four honest buckets, would-block/cannot-determine first and
          loudest. Tone flips green only when a bucket is genuinely empty. */}
      <div className="flex flex-wrap gap-2">
        <CountTile label="Would-have-blocked" count={wouldBlockCount} tone={hasBlock ? "danger" : "ok"} />
        <CountTile label="Cannot determine" count={cannotDetermineCount} tone={hasCannot ? "warn" : "ok"} />
        <CountTile label="Would allow" count={report.wouldAllow.length} tone="muted" />
        <CountTile label="Ungoverned" count={report.ungoverned.length} tone="muted" />
      </div>

      {/* Would-have-blocked: the load-bearing section. Always rendered (never
          hidden behind a toggle); its table shows zero rows when empty
          rather than disappearing, so "checked, none" is unmistakable. */}
      <section
        aria-label="Would-have-blocked flows"
        data-testid="would-block-section"
        className={
          hasBlock
            ? "rounded-lg border border-red-300 bg-red-50/60 p-3 dark:border-red-800 dark:bg-red-950/20"
            : "rounded-lg border border-emerald-300 bg-emerald-50/60 p-3 dark:border-emerald-800 dark:bg-emerald-950/20"
        }
      >
        <h4 className="mb-1 text-sm font-semibold">
          Would-have-blocked flows ({wouldBlockCount})
        </h4>
        {hasBlock ? (
          <p role="alert" className="mb-2 text-sm text-red-800 dark:text-red-300">
            {heldOut
              ? "This policy would have blocked these observed-good flows from the held-out window. Every entry should be traceable to the deliberately-uncovered long tail — review each before enforcing."
              : "This policy would have blocked these observed-good flows. Do not enforce until every one is understood."}
          </p>
        ) : (
          <p className="mb-2 text-sm text-emerald-800 dark:text-emerald-300">
            No observed-good flow would be blocked by this policy.
          </p>
        )}
        <FlowRefTable flows={report.wouldBlock} label="Would-have-blocked flows table" />
      </section>

      {/* Cannot-determine: the honest third bucket — surfaced loudly, never
          assumed safe. */}
      <section
        aria-label="Cannot-determine flows"
        data-testid="cannot-determine-section"
        className={
          hasCannot
            ? "rounded-lg border border-amber-300 bg-amber-50/60 p-3 dark:border-amber-800 dark:bg-amber-950/20"
            : "rounded-lg border border-slate-200 bg-slate-50/60 p-3 dark:border-slate-700 dark:bg-slate-900/20"
        }
      >
        <h4 className="mb-1 text-sm font-semibold">
          Cannot-determine flows ({cannotDetermineCount})
        </h4>
        {hasCannot ? (
          <p role="alert" className="mb-2 text-sm text-amber-900 dark:text-amber-300">
            The evaluator could not prove these flows stay permitted under the proposed policy. They are NOT counted as
            allowed — treat each as a potential break until resolved.
          </p>
        ) : (
          <p className="mb-2 text-sm text-slate-600 dark:text-slate-400">
            Every governed flow had a definitive verdict.
          </p>
        )}
        {hasCannot && <FlowRefTable flows={report.cannotDetermine} showReason label="Cannot-determine flows table" />}
      </section>

      {/* Would-allow: the reassuring bucket. Collapsed by default so it never
          competes for attention with the two sections above. */}
      <section aria-label="Would-allow flows" className="rounded-lg border border-slate-200 p-3 dark:border-slate-800">
        <button
          type="button"
          onClick={() => { setShowAllowed((v) => !v); }}
          aria-expanded={showAllowed}
          className="flex w-full items-center justify-between text-sm font-semibold"
        >
          <span>Would-allow flows ({report.wouldAllow.length})</span>
          <span aria-hidden className="text-xs text-slate-600 dark:text-slate-400">{showAllowed ? "Hide" : "Show"}</span>
        </button>
        {showAllowed && (
          <div className="mt-2">
            <FlowRefTable flows={report.wouldAllow} label="Would-allow flows table" />
          </div>
        )}
      </section>
    </div>
  );
}
