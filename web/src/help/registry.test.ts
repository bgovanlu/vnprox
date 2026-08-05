import { describe, expect, it } from "vitest";
import { getHelpTopic, searchHelp, allHelpTopics } from "./registry";
import { helpTopicForPath, DEFAULT_HELP_TOPIC } from "./routeTopics";
import { tokenizeInline, plainText } from "./inline";

describe("getHelpTopic", () => {
  it("resolves a registered id", () => {
    expect(getHelpTopic("safety-model")?.title).toContain("cutting yourself off");
  });

  it("returns undefined rather than throwing for an unknown id", () => {
    // A stale id in a bookmarked URL must render "no such topic", never
    // blank the page.
    expect(getHelpTopic("no-such-topic")).toBeUndefined();
  });
});

describe("searchHelp", () => {
  it("returns nothing for an empty query", () => {
    expect(searchHelp("   ")).toEqual([]);
  });

  it("ranks an exact title match above a body mention", () => {
    const hits = searchHelp("drift");
    expect(hits.length).toBeGreaterThan(1);
    expect(hits[0]?.topic.id).toBe("drift");
  });

  it("requires every term to match, not just one", () => {
    // "wireguard" and "microsegmentation" both match topics on their own;
    // no topic holds both, so an OR search would wrongly return several.
    expect(searchHelp("wireguard microsegmentation")).toEqual([]);
  });

  it("matches a keyword that never appears in the prose", () => {
    // "lockout" is a keyword on commit-confirm/protected-interfaces but
    // isn't a word either topic's body uses.
    const hits = searchHelp("lockout");
    expect(hits.map((h) => h.topic.id)).toContain("commit-confirm");
  });

  it("is case-insensitive", () => {
    expect(searchHelp("ROLLBACK").length).toBe(searchHelp("rollback").length);
  });

  it("reports which section a body-only match came from", () => {
    const hit = searchHelp("squatter").find((h) => h.topic.id === "ipam-page");
    expect(hit?.matchedIn).toBeTruthy();
  });
});

describe("helpTopicForPath", () => {
  it("resolves an exact route", () => {
    expect(helpTopicForPath("/topology")).toBe("topology-page");
  });

  it("resolves a parameterized route", () => {
    expect(helpTopicForPath("/changesets/abc123/review")).toBe("changeset-review-page");
  });

  it("prefers the longer of two matching prefixes", () => {
    // /settings/alert-rules must not resolve to /settings.
    expect(helpTopicForPath("/settings/alert-rules")).toBe("alert-rules-page");
    expect(helpTopicForPath("/settings")).toBe("settings-page");
  });

  it("falls back to a real topic for an unmapped path", () => {
    const id = helpTopicForPath("/nowhere-at-all");
    expect(id).toBe(DEFAULT_HELP_TOPIC);
    expect(getHelpTopic(id)).toBeDefined();
  });

  it("maps the index route's pathname", () => {
    expect(helpTopicForPath("/")).toBe("dashboard-page");
  });
});

describe("inline formatter", () => {
  it("leaves unmarked text as a single token", () => {
    expect(tokenizeInline("plain text")).toEqual([{ kind: "text", text: "plain text" }]);
  });

  it("tokenizes bold and code", () => {
    expect(tokenizeInline("hit **Apply** then `ifreload -a`")).toEqual([
      { kind: "text", text: "hit " },
      { kind: "bold", text: "Apply" },
      { kind: "text", text: " then " },
      { kind: "code", text: "ifreload -a" },
    ]);
  });

  it("treats an unclosed marker as literal text rather than swallowing the rest", () => {
    expect(tokenizeInline("a ** dangling marker")).toEqual([{ kind: "text", text: "a ** dangling marker" }]);
  });

  it("does not let a marker span a newline", () => {
    const input = "start **not\nclosed** end";
    expect(tokenizeInline(input).every((t) => t.kind === "text")).toBe(true);
  });

  it("strips markers for search", () => {
    expect(plainText("hit **Apply** then `x`")).toBe("hit Apply then x");
  });

  it("is not affected by regex lastIndex between calls", () => {
    const input = "**a** and **b**";
    expect(tokenizeInline(input)).toEqual(tokenizeInline(input));
  });
});

describe("registry integrity", () => {
  it("keeps every topic id kebab-case and unique", () => {
    const ids = allHelpTopics().map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids.filter((id) => !/^[a-z0-9]+(-[a-z0-9]+)*$/.test(id))).toEqual([]);
  });
});
