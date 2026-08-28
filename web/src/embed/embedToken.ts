// SPDX-License-Identifier: Apache-2.0

// Embed-mode authentication state (T-1706). When the app boots at an
// /embed/* route (see EmbedFrame.tsx), the embed token from the `?token=`
// query string is stashed here so the shared apiFetch wrapper (api/client.ts)
// authenticates every read call with `Authorization: Bearer <token>` and
// omits the session cookie entirely — the frontend mirror of the backend's
// distinct-middleware-path invariant: an embed view never authenticates with
// a session cookie, only its embed token (docs/security.md's embed-token
// model). This module holds no React state and has no DOM dependency, so
// api/client.ts can import it without pulling in the component tree.

let embedToken: string | null = null;

/** Marks this app instance as running inside an embed and sets the bearer
 * token every subsequent apiFetch call must use. Idempotent; passing an
 * empty string clears it. */
export function setEmbedToken(token: string): void {
  embedToken = token || null;
}

/** The active embed token, or null when the app is running as the ordinary
 * cookie-session SPA. api/client.ts branches on this. */
export function getEmbedToken(): string | null {
  return embedToken;
}

/** True when the app is running in embed mode. */
export function isEmbedMode(): boolean {
  return embedToken !== null;
}
