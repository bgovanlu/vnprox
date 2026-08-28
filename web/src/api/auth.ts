// SPDX-License-Identifier: Apache-2.0

// Auth API calls (docs/api.md §Auth) plus the CSRF-token plumbing the
// session cookie scheme implies.
//
// docs/api.md says: session cookie `vnprox_session` (HttpOnly, Secure,
// SameSite=Strict) + `X-VNPROX-CSRF` header on mutating requests. It does
// not spell out *how* the client learns the CSRF token value — since the
// session cookie is HttpOnly, JS can't read it directly. The standard
// pairing for this pattern is a second, non-HttpOnly cookie holding the
// same (or a derived) token that the client echoes back as a header
// ("double-submit cookie"). We assume that cookie is named
// `vnprox_csrf`; T-105 (real auth) should confirm this against the actual
// session middleware and adjust `readCsrfCookie` below if the name (or
// mechanism) differs.
import { apiFetch } from "./client";
import type { LoginRequest, MeResponse } from "./types";

const CSRF_COOKIE_NAME = "vnprox_csrf";

export function readCsrfCookie(): string | undefined {
  if (typeof document === "undefined") {
    return undefined;
  }
  const match = document.cookie
    .split("; ")
    .find((row) => row.startsWith(`${CSRF_COOKIE_NAME}=`));
  return match?.slice(CSRF_COOKIE_NAME.length + 1);
}

/** GET /auth/me — current user + capability flags. Rejects with ApiError
 * on any non-2xx (401 not-logged-in, or today, before T-105 lands real
 * auth, a 404 from the backend stub not implementing this route yet).
 * Callers should treat *any* rejection as "logged out", per T-005's task
 * card — see useSession in src/routes/RequireAuth.tsx. */
export function getMe(): Promise<MeResponse> {
  return apiFetch<MeResponse>("/auth/me");
}

/** POST /auth/login — sets the session cookie, returns `{user, caps}`. */
export function login(payload: LoginRequest): Promise<MeResponse> {
  return apiFetch<MeResponse>("/auth/login", { json: payload, csrfToken: readCsrfCookie() });
}

/** POST /auth/logout — destroys the session. */
export async function logout(): Promise<void> {
  await apiFetch("/auth/logout", { method: "POST", csrfToken: readCsrfCookie() });
}
