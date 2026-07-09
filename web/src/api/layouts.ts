// Saved-layout API calls against internal/api/layouts.go (an additive route
// this task added — see that file's doc comment: docs/api.md had no
// `/layouts` entry before T-107, and internal/store's LayoutRepo (T-003)
// had no HTTP surface until now).
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { LayoutResponse, TopologyLayoutPayload } from "./types";

/** GET /layouts/{name}. Rejects with a 404 ApiError (`code === "not_found"`)
 * when the user has never saved a layout under this name — callers should
 * treat that as "use the default auto-layout", not surface it as an error. */
export function fetchLayout(name: string): Promise<LayoutResponse> {
  return apiFetch<LayoutResponse>(`/layouts/${encodeURIComponent(name)}`);
}

/** PUT /layouts/{name} — upserts the named layout. */
export function saveLayout(name: string, layout: TopologyLayoutPayload): Promise<LayoutResponse> {
  return apiFetch<LayoutResponse>(`/layouts/${encodeURIComponent(name)}`, {
    method: "PUT",
    json: { layout },
    csrfToken: readCsrfCookie(),
  });
}
