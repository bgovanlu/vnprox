// TanStack Query hooks for T-907's named saved views, built on the
// existing per-user `layouts` mechanism (docs/api.md's Saved views &
// annotations section) rather than a dedicated views table — see
// docs/data-model.md §2's note on why `layouts` was reused here.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../api/client";
import { deleteLayout, listLayouts, saveLayout } from "../api/layouts";
import { fromSavedViewPayload, isSavedViewPayload, toSavedViewPayload, type SavedViewState } from "./savedViews";

/** Distinct from queries.ts's per-name `layoutKey` (["layouts", name]) —
 * this lists every saved view, not one named layout. Collides only if a
 * user names a saved view literally "list", an acceptable, harmless edge
 * case (the two query caches would simply invalidate together). */
export const SAVED_VIEWS_QUERY_KEY = ["layouts", "list"] as const;

export interface SavedViewListItem {
  name: string;
  updatedAt: number;
}

/** GET /layouts, filtered client-side to actual named views (excludes the
 * reserved "topology"/"onboarding" auto-layout blobs — see
 * isSavedViewPayload's doc comment). */
export function useSavedViewsQuery() {
  return useQuery<SavedViewListItem[]>({
    queryKey: SAVED_VIEWS_QUERY_KEY,
    queryFn: async () => {
      const res = await listLayouts();
      return res.items
        .filter((item) => isSavedViewPayload(item.layout))
        .map((item) => ({ name: item.name, updatedAt: item.updatedAt }));
    },
    staleTime: 5_000,
  });
}

export function useSaveViewMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name, state }: { name: string; state: SavedViewState }) => saveLayout(name, toSavedViewPayload(state)),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: SAVED_VIEWS_QUERY_KEY });
    },
  });
}

export function useDeleteViewMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => deleteLayout(name),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: SAVED_VIEWS_QUERY_KEY });
    },
  });
}

interface RawLayoutResponse {
  name: string;
  layout: unknown;
  updatedAt: number;
}

/** GET /layouts/{name}, typed for the "might be a saved view, might be the
 * reserved auto-layout shape" ambiguity `LayoutResponse`'s narrower
 * TopologyLayoutPayload typing doesn't express — mirrors fetchLayout
 * (api/layouts.ts) exactly, just with `layout: unknown` so the caller must
 * narrow (no unchecked cast, per CLAUDE.md's TypeScript rule). */
async function fetchRawLayout(name: string): Promise<RawLayoutResponse> {
  return apiFetch<RawLayoutResponse>(`/layouts/${encodeURIComponent(name)}`);
}

/** Loads a named saved view and validates its shape — undefined if the
 * name doesn't exist or (defensively) doesn't actually decode as a
 * SavedViewPayload (e.g. it was overwritten by something else out of
 * band). Not a hook: callers (SavedViewsMenu's "load" click) invoke this
 * imperatively, since applying a loaded view is a one-shot action, not
 * something a component needs to stay subscribed to. */
export async function loadSavedView(name: string): Promise<SavedViewState | undefined> {
  const res = await fetchRawLayout(name);
  return isSavedViewPayload(res.layout) ? fromSavedViewPayload(res.layout) : undefined;
}
