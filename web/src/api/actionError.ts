// SPDX-License-Identifier: Apache-2.0

// Phase 36: turning a failed remediation into something an operator can act
// on.
//
// The buttons report failures verbatim, deliberately — "Unit frr.service is
// masked." is actionable in a way that "could not start service" is not. But
// verbatim is the wrong answer for the one class of failure that is not
// about the cluster at all: a session that has gone stale renders as
// "missing or invalid X-VNPROX-CSRF header", which names an HTTP header at
// somebody trying to start a DHCP server. It is accurate and useless.
//
// Observed on the deployed instance on 2026-08-21, from a tab left open
// across a daemon restart: every remediation button failed with that text.
// The requests themselves were correct — a fresh browser sent the header and
// the routes answered 200 — so the remedy is a reload, and the message
// should say so.
import { ApiError } from "./client";

/** The auth-shaped failures a reload (or a fresh sign-in) resolves.
 * `csrf_required` is the stale-tab case; 401 is an expired or absent
 * session. */
function isStaleSession(err: unknown): boolean {
  if (!(err instanceof ApiError)) return false;
  return err.status === 401 || err.code === "csrf_required";
}

/** The message to show for a failed remediation.
 *
 * Everything the cluster said comes through unchanged. Only the
 * stale-session case is rewritten, because it is the only one where the
 * server's own words point the operator at the wrong thing entirely. */
export function actionErrorMessage(err: unknown, fallback: string): string {
  if (isStaleSession(err)) {
    return "Your session has expired — reload the page and sign in again, then retry.";
  }
  if (err instanceof Error && err.message !== "") return err.message;
  return fallback;
}
