// SPDX-License-Identifier: Apache-2.0

// Small shared status dot for the SDN cockpit tree/detail — same
// ok/down/degraded/unknown -> neutral/critical/degraded/neutral mapping the
// topology map uses (EntityNode.tsx's STATUS_CLASSES), so a zone's error
// node reads the same color whether you're looking at the tree or the map
// (T-401 acceptance criterion 4). T-4204: the down/degraded cases are the
// semantic status scale's bare tokens rather than hand-picked red/amber;
// ok/unknown stay neutral slate, matching STATUS_CLASSES' own choice not to
// paint every healthy/unreported node green (see that file's comment).
import clsx from "clsx";
import type { EntityStatus } from "../api/types";

const DOT_CLASSES: Record<EntityStatus, string> = {
  ok: "bg-slate-400 dark:bg-slate-500",
  down: "bg-status-critical",
  degraded: "bg-status-degraded",
  unknown: "bg-slate-300 dark:bg-slate-600",
};

export function StatusDot({ status, className }: { status: EntityStatus; className?: string }) {
  return (
    <span
      aria-hidden
      className={clsx("inline-block h-2 w-2 shrink-0 rounded-full", DOT_CLASSES[status], className)}
    />
  );
}
