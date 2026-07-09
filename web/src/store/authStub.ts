// Feature flag + demo-mode escape hatch for auth, per T-005's task card:
// "feature-flag/stub the real login POST so the shell is demoable
// without a working backend auth system yet". Real Proxmox-credential
// auth lands in T-105; until then `/auth/login` and `/auth/me` 404
// against T-002's backend stub (it doesn't implement the auth routes at
// all yet), so LoginPage offers a demo-mode bypass gated by this flag.
import { create } from "zustand";
import { persist } from "zustand/middleware";

/** Set `VITE_AUTH_STUB=false` to hide the demo-mode bypass once T-105
 * lands real auth and this shell should require it end to end. */
export const AUTH_STUB_ENABLED: boolean = import.meta.env.VITE_AUTH_STUB !== "false";

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
