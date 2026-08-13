// T-2802: whether this instance is the hosted public demo.
//
// Same shape and same reasoning as demo/useDemoMode.ts: a property of the
// *instance*, fixed for the process's whole lifetime, so it is read once at
// app mount into a plain store rather than kept in a query cache designed
// for answers that change.
import { create } from "zustand";

import { fetchVisitorSession, type VisitorSession } from "./visitorApi";

interface PublicDemoState {
  /** undefined until the edge (or its absence) has answered. Distinguished
   * from null on purpose: "not a public demo" and "we do not know yet" must
   * not render the same, or the tour would flash onto a normal instance. */
  session: VisitorSession | null | undefined;
  setSession: (session: VisitorSession | null) => void;
}

export const usePublicDemoStore = create<PublicDemoState>()((set) => ({
  session: undefined,
  setSession: (session) => {
    set({ session });
  },
}));

/** True iff this instance is the hosted, read-only public demo. */
export function useIsPublicDemo(): boolean {
  return usePublicDemoStore((s) => s.session !== undefined && s.session !== null);
}

/** The same question outside React, for the api/* modules that have to
 * choose a persistence path before any component renders. Reads the store
 * rather than re-asking the edge: detectPublicDemo runs once at app mount
 * and the answer cannot change while the page is open. */
export function isPublicDemoInstance(): boolean {
  const { session } = usePublicDemoStore.getState();
  return session !== undefined && session !== null;
}

/** Asks the edge once whether this is a public demo. A normal daemon serves
 * no such route and answers 404, which fetchVisitorSession maps to null. */
export async function detectPublicDemo(): Promise<void> {
  const session = await fetchVisitorSession();
  usePublicDemoStore.getState().setSession(session);
}
