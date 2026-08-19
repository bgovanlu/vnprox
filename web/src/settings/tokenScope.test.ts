// T-3003 (extended by T-3003-followup-01, 2026-08-19): the
// stored-scope/effective-scope and expiry rules, tested away from React so
// the claims the panel makes are checkable on their own.
//
// The narrowing table is asserted against `internal/auth.forceReadOnly`'s
// actual body — see tokenScope.ts's header for the full history of what it
// zeroes and why.
import { describe, expect, it } from "vitest";
import type { ApiToken, MeResponse } from "../api/types";
import {
  canGrantScope,
  tokenExpiry,
  tokenLifecycle,
  tokenScopeNarrowing,
  DEFAULT_TOKEN_TTL_SEC,
  STRIPPED_UNDER_READ_ONLY,
} from "./tokenScope";

const NOW = 1_700_000_000;

function token(partial: Partial<ApiToken>): ApiToken {
  return {
    id: "t1",
    name: "ci",
    scopes: [],
    createdBy: "root@pam",
    createdAt: NOW - 1000,
    ...partial,
  };
}

describe("tokenScopeNarrowing", () => {
  it("reports UNKNOWN, not 'unnarrowed', before GET /config has answered", () => {
    // The arc's recurring defect is an unknown rendered as a definite. Before
    // the instance config resolves, whether read_only is narrowing a token is
    // genuinely not known, and this must not answer "no".
    const n = tokenScopeNarrowing(["netRead", "netWrite"], undefined);
    expect(n.known).toBe(false);
    expect(n.narrowed).toBe(false);
    expect(n.removed).toEqual([]);
  });

  it("narrows nothing in a writable deployment", () => {
    const n = tokenScopeNarrowing(["netRead", "netWrite", "guestNet"], false);
    expect(n).toEqual({
      known: true,
      narrowed: false,
      effective: ["netRead", "netWrite", "guestNet"],
      removed: [],
    });
  });

  it("removes exactly the four original config-write flags forceReadOnly zeroes", () => {
    const stored = ["netRead", "netWrite", "sdnRead", "sdnWrite", "fwRead", "fwWrite", "guestNet", "audit"];
    const n = tokenScopeNarrowing(stored, true);
    expect(n.known).toBe(true);
    expect(n.narrowed).toBe(true);
    expect(n.removed).toEqual(["netWrite", "sdnWrite", "fwWrite", "guestNet"]);
    expect(n.effective).toEqual(["netRead", "sdnRead", "fwRead", "audit"]);
  });

  // T-3003-followup-01 (2026-08-19) replaces the predecessor of this test,
  // "leaves capture and automation alone under read_only", which pinned
  // internal/auth.forceReadOnly's PRE-fix behaviour on purpose (its own
  // comment said so). That behaviour is now fixed: capture is stripped
  // outright, and automation was split into a read half (`automation`,
  // still untouched — it also gates the WS "events" topic) and a write half
  // (`automationWrite`, now stripped alongside capture).
  it("strips capture and automationWrite, but not automation (the read half), under read_only", () => {
    expect(STRIPPED_UNDER_READ_ONLY).toContain("capture");
    expect(STRIPPED_UNDER_READ_ONLY).toContain("automationWrite");
    expect(STRIPPED_UNDER_READ_ONLY).not.toContain("automation");

    const n = tokenScopeNarrowing(["capture", "automation", "automationWrite", "netWrite"], true);
    expect(n.effective).toEqual(["automation"]);
    expect(n.removed).toEqual(["capture", "automationWrite", "netWrite"]);
  });

  it("is not 'narrowed' when read_only removes nothing this token had", () => {
    const n = tokenScopeNarrowing(["netRead", "audit"], true);
    expect(n.known).toBe(true);
    expect(n.narrowed).toBe(false);
    expect(n.removed).toEqual([]);
  });
});

describe("tokenExpiry", () => {
  it("treats an absent expiresAt as a deliberate 'never', not as missing data", () => {
    expect(tokenExpiry(token({}), NOW)).toEqual({ kind: "never" });
  });

  it("distinguishes a future expiry from a past one", () => {
    expect(tokenExpiry(token({ expiresAt: NOW + 60 }), NOW)).toEqual({ kind: "expires", at: NOW + 60 });
    expect(tokenExpiry(token({ expiresAt: NOW - 60 }), NOW)).toEqual({ kind: "expired", at: NOW - 60 });
  });

  it("counts an expiry exactly at now as expired", () => {
    expect(tokenExpiry(token({ expiresAt: NOW }), NOW).kind).toBe("expired");
  });
});

describe("tokenLifecycle", () => {
  it("reports revoked before expired", () => {
    expect(tokenLifecycle(token({ revokedAt: NOW - 10, expiresAt: NOW - 5 }), NOW)).toBe("revoked");
  });

  it("reports expired for a live-but-past-expiry token", () => {
    expect(tokenLifecycle(token({ expiresAt: NOW - 5 }), NOW)).toBe("expired");
  });

  it("reports active for a non-expiring, non-revoked token", () => {
    expect(tokenLifecycle(token({}), NOW)).toBe("active");
  });
});

describe("canGrantScope", () => {
  const session: MeResponse = {
    user: { username: "auditor", realm: "pve" },
    caps: {
      pve1: { netRead: true, netWrite: false, sdnRead: false, sdnWrite: false, fwRead: true, fwWrite: false, guestNet: false, audit: true, capture: false },
      pve2: { netRead: true, netWrite: true, sdnRead: false, sdnWrite: false, fwRead: true, fwWrite: false, guestNet: false, audit: true, capture: false },
    },
  };

  it("grants a scope held on ANY node, matching the cluster-wide token model", () => {
    // A minted token carries no per-node granularity, so Identity.CanGrantScope
    // asks HasCap("", scope) — the "any node" check.
    expect(canGrantScope(session, "netWrite")).toBe(true);
  });

  it("refuses a scope held on no node", () => {
    expect(canGrantScope(session, "sdnWrite")).toBe(false);
  });

  it("always grants automation and automationWrite, neither of which is PVE-derived", () => {
    expect(canGrantScope(session, "automation")).toBe(true);
    expect(canGrantScope(undefined, "automation")).toBe(true);
    expect(canGrantScope(session, "automationWrite")).toBe(true);
    expect(canGrantScope(undefined, "automationWrite")).toBe(true);
  });

  it("answers 'unknown' rather than 'denied' before the session loads", () => {
    expect(canGrantScope(undefined, "netRead")).toBeUndefined();
  });

  it("answers 'unknown' for a scope name it does not recognise", () => {
    expect(canGrantScope(session, "notAScope")).toBeUndefined();
  });
});

describe("DEFAULT_TOKEN_TTL_SEC", () => {
  it("is the daemon's 90 days", () => {
    expect(DEFAULT_TOKEN_TTL_SEC).toBe(90 * 24 * 60 * 60);
  });
});
