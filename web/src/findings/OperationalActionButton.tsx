// SPDX-License-Identifier: Apache-2.0

// Phase 36's Tier 2 ceremony, in one component.
//
// An operational remedy — install a package, start a unit, re-run a poll —
// has no changeset to stage, because there is no PVE configuration to diff.
// That absence is exactly why it needs its own ritual: without a diff to
// review, the confirmation dialog is the *only* place an operator is told
// what is about to happen and to which nodes. So this component exists
// rather than a bare `<Button onClick={mutate}>`, and remediation.ts's
// `confirms: true` is the contract it satisfies.
//
// What it does NOT do: decide whether the action is allowed (remediation.ts
// resolves nothing without `netWrite`), or perform the request (the surface
// owns its mutation, because the request shape and the result rendering
// differ per action). It owns the ceremony and nothing else.
import { useState, type ReactNode } from "react";
import { Button } from "../components/Button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "../components/Dialog";

export interface OperationalActionButtonProps {
  /** The trigger's label — from `remediationAction()`, so it stays in one
   * place per action rather than being retyped per surface. */
  label: string;
  /** The dialog heading: what is about to be done. */
  title: string;
  /** What will happen, and to which nodes. This must name the blast radius
   * — "installs lldpd on all nodes" is a different sentence from "installs
   * lldpd on pve1", and an operator who cannot tell them apart from the
   * dialog has not really been asked. */
  description: ReactNode;
  /** The affirmative button's label. Say the verb ("Install", "Start"), not
   * "OK": the last thing an operator reads before a mutation should be what
   * the mutation is. */
  confirmLabel: string;
  /** True while the mutation is in flight — disables both buttons and
   * relabels the affirmative one. */
  pending?: boolean;
  /** Rendered inside the dialog under the description: the outcome of the
   * last attempt, per node. Kept here rather than in a toast so a partial
   * failure stays on screen next to the retry, instead of disappearing
   * after a few seconds. */
  result?: ReactNode;
  onConfirm: () => void;
  /** Size of the trigger button; banners want "sm". */
  size?: "sm" | "md";
}

export function OperationalActionButton({
  label,
  title,
  description,
  confirmLabel,
  pending = false,
  result,
  onConfirm,
  size = "sm",
}: OperationalActionButtonProps) {
  const [open, setOpen] = useState(false);
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        // Never let the dialog close underneath an in-flight mutation: the
        // result would land with nothing to render it, and the operator
        // would be left with no idea whether it worked.
        if (pending) return;
        setOpen(next);
      }}
    >
      <Button
        size={size}
        variant="secondary"
        onClick={() => {
          setOpen(true);
        }}
      >
        {label}
      </Button>
      <DialogContent>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
        {result !== undefined && <div className="mt-3">{result}</div>}
        <div className="mt-4 flex justify-end gap-2">
          <DialogClose asChild>
            <Button size="sm" variant="ghost" disabled={pending}>
              Cancel
            </Button>
          </DialogClose>
          <Button size="sm" variant="primary" disabled={pending} onClick={onConfirm}>
            {pending ? "Working…" : confirmLabel}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

/** Per-node outcomes for a fan-out action, rendered honestly: a run where
 * two of five nodes failed is not "done".
 *
 * Deliberately lists successes as well as failures. An operator who asked
 * to change every node needs to see that every node was actually attempted
 * — "3 succeeded" with no list is indistinguishable from "3 of 5 were even
 * tried". */
export function NodeResultsList({ results }: { results: readonly { node: string; ok: boolean; error?: string }[] }) {
  if (results.length === 0) return null;
  const failed = results.filter((r) => !r.ok);
  return (
    <div className="text-xs">
      <p className="font-medium text-slate-700 dark:text-slate-200">
        {failed.length === 0
          ? `Succeeded on all ${String(results.length)} node${results.length === 1 ? "" : "s"}.`
          : `Failed on ${String(failed.length)} of ${String(results.length)} node${results.length === 1 ? "" : "s"}.`}
      </p>
      {/* Scrollable region, so it needs to be keyboard-reachable — same
          reason as UnrefFindingsBanner's list (axe
          `scrollable-region-focusable`, WCAG 2.1.1). This one matters
          particularly: on a large cluster the per-node failures are the
          whole point of the panel, and the ones that scroll out of view are
          exactly the ones an operator needs. */}
      <ul
        className="mt-1 max-h-40 space-y-0.5 overflow-y-auto"
        tabIndex={0}
        aria-label="Per-node results"
      >
        {results.map((r) => (
          <li key={r.node} className="flex flex-wrap items-baseline gap-1.5">
            <span
              aria-hidden
              className={`inline-block h-2 w-2 shrink-0 rounded-full ${r.ok ? "bg-emerald-500" : "bg-red-500"}`}
            />
            <span className="font-mono text-slate-700 dark:text-slate-200">{r.node}</span>
            {/* The state in words as well as in the dot's colour — WCAG
                1.4.1, the same rule the faceplate's LEDs follow. */}
            <span className={r.ok ? "text-emerald-700 dark:text-emerald-300" : "text-red-700 dark:text-red-300"}>
              {r.ok ? "ok" : "failed"}
            </span>
            {!r.ok && r.error !== undefined && r.error !== "" && (
              <span className="text-slate-600 dark:text-slate-300">— {r.error}</span>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
