// TanStack Query hooks for T-907's sticky-note annotations
// (internal/api/annotations.go, docs/api.md's Saved views & annotations
// section). There is one cluster-wide list (GET /annotations, no
// per-entity route — see that section's doc comment), so an entity's own
// notes are a client-side filter over the one cached list, mirroring how
// queries.ts's guest-group expansion derives per-group data from a shared
// cache rather than issuing one query per entity.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createAnnotation,
  createMapRegion,
  deleteAnnotation,
  deleteMapRegion,
  fetchAnnotations,
  fetchMapRegions,
} from "../api/annotations";
import type { Annotation, MapRegion } from "../api/types";

export const ANNOTATIONS_QUERY_KEY = ["annotations"] as const;
export const MAP_REGIONS_QUERY_KEY = ["map-regions"] as const;

export function useAnnotationsQuery() {
  return useQuery<Annotation[]>({
    queryKey: ANNOTATIONS_QUERY_KEY,
    queryFn: async () => (await fetchAnnotations()).items,
    staleTime: 15_000,
  });
}

/** T-2806's canvas regions. Keyed independently of `layouts` — a layout
 * save never invalidates or rewrites this cache, which is the client-side
 * half of "regions persist across layout changes and view switches"
 * (the server-side half is that they live in their own shared table). */
export function useMapRegionsQuery() {
  return useQuery<MapRegion[]>({
    queryKey: MAP_REGIONS_QUERY_KEY,
    queryFn: async () => (await fetchMapRegions()).items,
    staleTime: 15_000,
  });
}

export function useCreateMapRegionMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { label: string; x: number; y: number; w: number; h: number; color?: string; expiresAt?: number }) =>
      createMapRegion(input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: MAP_REGIONS_QUERY_KEY });
    },
  });
}

export function useDeleteMapRegionMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteMapRegion(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: MAP_REGIONS_QUERY_KEY });
    },
  });
}

/** One entity's pinned notes, filtered client-side from the shared list.
 * `ref` undefined (nothing selected) simply yields no notes, no separate
 * query. */
export function useAnnotationsForRef(ref: string | undefined): Annotation[] {
  const { data } = useAnnotationsQuery();
  if (!data || ref === undefined) return [];
  return data.filter((a) => a.ref === ref);
}

export function useCreateAnnotationMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ ref, content, expiresAt }: { ref: string; content: string; expiresAt?: number }) =>
      createAnnotation(ref, content, expiresAt ?? 0),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ANNOTATIONS_QUERY_KEY });
    },
  });
}

export function useDeleteAnnotationMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteAnnotation(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ANNOTATIONS_QUERY_KEY });
    },
  });
}
