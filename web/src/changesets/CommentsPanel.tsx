// SPDX-License-Identifier: Apache-2.0

// T-2003's comment thread: per-op and changeset-level review comments. Pure
// presentation over the `comments` array GET /changesets/{id} already
// returns (see api/types.ts's Changeset.comments) — this component owns no
// server truth of its own, only the add/delete mutations.
import { useState } from "react";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import { HelpAnchor } from "../help/HelpAnchor";
import type { Changeset, ChangesetComment, Op } from "../api/types";
import { groupCommentsByOp } from "./commentGrouping";
import { opKindLabel, summarizeOp } from "./opSummary";
import { useAddCommentMutation, useDeleteCommentMutation } from "./queries";

export interface CommentsPanelProps {
  changeset: Changeset;
}

function formatTimestamp(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString();
}

/** The posted comments. `label` gives the list an accessible name, which is
 * not decoration: without it the only way to look for a comment is a bare
 * text search over the whole review dialog, and that also matches the
 * compose <textarea> next to it — React keeps a controlled textarea's typed
 * value in its `defaultValue`, i.e. its textContent, so a test (or a
 * screen-reader user walking the dialog) cannot tell "a comment that was
 * saved" from "text someone is still typing". change-review.spec.ts asserted
 * exactly that way and passed while the POST behind it was still in flight.
 * A named list is the thing that makes "this comment is on the server"
 * expressible at all. */
function CommentThread({
  changesetId,
  comments,
  label,
}: {
  changesetId: string;
  comments: ChangesetComment[];
  label: string;
}) {
  const deleteMutation = useDeleteCommentMutation();
  return (
    <ul aria-label={label} className="space-y-1.5">
      {comments.map((c) => (
        <li
          key={c.id}
          className="rounded border border-slate-200 bg-white p-2 text-xs dark:border-slate-700 dark:bg-slate-900"
        >
          <div className="flex items-center justify-between gap-2">
            <span className="font-medium text-slate-700 dark:text-slate-200">{c.author}</span>
            <div className="flex items-center gap-2">
              <span className="text-[10px] text-slate-600 dark:text-slate-400">{formatTimestamp(c.createdAt)}</span>
              <button
                type="button"
                aria-label="Delete comment"
                className="text-slate-600 dark:text-slate-400 hover:text-red-600 dark:hover:text-red-400"
                disabled={deleteMutation.isPending}
                onClick={() => {
                  deleteMutation.mutate({ id: changesetId, commentId: c.id });
                }}
              >
                ×
              </button>
            </div>
          </div>
          <p className="mt-1 whitespace-pre-wrap text-slate-600 dark:text-slate-300">{c.body}</p>
        </li>
      ))}
    </ul>
  );
}

function AddCommentForm({ changesetId, opId }: { changesetId: string; opId?: string }) {
  const [body, setBody] = useState("");
  const addMutation = useAddCommentMutation();
  const { toast } = useToast();

  async function submit(): Promise<void> {
    const trimmed = body.trim();
    if (!trimmed) return;
    try {
      await addMutation.mutateAsync({ id: changesetId, opId, body: trimmed });
      setBody("");
    } catch {
      toast({ title: "Could not add comment", variant: "error" });
    }
  }

  return (
    <div className="mt-1.5 flex items-start gap-2">
      <textarea
        value={body}
        onChange={(e) => {
          setBody(e.target.value);
        }}
        placeholder={opId ? "Comment on this operation…" : "Add a comment…"}
        aria-label={opId ? "Comment on this operation" : "Add a changeset-level comment"}
        rows={2}
        className="flex-1 rounded border border-slate-300 px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-800"
      />
      <Button
        variant="secondary"
        size="sm"
        disabled={body.trim().length === 0 || addMutation.isPending}
        onClick={() => void submit()}
      >
        Comment
      </Button>
    </div>
  );
}

/** True when opId names no op currently on the changeset — the "deleting an
 * op does not silently orphan its comment" case (T-2003): the comment
 * survives (explicit cleanup only removes it when the op that owned it is
 * itself removed from a saved edit), but its target op is gone, so the
 * thread is shown with a note rather than pretending the op is still there. */
function isOrphaned(op: Op | undefined): boolean {
  return op === undefined;
}

export function CommentsPanel({ changeset }: CommentsPanelProps) {
  const comments = changeset.comments ?? [];
  const groups = groupCommentsByOp(comments);
  const changesetLevel = groups.get("") ?? [];

  // Every current op gets a comment thread (existing comments, if any, plus
  // an add-comment form) — one canonical per-op section, not a separate
  // "existing threads" list duplicating a "pick an op" list.
  const opSections = changeset.ops.map((op) => ({
    op,
    comments: op.id ? (groups.get(op.id) ?? []) : [],
  }));

  // Any comment whose op id no longer matches a current op (removed by an
  // edit before the server's own cleanup ran, or a comment added just before
  // a concurrent edit) — shown separately so it's never simply invisible.
  const orphaned = [...groups.entries()].filter(
    ([opId, list]) => opId !== "" && list.length > 0 && isOrphaned(changeset.ops.find((o) => o.id === opId)),
  );

  return (
    <div className="space-y-4 text-sm">
      <section>
        <h3 className="flex items-center gap-1.5 text-xs font-medium text-fg-subtle">
          Changeset comments
          <HelpAnchor topic="changeset-comments" />
        </h3>
        {changesetLevel.length > 0 && (
          <div className="mt-1.5">
            <CommentThread changesetId={changeset.id} comments={changesetLevel} label="Changeset comments" />
          </div>
        )}
        <AddCommentForm changesetId={changeset.id} />
      </section>

      {opSections.length > 0 && (
        <section>
          <h3 className="text-xs font-medium text-fg-subtle">Per-operation comments</h3>
          <ul className="mt-1.5 space-y-3">
            {opSections.map(({ op, comments: opComments }, i) => (
              <li key={op.id ?? i} className="rounded border border-slate-200 p-2 dark:border-slate-700">
                <p className="text-xs font-medium text-slate-600 dark:text-slate-300">
                  <span className="mr-1 rounded bg-slate-200/70 px-1 py-0.5 text-[10px] uppercase text-fg-muted dark:bg-slate-700/70 dark:text-slate-300">
                    {opKindLabel(op)}
                  </span>
                  {summarizeOp(op)}
                </p>
                {opComments.length > 0 && (
                  <div className="mt-1.5">
                    <CommentThread
                      changesetId={changeset.id}
                      comments={opComments}
                      label={`Comments on ${summarizeOp(op)}`}
                    />
                  </div>
                )}
                {op.id ? (
                  <AddCommentForm changesetId={changeset.id} opId={op.id} />
                ) : (
                  <p className="mt-1 text-[11px] italic text-slate-600 dark:text-slate-400">Save this draft to enable commenting.</p>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      {orphaned.length > 0 && (
        <section>
          <h3 className="text-xs font-medium text-amber-700 dark:text-amber-400">
            Comments on removed operations
          </h3>
          <p className="mt-1 text-[11px] text-slate-600 dark:text-slate-400">
            These operations are no longer part of the changeset; their comment threads are kept for history.
          </p>
          <ul className="mt-1.5 space-y-3">
            {orphaned.map(([opId, list]) => (
              <li key={opId} className="rounded border border-dashed border-slate-300 p-2 dark:border-slate-700">
                <CommentThread changesetId={changeset.id} comments={list} label="Comments on a removed operation" />
              </li>
            ))}
          </ul>
        </section>
      )}

      {comments.length === 0 && changeset.ops.length === 0 && (
        <p className="text-xs text-slate-600 dark:text-slate-400">No comments yet.</p>
      )}
    </div>
  );
}
