// SPDX-License-Identifier: Apache-2.0

// Changeset drawer UI state: which draft is currently "open" in the drawer,
// whether the drawer/review screen is visible, and the "apply with
// warnings" acknowledgment. Persisted to localStorage (zustand's `persist`
// middleware, same pattern as src/store/theme.ts) so a parked draft and an
// in-flight countdown survive a page reload (T-207 acceptance criterion 3:
// "Countdown banner survives reload mid-window") — the actual changeset
// data (ops/findings/status/confirmDeadline) always comes fresh from GET
// /changesets/{id} (see queries.ts), this store only remembers *which id*
// to ask for.
import { create } from "zustand";
import { persist } from "zustand/middleware";

interface ChangesetDrawerState {
  /** The changeset id currently shown in the drawer, or undefined if
   * nothing is being drafted. Set on creating a new draft, on resuming a
   * parked one, and on every op-adding action from anywhere in the app
   * (map drag-drop, entity editors, bulk guest reattach). */
  activeId: string | undefined;
  /** Whether the drawer panel itself is expanded. Distinct from
   * `activeId` so a parked draft can exist without the drawer being open. */
  drawerOpen: boolean;
  /** True once the user has clicked "Review & apply"; false again on
   * "back to drafting", discard, or once apply moves past draft/validated
   * (see drawerMachine.computeDrawerView, which ignores this once the
   * server status itself has moved past editable). */
  reviewRequested: boolean;
  /** The review screen's "apply with warnings" checkbox (docs/features/
   * change-management.md §2). Reset whenever a new draft becomes active or
   * review is re-entered after an edit, since a stale acknowledgment for a
   * since-changed op list would be unsafe to carry forward silently. */
  warningsAcknowledged: boolean;

  setActiveId: (id: string | undefined) => void;
  setDrawerOpen: (open: boolean) => void;
  openReview: () => void;
  closeReview: () => void;
  setWarningsAcknowledged: (ack: boolean) => void;
  /** Clears everything — called after a discard, or after an applied
   * changeset reaches a terminal state and the user dismisses the outcome. */
  reset: () => void;
}

export const useChangesetDrawerStore = create<ChangesetDrawerState>()(
  persist(
    (set) => ({
      activeId: undefined,
      drawerOpen: false,
      reviewRequested: false,
      warningsAcknowledged: false,

      setActiveId: (id) => {
        set({ activeId: id, drawerOpen: id !== undefined, reviewRequested: false, warningsAcknowledged: false });
      },
      setDrawerOpen: (open) => {
        set({ drawerOpen: open });
      },
      openReview: () => {
        set({ reviewRequested: true, warningsAcknowledged: false });
      },
      closeReview: () => {
        set({ reviewRequested: false });
      },
      setWarningsAcknowledged: (ack) => {
        set({ warningsAcknowledged: ack });
      },
      reset: () => {
        set({ activeId: undefined, drawerOpen: false, reviewRequested: false, warningsAcknowledged: false });
      },
    }),
    { name: "vnprox.changesetDrawer" },
  ),
);
