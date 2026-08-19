// T-3001: the spec/config/live disagreements, and the two ways out of one.
//
// Both actions are staging actions. Restoring intent hands the change engine a
// DRAFT and stops — this module never validates, applies or confirms anything,
// and imports no call that could. Adopting reality opens a pull request and
// changes nothing about the cluster at all. The ordinary review screen is
// where either one is actually carried out.
//
// The confirmations are separate on purpose (T-2703 shipped the two actions
// apart for the same reason): they have opposite blast radii, so there is no
// combined "reconcile" affordance and no state in which one click performs
// both. `pending` below holds at most one action, and each dialog's confirm
// button calls exactly one mutation.
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "../components/Button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { useChangesetDrawerStore } from "../changesets/store";
import { useToast } from "../components/Toast";
import { HelpAnchor } from "../help/HelpAnchor";
import type { DriftFinding, Reconciliation } from "../api/types";
import type { GitSyncState } from "./gitsyncState";
import { adoptAvailability } from "./gitsyncState";
import { ReconciliationCard } from "./ReconciliationCard";
import { useAdoptRealityMutation, useDriftQuery, useDriftWsBridge, useRestoreIntentMutation } from "./queries";

/** Whether a spec position exists to compare against at all. `"unknown"` when
 * neither the pin nor the git sync could be read — the state in which "no
 * disagreements" must NOT be reported as agreement. */
export type SpecPresence = "present" | "absent" | "unknown";

interface PendingAction {
  kind: "restore" | "adopt";
  finding: DriftFinding;
}

interface ThreeWayPanelProps {
  gitSync: GitSyncState;
  specPresence: SpecPresence;
  /** Tooltip naming the missing capability, or undefined when allowed. */
  writeDisabledReason?: string;
}

/** Narrows a finding to one that carries T-2703's three-position report. */
function withReconciliation(f: DriftFinding): (DriftFinding & { reconciliation: Reconciliation }) | undefined {
  return f.reconciliation === undefined ? undefined : { ...f, reconciliation: f.reconciliation };
}

function emptyStateCopy(presence: SpecPresence): { title: string; detail: string } {
  switch (presence) {
    case "unknown":
      return {
        title: "No spec/config/live disagreements are being reported",
        detail:
          "vnprox could not confirm whether a spec document is configured on this deployment, so this may mean there is nothing to compare rather than nothing wrong.",
      };
    case "absent":
      return {
        title: "There is no spec position yet",
        detail:
          "Nothing is pinned and no git sync is configured, so there is no declarative document to compare config and live against. This is not agreement — it is an unasked question.",
      };
    default:
      return {
        title: "Spec, config and live agree",
        detail:
          "Every entity the document declares matches both /etc/network/interfaces and the running kernel. Divergences that involve no spec position are reported in the findings stream instead.",
      };
  }
}

export function ThreeWayPanel({ gitSync, specPresence, writeDisabledReason }: ThreeWayPanelProps) {
  const { data: findings, isLoading, error } = useDriftQuery();
  const restoreMutation = useRestoreIntentMutation();
  const adoptMutation = useAdoptRealityMutation();
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);
  const navigate = useNavigate();
  const { toast } = useToast();
  const [pending, setPending] = useState<PendingAction | undefined>(undefined);
  const [actionError, setActionError] = useState<string | undefined>(undefined);

  useDriftWsBridge();

  const reconciliations = (findings ?? []).flatMap((f) => {
    const narrowed = withReconciliation(f);
    return narrowed === undefined ? [] : [narrowed];
  });

  async function confirmRestoreIntent(finding: DriftFinding): Promise<void> {
    setPending(undefined);
    setActionError(undefined);
    try {
      const changeset = await restoreMutation.mutateAsync(finding.id);
      setActiveId(changeset.id);
      toast({
        title: "Draft staged",
        description: "Nothing has been applied. Review, validate and apply it like any other changeset.",
        variant: "success",
      });
      void navigate(`/changesets/${encodeURIComponent(changeset.id)}/review`);
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "could not stage the restoring changeset");
    }
  }

  async function confirmAdoptReality(finding: DriftFinding): Promise<void> {
    setPending(undefined);
    setActionError(undefined);
    try {
      const proposal = await adoptMutation.mutateAsync(finding.id);
      toast({
        title: proposal.created ? "Pull request opened" : "Pull request updated",
        description: "The document now describes this entity as the cluster has it. The cluster is unchanged.",
        variant: "success",
      });
    } catch (err) {
      // The daemon's own refusal, verbatim: `501 not_implemented` (no
      // write-capable repository) and `422 nothing_to_propose` are different
      // answers and only this message tells them apart.
      setActionError(err instanceof Error ? err.message : "could not propose a spec commit");
    }
  }

  return (
    <section aria-labelledby="three-way-heading">
      <h2 id="three-way-heading" className="flex items-center gap-2 text-lg font-semibold">
        Spec, config and live
        <HelpAnchor topic="spec-reconciliation" />
      </h2>
      <p className="mt-1 text-sm text-slate-600 dark:text-slate-400">
        One row per entity the document declares whose three positions disagree. All three pairwise comparisons
        are shown, including the ones that agree — which pair agrees is what identifies the odd position out.
      </p>

      {actionError !== undefined && (
        <div
          role="alert"
          className="mt-3 rounded-md border border-red-300 bg-red-50 p-3 text-sm dark:border-red-900 dark:bg-red-950/40"
        >
          <p className="font-semibold text-slate-900 dark:text-slate-100">The daemon refused that action</p>
          <p className="mt-1 text-slate-800 dark:text-slate-100">{actionError}</p>
        </div>
      )}

      {isLoading && <p className="mt-2 text-sm text-slate-600 dark:text-slate-400">Loading the drift report…</p>}

      {error !== null && (
        <div
          role="alert"
          className="mt-2 rounded-md border border-slate-300 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-900"
        >
          <p className="text-sm font-semibold text-slate-900 dark:text-slate-100">
            Could not read the drift report
          </p>
          <p className="mt-1 text-xs text-slate-600 dark:text-slate-400">
            No conclusion can be drawn about spec, config or live from this screen until it can be read again.
          </p>
        </div>
      )}

      {!isLoading && error === null && reconciliations.length === 0 && (
        <div className="mt-2 rounded-md border border-slate-200 bg-slate-50 p-3 dark:border-slate-800 dark:bg-slate-900/60">
          <p className="text-sm font-semibold text-slate-900 dark:text-slate-100">
            {emptyStateCopy(specPresence).title}
          </p>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">{emptyStateCopy(specPresence).detail}</p>
        </div>
      )}

      {reconciliations.length > 0 && (
        <ul className="mt-3 flex flex-col gap-3">
          {reconciliations.map((f) => (
            <ReconciliationCard
              key={f.id}
              finding={f}
              reconciliation={f.reconciliation}
              adoptAvailability={adoptAvailability(gitSync)}
              writeDisabledReason={writeDisabledReason}
              onRestoreIntent={() => {
                setPending({ kind: "restore", finding: f });
              }}
              onAdoptReality={() => {
                setPending({ kind: "adopt", finding: f });
              }}
            />
          ))}
        </ul>
      )}

      <Dialog
        open={pending !== undefined}
        onOpenChange={(open) => {
          if (!open) {
            setPending(undefined);
          }
        }}
      >
        <DialogContent aria-label={pending?.kind === "adopt" ? "Confirm adopt reality" : "Confirm restore intent"}>
          {pending?.kind === "adopt" ? (
            <>
              <DialogTitle>Adopt reality into the document?</DialogTitle>
              <DialogDescription>
                This rewrites the spec document to describe{" "}
                <code className="font-mono">{pending.finding.reconciliation?.ref ?? pending.finding.id}</code> as
                the cluster currently has it, as a pull request on the spec repository.
              </DialogDescription>
              <ul className="mt-3 flex list-disc flex-col gap-1 pl-5 text-sm text-slate-600 dark:text-slate-300">
                <li>The cluster is not touched. No network change is staged, applied or scheduled.</li>
                <li>vnprox opens the request and stops — it never merges, approves or polls one.</li>
                <li>Whoever reviews that request is deciding that the current cluster state is the intent.</li>
              </ul>
              <div className="mt-5 flex justify-end gap-2">
                <DialogClose asChild>
                  <Button variant="secondary" size="sm">
                    Cancel
                  </Button>
                </DialogClose>
                <Button
                  size="sm"
                  disabled={adoptMutation.isPending}
                  onClick={() => {
                    void confirmAdoptReality(pending.finding);
                  }}
                >
                  {adoptMutation.isPending ? "Proposing…" : "Adopt reality"}
                </Button>
              </div>
            </>
          ) : (
            pending !== undefined && (
              <>
                <DialogTitle>Restore intent on the cluster?</DialogTitle>
                <DialogDescription>
                  This stages a draft changeset bringing{" "}
                  <code className="font-mono">{pending.finding.reconciliation?.ref ?? pending.finding.id}</code>{" "}
                  back to what the document declares.
                </DialogDescription>
                <ul className="mt-3 flex list-disc flex-col gap-1 pl-5 text-sm text-slate-600 dark:text-slate-300">
                  <li>Nothing is applied by this step. You get a draft, in the ordinary review screen.</li>
                  <li>Applying it later changes the network — validate and read the blast radius there first.</li>
                  <li>The document is not touched; this moves the cluster, not the intent.</li>
                </ul>
                <div className="mt-5 flex justify-end gap-2">
                  <DialogClose asChild>
                    <Button variant="secondary" size="sm">
                      Cancel
                    </Button>
                  </DialogClose>
                  <Button
                    size="sm"
                    disabled={restoreMutation.isPending}
                    onClick={() => {
                      void confirmRestoreIntent(pending.finding);
                    }}
                  >
                    {restoreMutation.isPending ? "Staging…" : "Stage the draft"}
                  </Button>
                </div>
              </>
            )
          )}
        </DialogContent>
      </Dialog>
    </section>
  );
}
