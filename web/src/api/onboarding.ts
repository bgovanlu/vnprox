// SPDX-License-Identifier: Apache-2.0

// Onboarding walkthrough progress persistence. Reuses the existing
// GET/PUT /layouts/{name} mechanism (internal/api/layouts.go; T-107) under
// the reserved name "onboarding" rather than adding a new backend
// table/route — docs/architecture.md §7's storage rule already lists
// "layouts: per-user saved topology layouts/filters" as the one app-owned
// slot for exactly this kind of opaque, frontend-owned UI state, and T-605's
// brief is explicit that this task should not grow the storage surface for
// a second, near-identical use.
//
// Deliberately a sibling to api/layouts.ts rather than a generalization of
// it: `fetchLayout`/`saveLayout` are typed specifically to
// TopologyLayoutPayload and have their own call sites/tests already
// depending on that; duplicating the two-line apiFetch calls here (instead
// of introducing a generic `<T>` across a file other tasks own) keeps this
// change small and avoids touching topology/queries.ts's existing contract.
import { apiFetch } from "./client";
import { readCsrfCookie } from "./auth";
import type { OnboardingProgress } from "./types";

const ONBOARDING_LAYOUT_NAME = "onboarding";

/** The GET/PUT /layouts/{name} response envelope, typed for the onboarding
 * payload specifically (mirrors api/types.ts's LayoutResponse, which is
 * pinned to TopologyLayoutPayload instead). */
export interface OnboardingLayoutResponse {
  name: string;
  layout: OnboardingProgress;
  updatedAt: number;
}

/** GET /layouts/onboarding. Rejects with a 404 ApiError (`status === 404`)
 * when this user has never run (or saved progress for) the walkthrough —
 * callers should treat that as "fresh install, start at step 1", per
 * api/layouts.ts's fetchLayout doc comment for the identical convention. */
export function fetchOnboardingProgress(): Promise<OnboardingLayoutResponse> {
  return apiFetch<OnboardingLayoutResponse>(`/layouts/${ONBOARDING_LAYOUT_NAME}`);
}

/** PUT /layouts/onboarding — upserts the walkthrough's persisted progress. */
export function saveOnboardingProgress(progress: OnboardingProgress): Promise<OnboardingLayoutResponse> {
  return apiFetch<OnboardingLayoutResponse>(`/layouts/${ONBOARDING_LAYOUT_NAME}`, {
    method: "PUT",
    json: { layout: progress },
    csrfToken: readCsrfCookie(),
  });
}
