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
    onSuccess: () => {
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
