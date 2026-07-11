// TanStack Query hooks for the onboarding walkthrough: persisted progress
// (GET/PUT /layouts/onboarding), the protected-interfaces suggest/confirm
// pair, and the LLDP neighbor list + guided install. Topology (step 1) and
// drift (step 4) are read via topology/queries.ts's useTopologyQuery and
// drift/queries.ts's useDriftQuery directly — no separate hook needed here,
// per the task brief's "thin, easily-adaptable consumer" instruction for
// the drift-derived health step (T-602 is reshaping internal/drift's
// finding shape concurrently; this file must not add a second place that
// shape is depended on).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api/client";
import { fetchOnboardingProgress, saveOnboardingProgress } from "../api/onboarding";
import { fetchLldpNeighbors, installLldp } from "../api/lldp";
import { fetchProtectedInterfaces, fetchProtectedInterfacesSuggest, saveProtectedInterfaces } from "../api/protectedInterfaces";
import type { OnboardingProgress } from "../api/types";
import { freshOnboardingProgress } from "./onboardingMachine";

export const ONBOARDING_PROGRESS_QUERY_KEY = ["onboarding", "progress"] as const;
export const PROTECTED_INTERFACES_QUERY_KEY = ["protected-interfaces"] as const;
export const PROTECTED_INTERFACES_SUGGEST_QUERY_KEY = ["protected-interfaces", "suggest"] as const;
export const LLDP_QUERY_KEY = ["lldp"] as const;

/** GET /layouts/onboarding — a 404 (never run/saved before) resolves to a
 * fresh, step-1 progress object rather than surfacing as an error, matching
 * topology/queries.ts's useLayoutQuery convention for the identical 404
 * case. */
export function useOnboardingProgressQuery() {
  return useQuery<OnboardingProgress>({
    queryKey: ONBOARDING_PROGRESS_QUERY_KEY,
    queryFn: async () => {
      try {
        const res = await fetchOnboardingProgress();
        return res.layout;
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) {
          return freshOnboardingProgress();
        }
        throw err;
      }
    },
    staleTime: Infinity,
    retry: false,
  });
}

/** Persists progress and updates the cache optimistically-but-verified: the
 * mutation's own resolved value (echoed back by the server) becomes the new
 * cached progress, so a save that's silently coerced/rejected server-side
 * never leaves the UI showing state the backend didn't actually accept. */
export function useSaveOnboardingProgressMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (progress: OnboardingProgress) => saveOnboardingProgress(progress),
    onSuccess: (res) => {
      queryClient.setQueryData(ONBOARDING_PROGRESS_QUERY_KEY, res.layout);
    },
  });
}

export function useProtectedInterfacesQuery() {
  return useQuery({
    queryKey: PROTECTED_INTERFACES_QUERY_KEY,
    queryFn: fetchProtectedInterfaces,
    staleTime: 15_000,
  });
}

export function useProtectedInterfacesSuggestQuery() {
  return useQuery({
    queryKey: PROTECTED_INTERFACES_SUGGEST_QUERY_KEY,
    queryFn: fetchProtectedInterfacesSuggest,
    staleTime: 15_000,
  });
}

export function useSaveProtectedInterfacesMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: saveProtectedInterfaces,
    onSuccess: (res) => {
      queryClient.setQueryData(PROTECTED_INTERFACES_QUERY_KEY, res);
    },
  });
}

export function useLldpQuery() {
  return useQuery({
    queryKey: LLDP_QUERY_KEY,
    queryFn: fetchLldpNeighbors,
    staleTime: 15_000,
  });
}

export function useLldpInstallMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: installLldp,
    onSuccess: () => {
      // The neighbor list only starts populating once lldpd has actually
      // seen a few LLDPDU exchanges (T-302); invalidate rather than assume,
      // so the walkthrough's step re-checks live rather than trusting a
      // stale "empty" cache entry.
      void queryClient.invalidateQueries({ queryKey: LLDP_QUERY_KEY });
    },
  });
}
