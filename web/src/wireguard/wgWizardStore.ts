// One shared launch signal for the "connect two clusters" wizard (T-1402),
// mirroring mgmt/mgmtWizardStore.ts's exact pattern: every entry point
// opens the same single wizard instance (mounted once in the app shell)
// rather than each owning its own copy.
import { create } from "zustand";

export interface WgWizardRequest {
  /** Preselects the source node — optional, the wizard's own first step
   * remains fully editable either way. */
  sourceNode?: string;
}

interface WgWizardState {
  request: WgWizardRequest | undefined;
  open: (request?: WgWizardRequest) => void;
  close: () => void;
}

export const useWgWizardStore = create<WgWizardState>((set) => ({
  request: undefined,
  open: (request) => {
    set({ request: request ?? {} });
  },
  close: () => {
    set({ request: undefined });
  },
}));
