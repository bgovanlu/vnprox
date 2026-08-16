// The declarative cluster network spec: `GET /spec`, `POST /spec/import`
// (T-1101) and the pin (T-1102, `GET`/`POST`/`DELETE /spec/pin`).
//
// Two things about this family are easy to get wrong from the client side,
// so they are stated here once:
//
//   1. There is no `plan` route and no `diff` route. The plan IS
//      `POST /spec/import`'s response — and that response is a real DRAFT
//      CHANGESET the daemon created, even when the plan is empty
//      (internal/api/spec.go calls change.Service.Create unconditionally).
//      Planning is therefore a `netWrite` + CSRF action with a side effect,
//      not a read; the UI has to say so before the operator clicks.
//   2. `ops` and `notInSpec` answer different questions. `ops` is what the
//      document says the cluster should change to; `notInSpec` is what the
//      cluster has that the document never mentions — reported, never
//      deleted (docs/api.md: import has no prune path).
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { Changeset } from "./types";

/** `GET /spec` — the live cluster rendered as a spec document. Byte-stable:
 * two exports of an unchanged cluster are byte-identical. */
export interface SpecExport {
  specVersion: number;
  content: string;
}

/** `POST /spec/import`'s response: the draft changeset the import created,
 * plus the Ref strings of live entities the document does not declare. */
export interface SpecImportResult extends Changeset {
  notInSpec: string[];
}

/** `GET`/`POST /spec/pin`. Every field but `pinned` is omitted when nothing
 * is pinned, so `content === undefined` means "no pin", never "empty
 * document". */
export interface SpecPin {
  pinned: boolean;
  content?: string;
  pinnedBy?: string;
  /** Unix seconds. */
  pinnedAt?: number;
}

/** GET /spec — `netRead`. */
export function fetchSpec(): Promise<SpecExport> {
  return apiFetch<SpecExport>("/spec");
}

/** POST /spec/import — `netWrite` + CSRF. Diffs `content` against live state
 * and **creates a draft changeset** for the result, empty plan included. The
 * draft is never applied by this call; it goes through the ordinary
 * validate/apply/confirm review like any other. */
export function importSpec(content: string): Promise<SpecImportResult> {
  return apiFetch<SpecImportResult>("/spec/import", {
    method: "POST",
    json: { content },
    csrfToken: readCsrfCookie(),
  });
}

/** GET /spec/pin — `netRead`. */
export function fetchSpecPin(): Promise<SpecPin> {
  return apiFetch<SpecPin>("/spec/pin");
}

/** POST /spec/pin — `netWrite` + CSRF. Validates (`specVersion` must parse)
 * and pins in place; re-pinning needs no unpin first. Applies nothing: the
 * pin is only what the `spec_drift`/`spec_reconciliation` checks compare
 * against. */
export function pinSpec(content: string): Promise<SpecPin> {
  return apiFetch<SpecPin>("/spec/pin", {
    method: "POST",
    json: { content },
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /spec/pin — `netWrite` + CSRF, `204`. Clears the pin; the cluster
 * is untouched. */
export async function unpinSpec(): Promise<void> {
  await apiFetch("/spec/pin", { method: "DELETE", csrfToken: readCsrfCookie() });
}
