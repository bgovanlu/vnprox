// SPDX-License-Identifier: Apache-2.0

// Editable rule table (T-502, docs/features/firewall.md §2): drag-to-
// reorder, inline enable/disable, delete, and a builder row with
// autocomplete + macro picker + expansion preview. Wraps the read-only
// RuleTable's row rendering with write affordances; every edit stages an
// Op via useDrawerActions — nothing here mutates anything directly (the
// change engine owns that after stage → validate → diff → apply →
// confirm).
//
// `focusPos` is T-504's deep-link consuming side (mirrors RuleTable's own
// prop of the same name): a simulator deny verdict's blocking rule (or
// T-505's firewall log correlation) can name a `guest`-origin rule's
// position to land on and highlight — see FirewallPage.tsx's `focusRule`
// parsing (web/src/firewall/focusRule.ts).
import { useEffect, useRef, useState } from "react";
import clsx from "clsx";
import { EmptyState } from "../components/EmptyState";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import { useToast } from "../components/Toast";
import { useSession } from "../api/useSession";
import { hasAnyCap, missingCapTooltip } from "../changesets/capabilities";
import { useDrawerActions } from "../changesets/useDrawerActions";
import type { FirewallObjectsResponse, RuleView } from "../api/types";
import { scrollIntoViewIfSupported } from "../lib/scrollIntoView";
import { validateRuleBuilder, type RuleBuilderFormValues } from "./builderValidation";
import { computeReorderMove } from "./dragReorder";
import { macroExpansionLabel, ruleMatchLabel } from "./format";
import { useFirewallEffectsQuery } from "./queries";
import { buildFwRuleCreateOp, buildFwRuleDeleteOp, buildFwRuleToggleOp, type RuleFormValues } from "./opBuilders";

function emptyBuilderForm(): RuleFormValues {
  return {
    direction: "in", action: "ACCEPT", proto: "", source: "", dest: "", sport: "",
    dport: "", iface: "", macro: "", log: "", comment: "", enabled: true,
  };
}

function EffectsPreview({ group }: { group: string }) {
  const { data, isLoading } = useFirewallEffectsQuery(group);
  if (!group) return null;
  if (isLoading) return <p className="text-xs text-slate-600 dark:text-slate-400">Computing matched guests…</p>;
  if (!data) return null;
  if (data.guests.length === 0) {
    return <p className="text-xs text-amber-600 dark:text-amber-400">Matches no guests yet (group not referenced anywhere reachable).</p>;
  }
  return (
    <p className="text-xs text-slate-600 dark:text-slate-400">
      Matches {data.guests.length} guest{data.guests.length === 1 ? "" : "s"}: {data.guests.join(", ")}
    </p>
  );
}

interface RuleBuilderRowProps {
  target: string;
  nextPos: number;
  objects: FirewallObjectsResponse | undefined;
  fwWriteDisabled: boolean;
  fwWriteTooltip: string | undefined;
}

function RuleBuilderRow({ target, nextPos, objects, fwWriteDisabled, fwWriteTooltip }: RuleBuilderRowProps) {
  const { addOps } = useDrawerActions();
  const { toast } = useToast();
  const [form, setForm] = useState<RuleFormValues>(emptyBuilderForm());

  const knownMacros = new Set((objects?.macros ?? []).map((m) => m.name));
  const aliasNames = (objects?.aliases ?? []).map((a) => a.name);
  const ipsetNames = (objects?.ipsets ?? []).map((s) => `+${s.name}`);
  const groupNames = (objects?.groups ?? []).map((g) => g.name);
  const addressSuggestions = [...aliasNames, ...ipsetNames];

  const validationForm: RuleBuilderFormValues = { direction: form.direction, action: form.action, proto: form.proto, macro: form.macro, dport: form.dport };
  const errors = validateRuleBuilder(validationForm, knownMacros);

  function update<K extends keyof RuleFormValues>(key: K, value: RuleFormValues[K]): void {
    setForm((f) => ({ ...f, [key]: value }));
  }

  function handleAdd(): void {
    if (errors.length > 0) {
      toast({ title: errors[0] ?? "Invalid rule", variant: "error" });
      return;
    }
    const op = buildFwRuleCreateOp(target, nextPos, form);
    void addOps([op], "Add firewall rule").then(() => {
      setForm(emptyBuilderForm());
      toast({ title: "Rule added to draft", variant: "success" });
    }).catch((err: unknown) => {
      toast({ title: err instanceof Error ? err.message : "Failed to add rule", variant: "error" });
    });
  }

  return (
    <div className="flex flex-col gap-2 border-t border-slate-200 p-3 dark:border-slate-800">
      <div className="flex flex-wrap items-center gap-2">
        <select aria-label="Direction" className="rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800" value={form.direction} onChange={(e) => { update("direction", e.target.value); }}>
          <option value="in">in</option>
          <option value="out">out</option>
          <option value="forward">forward</option>
          <option value="group">security group</option>
        </select>
        {form.direction === "group" ? (
          <select aria-label="Security group" className="rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800" value={form.action} onChange={(e) => { update("action", e.target.value); }}>
            <option value="">select a group…</option>
            {groupNames.map((g) => (
              <option key={g} value={g}>{g}</option>
            ))}
          </select>
        ) : (
          <select aria-label="Action" className="rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800" value={form.action} onChange={(e) => { update("action", e.target.value); }}>
            <option value="ACCEPT">ACCEPT</option>
            <option value="DROP">DROP</option>
            <option value="REJECT">REJECT</option>
          </select>
        )}
        <input aria-label="Source" list="fw-addr-suggestions" placeholder="source" className="w-28 rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800" value={form.source} onChange={(e) => { update("source", e.target.value); }} />
        <input aria-label="Dest" list="fw-addr-suggestions" placeholder="dest" className="w-28 rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800" value={form.dest} onChange={(e) => { update("dest", e.target.value); }} />
        <datalist id="fw-addr-suggestions">
          {addressSuggestions.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>
        <input aria-label="Proto" placeholder="proto" className="w-20 rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800" value={form.proto} onChange={(e) => { update("proto", e.target.value); }} />
        <input aria-label="Port" placeholder="dport" className="w-20 rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800" value={form.dport} onChange={(e) => { update("dport", e.target.value); }} />
        <input aria-label="Interface" placeholder="iface" className="w-20 rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800" value={form.iface} onChange={(e) => { update("iface", e.target.value); }} />
        <select aria-label="Macro" className="rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800" value={form.macro} onChange={(e) => { update("macro", e.target.value); }}>
          <option value="">no macro</option>
          {(objects?.macros ?? []).map((m) => (
            <option key={m.name} value={m.name}>{macroExpansionLabel(m.name, m.ports)}</option>
          ))}
        </select>
        <input aria-label="Comment" placeholder="comment" className="w-36 rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800" value={form.comment} onChange={(e) => { update("comment", e.target.value); }} />
        <button
          type="button"
          onClick={handleAdd}
          disabled={errors.length > 0 || fwWriteDisabled}
          title={fwWriteDisabled ? fwWriteTooltip : undefined}
          className="rounded bg-accent-600 px-3 py-1 text-xs font-medium text-white disabled:opacity-40"
        >
          Add rule
        </button>
      </div>
      {form.direction === "group" && form.action && <EffectsPreview group={form.action} />}
      {errors.length > 0 && (
        <ul className="text-xs text-red-600 dark:text-red-400">
          {errors.map((e) => (
            <li key={e}>{e}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

export interface RuleEditorProps {
  rules: RuleView[];
  /** Ruleset scope Ref this table's rules belong to — required to stage
   * any op; the table renders read-only (RuleTable's plain rendering)
   * when omitted. */
  target?: string;
  objects?: FirewallObjectsResponse;
  focusPos?: number;
}

export function RuleEditor({ rules, target, objects, focusPos }: RuleEditorProps) {
  const { addOps, replaceOps } = useDrawerActions();
  const { toast } = useToast();
  const [dragIndex, setDragIndex] = useState<number | undefined>(undefined);
  const containerRef = useRef<HTMLDivElement>(null);
  // fw.rule.* ops are staged with the user's own PVE ticket exactly like
  // every other firewall write (docs/features/firewall.md §3); this table
  // previously had zero capability gating at all (delete, the per-row
  // enable/disable toggle, drag-reorder, and the builder row's "Add rule"
  // were all always-enabled regardless of session privileges) — a real
  // gap the read-only E2E crawl caught (T-607), fixed here the same way
  // SdnPage.tsx's write-trigger buttons and ObjectsPanel.tsx's object
  // delete now are: hasAnyCap(session, "fwWrite"), not
  // capsForNode(session, "").fwWrite — that idiom silently evaluates to
  // NO_CAPS for any real multi-node session (see SdnPage.tsx's
  // useSdnWriteGate doc comment for the full explanation) — the ruleset's
  // own scope/node isn't threaded through RuleEditorProps today, and
  // fwWrite/Sys.Modify is the same PVE privilege regardless of scope.
  const { data: session } = useSession();
  const fwWrite = hasAnyCap(session, "fwWrite");
  const fwWriteDisabled = !fwWrite;
  const fwWriteTooltip = fwWrite ? undefined : missingCapTooltip(session, "", "fwWrite");

  useEffect(() => {
    if (focusPos === undefined) return;
    scrollIntoViewIfSupported(containerRef.current?.querySelector('[data-focused="true"]'), { block: "center" });
  }, [focusPos, rules]);

  function stage(op: Parameters<typeof addOps>[0][number], label: string): void {
    void addOps([op], label).catch((err: unknown) => {
      toast({ title: err instanceof Error ? err.message : "Failed to stage change", variant: "error" });
    });
  }

  function handleToggle(rule: RuleView): void {
    if (!target) return;
    stage(buildFwRuleToggleOp(target, rule.pos, !rule.enabled), "Toggle firewall rule");
  }

  function handleDelete(rule: RuleView): void {
    if (!target) return;
    stage(buildFwRuleDeleteOp(target, rule.pos), "Delete firewall rule");
  }

  function handleDrop(toIndex: number): void {
    if (!target || dragIndex === undefined) return;
    const result = computeReorderMove(target, rules, dragIndex, toIndex);
    setDragIndex(undefined);
    if (!result) return;
    void replaceOps([result.op]).catch(() => {
      // replaceOps requires an active draft; fall back to appending a new
      // draft the same way every other first-edit does.
      stage(result.op, "Reorder firewall rules");
    });
  }

  if (rules.length === 0 && !target) {
    return <EmptyState title="No rules" description="This ruleset has no rules configured at this scope." />;
  }

  return (
    <div ref={containerRef} className="flex flex-col gap-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Pos</TableHead>
            <TableHead>Enabled</TableHead>
            <TableHead>Direction</TableHead>
            <TableHead>Action</TableHead>
            <TableHead>Match</TableHead>
            <TableHead>Comment</TableHead>
            {target && <TableHead />}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rules.map((r, i) => {
            const focused = r.pos === focusPos;
            return (
            <TableRow
              key={r.pos}
              data-focused={focused ? "true" : undefined}
              className={clsx(
                !r.enabled && "opacity-50",
                focused && "bg-amber-100 ring-2 ring-inset ring-amber-500 dark:bg-amber-900/40",
              )}
              draggable={Boolean(target) && !fwWriteDisabled}
              onDragStart={() => { setDragIndex(i); }}
              onDragOver={(e) => { if (target && !fwWriteDisabled) e.preventDefault(); }}
              onDrop={() => { if (!fwWriteDisabled) handleDrop(i); }}
            >
              <TableCell>{r.pos}</TableCell>
              <TableCell>
                {target ? (
                  <input
                    type="checkbox"
                    aria-label={`Enabled: ${r.comment ?? `rule ${String(r.pos)}`}`}
                    checked={r.enabled}
                    disabled={fwWriteDisabled}
                    title={fwWriteDisabled ? fwWriteTooltip : undefined}
                    onChange={() => { handleToggle(r); }}
                  />
                ) : (
                  r.enabled ? "on" : "off"
                )}
              </TableCell>
              <TableCell className="uppercase">{r.direction}</TableCell>
              <TableCell className="font-mono">{r.action}</TableCell>
              <TableCell>
                <div className="flex flex-col gap-0.5">
                  <span>{ruleMatchLabel(r)}</span>
                  {r.macro && (
                    <span className="text-xs text-slate-600 dark:text-slate-400">{macroExpansionLabel(r.macro, r.macroExpansion)}</span>
                  )}
                  {r.direction === "group" && <EffectsPreview group={r.action} />}
                </div>
              </TableCell>
              <TableCell>{r.comment ?? "—"}</TableCell>
              {target && (
                <TableCell>
                  <button
                    type="button"
                    aria-label={`Delete rule ${String(r.pos)}`}
                    onClick={() => { handleDelete(r); }}
                    disabled={fwWriteDisabled}
                    title={fwWriteDisabled ? fwWriteTooltip : undefined}
                    // T-3406-followup-01: WCAG 1.4.11 exempts disabled
                    // controls from the contrast floor, but axe's automated
                    // color-contrast check does not reliably honor that
                    // carve-out (found true here: the control really is
                    // `disabled`, not merely styled to look like it). Rather
                    // than lean on an exemption a real assistive-tech user's
                    // tooling might not apply either, this uses a disabled
                    // shade that clears AA on its own — `cursor-not-allowed`
                    // plus the loss of the hover underline still carries the
                    // disabled affordance visually.
                    className="text-xs text-red-700 hover:underline disabled:cursor-not-allowed disabled:text-slate-600 dark:text-red-400 dark:disabled:text-slate-400"
                  >
                    Delete
                  </button>
                </TableCell>
              )}
            </TableRow>
            );
          })}
        </TableBody>
      </Table>
      {target && (
        <RuleBuilderRow
          target={target}
          nextPos={rules.length}
          objects={objects}
          fwWriteDisabled={fwWriteDisabled}
          fwWriteTooltip={fwWriteTooltip}
        />
      )}
    </div>
  );
}
