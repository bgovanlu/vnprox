// SPDX-License-Identifier: Apache-2.0

// Stored scope vs. effective scope for an automation token (T-2903), and the
// three-state expiry model that came with it.
//
// Pure functions with no React and no query layer, so the rules that decide
// what an operator is told are unit-testable on their own — the panel only
// renders what these return.
//
// THE RULE THIS FILE ENCODES, read out of the enforcement point rather than
// out of prose. `[server] read_only = true` narrows a bearer token exactly
// the way it narrows a cookie session: `internal/auth/middleware.go` builds
// `CapabilitiesFromScopes(scopes)` and then calls `forceReadOnly`.
//
// T-3003-followup-01 (2026-08-19): `forceReadOnly` used to zero exactly four
// flags (`NetWrite`/`SDNWrite`/`FWWrite`/`GuestNet`), leaving `capture` and
// `automation` untouched even though each gated a genuinely mutating route
// (`POST /captures[/stop]`, `POST`/`DELETE /webhooks`) — this module
// deliberately followed that CODE rather than the (then-wrong) doc comment
// claiming a wider strip, per its own header note. The owner's fix split
// `automation` into a read half (`automation` — the WS `"events"` topic and
// `GET /webhooks`, left untouched) and a write half (`automationWrite` —
// `POST`/`DELETE /webhooks`, now stripped), and added `capture` itself to the
// stripped set outright (no read/write split — `POST /captures` is at least
// as strict as `netWrite`'s gate, so it gets no read exception the way
// automation's WS topic does). `forceReadOnly` now zeroes six flags:
//
//     c.NetWrite = false; c.SDNWrite = false; c.FWWrite = false; c.GuestNet = false
//     c.Capture = false; c.AutomationWrite = false
//
// and nothing else — `automation` (the read half) survives. If the Go side
// changes again, `STRIPPED_UNDER_READ_ONLY` must change with it and
// `tokenScope.test.ts` will not catch that on its own.
import type { ApiToken, Capabilities, MeResponse } from "../api/types";

/** The token scope vocabulary `internal/auth.AllCaps` defines, in its
 * canonical order. `automation`/`automationWrite` are the two scopes always
 * grantable to any authenticated session (`Identity.CanGrantScope`) because
 * neither is derived from a PVE privilege and so neither has anything to
 * exceed. */
export const TOKEN_SCOPES: readonly string[] = [
  "netRead",
  "netWrite",
  "sdnRead",
  "sdnWrite",
  "fwRead",
  "fwWrite",
  "guestNet",
  "audit",
  "automation",
  "capture",
  "automationWrite",
];

/** Exactly the flags `internal/auth.forceReadOnly` zeroes — no more. */
export const STRIPPED_UNDER_READ_ONLY: readonly string[] = [
  "netWrite",
  "sdnWrite",
  "fwWrite",
  "guestNet",
  "capture",
  "automationWrite",
];

/** Which scopes a deployment-level `read_only` currently removes from a
 * token, and which survive.
 *
 * `readOnly === undefined` means "we have not been told yet" — `GET /config`
 * has not resolved. That is deliberately its own case: reporting "not
 * narrowed" before the answer is known is the arc's recurring defect
 * (an unknown rendered as a definite), so callers must branch on
 * `known === false` rather than treating it as "no narrowing". */
export interface TokenScopeNarrowing {
  /** False while `GET /config` has not answered. */
  known: boolean;
  /** True only when known AND at least one stored scope is being removed. */
  narrowed: boolean;
  /** Stored scopes that survive, in stored order. */
  effective: string[];
  /** Stored scopes `read_only` removes, in stored order. Empty when unknown. */
  removed: string[];
}

/** Computes the stored → effective scope narrowing for one token. */
export function tokenScopeNarrowing(stored: readonly string[], readOnly: boolean | undefined): TokenScopeNarrowing {
  if (readOnly === undefined) {
    return { known: false, narrowed: false, effective: [...stored], removed: [] };
  }
  if (!readOnly) {
    return { known: true, narrowed: false, effective: [...stored], removed: [] };
  }
  const removed = stored.filter((s) => STRIPPED_UNDER_READ_ONLY.includes(s));
  const effective = stored.filter((s) => !STRIPPED_UNDER_READ_ONLY.includes(s));
  return { known: true, narrowed: removed.length > 0, effective, removed };
}

/** A token's expiry, as three distinct facts rather than a date-or-blank.
 *
 * `never` is a real, deliberate state (docs/api.md: an explicit JSON `null`
 * mints a non-expiring token, and pre-v4.1 tokens have no expiry and keep
 * working — "expiry is never applied retroactively"). It must not render as
 * an empty cell, which reads as missing data. */
export type TokenExpiry = { kind: "never" } | { kind: "expires"; at: number } | { kind: "expired"; at: number };

export function tokenExpiry(token: ApiToken, nowSec: number): TokenExpiry {
  if (token.expiresAt === undefined) {
    return { kind: "never" };
  }
  return token.expiresAt <= nowSec ? { kind: "expired", at: token.expiresAt } : { kind: "expires", at: token.expiresAt };
}

/** Whether a token would authenticate a request right now. Revocation is
 * checked before expiry because a revoked token is revoked whatever its
 * expiry says; both produce the same 401 server-side but are different things
 * to tell an operator. */
export type TokenLifecycle = "revoked" | "expired" | "active";

export function tokenLifecycle(token: ApiToken, nowSec: number): TokenLifecycle {
  if (token.revokedAt !== undefined) {
    return "revoked";
  }
  return tokenExpiry(token, nowSec).kind === "expired" ? "expired" : "active";
}

/** The daemon's default token TTL (`internal/api/tokens.go`'s
 * `defaultTokenTTL`), in seconds — 90 days.
 *
 * Used ONLY to describe the default in the mint form's own prose and to
 * pre-fill the custom-expiry date picker. It is never sent: omitting
 * `expiresAt` is what selects the default, so the daemon remains the single
 * place the number is decided and the UI shows back whatever came out of the
 * 201 response. */
export const DEFAULT_TOKEN_TTL_SEC = 90 * 24 * 60 * 60;

/** Reads one scope's flag off a `Capabilities` value.
 *
 * An explicit accessor table rather than an index into the object, because
 * indexing a typed struct by an arbitrary string needs a cast, and a cast is
 * exactly how a scope name that no longer exists would silently read as
 * `false` — i.e. as a definite "you may not", rather than as the mistake it
 * is. A name absent from this table returns `undefined`, and callers treat
 * that as unknown rather than as denied. */
const SCOPE_ACCESSORS: Readonly<Record<string, (c: Capabilities) => boolean>> = {
  netRead: (c) => c.netRead,
  netWrite: (c) => c.netWrite,
  sdnRead: (c) => c.sdnRead,
  sdnWrite: (c) => c.sdnWrite,
  fwRead: (c) => c.fwRead,
  fwWrite: (c) => c.fwWrite,
  guestNet: (c) => c.guestNet,
  audit: (c) => c.audit,
  capture: (c) => c.capture,
  // `automation`/`automationWrite` have no accessor on purpose. Neither is
  // part of this client's `Capabilities` mirror (see that type's KNOWN GAP
  // note), and neither would be reachable anyway: canGrantScope
  // short-circuits both to `true` before consulting this table, because
  // `Identity.CanGrantScope` always grants both.
};

/** Whether the current session may mint a token carrying `scope`, mirroring
 * `internal/auth.Identity.CanGrantScope`:
 *
 *   - `automation` and `automationWrite` are ALWAYS grantable. Neither is
 *     derived from any PVE privilege, so there is nothing for either to
 *     exceed — holding an authenticated session at all is the whole bound.
 *   - every other scope must be held on at least one node, because a minted
 *     token carries no per-node granularity (`CapabilitiesFromScopes` builds
 *     a single cluster-wide entry).
 *
 * Returns `undefined` when the session has not loaded or the scope name is
 * unrecognised — "we cannot say", which the form renders as a disabled
 * control with a reason rather than as a refusal. */
export function canGrantScope(session: MeResponse | undefined, scope: string): boolean | undefined {
  if (scope === "automation" || scope === "automationWrite") {
    return true;
  }
  const accessor = SCOPE_ACCESSORS[scope];
  if (accessor === undefined) {
    return undefined;
  }
  if (session === undefined) {
    return undefined;
  }
  return Object.values(session.caps).some((c) => accessor(c));
}
