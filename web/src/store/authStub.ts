// SPDX-License-Identifier: Apache-2.0

// Demo-mode escape hatch for auth, originally T-005's stub for demoing the
// shell before real auth existed. Real Proxmox-credential auth landed in
// T-105 (`make dev` gives a working login against pvemock: root@pam /
// vnprox-mock), so the bypass is now OFF by default — it exists only for
// demoing the SPA with no backend at all, and must be explicitly opted
// into. Note this flag is client-side only either way: the backend still
// 401s every API call without a real session.
import { create } from "zustand";
import { persist } from "zustand/middleware";

/** Set `VITE_AUTH_STUB=true` (e.g. `VITE_AUTH_STUB=true npm run dev` in
 * web/) to show the demo-mode login bypass. Anything else — including the
 * flag being unset, as in `make dev` and production builds — keeps it
 * hidden, since real auth (T-105) is the default path. */
export const AUTH_STUB_ENABLED: boolean = import.meta.env.VITE_AUTH_STUB === "true";

interface DemoSessionState {
  demoSession: boolean;
  enterDemoMode: () => void;
  exitDemoMode: () => void;
}

export const useDemoSessionStore = create<DemoSessionState>()(
  persist(
    (set) => ({
      demoSession: false,
      enterDemoMode: () => {
        set({ demoSession: true });
      },
      exitDemoMode: () => {
        set({ demoSession: false });
      },
    }),
    { name: "vnprox.demoSession" },
  ),
);
