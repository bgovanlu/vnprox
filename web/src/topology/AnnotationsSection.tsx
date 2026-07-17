// T-907's sticky-note annotations, rendered wherever an entity's own
// detail is shown (InspectorPanel's "Notes" tab) — there is no on-canvas
// rendering of individual notes; per docs/api.md's Saved views &
// annotations section, "the frontend renders an entity's pinned notes
// wherever that entity's own detail is shown", which this component is.
import { useState } from "react";
import { Button } from "../components/Button";
import { useAnnotationsForRef, useCreateAnnotationMutation, useDeleteAnnotationMutation } from "./annotationsQueries";

export interface AnnotationsSectionProps {
  /** The pinned entity's Ref string (never a guest-group synthetic id —
   * InspectorPanel only mounts this for a real inventory ref). */
  entityRef: string;
}

const MAX_CONTENT_LENGTH = 4000;

/** Free-text sticky notes pinned to one map entity: list, pin, unpin.
 * Shared across every user (docs/data-model.md §2: "a shared team
 * scratchpad, not private per-user data like layouts") — every note shows
 * who pinned it, but any netRead-capable user can unpin any note. */
export function AnnotationsSection({ entityRef }: AnnotationsSectionProps) {
  const notes = useAnnotationsForRef(entityRef);
  const createMutation = useCreateAnnotationMutation();
  const deleteMutation = useDeleteAnnotationMutation();
  const [draft, setDraft] = useState("");

  function handlePin(): void {
    const content = draft.trim();
    if (content === "") return;
    createMutation.mutate(
      { ref: entityRef, content },
      {
        onSuccess: () => {
          setDraft("");
        },
      },
    );
  }

  return (
    <div className="space-y-3 text-xs">
      <p className="text-slate-400">
        Sticky notes pinned to this entity — visible to every user, never a copy of any PVE config.
      </p>
      <ul className="space-y-2">
        {notes.map((note) => (
          <li key={note.id} className="rounded border border-slate-200 p-2 dark:border-slate-700">
            <p className="whitespace-pre-wrap text-slate-700 dark:text-slate-200">{note.content}</p>
            <div className="mt-1 flex items-center justify-between text-slate-400">
              <span>{note.createdBy}</span>
              <Button
                size="sm"
                variant="ghost"
                aria-label={`Delete note: ${note.content}`}
                onClick={() => {
                  deleteMutation.mutate(note.id);
                }}
              >
                Delete
              </Button>
            </div>
          </li>
        ))}
        {notes.length === 0 && <li className="text-slate-400">No notes pinned to this entity yet.</li>}
      </ul>
      <div className="space-y-1.5">
        <label htmlFor="annotation-draft" className="sr-only">
          New note
        </label>
        <textarea
          id="annotation-draft"
          value={draft}
          onChange={(e) => {
            setDraft(e.target.value);
          }}
          rows={2}
          maxLength={MAX_CONTENT_LENGTH}
          placeholder="Pin a note to this entity…"
          className="w-full rounded border border-slate-200 bg-white p-2 text-xs text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200"
        />
        <Button
          size="sm"
          variant="secondary"
          disabled={draft.trim() === "" || createMutation.isPending}
          onClick={handlePin}
        >
          Pin note
        </Button>
      </div>
    </div>
  );
}
