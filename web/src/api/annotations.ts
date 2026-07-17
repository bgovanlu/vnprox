// Annotation API calls against internal/api/annotations.go (T-907's
// entity-pinned sticky notes — docs/api.md's Saved views & annotations
// section). Shared across every user (not per-user data like layouts), so
// there is one list, filtered client-side by `ref` where a caller wants
// one entity's notes (see annotationsQueries.ts's useAnnotationsForRef).
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { Annotation, AnnotationListResponse } from "./types";

/** GET /annotations — every pinned note, cluster/topology-wide. */
export function fetchAnnotations(): Promise<AnnotationListResponse> {
  return apiFetch<AnnotationListResponse>("/annotations");
}

/** POST /annotations — pins a new note to `ref`. `createdBy` is
 * server-stamped from the session, never client-supplied. */
export function createAnnotation(ref: string, content: string): Promise<Annotation> {
  return apiFetch<Annotation>("/annotations", {
    method: "POST",
    json: { ref, content },
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /annotations/{id} — unpins a note; resolves whether or not it
 * previously existed (the backend's delete is idempotent). */
export async function deleteAnnotation(id: string): Promise<void> {
  await apiFetch(`/annotations/${encodeURIComponent(id)}`, {
    method: "DELETE",
    csrfToken: readCsrfCookie(),
  });
}
