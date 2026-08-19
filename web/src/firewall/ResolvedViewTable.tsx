// The resolved view (docs/features/firewall.md §1): a guest's full
// effective evaluation order — cluster rules, spliced-in security-group
// rules, the guest's own rules, then the two directions' fallthrough
// default policies — each entry labeled with its origin. Pure/
// presentational over internal/fw's ResolvedView (via internal/api's JSON
// shape); all order/origin computation happens server-side.
//
// `focusRule` is T-504's deep-link consuming side (see RuleTable.tsx's
// identical `focusPos` doc comment and FirewallPage.tsx's parsing): a
// `cluster`/`group`-origin blocking rule only ever appears here (the raw
// per-scope RuleTable can't show it — it's not one of the guest's own
// rules), so this table needs the full `{pos, origin, groupName?}` triple
// to disambiguate, not just a position.
import { useEffect, useRef } from "react";
import clsx from "clsx";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { DefaultPolicyView, FwOrigin, ResolvedRuleView, ResolvedView } from "../api/types";
import { scrollIntoViewIfSupported } from "../lib/scrollIntoView";
import { ruleMatchLabel } from "./format";
import type { FocusRule } from "./focusRule";
import { matchesFocus } from "./focusRule";

const ORIGIN_LABEL: Record<FwOrigin, string> = {
  cluster: "Cluster",
  group: "Security group",
  guest: "Guest",
  default: "Default policy",
};

const ORIGIN_BADGE_CLASS: Record<FwOrigin, string> = {
  cluster: "bg-sky-50 text-sky-700 dark:bg-sky-950/40 dark:text-sky-300",
  group: "bg-violet-50 text-violet-700 dark:bg-violet-950/40 dark:text-violet-300",
  guest: "bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300",
  default: "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300",
};

export function OriginBadge({ origin, groupName }: { origin: FwOrigin; groupName?: string }) {
  return (
    <span className={clsx("rounded px-1.5 py-0.5 text-xs font-medium", ORIGIN_BADGE_CLASS[origin])}>
      {groupName ? `${ORIGIN_LABEL[origin]}: ${groupName}` : ORIGIN_LABEL[origin]}
    </span>
  );
}

function ResolvedRuleRow({ entry, focused }: { entry: ResolvedRuleView; focused: boolean }) {
  return (
    <TableRow
      data-focused={focused ? "true" : undefined}
      className={clsx(
        !entry.rule.enabled && "opacity-50",
        focused && "bg-amber-100 ring-2 ring-inset ring-amber-500 dark:bg-amber-900/40",
      )}
    >
      <TableCell>{entry.pos}</TableCell>
      <TableCell>
        <OriginBadge origin={entry.origin} groupName={entry.groupName} />
      </TableCell>
      <TableCell className="uppercase">{entry.rule.direction}</TableCell>
      <TableCell className="font-mono">{entry.rule.action}</TableCell>
      <TableCell>{ruleMatchLabel(entry.rule)}</TableCell>
      <TableCell>{entry.rule.comment ?? "—"}</TableCell>
    </TableRow>
  );
}

function DefaultPolicyRow({ policy }: { policy: DefaultPolicyView }) {
  return (
    <TableRow>
      <TableCell>—</TableCell>
      <TableCell>
        <OriginBadge origin="default" />
      </TableCell>
      <TableCell className="uppercase">{policy.direction}</TableCell>
      <TableCell className="font-mono">{policy.policy}</TableCell>
      <TableCell colSpan={2} className="text-xs text-slate-600 dark:text-slate-400">
        Fallthrough — from {ORIGIN_LABEL[policy.origin].toLowerCase()}
      </TableCell>
    </TableRow>
  );
}

export function ResolvedViewTable({ resolved, focusRule }: { resolved: ResolvedView; focusRule?: FocusRule }) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!focusRule) return;
    scrollIntoViewIfSupported(containerRef.current?.querySelector('[data-focused="true"]'), { block: "center" });
  }, [focusRule, resolved]);

  if (resolved.rules.length === 0) {
    return (
      <div className="flex flex-col gap-2">
        <EmptyState
          title="No rules apply"
          description="Neither the cluster nor this guest defines any rules — only the default policy applies."
        />
        <DefaultPoliciesFallback resolved={resolved} />
      </div>
    );
  }
  return (
    <div ref={containerRef}>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Pos</TableHead>
            <TableHead>Origin</TableHead>
            <TableHead>Direction</TableHead>
            <TableHead>Action</TableHead>
            <TableHead>Match</TableHead>
            <TableHead>Comment</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {resolved.rules.map((entry) => (
            <ResolvedRuleRow key={entry.pos} entry={entry} focused={matchesFocus(entry, focusRule)} />
          ))}
          <DefaultPolicyRow policy={resolved.defaultIn} />
          <DefaultPolicyRow policy={resolved.defaultOut} />
        </TableBody>
      </Table>
    </div>
  );
}

function DefaultPoliciesFallback({ resolved }: { resolved: ResolvedView }) {
  return (
    <Table>
      <TableBody>
        <DefaultPolicyRow policy={resolved.defaultIn} />
        <DefaultPolicyRow policy={resolved.defaultOut} />
      </TableBody>
    </Table>
  );
}
