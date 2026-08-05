// The microsegmentation planner panel, launched from a guest's firewall
// inspector (FirewallPage's GuestPanel). It calls T-1602's read-only
// synthesis routes and presents:
//   1. the proposed minimal covering-set policy, with coverage % and the
//      uncovered-flow count stated PLAINLY (never rounded to "everything"),
//   2. a monitor-only dry-run (DryRunReport) whose would-have-blocked and
//      cannot-determine buckets a reviewer must see before enforcing, and
//   3. a "Stage as changeset" action that hands the server-computed
//      `stagedOps` into the ordinary ChangesetDrawer (useDrawerActions) —
//      the same draft-accumulation path every other editor uses, no bespoke
//      apply affordance. Staging is GATED on having run a dry-run at least
//      once for the current proposal (T-1603 AC3): no one stages a policy
//      no one has dry-run.
//
// This component owns NO synthesis logic and NO enforcement path; it is UX
// over T-1602's API and the existing changeset drawer.
import { useState } from "react";
import { useToast } from "../components/Toast";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/Table";
import type { MicrosegDryRunReport, MicrosegProposal, RuleView } from "../api/types";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { useChangesetDrawerStore } from "../changesets/store";
import { ApiError } from "../api/client";
import { DryRunReport } from "./DryRunReport";
import { useMicrosegDryRunMutation, useMicrosegProposeMutation } from "./queries";
import { formatCoveragePct } from "./format";
import { HelpAnchor } from "../help/HelpAnchor";

interface MicrosegPlannerProps {
  /** The guest whose observed flows drive synthesis — its own inventory Ref
   * ("guest:<node>:<vmid>"), the same ref the firewall guest selector uses. */
  guestRef: string;
}

/** The current dry-run result, tied to the proposal it was run against. */
interface DryRunState {
  report: MicrosegDryRunReport;
  heldOut: boolean;
}

function ruleScopeLabel(rule: RuleView): string {
  const peer = rule.direction === "in" ? rule.source : rule.dest;
  if (peer) return peer;
  return rule.action === "ACCEPT" ? "—" : "any (default-deny)";
}

function ProposedRulesTable({ rules }: { rules: RuleView[] }) {
  return (
    <Table density="compact" aria-label="Proposed rules">
      <TableHeader>
        <TableRow>
          <TableHead>#</TableHead>
          <TableHead>Direction</TableHead>
          <TableHead>Action</TableHead>
          <TableHead>Proto</TableHead>
          <TableHead>Port</TableHead>
          <TableHead>Peer</TableHead>
          <TableHead>Comment</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rules.map((rule, i) => (
          <TableRow key={`${String(rule.pos)}-${rule.direction}-${String(i)}`}>
            <TableCell className="font-mono text-xs">{rule.pos}</TableCell>
            <TableCell className="font-mono text-xs">{rule.direction}</TableCell>
            <TableCell className="font-mono text-xs">{rule.action}</TableCell>
            <TableCell className="font-mono text-xs">{rule.proto ?? "—"}</TableCell>
            <TableCell className="font-mono text-xs">{rule.dport ?? "—"}</TableCell>
            <TableCell className="font-mono text-xs">{ruleScopeLabel(rule)}</TableCell>
            <TableCell className="text-xs text-slate-500 dark:text-slate-400">{rule.comment ?? ""}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export function MicrosegPlanner({ guestRef }: MicrosegPlannerProps) {
  const { toast } = useToast();
  const proposeMutation = useMicrosegProposeMutation();
  const dryRunMutation = useMicrosegDryRunMutation();
  const { addOps } = useDrawerActions();
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);

  const [proposal, setProposal] = useState<MicrosegProposal | undefined>(undefined);
  // Tied to the CURRENT proposal: re-proposing clears it (AC3), so a stale
  // dry-run can never enable staging a since-recomputed policy.
  const [dryRun, setDryRun] = useState<DryRunState | undefined>(undefined);
  const [staged, setStaged] = useState(false);

  function handlePropose(): void {
    proposeMutation.mutate(guestRef, {
      onSuccess: (prop) => {
        setProposal(prop);
        setDryRun(undefined); // invalidate any prior dry-run's staged-enablement
        setStaged(false);
      },
      onError: (err) => {
        toast({ title: proposeErrorMessage(err), variant: "error" });
      },
    });
  }

  function handleDryRun(heldOut: boolean): void {
    dryRunMutation.mutate(
      { guestRef, heldOut },
      {
        onSuccess: (report) => {
          setDryRun({ report, heldOut });
        },
        onError: (err) => {
          toast({ title: err instanceof Error ? err.message : "Dry-run failed", variant: "error" });
        },
      },
    );
  }

  function handleStage(): void {
    if (!proposal || dryRun === undefined) return;
    if (proposal.stagedOps.length === 0) {
      toast({ title: "Nothing to stage — this proposal has no rules.", variant: "error" });
      return;
    }
    void addOps(proposal.stagedOps, `Microsegment ${guestRef}`)
      .then((created) => {
        setActiveId(created.id); // open the drawer on the just-staged changeset
        setStaged(true);
        toast({ title: "Proposed rules added to the change drawer — review and apply there.", variant: "success" });
      })
      .catch((err: unknown) => {
        toast({ title: err instanceof Error ? err.message : "Could not stage the proposal", variant: "error" });
      });
  }

  const canStage = proposal !== undefined && dryRun !== undefined && !staged;

  return (
    <div className="flex flex-col gap-4" data-testid="microseg-planner">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            Microsegmentation planner
            <HelpAnchor topic="microseg-planner" />
          </h3>
          <p className="text-xs text-slate-500 dark:text-slate-400">
            Proposes the minimal firewall policy that preserves this guest&apos;s observed-good traffic, then dry-runs it
            before you enforce. Read-only until you stage it as a changeset.
          </p>
        </div>
        <button
          type="button"
          onClick={handlePropose}
          disabled={proposeMutation.isPending}
          className="rounded-md bg-accent-600 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-60"
        >
          {proposeMutation.isPending ? "Proposing…" : proposal ? "Re-propose policy" : "Propose policy"}
        </button>
      </div>

      {proposeMutation.isError && !proposal && (
        <p role="alert" className="rounded-md bg-amber-50 px-3 py-1.5 text-sm text-amber-800 dark:bg-amber-950/40 dark:text-amber-300">
          {proposeErrorMessage(proposeMutation.error)}
        </p>
      )}

      {proposal && (
        <div className="flex flex-col gap-4">
          {/* Coverage summary — stated plainly, never rounded to "everything". */}
          <div className="rounded-lg border border-slate-200 p-3 dark:border-slate-800" data-testid="coverage-summary">
            <p className="text-sm">
              <span className="font-semibold">{proposal.rules.length}</span> rules cover{" "}
              <span className="font-semibold">{formatCoveragePct(proposal.coveragePct)}</span> of this guest&apos;s
              observed-good traffic ({proposal.observedGoodFlowCount.toLocaleString()} flows over the learning window).
            </p>
            <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
              <span className="font-semibold tabular-nums">{proposal.uncoveredFlowCount.toLocaleString()}</span> observed-good
              flows fall in the deliberately-uncovered long tail (not covered by any proposed rule).
            </p>
            {(proposal.excludedAnomalyFlows > 0 || proposal.alreadyCoveredGroups > 0) && (
              <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
                {proposal.excludedAnomalyFlows > 0 && (
                  <>
                    {proposal.excludedAnomalyFlows.toLocaleString()} flow(s) excluded as anomalous (never legitimized into an
                    allow rule).{" "}
                  </>
                )}
                {proposal.alreadyCoveredGroups > 0 && (
                  <>{proposal.alreadyCoveredGroups.toLocaleString()} group(s) already covered by the guest&apos;s current firewall.</>
                )}
              </p>
            )}
          </div>

          <div>
            <h4 className="mb-1 text-sm font-semibold">Proposed policy</h4>
            <ProposedRulesTable rules={proposal.rules} />
          </div>

          {/* Dry-run controls. */}
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              onClick={() => { handleDryRun(false); }}
              disabled={dryRunMutation.isPending}
              className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium disabled:opacity-60 dark:border-slate-700"
            >
              {dryRunMutation.isPending ? "Dry-running…" : "Run dry-run"}
            </button>
            <button
              type="button"
              onClick={() => { handleDryRun(true); }}
              disabled={dryRunMutation.isPending}
              className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium disabled:opacity-60 dark:border-slate-700"
            >
              Dry-run against held-out window
            </button>
          </div>

          {dryRun && <DryRunReport report={dryRun.report} heldOut={dryRun.heldOut} />}

          {/* Stage — gated on a dry-run having run for THIS proposal. */}
          <div className="flex flex-wrap items-center gap-3 border-t border-slate-200 pt-3 dark:border-slate-800">
            <button
              type="button"
              onClick={handleStage}
              disabled={!canStage}
              className="rounded-md bg-accent-600 px-3 py-1.5 text-sm font-medium text-white disabled:cursor-not-allowed disabled:opacity-60"
            >
              Stage as changeset
            </button>
            {dryRun === undefined && (
              <span className="text-xs text-slate-500 dark:text-slate-400">
                Run a dry-run first — staging is disabled until this proposal has been dry-run at least once.
              </span>
            )}
            {staged && (
              <span className="text-xs text-emerald-700 dark:text-emerald-400">
                Staged. Review and apply it in the change drawer.
              </span>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

/** Maps a propose error to reviewer-legible copy: a 404 means the guest has
 * no observable flows yet (not an error the reviewer caused), everything
 * else is surfaced with its own message. */
function proposeErrorMessage(err: unknown): string {
  if (err instanceof ApiError && err.status === 404) {
    return "No observed flows for this guest yet — nothing to propose. A policy needs a flow history to learn from.";
  }
  return err instanceof Error ? err.message : "Could not compute a proposal";
}
