// SPDX-License-Identifier: Apache-2.0

// Annotation API calls against internal/api/annotations.go (T-907's
// entity-pinned sticky notes — docs/api.md's Saved views & annotations
// section). Shared across every user (not per-user data like layouts), so
// there is one list, filtered client-side by `ref` where a caller wants
// one entity's notes (see annotationsQueries.ts's useAnnotationsForRef).
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type {
  Annotation,
  AnnotationListResponse,
  MapRegion,
  MapRegionListResponse,
} from "./types";

/** GET /annotations — pinned notes, cluster/topology-wide.
 *
 * `includeExpired` defaults to false, which is the DISPLAY view: the daemon
 * judges each note's expiry against its own clock on this request and omits
 * the expired ones (T-2806). Pass true for the management view, where an
 * operator reads and unpins expired notes — they are hidden, never deleted. */
export function fetchAnnotations(includeExpired = false): Promise<AnnotationListResponse> {
  return apiFetch<AnnotationListResponse>(includeExpired ? "/annotations?includeExpired=true" : "/annotations");
}

/** POST /annotations — pins a new note to `ref`. `createdBy` is
 * server-stamped from the session, never client-supplied. `expiresAt` is
 * unix seconds; omitted/0 means the note never expires. */
export function createAnnotation(ref: string, content: string, expiresAt = 0): Promise<Annotation> {
  return apiFetch<Annotation>("/annotations", {
    method: "POST",
    json: { ref, content, expiresAt },
    csrfToken: readCsrfCookie(),
  });
}

/** GET /map-regions — labelled canvas regions (T-2806), same expiry
 * semantics as annotations. */
export function fetchMapRegions(includeExpired = false): Promise<MapRegionListResponse> {
  return apiFetch<MapRegionListResponse>(includeExpired ? "/map-regions?includeExpired=true" : "/map-regions");
}

/** POST /map-regions — draws a labelled region in canvas graph space. */
export function createMapRegion(input: {
  label: string;
  x: number;
  y: number;
  w: number;
  h: number;
  color?: string;
  expiresAt?: number;
}): Promise<MapRegion> {
  return apiFetch<MapRegion>("/map-regions", {
    method: "POST",
    json: {
      label: input.label,
      x: input.x,
      y: input.y,
      w: input.w,
      h: input.h,
      color: input.color ?? "",
      expiresAt: input.expiresAt ?? 0,
    },
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /map-regions/{id} — removes a region; idempotent. */
export async function deleteMapRegion(id: string): Promise<void> {
  await apiFetch(`/map-regions/${encodeURIComponent(id)}`, {
    method: "DELETE",
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
