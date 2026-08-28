// SPDX-License-Identifier: Apache-2.0

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
  importBlueprintBundle,
  instantiateBlueprint,
  saveBlueprint,
  suggestBlueprintAddress,
} from "../api/blueprints";
import type { Blueprint, CaptureBlueprintRequest, ImportBundleRequest, InstantiateBlueprintRequest } from "../api/types";

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

/** T-1107's bundle import (BlueprintImportDialog): a plain probe call (no
 * trust flags) and a trust-confirmed retry both go through this same
 * mutation — the response's `status` is what distinguishes them, not a
 * separate hook. A successful ("imported") response invalidates the list
 * query the same way useSaveBlueprintMutation does, since it saves a new
 * blueprint under the hood. */
export function useImportBundleMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: ImportBundleRequest) => importBlueprintBundle(req),
    onSuccess: (resp) => {
      if (resp.status === "imported") {
        void queryClient.invalidateQueries({ queryKey: BLUEPRINTS_QUERY_KEY });
      }
    },
  });
}
