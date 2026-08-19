// `doctor --live` in the browser (`GET /doctor/live`, T-2406).
//
// ── THE ONE RULE ──────────────────────────────────────────────────────────
// A skipped check must never render, or be counted, as a passing one. That is
// the defect family planning/tasks/phase-29.md's wave-4 delivery record
// generalises after finding it four times in a single arc — "an absent,
// skipped, or unknown state rendering as a definite one" — and `vnproxctl
// verify`'s own exit-code rule is the precedent copied here: a run in which
// everything skipped exits non-zero and says `0 passed`, because "we could
// not look" read as "we looked and it was fine" is how a validation figure
// becomes fiction.
//
// So this section renders FIVE mutually exclusive states — pass, warn, fail,
// skip, and unrecognised — each with its own `data-status`, its own styling,
// its own word, and its own line in the counts. `warn` is deliberately
// distinct from both pass and fail: `internal/doctor`'s own `Report.Failed`
// counts only `Fail`, so a warning is degraded-but-working.
//
// ── SCOPE, because the number is easy to overstate ────────────────────────
// `vnproxctl doctor` runs TEN checks. This route runs exactly
// `internal/doctor.LiveChecks` — the FOUR needing a credential only the
// running daemon holds. The other six are CLI-local observations of the
// machine the CLI stands on, and `RunLive`'s doc comment says mixing them
// "would blur which half of a report came from where". The header states
// that, so nobody reads a green four-of-four as a green ten-of-ten.
import clsx from "clsx";
import { Button } from "../components/Button";
import { asDoctorStatus } from "../api/doctor";
import type { DoctorResult, DoctorStatus } from "../api/types";
import { useDoctorLiveQuery } from "./platformQueries";
import { doctorCountsLine, doctorVerdict, summarizeDoctorResults } from "./doctorSummary";
import { PlatformSection, RefusalNotice } from "./platformCommon";

/** Per-status presentation. `skip` and `unknown` are visually quiet AND
 * verbally explicit; neither shares a colour family with `pass`. */
const STATUS_STYLE: Record<DoctorStatus | "unknown", { label: string; chip: string; card: string; gloss: string }> = {
  pass: {
    label: "PASS",
    chip: "bg-emerald-100 text-emerald-800 dark:bg-emerald-500/15 dark:text-emerald-300",
    card: "border-emerald-300 dark:border-emerald-500/40",
    gloss: "Checked, and healthy.",
  },
  warn: {
    label: "WARN",
    chip: "bg-amber-100 text-amber-800 dark:bg-amber-500/15 dark:text-amber-300",
    card: "border-amber-300 dark:border-amber-500/40",
    gloss: "Checked, and degraded but working. Not a failure.",
  },
  fail: {
    label: "FAIL",
    chip: "bg-red-100 text-red-800 dark:bg-red-500/15 dark:text-red-300",
    card: "border-red-300 dark:border-red-500/40",
    gloss: "Checked, and broken.",
  },
  skip: {
    label: "SKIPPED",
    chip: "bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-200",
    card: "border-dashed border-slate-400 dark:border-slate-600",
    gloss: "NOT checked. This is not a pass — the reason is below.",
  },
  unknown: {
    label: "UNRECOGNISED",
    chip: "bg-violet-100 text-violet-800 dark:bg-violet-500/15 dark:text-violet-300",
    card: "border-dashed border-violet-400 dark:border-violet-500/50",
    gloss: "The daemon reported a status this build does not know. Treat it as unchecked.",
  },
};

function CheckCard({ result }: { result: DoctorResult }) {
  const status = asDoctorStatus(result.status) ?? "unknown";
  const style = STATUS_STYLE[status];
  return (
    <li
      data-testid={`doctor-check-${result.check}`}
      data-status={status}
      className={clsx("rounded-md border p-3", style.card)}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span
          data-testid={`doctor-status-${result.check}`}
          className={clsx("rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide", style.chip)}
        >
          {style.label}
        </span>
        <code className="text-xs font-medium text-slate-700 dark:text-slate-200">{result.check}</code>
        {status === "unknown" && (
          <code className="text-[10px] text-violet-700 dark:text-violet-300">reported &ldquo;{result.status}&rdquo;</code>
        )}
      </div>
      <p className="mt-1 text-[11px] italic text-slate-600 dark:text-slate-400">{style.gloss}</p>
      <p className="mt-1 text-sm text-slate-700 dark:text-slate-200">{result.detail}</p>
      {result.remediation !== undefined && result.remediation !== "" && (
        <p className="mt-1 text-xs text-slate-600 dark:text-slate-300">
          <span className="font-semibold">Remediation: </span>
          {result.remediation}
        </p>
      )}
    </li>
  );
}

export function DoctorLiveSection() {
  const query = useDoctorLiveQuery();
  const results = query.data ?? [];
  const summary = summarizeDoctorResults(results);

  return (
    <PlatformSection
      title="Daemon self-check (doctor --live)"
      helpTopic="platform-doctor-live"
      description={
        <>
          The four checks that need a credential only the running daemon holds — the PVE token, the peer HMAC secret,
          and a reference clock reached through the first of those. The other six checks <code>vnproxctl doctor</code>{" "}
          runs observe the machine the CLI is standing on and are deliberately not answered here.
        </>
      }
      actions={
        <Button
          size="sm"
          variant="secondary"
          disabled={query.isFetching}
          onClick={() => {
            void query.refetch();
          }}
          data-testid="doctor-rerun"
        >
          {query.isFetching ? "Running…" : "Run checks"}
        </Button>
      }
    >
      {query.isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Running live checks…</p>}

      {query.error !== null && (
        <RefusalNotice
          error={query.error}
          testId="doctor-error"
          forbiddenHint={
            <>
              <code>GET /doctor/live</code> is gated on the <code>audit</code> capability — the same one the audit log
              and the support bundle need — because it reports which privileges the configured PVE token holds. Not
              being allowed to look is not the same as the checks failing.
            </>
          }
          unavailableHint="This daemon does not mount the live-check route — no live-check service is wired on it."
        />
      )}

      {query.isSuccess && (
        <>
          <ul className="space-y-2" data-testid="doctor-results">
            {results.map((result) => (
              <CheckCard key={result.check} result={result} />
            ))}
          </ul>

          <div
            className="mt-3 rounded-md border border-slate-200 p-3 text-sm dark:border-slate-700"
            data-testid="doctor-summary"
          >
            <p className="font-mono text-xs text-slate-600 dark:text-slate-300" data-testid="doctor-counts">
              {doctorCountsLine(summary)}
            </p>
            <p className="mt-1 text-slate-700 dark:text-slate-200" data-testid="doctor-verdict">
              {doctorVerdict(summary)}
            </p>
          </div>
        </>
      )}
    </PlatformSection>
  );
}
