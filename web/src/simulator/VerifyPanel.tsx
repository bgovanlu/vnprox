// T-806's "Verify live" result rendering: the simulated verdict and the
// observed live-probe outcome shown side by side, plus a distinct
// divergence callout when they disagree. Deliberately a separate small
// component (not folded into ResultPanel.tsx's own render tree) so it
// stays independently testable and only ever appears once a live probe
// result actually exists — the plain simulated-only view (ResultPanel)
// renders unconditionally, this renders additively beneath it.
//
// Honesty contract (docs/features/firewall.md §5/§6): the simulator's own
// "Simulated" labeling never changes here — this panel additionally shows
// what was *observed*, and when the two disagree, says so plainly. It
// never implies the live probe "corrects" the simulated verdict (no
// strikethrough, no "actually" framing) — both numbers are shown as
// independent facts, and the callout below only ever says they disagree,
// never which one is "right".
import clsx from "clsx";
import { HelpAnchor } from "../help/HelpAnchor";
import type { VerifyResult } from "../api/types";

const VERDICT_LABEL: Record<string, string> = {
  allow: "Allowed",
  deny: "Blocked",
  unreachable: "Unreachable",
  indeterminate: "Could not determine",
};

const OUTCOME_LABEL: Record<string, string> = {
  reachable: "Reachable",
  unreachable: "Unreachable",
  timeout: "Timed out",
  error: "Could not be attempted",
};

const OUTCOME_CLASS: Record<string, string> = {
  reachable: "text-emerald-700 dark:text-emerald-300",
  unreachable: "text-red-700 dark:text-red-300",
  timeout: "text-amber-700 dark:text-amber-300",
  error: "text-slate-500 dark:text-slate-400",
};

export function VerifyPanel({ verify }: { verify: VerifyResult }) {
  const { simulated, observed, diverges } = verify;
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-slate-200 p-3 dark:border-slate-800">
      <div className="flex items-center justify-between gap-2">
        <h3 className="flex items-center gap-1.5 text-sm font-semibold text-slate-700 dark:text-slate-200">
          Verify live
          <HelpAnchor topic="verify-live" />
        </h3>
        <span className="rounded bg-black/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide dark:bg-white/10">
          Live probe
        </span>
      </div>

      <div className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
        <div>
          <h4 className="mb-1 text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
            Simulated
          </h4>
          <p className="font-medium text-slate-800 dark:text-slate-100">{VERDICT_LABEL[simulated.verdict] ?? simulated.verdict}</p>
          <p className="text-xs text-slate-500 dark:text-slate-400">Static analysis of configured state.</p>
        </div>
        <div>
          <h4 className="mb-1 text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
            Observed
          </h4>
          <p className={clsx("font-medium", OUTCOME_CLASS[observed.outcome] ?? "text-slate-800 dark:text-slate-100")}>
            {OUTCOME_LABEL[observed.outcome] ?? observed.outcome}
          </p>
          <p className="text-xs text-slate-500 dark:text-slate-400">
            {observed.outcome === "error" ? (observed.execError ?? "The probe could not be attempted.") : (observed.detail ?? "A real ICMP/TCP probe run inside the source guest.")}
          </p>
        </div>
      </div>

      {diverges && (
        <div
          role="alert"
          className="rounded-lg border border-fuchsia-300 bg-fuchsia-50/60 p-3 text-sm text-fuchsia-900 dark:border-fuchsia-800 dark:bg-fuchsia-950/40 dark:text-fuchsia-100"
        >
          <p className="font-semibold">Live result disagrees with the simulated verdict</p>
          <p className="mt-1 text-xs">
            The simulator says <strong>{VERDICT_LABEL[simulated.verdict] ?? simulated.verdict}</strong>; the live probe
            observed <strong>{OUTCOME_LABEL[observed.outcome] ?? observed.outcome}</strong>. This is not a correction of
            either result — investigate before trusting one alone (e.g. connection tracking, a mid-path device, or a
            recent config change not yet reflected here).
          </p>
        </div>
      )}
    </div>
  );
}
