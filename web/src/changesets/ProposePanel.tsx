// SPDX-License-Identifier: Apache-2.0

// T-2702: propose a changeset as a pull request against the spec repository.
//
// This is the mirror image of T-2703's "adopt reality"
// (web/src/drift/ThreeWayPanel.tsx): same `Proposal` shape, same 501/422
// vocabulary, same reasoning for a separate confirmation — but the source
// here is a changeset already staged (and possibly already applied), not a
// drift finding. What matters for THIS panel:
//
//   * Proposing never touches the cluster. It renders the changeset as a
//     spec delta, commits it on a branch, pushes, and opens or updates a
//     pull request. The changeset itself is not mutated.
//   * Proposing is independent of Apply. It is not gated by approval, the
//     two-person rule, the mgmt-path acknowledgement, or foreign-pending
//     SDN — every one of those answers "is it safe to change the network",
//     and this changes nothing about the network. A changeset can be
//     proposed before it's applied, after, or never at all, in any order.
//   * Its own confirmation dialog, separate from Apply's "Apply" button and
//     its own confirm-window control, so a click here can never be mistaken
//     for a click that changes the cluster.
import { useState } from "react";
import { Button } from "../components/Button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";
import { useToast } from "../components/Toast";
import { HelpAnchor } from "../help/HelpAnchor";
import { gitSyncState, type GitSyncState } from "../drift/gitsyncState";
import { useGitSyncStatusQuery } from "../drift/queries";
import { useChangesetProposalQuery, useProposeChangesetMutation } from "./queries";

/** Mirrors drift/gitsyncState.ts's `adoptAvailability`: no route reports
 * whether a write-scoped push credential is configured, so the only
 * certain-in-advance "this cannot work" is no `[gitsync]` section at all —
 * no credential can exist with no repository to hold it. Every other state
 * is left to the daemon's own `501` on click, exactly like adopt-reality. */
function proposeUnavailable(gitSync: GitSyncState): boolean {
  return gitSync.kind === "not-configured";
}

export interface ProposePanelProps {
  changesetId: string;
}

export function ProposePanel({ changesetId }: ProposePanelProps) {
  const gitSyncQuery = useGitSyncStatusQuery();
  const gitSync = gitSyncState(gitSyncQuery.data, gitSyncQuery.isLoading, gitSyncQuery.error);
  const proposalQuery = useChangesetProposalQuery(changesetId);
  const proposeMutation = useProposeChangesetMutation();
  const { toast } = useToast();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [proposeError, setProposeError] = useState<string | undefined>(undefined);

  async function confirmPropose(): Promise<void> {
    setConfirmOpen(false);
    setProposeError(undefined);
    try {
      const proposal = await proposeMutation.mutateAsync(changesetId);
      toast({
        title: proposal.created ? "Pull request opened" : "Pull request updated",
        description: "The cluster is unchanged — this changeset was rendered as a spec commit, not applied.",
        variant: "success",
      });
    } catch (err) {
      // The daemon's own refusal, verbatim: `501 not_implemented` (no
      // write-capable repository configured) and `422 nothing_to_propose`
      // (an empty changeset, or one that makes no difference to the
      // document) are different answers, and only this message tells them
      // apart — see ThreeWayPanel's identical comment for adopt-reality.
      setProposeError(err instanceof Error ? err.message : "could not propose a pull request");
    }
  }

  const unavailable = proposeUnavailable(gitSync);
  const proposal = proposalQuery.data;

  return (
    <section
      className="mt-3 rounded-md border border-slate-200 p-3 dark:border-slate-800"
      aria-labelledby="propose-heading"
    >
      <h3 id="propose-heading" className="flex items-center gap-2 text-sm font-semibold">
        Propose as pull request
        <HelpAnchor topic="changeset-propose" />
      </h3>
      <p className="mt-1 text-xs text-slate-600 dark:text-slate-400">
        Renders this changeset as a commit against the spec repository and opens a pull request for it. This is{" "}
        <strong>not</strong> Apply — the cluster is never touched, and applying this changeset (now, later, or never)
        is a separate, unrelated step.
      </p>

      {unavailable && (
        <p className="mt-2 text-xs text-fg-subtle">
          Unavailable on this deployment: with no <code className="font-mono">[gitsync]</code> repository
          configured, there is nowhere to open a pull request.
        </p>
      )}

      {proposeError !== undefined && (
        <div
          role="alert"
          className="mt-2 rounded-md border border-red-300 bg-red-50 p-2 text-xs dark:border-red-900 dark:bg-red-950/40"
        >
          <p className="font-semibold text-slate-900 dark:text-slate-100">The daemon refused that proposal</p>
          <p className="mt-1 text-slate-800 dark:text-slate-100">{proposeError}</p>
        </div>
      )}

      {!unavailable && (
        <Button
          className="mt-2"
          size="sm"
          variant="secondary"
          disabled={proposeMutation.isPending}
          onClick={() => {
            setConfirmOpen(true);
          }}
        >
          {proposeMutation.isPending ? "Proposing…" : "Propose as pull request…"}
        </Button>
      )}

      {proposalQuery.isLoading && (
        <p className="mt-2 text-xs text-fg-subtle">Checking for an existing proposal…</p>
      )}
      {proposalQuery.error !== null && (
        <p className="mt-2 text-xs text-fg-subtle">
          Could not check whether this changeset was already proposed.
        </p>
      )}
      {proposal !== null && proposal !== undefined && (
        <p className="mt-2 text-xs text-slate-600 dark:text-slate-300">
          Already proposed as{" "}
          {proposal.pullRequestUrl !== undefined && proposal.pullRequestUrl !== "" ? (
            <a
              href={proposal.pullRequestUrl}
              target="_blank"
              rel="noreferrer"
              className="font-medium text-accent-fg underline"
            >
              {proposal.pullRequestId !== undefined && proposal.pullRequestId !== ""
                ? `pull request #${proposal.pullRequestId}`
                : "the open pull request"}
            </a>
          ) : (
            <span className="font-mono">
              {proposal.pullRequestId !== undefined && proposal.pullRequestId !== ""
                ? `pull request #${proposal.pullRequestId}`
                : "the open pull request"}
            </span>
          )}{" "}
          on <code className="font-mono">{proposal.branch}</code>. Merging it is your repository&apos;s business, not
          vnprox&apos;s.
        </p>
      )}

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent aria-label="Confirm propose as pull request">
          <DialogTitle>Propose this changeset as a pull request?</DialogTitle>
          <DialogDescription>
            This renders the changeset as a spec delta, commits it on a branch, pushes it, and opens (or updates) a
            pull request on the spec repository.
          </DialogDescription>
          <ul className="mt-3 flex list-disc flex-col gap-1 pl-5 text-sm text-slate-600 dark:text-slate-300">
            <li>The cluster is not touched. No network change is staged, applied or scheduled by this.</li>
            <li>vnprox opens the request and stops — it never merges, approves or polls one.</li>
            <li>Applying this changeset, if you do, is a separate action from this one.</li>
          </ul>
          <div className="mt-5 flex justify-end gap-2">
            <DialogClose asChild>
              <Button variant="secondary" size="sm">
                Cancel
              </Button>
            </DialogClose>
            <Button
              size="sm"
              disabled={proposeMutation.isPending}
              onClick={() => {
                void confirmPropose();
              }}
            >
              {proposeMutation.isPending ? "Proposing…" : "Propose"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </section>
  );
}
