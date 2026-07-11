// Object usage tracking (docs/features/firewall.md §2: "this alias is
// referenced by 9 rules — view") and the built-in macro catalog with
// expansion previews (acceptance criterion 4). Pure/presentational; usage
// counting itself is internal/fw.UsageCounts (server-side).
import { useState } from "react";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { FirewallObjectsResponse, ObjectUsageView } from "../api/types";
import { macroExpansionLabel } from "./format";

function UsageTable({ items }: { items: ObjectUsageView[] }) {
  const [expanded, setExpanded] = useState<string | undefined>(undefined);
  if (items.length === 0) {
    return <EmptyState title="None defined" description="No objects of this kind are configured anywhere in the cluster." />;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Scope</TableHead>
          <TableHead>Comment</TableHead>
          <TableHead>Referenced by</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((item) => {
          const key = `${item.kind}/${item.scope}/${item.name}`;
          const isExpanded = expanded === key;
          return (
            <TableRow key={key}>
              <TableCell className="font-mono">{item.name}</TableCell>
              <TableCell>{item.scope}</TableCell>
              <TableCell>{item.comment ?? "—"}</TableCell>
              <TableCell>
                {item.count === 0 ? (
                  <span className="text-slate-400">not referenced</span>
                ) : (
                  <button
                    type="button"
                    className="rounded px-1.5 py-0.5 text-accent-700 hover:underline dark:text-accent-400"
                    onClick={() => { setExpanded(isExpanded ? undefined : key); }}
                  >
                    referenced by {item.count} rule{item.count === 1 ? "" : "s"} — {isExpanded ? "hide" : "view"}
                  </button>
                )}
                {isExpanded && item.referencedBy && item.referencedBy.length > 0 && (
                  <ul className="mt-1 flex flex-col gap-0.5 text-xs text-slate-500 dark:text-slate-400">
                    {item.referencedBy.map((rr, i) => (
                      <li key={`${rr.ref}-${String(rr.pos)}-${String(i)}`}>
                        {rr.scope} · {rr.ref} · pos {rr.pos}
                      </li>
                    ))}
                  </ul>
                )}
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}

function MacroCatalog({ macros }: { macros: FirewallObjectsResponse["macros"] }) {
  if (macros.length === 0) {
    return null;
  }
  return (
    <div className="flex flex-col gap-2">
      <h3 className="text-sm font-semibold">Macro catalog</h3>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Macro</TableHead>
            <TableHead>Expansion</TableHead>
            <TableHead>Comment</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {macros.map((m) => (
            <TableRow key={m.name}>
              <TableCell className="font-mono">{m.name}</TableCell>
              <TableCell>{macroExpansionLabel(m.name, m.ports)}</TableCell>
              <TableCell>{m.comment ?? "—"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

export function ObjectsPanel({ objects }: { objects: FirewallObjectsResponse }) {
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">Aliases</h3>
        <UsageTable items={objects.aliases} />
      </div>
      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">IPSets</h3>
        <UsageTable items={objects.ipsets} />
      </div>
      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">Security groups</h3>
        <UsageTable items={objects.groups} />
      </div>
      <MacroCatalog macros={objects.macros} />
    </div>
  );
}
