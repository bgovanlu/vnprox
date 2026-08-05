// T-2203 — the coverage gate.
//
// The claim this phase makes is "100% of the product's screens have online
// help". A hand-maintained checklist can't back that claim, so this test
// derives the screen inventory from the shipped source: it parses App.tsx
// for every route the router actually declares and NavRail.tsx for every
// destination the nav rail actually offers, then requires each to resolve
// to a real, substantial help topic.
//
// ANTI-VACUITY. A source-parsing test that silently matches nothing passes
// trivially and reports full coverage of an empty set — the exact failure
// mode this repo has caught before. Every parse below therefore asserts a
// floor on how much it found AND that a known sentinel is among the
// results, so a regex that stops matching the real file fails loudly
// instead of quietly certifying nothing.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { allHelpTopics, getHelpTopic } from "./registry";
import { ROUTE_HELP, helpTopicForPath, DEFAULT_HELP_TOPIC } from "./routeTopics";
import type { HelpTopic } from "./types";

const SRC = resolve(__dirname, "..");

function read(relative: string): string {
  return readFileSync(join(SRC, relative), "utf8");
}

function captured(source: string, pattern: RegExp): string[] {
  return [...source.matchAll(pattern)].flatMap((m) => (m[1] === undefined ? [] : [m[1]]));
}

/** Every `path="…"` literal declared in the router. */
function routerPaths(): string[] {
  return [...new Set(captured(read("App.tsx"), /<Route[^>]*?\spath="([^"]+)"/g))];
}

/** Every `{ path: "…" }` entry in the nav rail's destination list. */
function navRailPaths(): string[] {
  return [...new Set(captured(read("layout/NavRail.tsx"), /\{\s*path:\s*"([^"]+)"/g))];
}

/** Every `<HelpAnchor topic="…">` placed anywhere in the tree. */
function anchoredTopics(): { topic: string; file: string }[] {
  const found: { topic: string; file: string }[] = [];
  const walk = (dir: string): void => {
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        if (entry !== "node_modules") {
          walk(full);
        }
        continue;
      }
      if (!entry.endsWith(".tsx") || entry.endsWith(".test.tsx")) {
        continue;
      }
      const source = readFileSync(full, "utf8");
      for (const topic of captured(source, /<HelpAnchor[^>]*?\stopic="([^"]+)"/g)) {
        found.push({ topic, file: full.slice(SRC.length + 1) });
      }
    }
  };
  walk(SRC);
  return found;
}

const PLACEHOLDER = /\b(TODO|TBD|FIXME|lorem ipsum|coming soon|to be written|xxx)\b/i;

describe("help coverage — the parsers actually parse", () => {
  // These three assertions are the gate on the gate. If a refactor changes
  // how routes or anchors are declared and the regexes above stop matching,
  // every coverage test below would pass over an empty set. These fail
  // first, and say why.
  it("finds a plausible number of routes in App.tsx, including a known one", () => {
    const paths = routerPaths();
    expect(paths.length).toBeGreaterThanOrEqual(20);
    expect(paths).toContain("/topology");
    expect(paths).toContain("/settings/federation");
  });

  it("finds a plausible number of nav destinations, including a known one", () => {
    const paths = navRailPaths();
    expect(paths.length).toBeGreaterThanOrEqual(10);
    expect(paths).toContain("/audit");
  });

  it("finds the HelpAnchors that are known to be placed in the tree", () => {
    const anchors = anchoredTopics();
    expect(anchors.length).toBeGreaterThanOrEqual(5);
    expect(anchors.map((a) => a.topic)).toContain("path-simulator");
  });
});

describe("help coverage — every screen has help", () => {
  it("maps every route declared in App.tsx to a registered topic", () => {
    const missing: string[] = [];
    for (const path of routerPaths()) {
      // The catch-all redirect is not a screen.
      if (path === "*") continue;
      const topicId = ROUTE_HELP[path];
      if (topicId === undefined) {
        missing.push(`${path} (no ROUTE_HELP entry)`);
        continue;
      }
      if (getHelpTopic(topicId) === undefined) {
        missing.push(`${path} → ${topicId} (topic not registered)`);
      }
    }
    expect(missing).toEqual([]);
  });

  it("maps every nav-rail destination to a registered topic", () => {
    const missing: string[] = [];
    for (const path of navRailPaths()) {
      const topicId = helpTopicForPath(path);
      if (topicId === DEFAULT_HELP_TOPIC && ROUTE_HELP[path] === undefined) {
        missing.push(`${path} (falls back to the default topic)`);
        continue;
      }
      if (getHelpTopic(topicId) === undefined) {
        missing.push(`${path} → ${topicId} (topic not registered)`);
      }
    }
    expect(missing).toEqual([]);
  });

  it("has no ROUTE_HELP entry for a route the router no longer declares", () => {
    // The other direction: stale mappings are how a "100%" number stays
    // green while pointing at screens that were deleted.
    const declared = new Set(routerPaths());
    // App.tsx's `<Route index>` has no path attribute of its own; "/" is
    // the pathname it serves and is therefore expected to be absent here.
    const stale = Object.keys(ROUTE_HELP).filter((p) => p !== "/" && !declared.has(p));
    expect(stale).toEqual([]);
  });

  it("resolves every <HelpAnchor topic> in the tree to a registered topic", () => {
    const dangling = anchoredTopics()
      .filter((a) => getHelpTopic(a.topic) === undefined)
      .map((a) => `${a.file}: topic="${a.topic}"`);
    expect(dangling).toEqual([]);
  });
});

describe("help coverage — content quality floor", () => {
  const topics = allHelpTopics();

  it("registers a substantial number of topics", () => {
    expect(topics.length).toBeGreaterThanOrEqual(50);
  });

  it("gives every topic a title, a real summary, and at least two sections", () => {
    const bad: string[] = [];
    for (const topic of topics) {
      if (topic.title.trim().length === 0) bad.push(`${topic.id}: empty title`);
      if (topic.summary.trim().length < 60) bad.push(`${topic.id}: summary too short`);
      if (topic.sections.length < 2) bad.push(`${topic.id}: fewer than 2 sections`);
      for (const section of topic.sections) {
        if (section.heading.trim().length === 0) bad.push(`${topic.id}: empty section heading`);
        if (section.body.trim().length < 80) {
          bad.push(`${topic.id} / ${section.heading}: body too short`);
        }
      }
    }
    expect(bad).toEqual([]);
  });

  it("contains no placeholder prose", () => {
    const bad: string[] = [];
    for (const topic of topics) {
      const blob = [
        topic.title,
        topic.summary,
        ...topic.sections.flatMap((s) => [s.heading, s.body]),
        ...(topic.steps ?? []),
      ].join("\n");
      if (PLACEHOLDER.test(blob)) {
        bad.push(topic.id);
      }
    }
    expect(bad).toEqual([]);
  });

  it("names a docRef that exists on disk for every topic", () => {
    // Help that invents behaviour is worse than no help. Every topic cites
    // the repo doc it was written from, and this checks the citation is
    // real — not that the prose matches it, which only a human can do.
    const repoRoot = resolve(SRC, "..", "..");
    const bad: string[] = [];
    for (const topic of topics) {
      try {
        statSync(join(repoRoot, topic.docRef));
      } catch {
        bad.push(`${topic.id}: ${topic.docRef}`);
      }
    }
    expect(bad).toEqual([]);
  });

  it("resolves every seeAlso reference", () => {
    const bad: string[] = [];
    for (const topic of topics) {
      for (const id of topic.seeAlso ?? []) {
        if (getHelpTopic(id) === undefined) {
          bad.push(`${topic.id} → ${id}`);
        }
        if (id === topic.id) {
          bad.push(`${topic.id} → itself`);
        }
      }
    }
    expect(bad).toEqual([]);
  });

  it("uses a unique title per topic", () => {
    const seen = new Map<string, string>();
    const collisions: string[] = [];
    for (const topic of topics) {
      const prior = seen.get(topic.title);
      if (prior !== undefined) {
        collisions.push(`"${topic.title}": ${prior} and ${topic.id}`);
      }
      seen.set(topic.title, topic.id);
    }
    expect(collisions).toEqual([]);
  });
});

describe("help coverage — no orphans", () => {
  it("leaves no topic unreachable from a screen or an anchor", () => {
    // Write-once-never-linked content is the other way a help system rots:
    // the topic exists, the coverage number counts it, and nobody can get
    // to it. Reachability is the transitive closure of seeAlso from the
    // real entry points — route topics and placed anchors.
    const byId = new Map<string, HelpTopic>(allHelpTopics().map((t) => [t.id, t]));
    const roots = [...Object.values(ROUTE_HELP), ...anchoredTopics().map((a) => a.topic), DEFAULT_HELP_TOPIC];

    const reachable = new Set<string>();
    const queue = [...roots];
    while (queue.length > 0) {
      const id = queue.pop();
      if (id === undefined || reachable.has(id)) continue;
      reachable.add(id);
      for (const next of byId.get(id)?.seeAlso ?? []) {
        queue.push(next);
      }
    }

    const orphans = allHelpTopics()
      .map((t) => t.id)
      .filter((id) => !reachable.has(id));
    expect(orphans).toEqual([]);
  });
});
