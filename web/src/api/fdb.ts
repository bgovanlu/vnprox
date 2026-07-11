// MAC/FDB browser API calls (docs/features/lldp-discovery.md §4;
// internal/api/fdb.go's GET /fdb).
import { apiFetch } from "./client";
import type { FDBResponse, FDBRow } from "./types";

/** GET /fdb, optionally narrowed by a MAC/partial-MAC query. A blank query
 * lists every cluster-wide FDB entry (Tools → MAC search's initial "browse
 * everything" state); a non-blank query switches the server to ranked
 * partial-match search (see internal/api's handleFDB doc comment) —
 * callers don't need to pick which themselves. */
export function fetchFDB(mac = ""): Promise<FDBRow[]> {
  const trimmed = mac.trim();
  const qs = trimmed ? `?mac=${encodeURIComponent(trimmed)}` : "";
  return apiFetch<FDBResponse>(`/fdb${qs}`).then((r) => r.items);
}
