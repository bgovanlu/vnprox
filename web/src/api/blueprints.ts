// Blueprints API calls (docs/api.md's Blueprints section; the exact
// contract is internal/blueprint + internal/api/blueprints.go).
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type {
  Blueprint,
  BlueprintsListResponse,
  CaptureBlueprintRequest,
  Changeset,
  InstantiateBlueprintRequest,
  SuggestAddressResponse,
} from "./types";

/** GET /blueprints — every bundled starter plus every saved blueprint. */
export function fetchBlueprints(): Promise<BlueprintsListResponse> {
  return apiFetch<BlueprintsListResponse>("/blueprints");
}

/** GET /blueprints/{id} — single blueprint detail (also the export/download
 * source: callers that want a file just JSON.stringify this response). */
export function fetchBlueprint(id: string): Promise<Blueprint> {
  return apiFetch<Blueprint>(`/blueprints/${encodeURIComponent(id)}`);
}

/** POST /blueprints — save (author-from-scratch, capture-then-save, or
 * import-from-file all go through this same call: an empty `id` mints a
 * new one, a non-empty `id` overwrites that saved blueprint). */
export function saveBlueprint(bp: Blueprint): Promise<Blueprint> {
  return apiFetch<Blueprint>("/blueprints", { method: "POST", json: bp, csrfToken: readCsrfCookie() });
}

/** DELETE /blueprints/{id} — remove a saved (non-starter) blueprint. */
export async function deleteBlueprint(id: string): Promise<void> {
  await apiFetch(`/blueprints/${encodeURIComponent(id)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}

/** POST /blueprints/capture — "blueprint-ify" a node's live network state
 * into an unsaved Blueprint; the caller decides whether to edit/save it. */
export function captureBlueprint(req: CaptureBlueprintRequest): Promise<Blueprint> {
  return apiFetch<Blueprint>("/blueprints/capture", { method: "POST", json: req, csrfToken: readCsrfCookie() });
}

/** GET /blueprints/{id}/suggest?param= — next-free-address suggestion for
 * one of the blueprint's addressSuggest-eligible params. */
export function suggestBlueprintAddress(id: string, param: string): Promise<SuggestAddressResponse> {
  return apiFetch<SuggestAddressResponse>(
    `/blueprints/${encodeURIComponent(id)}/suggest?param=${encodeURIComponent(param)}`,
  );
}

/** POST /blueprints/{id}/instantiate — params -> a draft changeset (the
 * normal review/apply flow takes it from there; this call never applies
 * anything itself). */
export function instantiateBlueprint(id: string, req: InstantiateBlueprintRequest): Promise<Changeset> {
  return apiFetch<Changeset>(`/blueprints/${encodeURIComponent(id)}/instantiate`, {
    method: "POST",
    json: req,
    csrfToken: readCsrfCookie(),
  });
}
