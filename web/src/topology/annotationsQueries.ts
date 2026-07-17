// TanStack Query hooks for T-907's sticky-note annotations
// (internal/api/annotations.go, docs/api.md's Saved views & annotations
// section). There is one cluster-wide list (GET /annotations, no
// per-entity route — see that section's doc comment), so an entity's own
// notes are a client-side filter over the one cached list, mirroring how
// queries.ts's guest-group expansion derives per-group data from a shared
// cache rather than issuing one query per entity.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createAnnotation, deleteAnnotation, fetchAnnotations } from "../api/annotations";
import type { Annotation } from "../api/types";

export const ANNOTATIONS_QUERY_KEY = ["annotations"] as const;

export function useAnnotationsQuery() {
  return useQuery<Annotation[]>({
    queryKey: ANNOTATIONS_QUERY_KEY,
    queryFn: async () => (await fetchAnnotations()).items,
    staleTime: 15_000,
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
    mutationFn: ({ ref, content }: { ref: string; content: string }) => createAnnotation(ref, content),
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
