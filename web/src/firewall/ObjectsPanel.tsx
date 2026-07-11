// Object usage tracking (docs/features/firewall.md §2: "this alias is
// referenced by 9 rules — view"), the usage-guarded delete (acceptance
// criterion 2: deleting a referenced object is blocked, with the
// reference list rendered and deep-links to each referencing rule's own
// editor tab), and the built-in macro catalog with expansion previews
// (acceptance criterion 4).
import { useState } from "react";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { useToast } from "../components/Toast";
import { useDrawerActions } from "../changesets/useDrawerActions";
import type { FirewallObjectsResponse, ObjectUsageView } from "../api/types";
import { macroExpansionLabel } from "./format";
import { buildFwAliasDeleteOp, buildFwGroupDeleteOp, buildFwIpsetDeleteOp } from "./opBuilders";
import { locateFwRulesetRef, type FwRulesetLocation } from "./refs";

const CLUSTER_TARGET = "fw-ruleset::cluster";

function buildDeleteOp(kind: ObjectUsageView["kind"], name: string) {
  switch (kind) {
    case "alias":
      return buildFwAliasDeleteOp(CLUSTER_TARGET, name);
    case "ipset":
      return buildFwIpsetDeleteOp(CLUSTER_TARGET, name);
    case "group":
      return buildFwGroupDeleteOp(CLUSTER_TARGET, name);
    default:
      return undefined;
  }
}

interface UsageTableProps {
  items: ObjectUsageView[];
  onNavigate?: (loc: FwRulesetLocation, pos: number) => void;
}

function UsageTable({ items, onNavigate }: UsageTableProps) {
  const [expanded, setExpanded] = useState<string | undefined>(undefined);
  const { addOps } = useDrawerActions();
  const { toast } = useToast();

  if (items.length === 0) {
    return <EmptyState title="None defined" description="No objects of this kind are configured anywhere in the cluster." />;
  }

  function handleDelete(item: ObjectUsageView): void {
    if (item.count > 0) {
      toast({
        title: `${item.name} is referenced by ${String(item.count)} rule${item.count === 1 ? "" : "s"} and cannot be deleted`,
        description: "Remove those references first (see the reference list below).",
        variant: "error",
      });
      return;
    }
    const op = buildDeleteOp(item.kind, item.name);
    if (!op) return;
    void addOps([op], `Delete ${item.kind} ${item.name}`)
      .then(() => { toast({ title: `${item.name} staged for deletion`, variant: "success" }); })
      .catch((err: unknown) => { toast({ title: err instanceof Error ? err.message : "Failed to stage delete", variant: "error" }); });
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Scope</TableHead>
          <TableHead>Comment</TableHead>
          <TableHead>Referenced by</TableHead>
          <TableHead />
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
                        {onNavigate ? (
                          <button
                            type="button"
                            className="text-accent-700 hover:underline dark:text-accent-400"
                            onClick={() => {
                              const loc = locateFwRulesetRef(rr.ref);
                              if (loc) onNavigate(loc, rr.pos);
                            }}
                          >
                            {rr.scope} · {rr.ref} · pos {rr.pos}
                          </button>
                        ) : (
                          <span>{rr.scope} · {rr.ref} · pos {rr.pos}</span>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
              </TableCell>
              <TableCell>
                {item.scope === "cluster" ? (
                  <button
                    type="button"
                    onClick={() => { handleDelete(item); }}
                    className="text-xs text-red-600 hover:underline disabled:cursor-not-allowed disabled:text-slate-300 dark:text-red-400"
                    title={item.count > 0 ? `Referenced by ${String(item.count)} rule(s) — cannot delete` : undefined}
                  >
                    Delete
                  </button>
                ) : (
                  <span className="text-xs text-slate-300 dark:text-slate-600" title="Delete node/guest-scope objects from that scope's own rule table">—</span>
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

export interface ObjectsPanelProps {
  objects: FirewallObjectsResponse;
  /** Deep-link handler: navigate the hierarchy nav to the scope/position a
   * "referenced by" entry names (acceptance criterion 2). Omit to render
   * the reference list as plain text (e.g. in isolation/tests). */
  onNavigate?: (loc: FwRulesetLocation, pos: number) => void;
}

export function ObjectsPanel({ objects, onNavigate }: ObjectsPanelProps) {
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">Aliases</h3>
        <UsageTable items={objects.aliases} onNavigate={onNavigate} />
      </div>
      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">IPSets</h3>
        <UsageTable items={objects.ipsets} onNavigate={onNavigate} />
      </div>
      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">Security groups</h3>
        <UsageTable items={objects.groups} onNavigate={onNavigate} />
      </div>
      <MacroCatalog macros={objects.macros} />
    </div>
  );
}
