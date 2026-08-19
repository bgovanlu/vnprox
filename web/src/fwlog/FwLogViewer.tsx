// Firewall log viewer (T-505, docs/features/firewall.md §4): a filterable,
// pausable, cluster-wide stream of pve-firewall log lines with honest rule
// correlation and a storm-safe follow mode (server rate cap + client
// render cap, both surfaced as a drop indicator — AC3). T-1006 adds a
// second tab on this same page: read-only analytics (hit counts,
// top-blocked charts, unused-rule report) aggregated over the same log
// buffer — see AnalyticsTab.tsx. "Log" is the default tab so every
// existing test/deep-link that lands on this page unchanged keeps seeing
// exactly what it did before this task.
import { useEffect, useMemo, useReducer, useState } from "react";
import { Link } from "react-router-dom";
import clsx from "clsx";
import type { FwLogEntry } from "../api/types";
import { EmptyState } from "../components/EmptyState";
import { HelpAnchor } from "../help/HelpAnchor";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { AnalyticsTab } from "./AnalyticsTab";
import { ruleDeepLinkPath } from "./deeplink";
import { useFwLogQuery, useFwLogWsBridge } from "./queries";
import {
  RENDER_CAP,
  fwLogReducer,
  initialFwLogViewState,
  selectVisibleEntries,
  type FwLogFilterState,
} from "./reducer";

type FwLogTab = "log" | "analytics";

function FwLogTabButton({ active, label, onClick }: { active: boolean; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={clsx(
        "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
        active
          ? "bg-accent-600/10 text-accent-700 dark:bg-accent-500/15 dark:text-accent-300"
          : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800/60",
      )}
    >
      {label}
    </button>
  );
}

/** Renders a falsy value (0, "", undefined) as an em dash, otherwise the
 * value itself as a string — an explicit if/return rather than `x ? x :
 * y`/`x || y` (which typescript-eslint's prefer-nullish-coalescing flags
 * here regardless of the field's actual optionality, since 0 and "" are
 * meaningful "nothing to show" values for these table cells, not just
 * absent ones — `??` alone wouldn't catch either). */
function displayOrDash(value: string | number | undefined): string {
  if (!value) {
    return "—";
  }
  return String(value);
}

const STATUS_LABEL: Record<FwLogEntry["correlation"]["status"], string> = {
  rule: "Rule",
  default_policy: "Default policy",
  ambiguous: "Ambiguous",
  unmatched: "Unmatched",
  unknown_chain: "Unrecognized",
  no_guest_data: "No data",
};

const STATUS_BADGE_CLASS: Record<FwLogEntry["correlation"]["status"], string> = {
  rule: "bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300",
  default_policy: "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300",
  ambiguous: "bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300",
  unmatched: "bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300",
  unknown_chain: "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400",
  no_guest_data: "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400",
};

function CorrelationCell({ entry }: { entry: FwLogEntry }) {
  const { correlation } = entry;
  const badge = (
    <span className={clsx("rounded px-1.5 py-0.5 text-xs font-medium", STATUS_BADGE_CLASS[correlation.status])} title={correlation.reason}>
      {STATUS_LABEL[correlation.status]}
    </span>
  );
  if (correlation.status === "rule" && correlation.rule) {
    return (
      <Link to={ruleDeepLinkPath(correlation.rule)} className="flex items-center gap-1.5 hover:underline">
        {badge}
        <span className="text-xs text-slate-600 dark:text-slate-400">view rule</span>
      </Link>
    );
  }
  return badge;
}

function FilterBar({
  filter,
  onChange,
}: {
  filter: FwLogFilterState;
  onChange: (patch: Partial<FwLogFilterState>) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <input
        aria-label="Filter by node"
        placeholder="node"
        value={filter.node}
        onChange={(e) => { onChange({ node: e.target.value }); }}
        className="w-28 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      <input
        aria-label="Filter by guest vmid"
        placeholder="vmid"
        value={filter.vmid}
        onChange={(e) => { onChange({ vmid: e.target.value }); }}
        className="w-24 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      <select
        aria-label="Filter by direction"
        value={filter.direction}
        onChange={(e) => { onChange({ direction: e.target.value as FwLogFilterState["direction"] }); }}
        className="rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      >
        <option value="">any direction</option>
        <option value="in">in</option>
        <option value="out">out</option>
      </select>
      <input
        aria-label="Filter by action"
        placeholder="action (e.g. DROP)"
        value={filter.action}
        onChange={(e) => { onChange({ action: e.target.value }); }}
        className="w-40 rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
    </div>
  );
}

export function FwLogViewer() {
  const [tab, setTab] = useState<FwLogTab>("log");
  const [state, dispatch] = useReducer(fwLogReducer, initialFwLogViewState);

  const apiFilter = useMemo(
    () => ({
      node: state.filter.node || undefined,
      vmid: state.filter.vmid && Number.isFinite(Number(state.filter.vmid)) ? Number(state.filter.vmid) : undefined,
      direction: state.filter.direction || undefined,
      action: state.filter.action || undefined,
    }),
    [state.filter.node, state.filter.vmid, state.filter.direction, state.filter.action],
  );

  const { data, isLoading, error } = useFwLogQuery(apiFilter);

  useEffect(() => {
    if (data) {
      dispatch({ type: "loaded", page: data });
    }
  }, [data]);

  useFwLogWsBridge((evt) => {
    dispatch({ type: "batch", entries: evt.entries, droppedTotal: evt.droppedTotal });
  });

  const visible = selectVisibleEntries(state);
  const rendered = visible.slice(-RENDER_CAP);
  const trimmedFromView = visible.length - rendered.length;
  const totalDropped = state.clientDroppedTotal + state.serverDroppedTotal;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="flex items-center gap-2 text-base font-semibold">
            Firewall log
            <HelpAnchor topic="fwlog-viewer" />
          </h2>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Cluster-wide pve-firewall log, live-following via WebSocket. Lines are correlated to the configured rule where
            determinable; ambiguous or unrecognized lines are labeled honestly rather than guessed (real pve-firewall log
            lines do not embed a rule position — see the completion report).
          </p>
        </div>
        <div role="tablist" aria-label="Firewall log views" className="flex items-center gap-1">
          <FwLogTabButton active={tab === "log"} label="Log" onClick={() => { setTab("log"); }} />
          <FwLogTabButton active={tab === "analytics"} label="Analytics" onClick={() => { setTab("analytics"); }} />
        </div>
      </div>

      {tab === "log" && (
        <div role="tabpanel" aria-label="Log" className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <FilterBar filter={state.filter} onChange={(patch) => { dispatch({ type: "setFilter", filter: patch }); }} />
            <div className="flex items-center gap-2">
              {state.paused ? (
                <button
                  type="button"
                  onClick={() => { dispatch({ type: "resume" }); }}
                  className="rounded-md bg-accent-600 px-3 py-1.5 text-sm font-medium text-white"
                >
                  Resume{state.pending.length > 0 ? ` (${String(state.pending.length)} new)` : ""}
                </button>
              ) : (
                <button
                  type="button"
                  onClick={() => { dispatch({ type: "pause" }); }}
                  className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium dark:border-slate-700"
                >
                  Pause
                </button>
              )}
              <button
                type="button"
                onClick={() => { dispatch({ type: "clear" }); }}
                className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium dark:border-slate-700"
              >
                Clear
              </button>
            </div>
          </div>

          {totalDropped > 0 && (
            <p role="status" className="rounded-md bg-amber-50 px-3 py-1.5 text-sm text-amber-800 dark:bg-amber-950/40 dark:text-amber-300">
              {totalDropped.toLocaleString()} lines dropped to keep up with volume (rate cap engaged) — the stream is
              complete again once the storm passes.
            </p>
          )}
          {trimmedFromView > 0 && (
            <p className="text-xs text-slate-600 dark:text-slate-400">
              Showing the most recent {RENDER_CAP.toLocaleString()} of {visible.length.toLocaleString()} matching lines.
            </p>
          )}

          {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading…</p>}
          {error && <EmptyState title="Could not load the firewall log" description="Try again in a moment." />}
          {!isLoading && !error && rendered.length === 0 && (
            <EmptyState title="No log lines yet" description="Nothing matches the current filter, or no traffic has been logged." />
          )}

          {rendered.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Time</TableHead>
                  <TableHead>Node</TableHead>
                  <TableHead>Guest</TableHead>
                  <TableHead>Dir</TableHead>
                  <TableHead>Action</TableHead>
                  <TableHead>Source → Dest</TableHead>
                  <TableHead>Correlation</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rendered.map((e) => (
                  <TableRow key={e.seq}>
                    <TableCell className="whitespace-nowrap font-mono text-xs">
                      {e.at ? new Date(e.at * 1000).toLocaleTimeString() : "—"}
                    </TableCell>
                    <TableCell>{e.node}</TableCell>
                    <TableCell>{displayOrDash(e.vmid)}</TableCell>
                    <TableCell>{displayOrDash(e.direction)}</TableCell>
                    <TableCell className="font-mono text-xs">{displayOrDash(e.action)}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {e.source ?? "?"} → {e.dest ?? "?"}
                    </TableCell>
                    <TableCell>
                      <CorrelationCell entry={e} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      )}

      {tab === "analytics" && (
        <div role="tabpanel" aria-label="Analytics">
          <AnalyticsTab />
        </div>
      )}
    </div>
  );
}
