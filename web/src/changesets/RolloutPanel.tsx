// T-3005: the rollout-state view for a changeset paused mid-canary.
//
// This is the half the card calls load-bearing. Everything it renders comes
// from `changeset.applyStage` — the server's persisted read model — so a
// browser reload, a new tab, or a daemon restart all produce the same
// screen: nothing about the hold is remembered on this side.
import clsx from "clsx";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import type { Changeset } from "../api/types";
import { HelpAnchor } from "../help/HelpAnchor";
import { deriveRollout, type NodeRolloutStatus } from "./rolloutState";
import { useContinueStagedApplyMutation, useRollbackChangesetMutation } from "./queries";

const STATUS_LABEL: Record<NodeRolloutStatus, string> = {
  done: "applied",
  pending: "not contacted",
  unknown: "unknown",
};

const STATUS_CLASS: Record<NodeRolloutStatus, string> = {
  done: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-200",
  pending: "border-slate-300 bg-slate-50 text-slate-700 dark:border-slate-600 dark:bg-slate-900 dark:text-slate-200",
  // Deliberately loud. An unknown node is the state an operator most needs
  // to notice, so it must never read like a quiet "pending".
  unknown: "border-amber-400 bg-amber-50 text-amber-900 dark:border-amber-600 dark:bg-amber-950 dark:text-amber-100",
};

function deadlineText(label: string, unixSec: number | undefined): string {
  if (unixSec === undefined) return `${label}: not reported`;
  const remaining = Math.round(unixSec - Date.now() / 1000);
  if (remaining <= 0) return `${label}: elapsed`;
  return `${label}: ${String(remaining)}s`;
}

export interface RolloutPanelProps {
  changeset: Changeset;
}

/** Renders nothing at all when this changeset has no staged apply — an
 * ordinary all-at-once apply must look exactly as it always did. */
export function RolloutPanel({ changeset }: RolloutPanelProps) {
  const rollout = deriveRollout(changeset);
  const continueMutation = useContinueStagedApplyMutation();
  const rollbackMutation = useRollbackChangesetMutation();
  const { toast } = useToast();

  if (!rollout) return null;

  return (
    <section
      className="w-full rounded-md border border-sky-300 bg-white/70 p-3 text-left text-xs text-slate-800 dark:border-sky-700 dark:bg-slate-900/70 dark:text-slate-100"
      aria-label="Canary rollout state"
      data-testid="rollout-panel"
    >
      <div className="flex items-center gap-1.5">
        <h3 className="text-xs font-semibold">{rollout.headline}</h3>
        <HelpAnchor topic="canary-apply" />
      </div>
      <p className="mt-1">{rollout.gateExplanation}</p>
      {!rollout.recognized && (
        <p className="mt-1 font-medium" data-testid="rollout-unrecognized">
          Reported stage state: {rollout.state}
        </p>
      )}

      <p className="mt-1 text-slate-600 dark:text-slate-300">
        {deadlineText("Hold ends in", rollout.holdDeadline)} · {deadlineText("Confirm window closes in", rollout.confirmDeadline)}
      </p>

      {rollout.nodesUnknown ? (
        <p className="mt-2 rounded border border-amber-400 bg-amber-50 p-2 font-medium text-amber-900 dark:border-amber-600 dark:bg-amber-950 dark:text-amber-100">
          The server did not report which nodes have been applied. Do not read this as &ldquo;none&rdquo; — treat the
          changeset as partially applied and check it before continuing.
        </p>
      ) : (
        <ul className="mt-2 flex flex-col gap-1" data-testid="rollout-nodes">
          {rollout.nodes.map((n) => (
            <li
              key={n.node}
              data-testid={`rollout-node-${n.node}`}
              className={clsx("rounded border px-2 py-1", STATUS_CLASS[n.status])}
            >
              <span className="font-medium">{n.node}</span>
              <span> — {STATUS_LABEL[n.status]}</span>
              {n.note !== undefined && <span className="block">{n.note}</span>}
            </li>
          ))}
          {rollout.nodes.length === 0 && (
            <li className="rounded border border-amber-400 bg-amber-50 px-2 py-1 font-medium text-amber-900 dark:border-amber-600 dark:bg-amber-950 dark:text-amber-100">
              No nodes were reported for this stage.
            </li>
          )}
        </ul>
      )}

      <div className="mt-2 flex gap-2">
        <Button
          size="sm"
          variant="primary"
          disabled={!rollout.canContinue || continueMutation.isPending}
          onClick={() => {
            continueMutation.mutate(changeset.id, {
              onError: () => {
                toast({
                  title: "Could not continue the rollout",
                  description: "The server may have already promoted or aborted this hold.",
                  variant: "error",
                });
              },
            });
          }}
        >
          Continue to remaining nodes
        </Button>
        <Button
          size="sm"
          variant="destructive"
          disabled={rollbackMutation.isPending}
          onClick={() => {
            rollbackMutation.mutate(changeset.id, {
              onError: () => {
                toast({ title: "Could not abort the rollout", variant: "error" });
              },
            });
          }}
        >
          Abort and restore applied nodes
        </Button>
      </div>
    </section>
  );
}
