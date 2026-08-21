// Firewall log analytics tab (T-1006, docs/features/firewall.md §4): a
// hit-count table, top-blocked source/destination bar charts (recharts,
// per docs/development.md's approved chart library), and an unused-rule
// list — all read from GET /firewall/analytics, closing the loop between
// firewall editing and reality. Every rule identity renders through the
// same "edit rule" deep link T-505's log viewer and T-504's simulator
// already established (ruleDeepLinkPath — guestRef+pos+origin, never DOM
// position), landing on the exact rule row in the editor (FirewallPage.tsx
// / ResolvedViewTable.tsx's focus highlight).
import { useState } from "react";
import { Link } from "react-router-dom";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { FwLogRuleRef, FwRuleHitCount, FwTopBlocked, FwUnusedRule } from "../api/types";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { OriginBadge } from "../firewall/ResolvedViewTable";
import { ruleDeepLinkPath } from "./deeplink";
import { useFwAnalyticsQuery } from "./queries";

const WINDOW_OPTIONS: { hours: number; label: string }[] = [
  { hours: 1, label: "1 hour" },
  { hours: 6, label: "6 hours" },
  { hours: 24, label: "24 hours" },
  { hours: 24 * 7, label: "7 days" },
  { hours: 24 * 30, label: "30 days" },
];

function RuleCell({ rule }: { rule: FwLogRuleRef }) {
  return (
    <Link to={ruleDeepLinkPath(rule)} className="flex items-center gap-1.5 hover:underline">
      <OriginBadge origin={rule.origin} groupName={rule.groupName} />
      <span className="font-mono text-xs text-slate-500 dark:text-slate-400">pos {rule.pos}</span>
    </Link>
  );
}

function formatLastSeen(unixSeconds: number | undefined): string {
  if (!unixSeconds) {
    return "—";
  }
  return new Date(unixSeconds * 1000).toLocaleString();
}

function HitCountsTable({ hitCounts }: { hitCounts: FwRuleHitCount[] }) {
  if (hitCounts.length === 0) {
    return <EmptyState title="No rule hits in this window" description="Nothing correlated to a specific rule in the selected window." density="compact" />;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Rule</TableHead>
          <TableHead>Guest</TableHead>
          <TableHead>Hits</TableHead>
          <TableHead>Last seen</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {hitCounts.map((h) => (
          <TableRow key={`${h.rule.guestRef}|${h.rule.origin}|${h.rule.groupName ?? ""}|${String(h.rule.pos)}`}>
            <TableCell>
              <RuleCell rule={h.rule} />
            </TableCell>
            <TableCell className="font-mono text-xs">{h.rule.guestRef}</TableCell>
            <TableCell>{h.hits}</TableCell>
            <TableCell className="whitespace-nowrap text-xs">{formatLastSeen(h.lastSeenAt)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function TopBlockedChart({ title, data }: { title: string; data: { value: number; label: string }[] }) {
  if (data.length === 0) {
    return (
      <div>
        <h3 className="mb-1 text-sm font-semibold">{title}</h3>
        <p className="text-xs text-slate-600 dark:text-slate-400">No DROP/REJECT lines in this window.</p>
      </div>
    );
  }
  return (
    <div>
      <h3 className="mb-1 text-sm font-semibold">{title}</h3>
      <div className="h-48" data-testid={`analytics-chart-${title.toLowerCase().replace(/\s+/g, "-")}`}>
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} layout="vertical" margin={{ left: 8, right: 16 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="currentColor" className="text-slate-200 dark:text-slate-700" horizontal={false} />
            <XAxis type="number" tick={{ fontSize: 9 }} allowDecimals={false} />
            <YAxis type="category" dataKey="label" tick={{ fontSize: 10 }} width={110} />
            <Tooltip />
            <Bar dataKey="value" name="Blocked" fill="#f43f5e" radius={[0, 4, 4, 0]} />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}

function UnusedRulesList({ unusedRules }: { unusedRules: FwUnusedRule[] }) {
  if (unusedRules.length === 0) {
    return <EmptyState title="No unused rules" description="Every enabled rule reachable in a known guest's evaluation order has matched something within the window." density="compact" />;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Rule</TableHead>
          <TableHead>Guest</TableHead>
          <TableHead>Last hit</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {unusedRules.map((u) => (
          <TableRow key={`${u.rule.guestRef}|${u.rule.origin}|${u.rule.groupName ?? ""}|${String(u.rule.pos)}`}>
            <TableCell>
              <RuleCell rule={u.rule} />
            </TableCell>
            <TableCell className="font-mono text-xs">{u.rule.guestRef}</TableCell>
            <TableCell className="text-xs">
              {u.daysSinceLastHit < 0 ? "no observed hit in the retained log" : `${String(u.daysSinceLastHit)} days ago`}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function toChartData(entries: FwTopBlocked["sources"]): { value: number; label: string }[] {
  // recharts' vertical-layout BarChart renders top-to-bottom in array
  // order, so the highest count should come LAST to read top-to-bottom as
  // "most blocked at the top" — reverse the already-descending API order.
  return entries.map((e) => ({ label: e.value, value: e.count })).reverse();
}

export function AnalyticsTab() {
  const [windowHours, setWindowHours] = useState(24);
  const { data, isLoading, error } = useFwAnalyticsQuery({ windowHours });

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm text-slate-500 dark:text-slate-400">
          Per-rule hit counts, top blocked sources/destinations, and rules that haven&apos;t matched anything — aggregated
          over the same log buffer above.
        </p>
        <label className="flex items-center gap-2 text-sm">
          <span className="text-slate-500 dark:text-slate-400">Window</span>
          <select
            aria-label="Analytics window"
            value={windowHours}
            onChange={(e) => { setWindowHours(Number(e.target.value)); }}
            className="rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
          >
            {WINDOW_OPTIONS.map((o) => (
              <option key={o.hours} value={o.hours}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
      </div>

      {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading analytics…</p>}
      {error && <EmptyState title="Could not load firewall analytics" description="Try again in a moment." />}

      {data && (
        <>
          <div>
            <h3 className="mb-1 text-sm font-semibold">Rule hit counts</h3>
            <HitCountsTable hitCounts={data.hitCounts} />
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <TopBlockedChart title="Top blocked sources" data={toChartData(data.topBlocked.sources)} />
            <TopBlockedChart title="Top blocked destinations" data={toChartData(data.topBlocked.destinations)} />
          </div>

          <div>
            <h3 className="mb-1 text-sm font-semibold">Unused rules</h3>
            <UnusedRulesList unusedRules={data.unusedRules} />
          </div>
        </>
      )}
    </div>
  );
}
