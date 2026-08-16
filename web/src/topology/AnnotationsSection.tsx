// T-907's sticky-note annotations, rendered wherever an entity's own
// detail is shown (InspectorPanel's "Notes" tab) — there is no on-canvas
// rendering of individual notes; per docs/api.md's Saved views &
// annotations section, "the frontend renders an entity's pinned notes
// wherever that entity's own detail is shown", which this component is.
import { useState } from "react";
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import { useAnnotationsForRef, useCreateAnnotationMutation, useDeleteAnnotationMutation } from "./annotationsQueries";

export interface AnnotationsSectionProps {
  /** The pinned entity's Ref string (never a guest-group synthetic id —
   * InspectorPanel only mounts this for a real inventory ref). */
  entityRef: string;
}

const MAX_CONTENT_LENGTH = 4000;

/** T-2806: shown on a note whose entity no longer exists. The note is kept
 * rather than dropped — it is often the only record of why the entity was
 * removed — so the UI has to say what happened to what it was pinned to. */
export const ORPHAN_BADGE = "Entity no longer exists";

/** Expiry options offered when pinning a note, in days. "Never" is the
 * default: a note only self-destructs when the author says so. */
const EXPIRY_CHOICES: { label: string; days: number }[] = [
  { label: "Never expires", days: 0 },
  { label: "Expires in 7 days", days: 7 },
  { label: "Expires in 30 days", days: 30 },
  { label: "Expires in 90 days", days: 90 },
];

function expiryLabel(expiresAt: number): string {
  return `expires ${new Date(expiresAt * 1000).toISOString().slice(0, 10)}`;
}

/** Free-text sticky notes pinned to one map entity: list, pin, unpin.
 * Shared across every user (docs/data-model.md §2: "a shared team
 * scratchpad, not private per-user data like layouts") — every note shows
 * who pinned it, but any netRead-capable user can unpin any note. */
export function AnnotationsSection({ entityRef }: AnnotationsSectionProps) {
  const notes = useAnnotationsForRef(entityRef);
  const createMutation = useCreateAnnotationMutation();
  const deleteMutation = useDeleteAnnotationMutation();
  const [draft, setDraft] = useState("");
  const [expiryDays, setExpiryDays] = useState(0);

  function handlePin(): void {
    const content = draft.trim();
    if (content === "") return;
    // The expiry instant is computed here only to be STORED; whether a note
    // is expired is decided by the daemon on every read (T-2806 AC3), never
    // by this component's clock.
    const expiresAt = expiryDays === 0 ? 0 : Math.floor(Date.now() / 1000) + expiryDays * 86_400;
    createMutation.mutate(
      { ref: entityRef, content, expiresAt },
      {
        onSuccess: () => {
          setDraft("");
          setExpiryDays(0);
        },
      },
    );
  }

  return (
    <div className="space-y-3 text-xs">
      <p className="flex items-center gap-1.5 text-slate-400">
        <span>Sticky notes pinned to this entity — visible to every user, never a copy of any PVE config.</span>
        <HelpAnchor topic="map-annotations" />
      </p>
      <ul className="space-y-2">
        {notes.map((note) => (
          <li key={note.id} className="rounded border border-slate-200 p-2 dark:border-slate-700">
            {note.orphaned && (
              <p className="mb-1 rounded bg-amber-100 px-1 py-0.5 text-[10px] font-medium text-amber-800 dark:bg-amber-950 dark:text-amber-200">
                {ORPHAN_BADGE}
              </p>
            )}
            {/* The note text. Rendered as a text child, never as HTML: the
                content is free text ANOTHER operator typed, and this is one
                of the render paths T-2806 AC6 requires to escape it. */}
            <p className="whitespace-pre-wrap text-slate-700 dark:text-slate-200">{note.content}</p>
            <div className="mt-1 flex items-center justify-between text-slate-400">
              <span>
                {note.createdBy}
                {note.expiresAt !== 0 && <span className="ml-2">{expiryLabel(note.expiresAt)}</span>}
              </span>
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
        <div className="flex items-center gap-2">
          <label htmlFor="annotation-expiry" className="sr-only">
            Note expiry
          </label>
          <select
            id="annotation-expiry"
            value={expiryDays}
            onChange={(e) => {
              setExpiryDays(Number(e.target.value));
            }}
            className="rounded border border-slate-200 bg-white p-1 text-xs text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200"
          >
            {EXPIRY_CHOICES.map((choice) => (
              <option key={choice.days} value={choice.days}>
                {choice.label}
              </option>
            ))}
          </select>
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
    </div>
  );
}
