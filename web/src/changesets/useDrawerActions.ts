// The one entry point every op-producing feature (entity editors, map
// drag-drop, bulk guest reattach) calls to land an op in the drawer —
// "Edits collect in the change drawer" (docs/user-guide.md §3) needs a
// single accumulation path so the drawer, the review screen, and the
// server's own ops array never disagree about what's pending.
import { useQueryClient } from "@tanstack/react-query";
import { getChangeset } from "../api/changesets";
import type { Changeset, Op } from "../api/types";
import { isDraftEditable } from "./drawerMachine";
import { changesetKey, useCreateChangesetMutation, useUpdateChangesetMutation } from "./queries";
import { useChangesetDrawerStore } from "./store";

export interface DrawerActions {
  /** Appends `ops` to the currently-active draft, creating a new one (with
   * `titleIfNew`) if none is active or the active one is no longer
   * editable (already applied/discarded — its id is simply superseded by a
   * fresh draft, matching "editing never mutates anything until apply"). */
  addOps: (ops: Op[], titleIfNew?: string) => Promise<Changeset>;
  /** Replaces the active draft's ops wholesale (reorder/remove — the
   * drawer's own list-editing actions, which always operate on the full
   * array since PUT /changesets/{id} replaces it). Throws if there is no
   * active draft. */
  replaceOps: (ops: Op[]) => Promise<Changeset>;
  /** Replaces the trailing `count` ops of the active draft with `ops` —
   * the editor re-submit path (useEditorSubmit): a previous submit's
   * erroring op(s) sit at the tail (addOps appends), and re-submitting the
   * corrected form must amend them in place rather than append duplicates.
   * Falls back to a plain append when there's no active editable draft
   * anymore (e.g. the user discarded it while the editor stayed open). */
  amendLastOps: (count: number, ops: Op[], titleIfNew?: string) => Promise<Changeset>;
}

export function useDrawerActions(): DrawerActions {
  const queryClient = useQueryClient();
  const activeId = useChangesetDrawerStore((s) => s.activeId);
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);
  const createMutation = useCreateChangesetMutation();
  const updateMutation = useUpdateChangesetMutation();

  async function currentChangeset(id: string): Promise<Changeset> {
    const cached = queryClient.getQueryData<Changeset>(changesetKey(id));
    if (cached) return cached;
    return getChangeset(id);
  }

  async function addOps(ops: Op[], titleIfNew = "Untitled draft"): Promise<Changeset> {
    if (activeId) {
      const current = await currentChangeset(activeId);
      if (isDraftEditable(current)) {
        const updated = await updateMutation.mutateAsync({ id: activeId, ops: [...current.ops, ...ops] });
        return updated;
      }
    }
    const created = await createMutation.mutateAsync({ title: titleIfNew, ops });
    setActiveId(created.id);
    return created;
  }

  async function replaceOps(ops: Op[]): Promise<Changeset> {
    if (!activeId) {
      throw new Error("useDrawerActions.replaceOps: no active draft");
    }
    return updateMutation.mutateAsync({ id: activeId, ops });
  }

  async function amendLastOps(count: number, ops: Op[], titleIfNew = "Untitled draft"): Promise<Changeset> {
    if (activeId && count > 0) {
      const current = await currentChangeset(activeId);
      if (isDraftEditable(current) && current.ops.length >= count) {
        const kept = current.ops.slice(0, current.ops.length - count);
        return updateMutation.mutateAsync({ id: activeId, ops: [...kept, ...ops] });
      }
    }
    return addOps(ops, titleIfNew);
  }

  return { addOps, replaceOps, amendLastOps };
}
