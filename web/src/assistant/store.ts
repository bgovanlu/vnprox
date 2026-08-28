// SPDX-License-Identifier: Apache-2.0

// T-2808: the assistant panel's open/closed state.
//
// A store rather than a prop, for the same reason help/store.ts is one: the
// panel is opened from the top bar today and could be opened from a finding
// or an entity tomorrow, and neither should need a provider above it.
//
// NOTHING about a conversation lives here. The question, the answer, and the
// tool evidence are component state that dies with the panel — they are
// never persisted, never put in a zustand `persist` store, and never sent to
// vnproxd (AC6).
import { create } from "zustand";

interface AssistantState {
  open: boolean;
  openPanel: () => void;
  closePanel: () => void;
}

export const useAssistantStore = create<AssistantState>()((set) => ({
  open: false,
  openPanel: () => {
    set({ open: true });
  },
  closePanel: () => {
    set({ open: false });
  },
}));
