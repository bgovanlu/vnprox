// SPDX-License-Identifier: Apache-2.0

// Network posture score & report API (T-1607). Read-only: the posture score
// is a derived artifact recomputed by the daemon on a schedule, never written
// through a request. The exact JSON contract is internal/posture/posture.go +
// internal/api/posture.go.
import { apiFetch } from "./client";

/** One named contributing dimension of the posture score (posture.Factor).
 * Every field is independently inspectable — the "never folded into overall
 * opaquely" contract. A factor that could not be assessed carries
 * `evaluated: false`, `scorePct: -1`, `contribution: 0`, and a non-empty
 * `caveat`; a reader must branch on `evaluated` before trusting `scorePct`. */
export interface PostureFactor {
  name: string;
  detail: string;
  caveat?: string;
  value: number;
  contribution: number;
  weight: number;
  scorePct: number;
  evaluated: boolean;
}

/** The computed posture report (posture.Posture). `overall` is the weighted
 * mean (0..100) of the *evaluated* factors only; `qualified` is the single
 * honesty flag meaning "at least one dimension is unknown or partial." */
export interface Posture {
  factors: PostureFactor[];
  computedAt: number;
  overall: number;
  qualified: boolean;
}

/** GET /posture — the most recent computed score. Throws ApiError with
 * status 404 when no score has been computed yet (a freshly-started daemon
 * before the first scheduled tick), which callers render as a "not yet
 * available" state rather than an error. */
export function fetchPosture(): Promise<Posture> {
  return apiFetch<Posture>("/posture");
}
