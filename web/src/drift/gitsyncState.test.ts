// The state classification behind the cockpit, tested directly because the
// property it holds is a phrasing rule the components then just render:
// "off", "broken" and "we could not ask" are three states, and an unknown is
// never resolved into a definite one.
import { describe, expect, it } from "vitest";
import type { GitSyncStatus } from "../api/gitsync";
import { adoptAvailability, gitSyncState, instantLabel, specPresence } from "./gitsyncState";
import {
  cellText,
  notInSpecSummary,
  oddPositionOut,
  opsSummary,
  pairSummary,
  presenceLabel,
  valueAt,
} from "./positions";
import type { PairDiff, Reconciliation } from "../api/types";

const enabled: GitSyncStatus = { enabled: true, requireSignedCommits: false, planOpCount: 0 };

describe("gitSyncState", () => {
  it("is loading before the first answer", () => {
    expect(gitSyncState(undefined, true, null).kind).toBe("loading");
    expect(gitSyncState(undefined, false, null).kind).toBe("loading");
  });

  it("treats a failed read as unreadable, not as a disabled sync", () => {
    const state = gitSyncState(undefined, false, new Error("netRead capability required"));
    expect(state.kind).toBe("unreadable");
    expect(state).toMatchObject({ message: "netRead capability required" });
  });

  it("prefers an error over a stale cached status", () => {
    // A status we could not refresh is not evidence of the current state.
    expect(gitSyncState(enabled, false, new Error("boom")).kind).toBe("unreadable");
  });

  it("distinguishes not-configured from failing from healthy", () => {
    expect(gitSyncState({ enabled: false, requireSignedCommits: false, planOpCount: 0 }, false, null).kind).toBe(
      "not-configured",
    );
    expect(gitSyncState({ ...enabled, lastError: "remote unreachable" }, false, null)).toMatchObject({
      kind: "failing",
      message: "remote unreachable",
    });
    // An empty lastError is not a failure — the field is omitempty on the wire.
    expect(gitSyncState({ ...enabled, lastError: "" }, false, null).kind).toBe("healthy");
  });
});

describe("adoptAvailability", () => {
  it("is only certain when there is no [gitsync] section at all", () => {
    expect(adoptAvailability({ kind: "not-configured" })).toBe("unavailable");
    expect(adoptAvailability({ kind: "healthy", status: enabled })).toBe("unknown");
    expect(adoptAvailability({ kind: "failing", status: enabled, message: "x" })).toBe("unknown");
    expect(adoptAvailability({ kind: "unreadable", message: "x" })).toBe("unknown");
    expect(adoptAvailability({ kind: "loading" })).toBe("unknown");
  });
});

describe("specPresence", () => {
  it("is present from either source", () => {
    expect(specPresence({ kind: "not-configured" }, { pinned: true }, null)).toBe("present");
    expect(specPresence({ kind: "healthy", status: enabled }, { pinned: false }, null)).toBe("present");
    expect(specPresence({ kind: "failing", status: enabled, message: "x" }, { pinned: false }, null)).toBe(
      "present",
    );
  });

  it("is absent only when BOTH sources are known to be missing", () => {
    expect(specPresence({ kind: "not-configured" }, { pinned: false }, null)).toBe("absent");
  });

  it("is unknown when either read failed or has not answered", () => {
    expect(specPresence({ kind: "not-configured" }, undefined, null)).toBe("unknown");
    expect(specPresence({ kind: "not-configured" }, { pinned: false }, new Error("boom"))).toBe("unknown");
    expect(specPresence({ kind: "loading" }, { pinned: false }, null)).toBe("unknown");
    expect(specPresence({ kind: "unreadable", message: "x" }, { pinned: false }, null)).toBe("unknown");
  });
});

describe("instantLabel", () => {
  it("never renders an omitted timestamp as the epoch", () => {
    expect(instantLabel(undefined, "never")).toBe("never");
    expect(instantLabel(0, "never")).toBe("never");
    expect(instantLabel(1_754_000_000, "never")).not.toBe("never");
  });
});

describe("the three-position vocabulary", () => {
  it("keeps 'never reported' distinct from a value and from an empty one", () => {
    expect(cellText(undefined)).toEqual({ text: "not reported", known: false });
    expect(cellText({ position: "live", value: "1500", known: false })).toEqual({
      text: "not reported",
      known: false,
    });
    expect(cellText({ position: "live", value: "", known: true })).toEqual({ text: "(empty)", known: true });
    expect(cellText({ position: "live", value: "1500", known: true })).toEqual({ text: "1500", known: true });
  });

  it("finds a field's value at a position, or reports none", () => {
    const field = {
      field: "mtu",
      values: [{ position: "spec" as const, value: "9000", known: true }],
      differs: [],
    };
    expect(valueAt(field, "spec")?.value).toBe("9000");
    expect(valueAt(field, "live")).toBeUndefined();
  });

  it("says whether a pair agrees, differs, or had nothing to compare", () => {
    const agree: PairDiff = { a: "config", b: "live", fields: [], comparable: true };
    const differ: PairDiff = { a: "spec", b: "config", fields: ["mtu", "ports"], comparable: true };
    const incomparable: PairDiff = { a: "spec", b: "live", fields: [], comparable: false };

    expect(pairSummary(agree)).toBe("agree on every field both reported");
    expect(pairSummary(differ)).toBe("differ on mtu, ports");
    expect(pairSummary(incomparable)).toBe(
      "nothing to compare — neither position reported a field the other did",
    );
  });

  it("names the odd position out only when exactly one comparable pair agrees", () => {
    const base: Reconciliation = {
      ref: "bridge:pve1:vmbr0",
      inSpec: true,
      inConfig: true,
      inLive: true,
      fields: [],
      pairs: [
        { a: "spec", b: "config", fields: ["mtu"], comparable: true },
        { a: "config", b: "live", fields: [], comparable: true },
        { a: "spec", b: "live", fields: ["mtu"], comparable: true },
      ],
      actions: { adoptReality: false, restoreIntent: false },
    };
    expect(oddPositionOut(base)).toBe("spec");

    // All three differ: there is no single odd one out to name.
    expect(
      oddPositionOut({
        ...base,
        pairs: base.pairs.map((p) => ({ ...p, fields: ["mtu"] })),
      }),
    ).toBeUndefined();

    // An incomparable pair leaves the question open rather than guessing.
    const incomparableLast: PairDiff[] = [
      { a: "spec", b: "config", fields: ["mtu"], comparable: true },
      { a: "config", b: "live", fields: [], comparable: true },
      { a: "spec", b: "live", fields: [], comparable: false },
    ];
    expect(oddPositionOut({ ...base, pairs: incomparableLast })).toBeUndefined();
  });

  it("describes presence per position in that position's own words", () => {
    expect(presenceLabel("spec", true)).toBe("declared");
    expect(presenceLabel("spec", false)).toBe("not declared");
    expect(presenceLabel("live", true)).toBe("present");
    expect(presenceLabel("config", false)).toBe("absent");
  });
});

describe("the plan's two facts", () => {
  it("states 'no operations' and 'nothing undeclared' as separate sentences", () => {
    const ops = opsSummary(0);
    const undeclared = notInSpecSummary(0);
    expect(ops).not.toBe(undeclared);
    expect(ops).toMatch(/No operations/);
    expect(undeclared).toMatch(/Nothing undeclared/);
  });

  it("never implies an undeclared entity would be deleted", () => {
    expect(notInSpecSummary(1)).toMatch(/reported, never deleted/);
    expect(notInSpecSummary(3)).toMatch(/reported, never deleted/);
  });

  it("counts singular and plural correctly", () => {
    expect(opsSummary(1)).toMatch(/^1 operation would/);
    expect(opsSummary(4)).toMatch(/^4 operations would/);
  });
});
