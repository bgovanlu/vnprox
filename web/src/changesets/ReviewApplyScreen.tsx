// The review screen (docs/features/change-management.md §3: "three tabs:
// Summary, File diff, Plan. Nothing applies until the user has seen this
// screen."). Built on the modal <Drawer/> primitive (side="right", a full
// height sheet) rather than the persistent bottom-right drawer panel — this
// screen genuinely should block interaction with the rest of the app while
// open, matching a Dialog's semantics.
import { useEffect, useMemo, useState } from "react";
import * as RadixTabs from "@radix-ui/react-tabs";
import { Button } from "../components/Button";
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from "../components/Drawer";
import { useToast } from "../components/Toast";
import type { Changeset } from "../api/types";
import { canApply } from "./drawerMachine";
import { FixButton } from "./FixButton";
import { opKindLabel, refNode, summarizeOp } from "./opSummary";
import { buildPlanPreview } from "./planPreview";
import { useChangesetDiffQuery, useValidateChangesetMutation, useApplyChangesetMutation } from "./queries";
import { useChangesetDrawerStore } from "./store";

export interface ReviewApplyScreenProps {
  changeset: Changeset;
  onClose: () => void;
}

const tabTriggerClass =
  "rounded-t px-3 py-1.5 text-xs font-medium text-slate-500 data-[state=active]:border-b-2 data-[state=active]:border-accent-600 data-[state=active]:text-accent-700 dark:text-slate-400 dark:data-[state=active]:text-accent-400";

const DEFAULT_CONFIRM_TIMEOUT_SEC = 120;

export function ReviewApplyScreen({ changeset, onClose }: ReviewApplyScreenProps) {
  const { data: diff, isLoading: diffLoading } = useChangesetDiffQuery(changeset.id, true);
  const validateMutation = useValidateChangesetMutation();
  const applyMutation = useApplyChangesetMutation();
  const warningsAcknowledged = useChangesetDrawerStore((s) => s.warningsAcknowledged);
  const setWarningsAcknowledged = useChangesetDrawerStore((s) => s.setWarningsAcknowledged);
  const { toast } = useToast();
  const [confirmTimeoutSec, setConfirmTimeoutSec] = useState(DEFAULT_CONFIRM_TIMEOUT_SEC);

  // Re-run validation on open ("Runs on every draft change and again
  // immediately before apply — state may have moved", docs/features/
  // change-management.md §2) — but only once per mount, not on every
  // re-render.
  useEffect(() => {
    validateMutation.mutate(changeset.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- run once per changeset id
  }, [changeset.id]);

  const errors = changeset.findings.filter((f) => f.severity === "error");
  const warnings = changeset.findings.filter((f) => f.severity === "warning");
  const applyEnabled = canApply(changeset, warningsAcknowledged) && !applyMutation.isPending;

  // Pre-apply the server hasn't built a plan yet (plan_json is written at
  // apply time) — show the client-side preview mirroring BuildPlan so the
  // user still sees the exact ordered steps before clicking Apply
  // (docs/features/change-management.md §3). Post-apply the persisted
  // server plan wins.
  const planPreview = useMemo(() => buildPlanPreview(changeset.ops), [changeset.ops]);
  const planSteps = changeset.plan && changeset.plan.steps.length > 0 ? changeset.plan.steps : planPreview.plan.steps;
  const planIsPreview = !(changeset.plan && changeset.plan.steps.length > 0);

  async function handleApply(): Promise<void> {
    try {
      await applyMutation.mutateAsync({ id: changeset.id, confirmTimeoutSec });
      onClose();
    } catch {
      toast({ title: "Apply failed to start", description: "See the drawer for details.", variant: "error" });
    }
  }

  return (
    <Drawer open onOpenChange={(open) => { if (!open) onClose(); }}>
      <DrawerContent side="right" aria-describedby="review-apply-description" className="max-w-2xl">
        <DrawerTitle>Review &amp; apply — {changeset.title || "Untitled draft"}</DrawerTitle>
        <DrawerDescription id="review-apply-description">
          {changeset.ops.length} operation{changeset.ops.length === 1 ? "" : "s"} across{" "}
          {new Set(changeset.ops.map((o) => (o.target ? refNode(o.target) : "cluster"))).size} node(s).
        </DrawerDescription>

        {errors.length > 0 && (
          <div className="mt-3 rounded-md border border-red-300 bg-red-50 p-2 text-xs text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200">
            <p className="font-medium">Blocking errors — apply is disabled until these are resolved:</p>
            <ul className="mt-1 space-y-1">
              {errors.map((f, i) => (
                <li key={i}>
                  {f.message}
                  {f.fix && <FixButton changeset={changeset} fix={f.fix} />}
                </li>
              ))}
            </ul>
          </div>
        )}

        <RadixTabs.Root defaultValue="summary" className="mt-4 flex flex-1 flex-col">
          <RadixTabs.List className="flex gap-1 border-b border-slate-200 dark:border-slate-700">
            <RadixTabs.Trigger value="summary" className={tabTriggerClass}>
              Summary
            </RadixTabs.Trigger>
            <RadixTabs.Trigger value="filediff" className={tabTriggerClass}>
              File diff
            </RadixTabs.Trigger>
            <RadixTabs.Trigger value="plan" className={tabTriggerClass}>
              Plan
            </RadixTabs.Trigger>
          </RadixTabs.List>

          <RadixTabs.Content value="summary" className="mt-3 flex-1 overflow-y-auto">
            <ul className="space-y-2 text-sm">
              {changeset.ops.map((op, i) => (
                <li key={i} className="rounded border border-slate-200 p-2 dark:border-slate-700">
                  <span className="mr-1.5 rounded bg-slate-200/70 px-1 py-0.5 text-[10px] uppercase text-slate-500 dark:bg-slate-700/70 dark:text-slate-300">
                    {opKindLabel(op)}
                  </span>
                  {summarizeOp(op)}
                </li>
              ))}
              {changeset.ops.length === 0 && <li className="text-slate-400">No operations.</li>}
            </ul>
          </RadixTabs.Content>

          <RadixTabs.Content value="filediff" className="mt-3 flex-1 overflow-y-auto">
            {diffLoading && <p className="text-xs text-slate-400">Loading diff…</p>}
            {diff && diff.files.length === 0 && (
              <p className="text-xs text-slate-400">
                No node interfaces-file changes — this changeset only touches ops without a file representation yet
                (e.g. SDN/firewall/guest-NIC ops).
              </p>
            )}
            {diff && (
              <div className="space-y-3">
                {diff.files.map((f) => (
                  <section key={`${f.node}:${f.path}`}>
                    <h3 className="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">
                      {f.node}: {f.path} {!f.changed && "(unchanged)"}
                    </h3>
                    <pre className="overflow-x-auto rounded border border-slate-200 bg-slate-50 p-2 font-mono text-[11px] leading-snug text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200">
                      {f.unified || "(no diff)"}
                    </pre>
                  </section>
                ))}
              </div>
            )}
          </RadixTabs.Content>

          <RadixTabs.Content value="plan" className="mt-3 flex-1 overflow-y-auto">
            {planIsPreview && planSteps.length > 0 && (
              <p className="mb-2 text-xs text-slate-400">
                Preview — the authoritative plan is finalized (and persisted) at apply time; these are the steps it
                will contain for the ops as they stand.
              </p>
            )}
            {planIsPreview && planPreview.unsupportedOps.length > 0 && (
              <p className="mb-2 rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
                Not yet executable by the apply engine: {planPreview.unsupportedOps.join(", ")}. Applying a changeset
                containing these ops will be refused (they can be drafted and validated, but their execution lands in
                a later release).
              </p>
            )}
            {planSteps.length > 0 ? (
              <ol className="space-y-1.5 text-sm">
                {planSteps.map((s, i) => (
                  <li key={i} className="rounded border border-slate-200 p-2 dark:border-slate-700">
                    <span className="mr-1.5 rounded bg-sky-100 px-1 py-0.5 text-[10px] uppercase text-sky-700 dark:bg-sky-950 dark:text-sky-300">
                      {s.kind}
                    </span>
                    {s.node && <span className="mr-1 text-xs text-slate-400">[{s.node}]</span>}
                    {s.summary}
                  </li>
                ))}
              </ol>
            ) : (
              <p className="text-xs text-slate-400">No executable steps for the current op list.</p>
            )}
          </RadixTabs.Content>
        </RadixTabs.Root>

        {warnings.length > 0 && (
          <div className="mt-3 rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
            <ul className="space-y-1">
              {warnings.map((f, i) => (
                <li key={i}>{f.message}</li>
              ))}
            </ul>
            <label className="mt-2 flex items-center gap-2">
              <input
                type="checkbox"
                checked={warningsAcknowledged}
                onChange={(e) => {
                  setWarningsAcknowledged(e.target.checked);
                }}
              />
              Apply with warnings
            </label>
          </div>
        )}

        <div className="mt-4 flex items-center gap-2">
          <label className="flex items-center gap-1.5 text-xs text-slate-500 dark:text-slate-400">
            Confirm window (s)
            <input
              type="number"
              min={30}
              max={600}
              value={confirmTimeoutSec}
              onChange={(e) => {
                setConfirmTimeoutSec(Number(e.target.value));
              }}
              className="w-16 rounded border border-slate-300 px-1.5 py-0.5 text-xs dark:border-slate-700 dark:bg-slate-800"
            />
          </label>
          <div className="ml-auto flex gap-2">
            <Button variant="ghost" size="sm" onClick={onClose}>
              Back to drafting
            </Button>
            <Button
              variant="primary"
              size="sm"
              disabled={!applyEnabled}
              onClick={() => void handleApply()}
            >
              Apply
            </Button>
          </div>
        </div>
      </DrawerContent>
    </Drawer>
  );
}

