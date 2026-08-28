// SPDX-License-Identifier: Apache-2.0

// T-2805 — where the most recent staging response's advisory-lock warning
// lives until the operator has seen it.
//
// Deliberately NOT part of the persisted drawer store (store.ts, which
// zustand's `persist` writes to localStorage): a lock warning is a statement
// about who held what at one instant, and restoring one from localStorage a
// day later would show a collision that has long since expired. Nor can it
// live in the TanStack query cache under the changeset key — `locks` is only
// ever present on a create/update response, so the next `GET /changesets/{id}`
// would blank it and the warning would vanish mid-read.
//
// Ephemeral, in-memory, and cleared on every staging round trip that has
// nothing to warn about.
import { create } from "zustand";
import type { ChangesetLocks } from "../api/types";

interface LockNoticeState {
  /** The changeset the notice belongs to, so a warning from one draft never
   * renders over another. */
  changesetId: string | undefined;
  locks: ChangesetLocks | undefined;
  /** Records (or, with an empty/absent `locks`, clears) the notice for one
   * changeset. Every staging round trip calls this, so a collision that has
   * been resolved stops being shown without anyone dismissing it. */
  setNotice: (changesetId: string, locks: ChangesetLocks | undefined) => void;
  clear: () => void;
}

export const useLockNoticeStore = create<LockNoticeState>()((set) => ({
  changesetId: undefined,
  locks: undefined,
  setNotice: (changesetId, locks) => {
    const empty = !locks || ((locks.held?.length ?? 0) === 0 && (locks.overridden?.length ?? 0) === 0);
    set(empty ? { changesetId: undefined, locks: undefined } : { changesetId, locks });
  },
  clear: () => {
    set({ changesetId: undefined, locks: undefined });
  },
}));
