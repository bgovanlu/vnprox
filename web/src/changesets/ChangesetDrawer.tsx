// The persistent changeset drawer (docs/features/change-management.md §1;
// docs/user-guide.md §3: "Edits collect in the change drawer (bottom
// right)"). Deliberately NOT built on the Radix Dialog-based <Drawer/>
// primitive (components/Drawer.tsx) — that primitive is modal (overlay +
// focus trap), but this drawer must stay usable *while* the user keeps
// editing anywhere else in the UI (map drag-drops, form edits, firewall
// rule changes all "land here" without interrupting what the user was
// doing). It's a plain always-mounted floating panel instead; only the
// Review & apply screen (ReviewApplyScreen.tsx) and the entity editors are
// modal.
import { useMemo, useState } from "react";
import clsx from "clsx";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import type { Finding, Op } from "../api/types";
import { usePaletteActions, type PaletteAction } from "../keyboard/actions";
import { useNarrowViewport } from "../lib/useNarrowViewport";
import { canReview, computeDrawerView, isDraftEditable } from "./drawerMachine";
import { opKindLabel, summarizeOp } from "./opSummary";
import {
  useChangesetQuery,
  useChangesetWsBridge,
  useDiscardChangesetMutation,
  useResumableDraftsQuery,
} from "./queries";
import { useChangesetDrawerStore } from "./store";
import { useDrawerActions } from "./useDrawerActions";
import { CountdownBanner } from "./CountdownBanner";
import { FixButton } from "./FixButton";
import { ReviewApplyScreen } from "./ReviewApplyScreen";

function findingsForOp(findings: Finding[], op: Op): Finding[] {
  if (!op.target) return [];
  return findings.filter((f) => f.ref === op.target);
}

function severityBadgeClass(severity: Finding["severity"]): string {
  switch (severity) {
    case "error":
      return "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300";
    case "warning":
      return "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300";
    default:
      return "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300";
  }
}

/** Mount once, near the app root (AppShell), so it persists across route
 * navigation — the drawer is not scoped to any one page. */
export function ChangesetDrawer() {
  useChangesetWsBridge();

  const narrow = useNarrowViewport();
  const activeId = useChangesetDrawerStore((s) => s.activeId);
  const drawerOpen = useChangesetDrawerStore((s) => s.drawerOpen);
  const setDrawerOpen = useChangesetDrawerStore((s) => s.setDrawerOpen);
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);
  const reviewRequested = useChangesetDrawerStore((s) => s.reviewRequested);
  const openReview = useChangesetDrawerStore((s) => s.openReview);
  const closeReview = useChangesetDrawerStore((s) => s.closeReview);

  const { data: changeset } = useChangesetQuery(activeId);
  const { data: resumable } = useResumableDraftsQuery();
  const discardMutation = useDiscardChangesetMutation();
  const { replaceOps } = useDrawerActions();
  const { toast } = useToast();
  const [resumeMenuOpen, setResumeMenuOpen] = useState(false);

  const view = computeDrawerView(changeset, reviewRequested);
  const editable = isDraftEditable(changeset);
  // T-909: reordering/removing ops and discarding a draft are "editing
  // entities" per the task card's objective — desktop-only, even though
  // the drawer itself (and "Review & apply", a pure UI-state transition
  // with no server call) stays reachable at narrow width so an
  // already-drafted changeset can still be taken through review, apply,
  // and the confirm/rollback ceremony from a phone.
  const editUiEnabled = editable && !narrow;
  const otherDrafts = (resumable ?? []).filter((c) => c.id !== activeId);

  // T-903 command-palette verb: "Open drafts" — this drawer is mounted once
  // app-wide (not scoped to any one route, per this file's own doc comment
  // above), so the verb is reachable from ⌘K on every page, not just
  // wherever a draft happened to originate.
  const changesetPaletteActions = useMemo<PaletteAction[]>(
    () => [
      {
        id: "open-drafts",
        label: "Open drafts",
        hint: "Changesets",
        perform: () => {
          setDrawerOpen(true);
        },
      },
    ],
    [setDrawerOpen],
  );
  usePaletteActions("changesets", changesetPaletteActions);

  if (view === "empty" && otherDrafts.length === 0) {
    return null;
  }

  function moveOp(index: number, direction: -1 | 1): void {
    if (!changeset) return;
    const next = [...changeset.ops];
    const targetIndex = index + direction;
    if (targetIndex < 0 || targetIndex >= next.length) return;
    const a = next[index];
    const b = next[targetIndex];
    if (!a || !b) return;
    next[index] = b;
    next[targetIndex] = a;
    void replaceOps(next).catch(() => {
      toast({ title: "Could not reorder", description: "The change list wasn't updated — try again.", variant: "error" });
    });
  }

  function removeOp(index: number): void {
    if (!changeset) return;
    const next = changeset.ops.filter((_, i) => i !== index);
    void replaceOps(next).catch(() => {
      toast({ title: "Could not remove op", variant: "error" });
    });
  }

  async function handleDiscard(): Promise<void> {
    if (!activeId) return;
    try {
      await discardMutation.mutateAsync(activeId);
      setActiveId(undefined);
      toast({ title: "Draft discarded" });
    } catch {
      toast({ title: "Could not discard draft", variant: "error" });
    }
  }

  return (
    <>
      <div
        role="region"
        aria-label="Change drawer"
        className={clsx(
          // T-909: below `sm` (640px), anchoring via `right-4` alone while
          // `w-full` resolves against the viewport pushes the box's left
          // edge off-screen (right offset + full width > viewport width on
          // a phone) — clipping the drawer's content. `inset-x-4` fixes the
          // width from both sides instead, so the browser never needs to
          // solve for an overflowing implicit width; `sm:` restores the
          // original bottom-right-anchored max-w-sm box once there's room.
          "fixed inset-x-4 bottom-4 z-30 flex flex-col overflow-hidden rounded-lg border shadow-xl",
          "sm:inset-x-auto sm:left-auto sm:right-4 sm:w-full sm:max-w-sm",
          "border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-900",
        )}
      >
        <button
          type="button"
          onClick={() => {
            setDrawerOpen(!drawerOpen);
          }}
          className="flex items-center justify-between gap-2 bg-slate-50 px-3 py-2 text-left text-sm font-medium dark:bg-slate-800/60"
        >
          <span>
            {changeset ? changeset.title || "Untitled draft" : "Changes"}
            {changeset && changeset.ops.length > 0 && (
              <span className="ml-2 rounded-full bg-accent-600/15 px-1.5 py-0.5 text-xs text-accent-700 dark:text-accent-300">
                {changeset.ops.length}
              </span>
            )}
          </span>
          <span aria-hidden>{drawerOpen ? "▾" : "▴"}</span>
        </button>

        {drawerOpen && (
          <div className="flex max-h-[60vh] flex-col gap-2 overflow-y-auto p-3 text-sm">
            {otherDrafts.length > 0 && (
              <div>
                <button
                  type="button"
                  className="text-xs text-accent-700 underline dark:text-accent-400"
                  onClick={() => {
                    setResumeMenuOpen(!resumeMenuOpen);
                  }}
                >
                  {resumeMenuOpen ? "Hide" : "Resume"} parked drafts ({otherDrafts.length})
                </button>
                {resumeMenuOpen && (
                  <ul className="mt-1 space-y-1">
                    {otherDrafts.map((d) => (
                      <li key={d.id}>
                        <button
                          type="button"
                          className="w-full rounded px-1.5 py-1 text-left text-xs hover:bg-slate-100 dark:hover:bg-slate-800"
                          onClick={() => {
                            setActiveId(d.id);
                            setResumeMenuOpen(false);
                          }}
                        >
                          {d.title || "Untitled draft"} ({d.ops.length} ops, {d.status})
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            )}

            {!changeset && <p className="text-xs text-slate-400">No active draft. Edit anything to start one.</p>}

            {changeset && changeset.ops.length === 0 && (
              <p className="text-xs text-slate-400">This draft has no ops yet.</p>
            )}

            {changeset && changeset.ops.length > 0 && (
              <ul className="space-y-1.5">
                {changeset.ops.map((op, i) => {
                  const opFindings = findingsForOp(changeset.findings, op);
                  return (
                    <li key={`${op.op}-${op.target ?? "none"}-${String(i)}`} className="rounded border border-slate-200 p-2 dark:border-slate-700">
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                          <span className="mr-1 shrink-0 rounded bg-slate-200/70 px-1 py-0.5 text-[10px] uppercase text-slate-500 dark:bg-slate-700/70 dark:text-slate-300">
                            {opKindLabel(op)}
                          </span>
                          <span className="text-xs text-slate-700 dark:text-slate-200">{summarizeOp(op)}</span>
                        </div>
                        {editUiEnabled && (
                          <div className="flex shrink-0 gap-0.5">
                            <button
                              type="button"
                              aria-label="Move up"
                              className="px-1 text-slate-400 hover:text-slate-700 disabled:opacity-30 dark:hover:text-slate-200"
                              disabled={i === 0}
                              onClick={() => {
                                moveOp(i, -1);
                              }}
                            >
                              ↑
                            </button>
                            <button
                              type="button"
                              aria-label="Move down"
                              className="px-1 text-slate-400 hover:text-slate-700 disabled:opacity-30 dark:hover:text-slate-200"
                              disabled={i === changeset.ops.length - 1}
                              onClick={() => {
                                moveOp(i, 1);
                              }}
                            >
                              ↓
                            </button>
                            <button
                              type="button"
                              aria-label="Remove"
                              className="px-1 text-red-500 hover:text-red-700"
                              onClick={() => {
                                removeOp(i);
                              }}
                            >
                              ✕
                            </button>
                          </div>
                        )}
                      </div>
                      {opFindings.length > 0 && (
                        <ul className="mt-1 space-y-0.5">
                          {opFindings.map((f, fi) => (
                            <li key={fi}>
                              <span className={clsx("rounded px-1 py-0.5 text-[10px] font-medium", severityBadgeClass(f.severity))}>
                                {f.severity}
                              </span>{" "}
                              <span className="text-[11px] text-slate-500 dark:text-slate-400">{f.message}</span>
                              {f.fix && editUiEnabled && <FixButton changeset={changeset} fix={f.fix} />}
                            </li>
                          ))}
                        </ul>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}

            {/* T-909: reordering, removing, and discarding ops is desktop-only
             * "editing entities" — say so explicitly rather than just
             * disabling the buttons below with no explanation. Reviewing and
             * applying an already-drafted changeset stays available (it's
             * part of the confirm/rollback ceremony this task must not
             * degrade), so "Review & apply" is deliberately NOT gated here. */}
            {narrow && changeset && changeset.ops.length > 0 && (
              <p className="text-[11px] text-slate-400">
                Reorder, remove, or discard ops on desktop — review and apply this draft as-is from here.
              </p>
            )}

            {changeset && (
              <div className="mt-1 flex gap-2">
                <Button
                  size="sm"
                  variant="primary"
                  disabled={!canReview(changeset)}
                  onClick={openReview}
                >
                  Review &amp; apply
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={!editable || narrow}
                  title={narrow && editable ? "Discard this draft on desktop." : undefined}
                  onClick={() => void handleDiscard()}
                >
                  Discard
                </Button>
              </div>
            )}
          </div>
        )}
      </div>

      {(view === "awaiting_confirm" || view === "applying" || view === "done") && changeset && (
        <CountdownBanner changeset={changeset} />
      )}

      {view === "reviewing" && changeset && <ReviewApplyScreen changeset={changeset} onClose={closeReview} />}
    </>
  );
}
