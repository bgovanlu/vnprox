// Read-only per-scope rule table (docs/features/firewall.md §2's rule
// editor's table, minus the write affordances T-502 adds: drag-to-reorder,
// inline enable/disable, the builder row). Renders a rule's macro
// expansion preview inline when present (acceptance criterion 4).
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { RuleView } from "../api/types";
import { macroExpansionLabel, ruleMatchLabel } from "./format";

export function RuleTable({ rules }: { rules: RuleView[] }) {
  if (rules.length === 0) {
    return <EmptyState title="No rules" description="This ruleset has no rules configured at this scope." />;
  }
  return (
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
        {rules.map((r) => (
          <TableRow key={r.pos} className={r.enabled ? undefined : "opacity-50"}>
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
        ))}
      </TableBody>
    </Table>
  );
}
