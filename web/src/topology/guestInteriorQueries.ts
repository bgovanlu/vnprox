// TanStack Query hooks for T-1304's guest network interior inspector
// (internal/api/guestinterior.go, docs/api.md's Guest interior section).
// Mirrors annotationsQueries.ts's shape: one query per concern (toggle
// state, the interior view itself), a mutation that invalidates the
// toggle query on success.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchGuestInterior, fetchGuestInteriorToggle, setGuestInteriorToggle } from "../api/guestInterior";
import { ApiError } from "../api/client";
import type { GuestInterior, GuestInteriorToggle } from "../api/types";

export function guestInteriorToggleKey(ref: string) {
  return ["guestInteriorToggle", ref] as const;
}

export function guestInteriorKey(ref: string) {
  return ["guestInterior", ref] as const;
}

/** The opt-in toggle's current state — always fetched, regardless of
 * whether it's on, since the InteriorTab needs it to decide what to
 * render (the opt-in copy, or the interior view itself). */
export function useGuestInteriorToggleQuery(ref: string | undefined) {
  return useQuery<GuestInteriorToggle>({
    queryKey: guestInteriorToggleKey(ref ?? ""),
    queryFn: () => fetchGuestInteriorToggle(ref ?? ""),
    enabled: ref !== undefined,
    staleTime: 15_000,
  });
}

export function useSetGuestInteriorToggleMutation(ref: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) => setGuestInteriorToggle(ref, enabled),
    // Write the known-new value into the cache BEFORE invalidating.
    //
    // NOT the fix for T-2108's guest-interior failure — that was a 400 from the
    // API (see internal/api/guestinterior.go's parseGuestRef), and the e2e spec
    // passes with or without this change. Recorded plainly because attributing
    // it to the bug it did not fix would be worse than not making it.
    //
    // It is still correct on its own terms. InteriorTab holds an optimistic
    // `pendingToggle` and clears it in the mutation's `onSettled`, while
    // `invalidateQueries` only *schedules* a refetch. Between those two there
    // is a window where the optimistic value is gone and `toggleQuery.data`
    // still holds the old state, so the checkbox renders its previous value
    // before flipping back. setQueryData closes that window deterministically;
    // the invalidate still runs, to reconcile against what the server stored.
    //
    // The window is real by construction but was never observed to be
    // user-visible — React may batch it away entirely. Left in as a correctness
    // improvement, not as a fix for a reported symptom.
    onSuccess: (_result, enabled) => {
      queryClient.setQueryData<GuestInteriorToggle>(guestInteriorToggleKey(ref), { ref, enabled });
      void queryClient.invalidateQueries({ queryKey: guestInteriorToggleKey(ref) });
      void queryClient.invalidateQueries({ queryKey: guestInteriorKey(ref) });
    },
  });
}

/** The interior read set itself — only fetched once the toggle is known
 * to be on (`enabled` gate), so flipping the toggle off never leaves a
 * stale "reaching into the guest" request in flight. A 404
 * `interior_not_enabled` response (the toggle flipped off — or was never
 * on — between the toggle query resolving and this one running) is
 * treated as "no data" rather than surfaced as a fetch error, since it is
 * an expected, non-exceptional state this hook's own `enabled` gate can
 * still race. */
export function useGuestInteriorQuery(ref: string | undefined, toggleEnabled: boolean) {
  return useQuery<GuestInterior | undefined>({
    queryKey: guestInteriorKey(ref ?? ""),
    queryFn: async () => {
      try {
        return await fetchGuestInterior(ref ?? "");
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) {
          return undefined;
        }
        throw err;
      }
    },
    enabled: ref !== undefined && toggleEnabled,
    staleTime: 5_000,
  });
}
