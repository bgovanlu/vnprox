// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import {
  holderLabel,
  lockHolders,
  lockNotice,
  othersPresent,
  presenceSentence,
  refLabel,
} from "./lockWarning";
import type { EntityLock } from "../api/types";

function lock(over: Partial<EntityLock> = {}): EntityLock {
  return {
    ref: "bridge:pve1:vmbr0",
    changesetId: "cs-1",
    holder: "alice@pam",
    acquiredAt: 1_700_000_000,
    expiresAt: 1_700_000_900,
    mine: false,
    ...over,
  };
}

describe("holderLabel", () => {
  it("names the holder when the session may see identities", () => {
    expect(holderLabel(lock())).toBe("alice@pam");
  });

  // T-2805 AC5: `holder` is omitted for a session without the `audit`
  // capability. The copy must still be a sentence.
  it("degrades to a truthful stand-in when the identity is withheld", () => {
    expect(holderLabel(lock({ holder: undefined }))).toBe("another operator");
    expect(holderLabel(lock({ holder: "" }))).toBe("another operator");
  });
});

describe("refLabel", () => {
  it("scopes a node-owned entity by its node", () => {
    expect(refLabel("bridge:pve1:vmbr0")).toBe("vmbr0 on pve1");
  });

  it("leaves a cluster-scoped entity unscoped", () => {
    expect(refLabel("sdn-vnet::zone1/vnet1")).toBe("zone1/vnet1");
  });

  it("keeps ids containing colons intact", () => {
    expect(refLabel("sdn-subnet::10.0.0.0/24")).toBe("10.0.0.0/24");
  });

  it("falls back to the raw ref rather than throwing on anything unexpected", () => {
    expect(refLabel("nonsense")).toBe("nonsense");
    expect(refLabel("kind:node:")).toBe("kind:node:");
  });
});

describe("lockNotice", () => {
  it("shows nothing for an uncontended staging", () => {
    expect(lockNotice(undefined).show).toBe(false);
    expect(lockNotice({}).show).toBe(false);
    expect(lockNotice({ held: [], overridden: [] }).show).toBe(false);
  });

  it("warns, names the holder, and says the change is NOT blocked", () => {
    const notice = lockNotice({ held: [lock()] });
    expect(notice.show).toBe(true);
    expect(notice.kind).toBe("warning");
    expect(notice.lines).toEqual(["vmbr0 on pve1 — held by alice@pam"]);
    // The load-bearing assertion: a lock is advisory, so the copy must not
    // read as a rejection the operator has to clear.
    expect(notice.action).toContain("staged either way");
    expect(notice.action).toContain("does not block");
  });

  it("pluralises across several contended entities", () => {
    const notice = lockNotice({
      held: [lock(), lock({ ref: "bridge:pve2:vmbr1", holder: "bob@pam" })],
    });
    expect(notice.heading).toContain("2 of these entities");
    expect(notice.lines).toHaveLength(2);
    expect(notice.lines[1]).toContain("bob@pam");
  });

  it("reports a deliberate override, and says it was recorded", () => {
    const notice = lockNotice({ overridden: [lock()] });
    expect(notice.kind).toBe("override");
    expect(notice.lines[0]).toContain("was held by alice@pam");
    expect(notice.action).toContain("audit log");
  });

  it("prefers the override notice when both are present", () => {
    const notice = lockNotice({
      held: [lock({ ref: "bridge:pve2:vmbr1" })],
      overridden: [lock()],
    });
    expect(notice.kind).toBe("override");
  });

  it("renders without an identity when one is withheld", () => {
    const notice = lockNotice({ held: [lock({ holder: undefined })] });
    expect(notice.lines[0]).toBe("vmbr0 on pve1 — held by another operator");
    expect(notice.lines[0]).not.toContain("undefined");
  });
});

describe("lockHolders", () => {
  it("de-duplicates and sorts the named holders", () => {
    expect(
      lockHolders({
        held: [lock(), lock({ ref: "bridge:pve2:vmbr1" })],
        overridden: [lock({ ref: "bridge:pve3:vmbr2", holder: "bob@pam" })],
      }),
    ).toEqual(["alice@pam", "bob@pam"]);
  });

  it("returns nothing when identities are withheld", () => {
    expect(lockHolders({ held: [lock({ holder: undefined })] })).toEqual([]);
  });
});

describe("othersPresent", () => {
  it("subtracts the caller, who is counted by the server", () => {
    expect(othersPresent(1, true)).toBe(0);
    expect(othersPresent(3, true)).toBe(2);
  });

  it("never goes negative", () => {
    expect(othersPresent(0, true)).toBe(0);
  });

  it("counts everyone when the caller is not among them", () => {
    expect(othersPresent(2, false)).toBe(2);
  });
});

describe("presenceSentence", () => {
  it("says nothing when nobody else is there", () => {
    expect(presenceSentence(0, [])).toBe("");
  });

  // AC5's UI consequence: a caller without the capability still learns HOW
  // MANY, because a count is not an identity.
  it("states the count when identities are withheld", () => {
    expect(presenceSentence(1, [])).toBe("1 other person is viewing this.");
    expect(presenceSentence(3, [])).toBe("3 other people are viewing this.");
  });

  it("names people when it may", () => {
    expect(presenceSentence(1, ["alice@pam"])).toBe("alice@pam is also viewing this.");
    expect(presenceSentence(2, ["alice@pam", "bob@pam"])).toBe("alice@pam and bob@pam are also viewing this.");
    expect(presenceSentence(3, ["alice@pam", "bob@pam", "carol@pam"])).toBe(
      "alice@pam, bob@pam and carol@pam are also viewing this.",
    );
  });
});
