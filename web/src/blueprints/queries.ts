// TanStack Query hooks for T-603's blueprint list/detail/save/delete/
// capture/instantiate/suggest calls (docs/api.md's Blueprints section).
// Mirrors internal/drift's queries.ts convention: one hook per API call,
// mutations invalidate the list/detail queries they affect.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  captureBlueprint,
  deleteBlueprint,
  fetchBlueprint,
  fetchBlueprints,
  instantiateBlueprint,
  saveBlueprint,
  suggestBlueprintAddress,
} from "../api/blueprints";
import type { Blueprint, CaptureBlueprintRequest, InstantiateBlueprintRequest } from "../api/types";

export const BLUEPRINTS_QUERY_KEY = ["blueprints"] as const;
export const blueprintQueryKey = (id: string) => ["blueprints", id] as const;

export function useBlueprintsQuery() {
  return useQuery({ queryKey: BLUEPRINTS_QUERY_KEY, queryFn: fetchBlueprints, staleTime: 15_000 });
}

export function useBlueprintQuery(id: string | undefined) {
  return useQuery({
    queryKey: blueprintQueryKey(id ?? ""),
    queryFn: () => fetchBlueprint(id ?? ""),
    enabled: id !== undefined,
  });
}

export function useSaveBlueprintMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (bp: Blueprint) => saveBlueprint(bp),
    onSuccess: (saved) => {
      void queryClient.invalidateQueries({ queryKey: BLUEPRINTS_QUERY_KEY });
      queryClient.setQueryData(blueprintQueryKey(saved.id), saved);
    },
  });
}

export function useDeleteBlueprintMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteBlueprint(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: BLUEPRINTS_QUERY_KEY });
    },
  });
}

export function useCaptureBlueprintMutation() {
  return useMutation({
    mutationFn: (req: CaptureBlueprintRequest) => captureBlueprint(req),
  });
}

export function useInstantiateBlueprintMutation() {
  return useMutation({
    mutationFn: ({ id, req }: { id: string; req: InstantiateBlueprintRequest }) => instantiateBlueprint(id, req),
  });
}

export function useSuggestAddressMutation() {
  return useMutation({
    mutationFn: ({ id, param }: { id: string; param: string }) => suggestBlueprintAddress(id, param),
  });
}
