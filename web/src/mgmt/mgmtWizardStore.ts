// SPDX-License-Identifier: Apache-2.0

// One shared launch signal for T-703's management-redundancy wizard, so
// every entry point — the mgmt_single_path finding, the inspector's
// "Management path" section, and the topology New menu — opens the same
// single wizard instance (mounted once in the app shell) rather than each
// owning its own copy. Mirrors editorLauncherStore's pattern exactly.
import { create } from "zustand";

export interface MgmtWizardRequest {
  /** The node whose management path this run targets. */
  node: string;
}

interface MgmtWizardState {
  request: MgmtWizardRequest | undefined;
  open: (request: MgmtWizardRequest) => void;
  close: () => void;
}

export const useMgmtWizardStore = create<MgmtWizardState>((set) => ({
  request: undefined,
  open: (request) => {
    set({ request });
  },
  close: () => {
    set({ request: undefined });
  },
}));
