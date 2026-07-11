// Read-only per-scope rule table (docs/features/firewall.md §2's rule
// editor's table, minus the write affordances T-502 adds: drag-to-reorder,
// inline enable/disable, the builder row). Renders a rule's macro
// expansion preview inline when present (acceptance criterion 4).
//
// `focusPos` is T-504's deep-link consuming side: a simulator deny
// verdict's blocking rule (or T-505's firewall log correlation) can name a
// `guest`-origin rule's position to land on and highlight — see
// FirewallPage.tsx's `focusRule` parsing (web/src/firewall/focusRule.ts).
import { useEffect, useRef } from "react";
import clsx from "clsx";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { RuleView } from "../api/types";
import { scrollIntoViewIfSupported } from "../lib/scrollIntoView";
import { macroExpansionLabel, ruleMatchLabel } from "./format";

export function RuleTable({ rules, focusPos }: { rules: RuleView[]; focusPos?: number }) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (focusPos === undefined) return;
    scrollIntoViewIfSupported(containerRef.current?.querySelector('[data-focused="true"]'), { block: "center" });
  }, [focusPos, rules]);

  if (rules.length === 0) {
    return <EmptyState title="No rules" description="This ruleset has no rules configured at this scope." />;
  }
  return (
    <div ref={containerRef}>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Pos</TableHead>
            <TableHead>Enabled</TableHead>
            <TableHead>Direction</TableHead>
            <TableHead>Action</TableHead>
            <TableHead>Match</TableHead>
            <TableHead>Comment</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rules.map((r) => {
            const focused = r.pos === focusPos;
            return (
              <TableRow
                key={r.pos}
                data-focused={focused ? "true" : undefined}
                className={clsx(
                  !r.enabled && "opacity-50",
                  focused && "bg-amber-100 ring-2 ring-inset ring-amber-500 dark:bg-amber-900/40",
                )}
              >
                <TableCell>{r.pos}</TableCell>
                <TableCell>{r.enabled ? "on" : "off"}</TableCell>
                <TableCell className="uppercase">{r.direction}</TableCell>
                <TableCell className="font-mono">{r.action}</TableCell>
                <TableCell>
                  <div className="flex flex-col gap-0.5">
                    <span>{ruleMatchLabel(r)}</span>
                    {r.macro && (
                      <span className="text-xs text-slate-500 dark:text-slate-400">
                        {macroExpansionLabel(r.macro, r.macroExpansion)}
                      </span>
                    )}
                  </div>
                </TableCell>
                <TableCell>{r.comment ?? "—"}</TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
