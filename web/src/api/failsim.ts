// Failure-impact simulation API calls (docs/api.md's Failure-impact
// simulation section, T-1604; internal/api/failsim.go's GET
// /failsim/spof-score). Pure read-only synthesis: this route never induces
// a failure, never mutates, and never persists a result, and there is no
// `failsim.*` changeset op anywhere in internal/change.
//
// POST /changesets/{id}/preflight-impact lives on the changeset, not here —
// it is the review screen's concern, not this client's.
import { apiFetch } from "./client";
import type { SpofScore } from "./types";

/** GET /failsim/spof-score — every enumerated element whose removal has a
 * nonzero *known* impact, plus the 0-100 resilience score. A purely
 * redundant element is excluded server-side, so an empty `entries` means
 * "no known single points of failure", not "not computed". */
export function fetchSpofScore(): Promise<SpofScore> {
  return apiFetch<SpofScore>("/failsim/spof-score");
}
