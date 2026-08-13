// Saved-layout API calls against internal/api/layouts.go (an additive route
// this task added — see that file's doc comment: docs/api.md had no
// `/layouts` entry before T-107, and internal/store's LayoutRepo (T-003)
// had no HTTP surface until now).
//
// T-2802: on the hosted public demo — and only there — all four operations
// go to the edge's per-visitor scratch store instead (tour/visitorLayouts.ts).
// The daemon's own /layouts writes are refused at the edge like every other
// write, and a map whose nodes cannot be dragged is not much of a demo. The
// branch is here, at the one seam every layout call already passes through,
// rather than in each of the half-dozen call sites.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import { isPublicDemoInstance } from "../tour/usePublicDemo";
import { deleteVisitorLayout, fetchVisitorLayout, listVisitorLayouts, saveVisitorLayout } from "../tour/visitorLayouts";
import type { LayoutListResponse, LayoutResponse, TopologyLayoutPayload } from "./types";

/** GET /layouts/{name}. Rejects with a 404 ApiError (`code === "not_found"`)
 * when the user has never saved a layout under this name — callers should
 * treat that as "use the default auto-layout", not surface it as an error. */
export function fetchLayout(name: string): Promise<LayoutResponse> {
  if (isPublicDemoInstance()) return fetchVisitorLayout(name);
  return apiFetch<LayoutResponse>(`/layouts/${encodeURIComponent(name)}`);
}

/** PUT /layouts/{name} — upserts the named layout. `layout` is typed
 * `unknown` here (not TopologyLayoutPayload) so this same function backs
 * both the auto-persisted canvas layout (TopologyLayoutPayload) and T-907's
 * named saved views (SavedViewPayload) — the backend stores either shape
 * verbatim, opaque JSON either way (docs/api.md's Saved views &
 * annotations section). */
export function saveLayout(name: string, layout: unknown): Promise<LayoutResponse> {
  if (isPublicDemoInstance()) return saveVisitorLayout(name, layout as TopologyLayoutPayload);
  return apiFetch<LayoutResponse>(`/layouts/${encodeURIComponent(name)}`, {
    method: "PUT",
    json: { layout },
    csrfToken: readCsrfCookie(),
  });
}

/** GET /layouts (T-907) — every layout/saved-view the requesting user has
 * saved, including the reserved "topology"/"onboarding" auto-layout blobs.
 * Callers narrow to actual named views via isSavedViewPayload
 * (savedViews.ts). */
export function listLayouts(): Promise<LayoutListResponse> {
  if (isPublicDemoInstance()) return listVisitorLayouts();
  return apiFetch<LayoutListResponse>("/layouts");
}

/** DELETE /layouts/{name} (T-907) — removes a saved layout/view; resolves
 * whether or not it previously existed (the backend's delete is
 * idempotent). */
export async function deleteLayout(name: string): Promise<void> {
  if (isPublicDemoInstance()) {
    await deleteVisitorLayout(name);
    return;
  }
  await apiFetch(`/layouts/${encodeURIComponent(name)}`, {
    method: "DELETE",
    csrfToken: readCsrfCookie(),
  });
}
