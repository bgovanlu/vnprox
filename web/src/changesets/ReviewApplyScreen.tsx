// SPDX-License-Identifier: Apache-2.0

// The review screen (docs/features/change-management.md §3: "four tabs:
// Summary, File diff, Plan, Discussion. Nothing applies until the user has
// seen this screen."). Built on the modal <Drawer/> primitive (side="right",
// a full height sheet) rather than the persistent bottom-right drawer panel
// — this screen genuinely should block interaction with the rest of the app
// while open, matching a Dialog's semantics. T-2003 added the review
// surface (ApprovalPanel, CommentsPanel, the shareable review link) — see
// approvalGate.ts's doc comment for why the Apply-button gating here is a
// client-side echo, never the enforcement itself.
import { useEffect, useMemo, useState } from "react";
import * as RadixTabs from "@radix-ui/react-tabs";
import { Button } from "../components/Button";
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from "../components/Drawer";
import { useToast } from "../components/Toast";
import { ApiError } from "../api/client";
import type { Changeset } from "../api/types";
import { canApply } from "./drawerMachine";
import { FixButton } from "./FixButton";
import { opKindLabel, refNode, summarizeOp } from "./opSummary";
import { buildPlanPreview } from "./planPreview";
import { ImpactPanel } from "./ImpactPanel";
import {
  useAckSdnForeignPendingMutation,
  useChangesetDiffQuery,
  useChangesetImpactQuery,
  useSdnForeignPendingQuery,
  useValidateChangesetMutation,
  useApplyChangesetMutation,
} from "./queries";
import { describeSdnPendingEntry, sdnForeignPendingBlocksApply, sdnPendingSetKey } from "./sdnForeignPendingGate";
import { useChangesetDrawerStore } from "./store";
import { preApplyRevertNotice } from "./revertCoverage";
import { mgmtStrings } from "../mgmt/strings";
import { ApprovalPanel } from "./ApprovalPanel";
import { CommentsPanel } from "./CommentsPanel";
import {
  APPROVAL_REQUIRED_MESSAGE,
  blocksApply,
  twoPersonBlocksApply,
  twoPersonRequiredMessage,
} from "./approvalGate";
import { reviewLinkFor } from "./reviewLink";
import { ApplyStrategyPanel } from "./ApplyStrategyPanel";
import { buildApplyStrategy, canaryEligibility, defaultSelection, selectionError } from "./applyStrategy";
import { PolicyVerdictPanel } from "./PolicyVerdictPanel";
import { ProposePanel } from "./ProposePanel";
import { BreakGlassPanel } from "./BreakGlassPanel";
import { FreezeOverridePanel } from "./FreezeOverridePanel";
import { freezeBlocksApply } from "./freezeOverride";
import { policyFindings, policyVerdict } from "./policyVerdict";
import { useBreakGlassMutation, useFreezeOverrideMutation, usePoliciesQuery, usePolicyVerdictQuery } from "./governanceQueries";

export interface ReviewApplyScreenProps {
  changeset: Changeset;
  onClose: () => void;
}

/** T-703's server-side confirm-window floor (change.MgmtConfirmTimeoutFloor):
 * a management-path changeset's window defaults to, and cannot be set below,
 * 180s. Mirrored client-side so the UI never even offers a lower value; the
 * server rejects a lower one with 400 confirm_window_too_short regardless. */
const MGMT_CONFIRM_FLOOR_SEC = 180;

/** The node a touchesMgmtPath changeset targets — for the typed
 * acknowledgement. Every ops-touching-the-path op carries the node in its
 * target ref; the first non-cluster op's node is the one to type. */
function mgmtNodeOf(changeset: Changeset): string {
  for (const op of changeset.ops) {
    if (!op.target) continue;
    const parts = op.target.split(":");
    if (parts.length === 3 && parts[1]) return parts[1];
  }
  return "";
}

const tabTriggerClass =
  "rounded-t px-3 py-1.5 text-xs font-medium text-fg-subtle data-[state=active]:border-b-2 data-[state=active]:border-accent-600 data-[state=active]:text-accent-700 dark:data-[state=active]:text-accent-400";

const DEFAULT_CONFIRM_TIMEOUT_SEC = 120;

export function ReviewApplyScreen({ changeset, onClose }: ReviewApplyScreenProps) {
  const { data: diff, isLoading: diffLoading } = useChangesetDiffQuery(changeset.id, true);
  // T-2404: the blast-radius preview. Fetched alongside the diff rather than
  // lazily on tab open, so the headline verdict is available before the
  // operator decides which tab to read.
  const { data: impact, isLoading: impactLoading, isError: impactError } = useChangesetImpactQuery(changeset.id, true);
  const validateMutation = useValidateChangesetMutation();
  const applyMutation = useApplyChangesetMutation();
  const warningsAcknowledged = useChangesetDrawerStore((s) => s.warningsAcknowledged);
  const setWarningsAcknowledged = useChangesetDrawerStore((s) => s.setWarningsAcknowledged);
  const { toast } = useToast();

  // T-3101-followup-01: "this apply will also commit ...". Always enabled
  // (like the impact query above) — a changeset with no SDN ops simply gets
  // an empty `entries` back, no PVE round trip on the server side
  // (change.Service.SDNForeignPending's own plan.hasSDN() short-circuit).
  const sdnForeignPendingQuery = useSdnForeignPendingQuery(changeset.id, true);
  const sdnForeignPendingEntries = sdnForeignPendingQuery.data?.entries;
  const ackSdnForeignPending = useAckSdnForeignPendingMutation(changeset.id);
  // The key of whatever set this screen last successfully acknowledged, in
  // THIS session — reset whenever the changeset identity changes so a
  // reopened drawer never inherits a stale ack from a different changeset.
  const [sdnAckedKey, setSdnAckedKey] = useState<string | undefined>(undefined);
  useEffect(() => {
    setSdnAckedKey(undefined);
  }, [changeset.id]);
  const sdnForeignPendingBlocks = sdnForeignPendingBlocksApply(sdnForeignPendingEntries, sdnAckedKey);

  async function handleAckSdnForeignPending(): Promise<void> {
    try {
      const res = await ackSdnForeignPending.mutateAsync();
      setSdnAckedKey(sdnPendingSetKey(res.entries));
    } catch {
      toast({ title: "Could not record acknowledgement", description: "See the drawer for details.", variant: "error" });
    }
  }

  const touchesMgmt = changeset.touchesMgmtPath === true;
  const mgmtNode = touchesMgmt ? mgmtNodeOf(changeset) : "";
  const [confirmTimeoutSec, setConfirmTimeoutSec] = useState(
    touchesMgmt ? MGMT_CONFIRM_FLOOR_SEC : DEFAULT_CONFIRM_TIMEOUT_SEC,
  );
  // The typed node-name acknowledgement (T-703): apply stays disabled until
  // it matches for a management-path changeset.
  const [ackText, setAckText] = useState("");
  const ackSatisfied = !touchesMgmt || ackText.trim() === mgmtNode;
  // T-3005: the apply-strategy picker's own state. Its default is `mode:
  // all` with auto-rollback off, which is exactly today's behaviour — an
  // operator who never opens this panel sends the same request body as
  // before.
  const [strategySelection, setStrategySelection] = useState(defaultSelection);
  const [autoRollbackOnError, setAutoRollbackOnError] = useState(false);

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
  // T-2003: a client-side echo of the server's review-approval gate —
  // disables the button and explains why, but the server (beginApply)
  // re-checks stored approval state on every apply attempt regardless of
  // what this flag says (see approvalGate.ts's own doc comment).
  const approvalBlocks = blocksApply(changeset.approval);
  // T-2604: the same client-side echo for the two-person rule on protected
  // op classes — the server's own two_person_required refusal at apply is
  // what actually enforces it.
  const twoPersonBlocks = twoPersonBlocksApply(changeset.approval);

  // T-3002: what the cluster's INSTALLED policy set says about this exact
  // changeset. The changeset's own findings carry the refusal as prose with
  // no rule id on the wire, so the rule and its assertions are read from the
  // two policy routes and joined here — see policyVerdict.ts.
  const policiesQuery = usePoliciesQuery();
  const policyTestQuery = usePolicyVerdictQuery(changeset.id);
  const verdict = policyVerdict({
    status: policiesQuery.data,
    statusError: policiesQuery.error,
    result: policyTestQuery.data,
    resultError: policyTestQuery.error,
    isLoading: policiesQuery.isLoading || policyTestQuery.isLoading,
  });
  const changesetPolicyFindings = useMemo(() => policyFindings(changeset.findings), [changeset.findings]);

  // T-3002: T-2604's break-glass, which had no caller in web/src. It
  // overrides the distinct-approver count and nothing else, so it is offered
  // only while that is what blocks.
  const breakGlass = useBreakGlassMutation(changeset.id);
  const breakGlassError =
    breakGlass.error instanceof Error && breakGlass.error.message !== "" ? breakGlass.error.message : undefined;

  // T-4006: a declared freeze window's audited override. Unlike break-glass
  // it does not gate an authorization check — it downgrades the freeze
  // rule's blocking finding to a visible warning — so it is offered
  // whenever THAT is what is blocking (freezeBlocksApply), independent of
  // twoPersonBlocks above.
  const freezeBlocked = freezeBlocksApply(changeset.findings, policyTestQuery.data);
  const freezeOverride = useFreezeOverrideMutation(changeset.id);
  const freezeOverrideError =
    freezeOverride.error instanceof Error && freezeOverride.error.message !== "" ? freezeOverride.error.message : undefined;

  // Pre-apply the server hasn't built a plan yet (plan_json is written at
  // apply time) — show the client-side preview mirroring BuildPlan so the
  // user still sees the exact ordered steps before clicking Apply
  // (docs/features/change-management.md §3). Post-apply the persisted
  // server plan wins.
  const planPreview = useMemo(() => buildPlanPreview(changeset.ops), [changeset.ops]);
  const planSteps = changeset.plan && changeset.plan.steps.length > 0 ? changeset.plan.steps : planPreview.plan.steps;
  const planIsPreview = !(changeset.plan && changeset.plan.steps.length > 0);

  // T-3005: an unsendable canary selection disables Apply here rather than
  // being discovered as a 422 after the click. The server refuses it either
  // way (before any snapshot or mutation) — this is only the echo.
  const canaryElig = useMemo(() => canaryEligibility(planSteps), [planSteps]);
  const strategyError = selectionError(strategySelection, canaryElig, confirmTimeoutSec);

  const applyEnabled =
    canApply(changeset, warningsAcknowledged) &&
    ackSatisfied &&
    !approvalBlocks &&
    !twoPersonBlocks &&
    !sdnForeignPendingBlocks &&
    strategyError === undefined &&
    !applyMutation.isPending;

  // T-1805: state plainly, before apply, which parts of this changeset depend
  // on the sealed PVE ticket to undo themselves and for how long — no
  // changeset may imply a safety net it does not have. Recomputed as the
  // operator adjusts the confirm window, since the window is half the promise.
  const revertNotice = useMemo(
    () => preApplyRevertNotice(changeset.ops, confirmTimeoutSec),
    [changeset.ops, confirmTimeoutSec],
  );

  async function handleApply(): Promise<void> {
    // `applyStrategy` is undefined for `mode: all` — omitted from the body
    // entirely, so the default apply path's request is unchanged. Likewise
    // `autoRollbackOnError` is only sent when the operator ticked it; an
    // absent field means "the cluster default", which is itself false.
    const applyStrategy = buildApplyStrategy(strategySelection, canaryElig);
    try {
      await applyMutation.mutateAsync({
        id: changeset.id,
        confirmTimeoutSec,
        ...(touchesMgmt && mgmtNode ? { mgmtAck: { node: mgmtNode } } : {}),
        ...(applyStrategy ? { applyStrategy } : {}),
        ...(autoRollbackOnError ? { autoRollbackOnError: true } : {}),
      });
      onClose();
    } catch (err) {
      // T-3101-followup-01: the server re-checks live at apply time, so an
      // acknowledgement this screen believed was current can still be
      // rejected — a foreign edit appeared in the gap between ack and
      // click. Re-fetching surfaces exactly what changed instead of
      // leaving the operator staring at a generic failure with a stale
      // "acknowledged" state that no longer matches what the server sees.
      if (err instanceof ApiError && err.code === "sdn_foreign_pending_unacknowledged") {
        setSdnAckedKey(undefined);
        void sdnForeignPendingQuery.refetch();
        toast({
          title: "Foreign SDN changes need re-acknowledging",
          description: "PVE's pending SDN state changed since you last acknowledged it — review and acknowledge again below.",
          variant: "error",
        });
        return;
      }
      toast({ title: "Apply failed to start", description: "See the drawer for details.", variant: "error" });
    }
  }

  async function handleCopyReviewLink(): Promise<void> {
    const link = reviewLinkFor(changeset.id);
    try {
      await navigator.clipboard.writeText(link);
      toast({ title: "Review link copied", description: link });
    } catch {
      toast({ title: "Could not copy link", description: link, variant: "error" });
    }
  }

  return (
    <Drawer open onOpenChange={(open) => { if (!open) onClose(); }}>
      <DrawerContent side="right" aria-describedby="review-apply-description" className="max-w-2xl">
        <div className="flex items-start justify-between gap-2">
          <DrawerTitle>Review &amp; apply — {changeset.title || "Untitled draft"}</DrawerTitle>
          <Button variant="ghost" size="sm" onClick={() => void handleCopyReviewLink()}>
            Copy review link
          </Button>
        </div>
        <DrawerDescription id="review-apply-description">
          {changeset.ops.length} operation{changeset.ops.length === 1 ? "" : "s"} across{" "}
          {new Set(changeset.ops.map((o) => (o.target ? refNode(o.target) : "cluster"))).size} node(s).
        </DrawerDescription>

        <ApprovalPanel changesetId={changeset.id} approval={changeset.approval} />

        {/* T-3002: above the generic blocking-errors list on purpose. A
            `deny` also arrives there as a `policy.violation` message, but as
            one line among any other validation failure; this is the same
            refusal with the rule id, its severity and its assertions
            attached, which is what an operator needs to act on it. */}
        <PolicyVerdictPanel verdict={verdict} findings={changesetPolicyFindings} />

        {/* T-2702: proposing a pull request against the spec repository —
            deliberately its own section, above the tabs and the Apply
            controls, with its own confirmation. It is not gated by
            anything below that gates Apply, and clicking it can never
            apply, confirm or roll back anything (see ProposePanel's own
            doc comment). */}
        <ProposePanel changesetId={changeset.id} />

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
          <RadixTabs.List className="flex gap-1 border-b border-border">
            <RadixTabs.Trigger value="summary" className={tabTriggerClass}>
              Summary
            </RadixTabs.Trigger>
            <RadixTabs.Trigger value="filediff" className={tabTriggerClass}>
              File diff
            </RadixTabs.Trigger>
            <RadixTabs.Trigger value="impact" className={tabTriggerClass}>
              Blast radius
            </RadixTabs.Trigger>
            <RadixTabs.Trigger value="plan" className={tabTriggerClass}>
              Plan
            </RadixTabs.Trigger>
            <RadixTabs.Trigger value="discussion" className={tabTriggerClass}>
              Discussion{changeset.comments && changeset.comments.length > 0 ? ` (${String(changeset.comments.length)})` : ""}
            </RadixTabs.Trigger>
          </RadixTabs.List>

          <RadixTabs.Content value="summary" className="mt-3 flex-1 overflow-y-auto">
            <ul className="space-y-2 text-sm">
              {changeset.ops.map((op, i) => (
                <li key={i} className="rounded border border-border p-2">
                  <span className="mr-1.5 rounded bg-slate-200/70 px-1 py-0.5 text-[10px] uppercase text-fg-muted dark:bg-slate-700/70 dark:text-slate-300">
                    {opKindLabel(op)}
                  </span>
                  {summarizeOp(op)}
                </li>
              ))}
              {changeset.ops.length === 0 && <li className="text-fg-muted">No operations.</li>}
            </ul>
          </RadixTabs.Content>

          <RadixTabs.Content value="impact" className="mt-3 flex-1 overflow-y-auto">
            <ImpactPanel impact={impact} loading={impactLoading} error={impactError} />
            {/* T-2605: the blast radius says who notices; this says what the map
                would look like. The preview lives on the map rather than in
                this drawer because a projected topology is a topology — the
                link carries the changeset id in the URL so the resulting view
                is shareable (topology/previewQuery.ts). */}
            <a
              // T-4201: raw indigo, while this same file's tab styling
              // above already used the accent alias. Identical on screen
              // only for as long as the accent pointed at indigo.
              className="mt-3 inline-block text-xs font-medium text-accent-fg underline"
              href={`/topology?previewChangeset=${encodeURIComponent(changeset.id)}`}
            >
              Preview the post-apply map →
            </a>
          </RadixTabs.Content>

          <RadixTabs.Content value="filediff" className="mt-3 flex-1 overflow-y-auto">
            {diffLoading && <p className="text-xs text-fg-muted">Loading diff…</p>}
            {diff && diff.files.length === 0 && (
              <p className="text-xs text-fg-muted">
                No node interfaces-file changes — this changeset only touches ops without a file representation yet
                (e.g. SDN/firewall/guest-NIC ops).
              </p>
            )}
            {diff && (
              <div className="space-y-3">
                {diff.files.map((f) => (
                  <section key={`${f.node}:${f.path}`}>
                    <h3 className="mb-1 text-xs font-medium text-fg-subtle">
                      {/* T-2003: SDN config diffs are cluster-scoped (no
                       * single owning node — apply_snapshot.go's
                       * sdn*SnapshotPath convention), unlike every
                       * per-node interfaces-file entry. */}
                      {f.node ? `${f.node}: ${f.path}` : f.path} {!f.changed && "(unchanged)"}
                    </h3>
                    <pre className="overflow-x-auto rounded border border-border bg-slate-50 p-2 font-mono text-[11px] leading-snug text-fg-body dark:bg-slate-900">
                      {f.unified || "(no diff)"}
                    </pre>
                  </section>
                ))}
              </div>
            )}
          </RadixTabs.Content>

          <RadixTabs.Content value="plan" className="mt-3 flex-1 overflow-y-auto">
            {planIsPreview && planSteps.length > 0 && (
              <p className="mb-2 text-xs text-fg-muted">
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
                  <li key={i} className="rounded border border-border p-2">
                    <span className="mr-1.5 rounded bg-sky-100 px-1 py-0.5 text-[10px] uppercase text-sky-700 dark:bg-sky-950 dark:text-sky-300">
                      {s.kind}
                    </span>
                    {s.node && <span className="mr-1 text-xs text-fg-muted">[{s.node}]</span>}
                    {s.summary}
                  </li>
                ))}
              </ol>
            ) : (
              <p className="text-xs text-fg-muted">No executable steps for the current op list.</p>
            )}
          </RadixTabs.Content>

          <RadixTabs.Content value="discussion" className="mt-3 flex-1 overflow-y-auto">
            <CommentsPanel changeset={changeset} />
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

        {/* T-3101-followup-01: "this apply will also commit ...". Real PVE's
            PUT /cluster/sdn (this changeset's trailing sdn_apply step, if it
            has one) commits ALL pending SDN state cluster-wide, not just
            this changeset's own ops — so anything staged outside vnprox
            (e.g. the PVE GUI) and left unapplied rides along. Shown even
            while already acknowledged (sdnForeignPendingBlocks false), so
            the operator can see what they signed off on, not just that they
            did. */}
        {sdnForeignPendingEntries && sdnForeignPendingEntries.length > 0 && (
          <div
            className="mt-3 rounded-md border border-red-300 bg-red-50 p-2 text-xs text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200"
            role="group"
            aria-label="Foreign pending SDN changes"
          >
            <p className="font-medium">
              This apply will also commit {sdnForeignPendingEntries.length} SDN change
              {sdnForeignPendingEntries.length === 1 ? "" : "s"} staged outside vnprox — never validated, diffed, or
              covered by this changeset&apos;s rollback:
            </p>
            <ul className="mt-1 space-y-1">
              {sdnForeignPendingEntries.map((e, i) => (
                <li key={i} className="font-mono">
                  {describeSdnPendingEntry(e)}
                </li>
              ))}
            </ul>
            {sdnForeignPendingBlocks ? (
              <Button
                className="mt-2"
                variant="secondary"
                size="sm"
                disabled={ackSdnForeignPending.isPending}
                onClick={() => void handleAckSdnForeignPending()}
              >
                {ackSdnForeignPending.isPending ? "Acknowledging…" : "Acknowledge and continue"}
              </Button>
            ) : (
              <p className="mt-2 font-medium">Acknowledged — Apply will proceed with these included.</p>
            )}
          </div>
        )}

        {/* T-3005: how this apply fans out, and whether a new error finding
            cuts the window short. Placed above the mgmt-path acknowledgement
            and the confirm-window control because the hold is clamped to
            that window and the operator should pick the strategy before
            sizing the window around it. */}
        <ApplyStrategyPanel
          planSteps={planSteps}
          selection={strategySelection}
          onSelectionChange={setStrategySelection}
          autoRollbackOnError={autoRollbackOnError}
          onAutoRollbackChange={setAutoRollbackOnError}
          confirmTimeoutSec={confirmTimeoutSec}
        />

        {revertNotice.show && (
          <div
            className="mt-3 rounded-md border border-sky-300 bg-sky-50 p-3 text-xs text-sky-900 dark:border-sky-700 dark:bg-sky-950 dark:text-sky-100"
            role="note"
            aria-label="Unattended revert coverage"
            data-testid="revert-coverage-notice"
          >
            <p className="font-medium">{revertNotice.heading}</p>
            <p className="mt-1">{revertNotice.body}</p>
          </div>
        )}

        {touchesMgmt && (
          <div
            className="mt-3 rounded-md border border-red-300 bg-red-50 p-3 text-xs text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200"
            role="group"
            aria-label="Management-path acknowledgement"
          >
            <p className="font-medium">{mgmtStrings.ack.heading}</p>
            <p className="mt-1">{mgmtStrings.ack.body}</p>
            <p className="mt-1">{mgmtStrings.ack.confirmWindowNote}</p>
            <label className="mt-2 flex flex-col gap-1">
              <span className="font-medium">{mgmtStrings.ack.typePrompt(mgmtNode)}</span>
              <input
                type="text"
                value={ackText}
                onChange={(e) => {
                  setAckText(e.target.value);
                }}
                aria-label="Type node name to acknowledge"
                className="w-48 rounded border border-red-300 px-1.5 py-0.5 text-xs dark:border-red-700 dark:bg-slate-900"
              />
              {ackText.trim().length > 0 && !ackSatisfied && (
                <span className="text-[11px]">{mgmtStrings.ack.mismatch}</span>
              )}
            </label>
          </div>
        )}

        {/* T-3002: the emergency override, three deliberate actions away and
            never fewer. Offered only while the two-person rule is what
            blocks — it overrides the distinct-approver count and nothing
            else. */}
        <BreakGlassPanel
          record={changeset.approval?.twoPerson?.breakGlass}
          blocked={twoPersonBlocks}
          pending={breakGlass.isPending}
          error={breakGlassError}
          onInvoke={(reason) => {
            breakGlass.mutate(reason);
          }}
        />

        {/* T-4006: the freeze-window override, same three-deliberate-actions
            shape as break-glass above. Offered only while a freeze-tagged
            deny rule is what blocks — it downgrades that rule's finding to
            a visible warning, and nothing else. */}
        <FreezeOverridePanel
          record={freezeOverride.data}
          blocked={freezeBlocked}
          pending={freezeOverride.isPending}
          error={freezeOverrideError}
          onInvoke={(reason) => {
            freezeOverride.mutate(reason);
          }}
        />

        <div className="mt-4 flex items-center gap-2">
          <label className="flex items-center gap-1.5 text-xs text-fg-subtle">
            Confirm window (s)
            <input
              type="number"
              min={touchesMgmt ? MGMT_CONFIRM_FLOOR_SEC : 30}
              max={600}
              value={confirmTimeoutSec}
              onChange={(e) => {
                const v = Number(e.target.value);
                // T-703: never allow a management-path window below the floor
                // (the server rejects it anyway; the UI just never offers it).
                setConfirmTimeoutSec(touchesMgmt ? Math.max(v, MGMT_CONFIRM_FLOOR_SEC) : v);
              }}
              className="w-16 rounded border border-border-strong px-1.5 py-0.5 text-xs dark:bg-slate-800"
            />
          </label>
          <div className="ml-auto flex flex-col items-end gap-1">
            {approvalBlocks && (
              <p className="max-w-xs text-right text-[11px] text-red-700 dark:text-red-300" role="alert">
                {APPROVAL_REQUIRED_MESSAGE}
              </p>
            )}
            {twoPersonBlocks && (
              <p className="max-w-xs text-right text-[11px] text-red-700 dark:text-red-300" role="alert">
                {twoPersonRequiredMessage(changeset.approval)}
              </p>
            )}
            {sdnForeignPendingBlocks && (
              <p className="max-w-xs text-right text-[11px] text-red-700 dark:text-red-300" role="alert">
                Acknowledge the foreign pending SDN changes above before applying.
              </p>
            )}
            <div className="flex gap-2">
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
        </div>
      </DrawerContent>
    </Drawer>
  );
}

