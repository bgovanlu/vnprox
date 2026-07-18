// Blueprints API calls (docs/api.md's Blueprints section; the exact
// contract is internal/blueprint + internal/api/blueprints.go).
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type {
  Blueprint,
  BlueprintBundle,
  BlueprintSigner,
  BlueprintSignersListResponse,
  BlueprintSigningKeyResponse,
  BlueprintsListResponse,
  CaptureBlueprintRequest,
  Changeset,
  ImportBundleRequest,
  ImportBundleResponse,
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

// --- Blueprint sharing bundles (T-1107, docs/features/blueprints.md §5) --

/** GET /blueprints/{id}/bundle?sign= — the (optionally signed) sharable
 * bundle for download. `sign` defaults to unsigned (matching the route's
 * own `?sign=` default). */
export function fetchBlueprintBundle(id: string, sign: boolean): Promise<BlueprintBundle> {
  const query = sign ? "?sign=true" : "";
  return apiFetch<BlueprintBundle>(`/blueprints/${encodeURIComponent(id)}/bundle${query}`);
}

/** GET /blueprints/signing-key — this installation's own bundle-signing
 * public key, for sharing out-of-band with a receiving admin. */
export function fetchBlueprintSigningKey(): Promise<BlueprintSigningKeyResponse> {
  return apiFetch<BlueprintSigningKeyResponse>("/blueprints/signing-key");
}

/** POST /blueprints/import — verify a bundle's signature (if any) against
 * the trust store and, per the response's `status`, either save it as a
 * new blueprint or report why it didn't (docs/api.md's Blueprint bundles
 * section: unsigned/untrustedSignature/invalidSignature are none of them
 * errors — each is a normal response the import dialog inspects `status`
 * on). Passing `trustUnsigned`/`trustNewKey` is the caller's explicit
 * trust decision, never implied by this function itself. */
export function importBlueprintBundle(req: ImportBundleRequest): Promise<ImportBundleResponse> {
  return apiFetch<ImportBundleResponse>("/blueprints/import", { method: "POST", json: req, csrfToken: readCsrfCookie() });
}

/** GET /blueprint-signers — every pinned (trusted) signer. */
export function fetchBlueprintSigners(): Promise<BlueprintSignersListResponse> {
  return apiFetch<BlueprintSignersListResponse>("/blueprint-signers");
}

/** POST /blueprint-signers — pin a new trusted signer. */
export function addBlueprintSigner(publicKey: string, label?: string): Promise<BlueprintSigner> {
  return apiFetch<BlueprintSigner>("/blueprint-signers", {
    method: "POST",
    json: { publicKey, label },
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /blueprint-signers/{fingerprint} — un-pin a trusted signer. */
export async function deleteBlueprintSigner(fingerprint: string): Promise<void> {
  await apiFetch(`/blueprint-signers/${encodeURIComponent(fingerprint)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}
