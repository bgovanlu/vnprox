// T-2605: the post-apply preview query for the map's preview mode.
//
// Kept in its own module (the topologyDiffQuery.ts convention) so TopologyPage
// imports a hook rather than growing another inline useQuery, and so the "only
// fetch when a changeset is actually selected" rule lives in one place: a map
// with no preview selected must not call the endpoint at all.
import { useQuery } from "@tanstack/react-query";

import { fetchChangesetPreview, type ChangesetPreviewResponse } from "../api/changesetPreview";

/** Fetches the projection for a changeset, or nothing at all when none is
 * selected. Not polled: a projection only moves when the changeset's ops or the
 * live graph move, and a preview that silently re-fetched would shift under an
 * operator mid-read — the same reasoning the point-in-time diff query states. */
export function useChangesetPreviewQuery(changesetId: string) {
  return useQuery<ChangesetPreviewResponse>({
    queryKey: ["changeset-preview", changesetId],
    queryFn: () => fetchChangesetPreview(changesetId),
    enabled: changesetId !== "",
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
  });
}
