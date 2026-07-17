// The commit-confirm countdown banner (docs/features/change-management.md
// §4: "The countdown renders as a full-width banner"; docs/user-guide.md
// §3: "A countdown banner appears (default 2 minutes)"), plus the terminal
// outcome banner (applied/committed, rolled back, or failed) it hands off
// to once the window closes — kept in one component since both are "the
// banner that explains what's happening to the changeset you just
// applied" and share the same "survives reload" property (T-207 acceptance
// criterion 3): everything rendered here comes from the `changeset` prop
// (itself sourced fresh from GET /changesets/{id} on every mount — see
// ChangesetDrawer, which also mounts useChangesetWsBridge so a live
// `changeset.status` WS push updates this immediately without a poll).
import { useEffect, useState } from "react";
import clsx from "clsx";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import type { Changeset } from "../api/types";
import { useReducedMotion } from "../lib/useReducedMotion";
import { useChangesetDrawerStore } from "./store";
import { useConfirmChangesetMutation, useRollbackChangesetMutation } from "./queries";

function secondsRemaining(confirmDeadline: number | undefined, nowMs: number): number {
  if (confirmDeadline === undefined) return 0;
  return Math.max(0, Math.round(confirmDeadline * 1000 - nowMs) / 1000);
}

export interface CountdownBannerProps {
  changeset: Changeset;
}

export function CountdownBanner({ changeset }: CountdownBannerProps) {
  const [nowMs, setNowMs] = useState(() => Date.now());
  const confirmMutation = useConfirmChangesetMutation();
  const rollbackMutation = useRollbackChangesetMutation();
  const reset = useChangesetDrawerStore((s) => s.reset);
  const { toast } = useToast();
  // T-905: the "unconfirmed changeset" pulse this card names — a plain
  // static amber dot when `prefers-reduced-motion: reduce` is set.
  const reducedMotion = useReducedMotion();

  useEffect(() => {
    if (changeset.status !== "awaiting_confirm") return;
    const timer = setInterval(() => {
      setNowMs(Date.now());
    }, 500);
    return () => {
      clearInterval(timer);
    };
  }, [changeset.status]);

  if (changeset.status === "applying") {
    return (
      <div
        role="status"
        className="fixed inset-x-0 top-0 z-40 border-b border-sky-300 bg-sky-50 px-4 py-2 text-center text-sm text-sky-800 dark:border-sky-700 dark:bg-sky-950 dark:text-sky-200"
      >
        Applying changeset "{changeset.title}" — each step's outcome streams in as it completes.
      </div>
    );
  }

  if (changeset.status === "awaiting_confirm") {
    const remaining = secondsRemaining(changeset.confirmDeadline, nowMs);
    const expired = changeset.confirmDeadline !== undefined && remaining <= 0;
    return (
      <div
        role="alert"
        className="fixed inset-x-0 top-0 z-40 flex flex-wrap items-center justify-center gap-3 border-b border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100"
      >
        <span
          aria-hidden
          data-testid="countdown-pulse-dot"
          className={clsx(
            "h-2 w-2 shrink-0 rounded-full bg-amber-500",
            !reducedMotion && !expired && "animate-pulse",
          )}
        />
        <span className="font-medium">
          vnprox applied your change — confirm you still have connectivity{" "}
          {expired ? "(rolling back now…)" : `(${String(Math.ceil(remaining))}s remaining)`}.
        </span>
        <Button
          size="sm"
          variant="primary"
          disabled={confirmMutation.isPending}
          onClick={() => {
            confirmMutation.mutate(changeset.id, {
              onError: () => {
                toast({ title: "Could not confirm", description: "If this persists, the server will roll back at the deadline.", variant: "error" });
              },
            });
          }}
        >
          Confirm
        </Button>
        <Button
          size="sm"
          variant="destructive"
          disabled={rollbackMutation.isPending}
          onClick={() => {
            rollbackMutation.mutate(changeset.id, {
              onError: () => {
                toast({ title: "Could not roll back", variant: "error" });
              },
            });
          }}
        >
          Roll back now
        </Button>
      </div>
    );
  }

  const outcome = outcomeFor(changeset);
  if (!outcome) return null;

  return (
    <div
      role="status"
      className={clsx(
        "fixed inset-x-0 top-0 z-40 flex flex-wrap items-center justify-center gap-3 border-b px-4 py-3 text-sm",
        outcome.className,
      )}
    >
      <span className="font-medium">{outcome.message}</span>
      <Button size="sm" variant="ghost" onClick={reset}>
        Dismiss
      </Button>
    </div>
  );
}

function outcomeFor(changeset: Changeset): { message: string; className: string } | undefined {
  switch (changeset.status) {
    case "committed":
      return {
        message: `"${changeset.title}" was applied and committed.`,
        className: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-200",
      };
    case "rolled_back":
      return {
        message: `"${changeset.title}" was rolled back${changeset.applyLog?.rolledBackBy ? ` by ${changeset.applyLog.rolledBackBy}` : ""}. The failure step is preserved in the apply log for diagnosis.`,
        className: "border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200",
      };
    case "failed": {
      // T-205 routes any apply-step failure here (completed steps are
      // rolled back where possible; the rollback trail with any of its own
      // failures is in the apply log) — so the wording must not claim more
      // than "the apply failed"; the log has the specifics either way.
      const step = changeset.applyLog?.failedStep;
      return {
        message: `"${changeset.title}" failed to apply${step !== undefined ? ` at step ${String(step + 1)}` : ""} — completed steps were rolled back where possible; check the apply log.`,
        className: "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200",
      };
    }
    default:
      return undefined;
  }
}
