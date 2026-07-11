// The simulation result panel (docs/features/firewall.md §5's output
// contract + T-504's AC1-3): verdict banner, hop-by-hop list, the
// blocking-rule card with its firewall-editor deep link, the missing-link
// explanation, and the always-visible caveat list. The "simulated" badge
// (spec's labeling requirement) is rendered unconditionally — every result
// from this route genuinely is a static simulation, never a live probe.
import clsx from "clsx";
import { Link } from "react-router-dom";
import type { SimCaveat, SimCaveatSeverity, SimResolvedEndpoint, SimVerdict, SimulateResult } from "../api/types";
import { ruleMatchLabel } from "../firewall/format";
import { blockingRuleDeepLinkPath } from "./deeplink";

const VERDICT_LABEL: Record<SimVerdict, string> = {
  allow: "Allowed",
  deny: "Blocked",
  unreachable: "Unreachable",
  indeterminate: "Could not determine",
};

const VERDICT_BANNER_CLASS: Record<SimVerdict, string> = {
  allow: "border-emerald-300 bg-emerald-50 text-emerald-900 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-100",
  deny: "border-red-300 bg-red-50 text-red-900 dark:border-red-700 dark:bg-red-950 dark:text-red-100",
  unreachable: "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100",
  indeterminate: "border-violet-300 bg-violet-50 text-violet-900 dark:border-violet-700 dark:bg-violet-950 dark:text-violet-100",
};

const CAVEAT_SEVERITY_CLASS: Record<SimCaveatSeverity, string> = {
  info: "border-slate-200 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300",
  warning: "border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200",
  blocker: "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200",
};

const CAVEAT_SEVERITY_LABEL: Record<SimCaveatSeverity, string> = {
  info: "Info",
  warning: "Warning",
  blocker: "Blocker",
};

function endpointSummary(ep: SimResolvedEndpoint): string {
  if (ep.kind === "external") return "External / WAN";
  const parts: string[] = [];
  if (ep.guest) parts.push(ep.guest);
  if (ep.ip) parts.push(ep.ip + (ep.ipSource ? ` (${ep.ipSource})` : ""));
  if (ep.node) parts.push(`on ${ep.node}`);
  if (parts.length === 0 && ep.description) return ep.description;
  return parts.length > 0 ? parts.join(" · ") : "unresolved";
}

/** Caveats sorted blocker-first (they explain an indeterminate verdict and
 * are the most load-bearing to read) then warning then info — but every
 * caveat renders regardless of position, never collapsed (AC3). */
function sortCaveats(caveats: readonly SimCaveat[]): SimCaveat[] {
  const rank: Record<SimCaveatSeverity, number> = { blocker: 0, warning: 1, info: 2 };
  return [...caveats].sort((a, b) => rank[a.severity] - rank[b.severity]);
}

export function VerdictBanner({ result }: { result: SimulateResult }) {
  return (
    <div
      role={result.verdict === "allow" ? "status" : "alert"}
      className={clsx("flex flex-wrap items-center gap-2 rounded-lg border px-4 py-3", VERDICT_BANNER_CLASS[result.verdict])}
    >
      <span className="text-sm font-semibold">{VERDICT_LABEL[result.verdict]}</span>
      <span className="rounded bg-black/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide dark:bg-white/10">
        Simulated
      </span>
      {result.verdict === "indeterminate" && (
        <span className="text-xs">See the blocker caveats below for why.</span>
      )}
    </div>
  );
}

function HopList({ result }: { result: SimulateResult }) {
  if (result.hops.length === 0) {
    return <p className="text-sm text-slate-400">No path traced yet.</p>;
  }
  return (
    <ol className="flex flex-col gap-1.5 text-sm">
      {result.hops.map((hop, i) => (
        <li key={`${hop.ref ?? hop.kind}-${String(i)}`} className="flex items-start gap-2">
          <span className="mt-0.5 shrink-0 rounded bg-slate-200 px-1.5 py-0.5 text-[10px] font-medium text-slate-600 dark:bg-slate-800 dark:text-slate-300">
            {i + 1}
          </span>
          <div>
            <span className="font-medium text-slate-800 dark:text-slate-100">{hop.label}</span>
            <span className="ml-2 text-xs text-slate-400">{hop.kind}</span>
            {hop.node && <span className="ml-1 text-xs text-slate-400">· {hop.node}</span>}
            {hop.detail && <p className="text-xs text-slate-500 dark:text-slate-400">{hop.detail}</p>}
          </div>
        </li>
      ))}
    </ol>
  );
}

function BlockingRuleCard({ result }: { result: SimulateResult }) {
  const rule = result.blockingRule;
  if (!rule) return null;
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-red-300 bg-red-50/60 p-3 dark:border-red-800 dark:bg-red-950/40">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold text-red-900 dark:text-red-100">Blocking rule</h3>
        <Link
          to={blockingRuleDeepLinkPath(rule)}
          className="rounded bg-red-600 px-2.5 py-1 text-xs font-medium text-white hover:bg-red-500"
        >
          Open in firewall editor
        </Link>
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        <dt className="text-slate-500 dark:text-slate-400">Enforcement point</dt>
        <dd>{rule.enforcementPoint === "source-guest-out" ? "Source guest, outbound" : "Destination guest, inbound"}</dd>
        <dt className="text-slate-500 dark:text-slate-400">Origin</dt>
        <dd>
          {rule.origin}
          {rule.groupName ? `: ${rule.groupName}` : ""} (position {rule.pos})
        </dd>
        <dt className="text-slate-500 dark:text-slate-400">Action</dt>
        <dd className="font-mono uppercase">{rule.action}</dd>
        <dt className="text-slate-500 dark:text-slate-400">Match</dt>
        <dd>{ruleMatchLabel(rule.rule)}</dd>
        {rule.rule.comment && (
          <>
            <dt className="text-slate-500 dark:text-slate-400">Comment</dt>
            <dd>{rule.rule.comment}</dd>
          </>
        )}
      </dl>
    </div>
  );
}

function MissingLinkCard({ result }: { result: SimulateResult }) {
  const missing = result.missing;
  if (!missing) return null;
  return (
    <div className="flex flex-col gap-1 rounded-lg border border-amber-300 bg-amber-50/60 p-3 dark:border-amber-800 dark:bg-amber-950/40">
      <h3 className="text-sm font-semibold text-amber-900 dark:text-amber-100">Missing link</h3>
      <p className="text-sm text-amber-900 dark:text-amber-100">{missing.message}</p>
      {(missing.atNode !== undefined || missing.atRef !== undefined) && (
        <p className="text-xs text-amber-700 dark:text-amber-300">
          {missing.atNode && <>Node: {missing.atNode}. </>}
          {missing.atRef && <>Ref: {missing.atRef}</>}
        </p>
      )}
    </div>
  );
}

/** Every caveat, always rendered — never collapsed by default (T-504 AC3:
 * "Caveats always visible when present"). No accordion/"show more" control
 * exists here on purpose. */
function CaveatList({ result }: { result: SimulateResult }) {
  const caveats = sortCaveats(result.caveats);
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-sm font-semibold text-slate-700 dark:text-slate-200">Caveats ({caveats.length})</h3>
      <ul className="flex flex-col gap-1.5">
        {caveats.map((c, i) => (
          <li
            key={`${c.code}-${String(i)}`}
            className={clsx("rounded border px-2.5 py-1.5 text-xs", CAVEAT_SEVERITY_CLASS[c.severity])}
          >
            <span className="mr-1.5 rounded bg-black/10 px-1 py-0.5 text-[10px] font-semibold uppercase dark:bg-white/10">
              {CAVEAT_SEVERITY_LABEL[c.severity]}
            </span>
            <span className="font-mono text-[10px] text-slate-500 dark:text-slate-400">{c.code}</span>
            <p className="mt-0.5">{c.message}</p>
            {c.feature && <p className="mt-0.5 text-slate-500 dark:text-slate-400">Feature: {c.feature}</p>}
          </li>
        ))}
      </ul>
    </div>
  );
}

export function ResultPanel({ result }: { result: SimulateResult }) {
  return (
    <div className="flex flex-col gap-4">
      <VerdictBanner result={result} />

      <div className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
        <div>
          <h3 className="mb-1 text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">Source</h3>
          <p>{endpointSummary(result.src)}</p>
        </div>
        <div>
          <h3 className="mb-1 text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
            Destination
          </h3>
          <p>{endpointSummary(result.dst)}</p>
        </div>
      </div>

      <BlockingRuleCard result={result} />
      <MissingLinkCard result={result} />

      <div>
        <h3 className="mb-1.5 text-sm font-semibold text-slate-700 dark:text-slate-200">Path</h3>
        <HopList result={result} />
      </div>

      <CaveatList result={result} />
    </div>
  );
}
