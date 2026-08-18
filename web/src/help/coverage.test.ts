// T-2203 — the coverage gate. Extended by T-3006 to see panels.
//
// The claim this phase makes is "100% of the product's screens have online
// help". A hand-maintained checklist can't back that claim, so this test
// derives the screen inventory from the shipped source: it parses App.tsx
// for every route the router actually declares and Sidebar.tsx (T-3402;
// formerly NavRail.tsx) for every destination the sidebar actually offers,
// then requires each to resolve to a real, substantial help topic.
//
// T-3006 — WHY THIS FILE GREW. The inventory above is routed screens only,
// so the gate was structurally blind to panels: T-2005 shipped the whole
// installable-PWA surface with zero help topics and nothing failed. Three
// source-derived checks close that, and they only work as a set:
//
//   1. FORWARD (panel census → topic). Every panel-shaped module the app
//      actually renders must declare a help topic. "Panel-shaped" is a
//      filename convention this codebase already follows (`*Panel.tsx`,
//      `*Wizard.tsx`, `*Section.tsx`, `*View.tsx`, …) and "actually
//      renders" is import-graph reachability from main.tsx — both read off
//      the tree, neither written down anywhere a person can edit to make a
//      failure go away.
//   2. DECLARATION INTEGRITY. A panel may declare its topic by rendering
//      <HelpAnchor topic="…"> itself, or by handing a literal to a wrapper
//      that renders one (settings/platformCommon.tsx's PlatformSection is
//      the case that forced this). The second form is only trustworthy if
//      every component taking a `helpTopic` prop really does anchor it, so
//      that is asserted directly rather than assumed.
//   3. REVERSE (topic → surface). The forward check proves every panel has
//      a topic. It cannot notice a topic describing a panel that does not
//      exist — which is exactly how `ipv6-planning` came to document a
//      planning grid nobody ever built (T-3004-followup-01). So every
//      topic whose own `surface` says it documents a panel or a dialog
//      must be placed at a `?` somewhere in the tree. A topic about
//      vapourware has nowhere to be placed, and fails here.
//
// ANTI-VACUITY. A source-parsing test that silently matches nothing passes
// trivially and reports full coverage of an empty set — the exact failure
// mode this repo has caught before. Every parse below therefore asserts a
// floor on how much it found AND that a known sentinel is among the
// results, so a regex that stops matching the real file fails loudly
// instead of quietly certifying nothing.
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { allHelpTopics, getHelpTopic } from "./registry";
import { ROUTE_HELP, helpTopicForPath, DEFAULT_HELP_TOPIC } from "./routeTopics";
import type { HelpSurface, HelpTopic } from "./types";

const SRC = resolve(__dirname, "..");

function read(relative: string): string {
  return readFileSync(join(SRC, relative), "utf8");
}

function rel(full: string): string {
  return full.slice(SRC.length + 1);
}

/** Every non-test module under web/src, as absolute paths. */
function sourceFiles(): string[] {
  const found: string[] = [];
  const walk = (dir: string): void => {
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        if (entry !== "node_modules") {
          walk(full);
        }
        continue;
      }
      if (!/\.tsx?$/.test(entry) || /\.test\.tsx?$/.test(entry)) {
        continue;
      }
      found.push(full);
    }
  };
  walk(SRC);
  return found;
}

function captured(source: string, pattern: RegExp): string[] {
  return [...source.matchAll(pattern)].flatMap((m) => (m[1] === undefined ? [] : [m[1]]));
}

/** Every `path="…"` literal declared in the router. */
function routerPaths(): string[] {
  return [...new Set(captured(read("App.tsx"), /<Route[^>]*?\spath="([^"]+)"/g))];
}

/** Every `{ path: "…" }` entry in the sidebar's destination list (T-3402:
 * Sidebar.tsx's FLAT_ITEMS, NAV_GROUPS' items, and SETTINGS_ITEM all use
 * this same object shape, so one regex over the whole file still finds
 * every destination regardless of which list it lives in). */
function navRailPaths(): string[] {
  return [...new Set(captured(read("layout/Sidebar.tsx"), /\{\s*path:\s*"([^"]+)"/g))];
}

// The two ways a module declares "this surface's help topic is X".
//
// The direct one is the `?` itself. The indirect one exists because some
// panels get their heading chrome from a shared wrapper (PlatformSection),
// and duplicating an anchor into the caller just to satisfy a parser would
// put two `?`s on one heading. Integrity of the indirect form is asserted
// separately, below — it is not taken on trust.
const ANCHOR_LITERAL = /<HelpAnchor[^>]*?\stopic="([^"]+)"/g;
const HELP_TOPIC_PROP_LITERAL = /\bhelpTopic\s*[=:]\s*\{?\s*"([^"]+)"/g;

/** Every help topic declared anywhere in the tree, with the file that
 * declared it. */
function anchoredTopics(): { topic: string; file: string }[] {
  const found: { topic: string; file: string }[] = [];
  for (const full of sourceFiles()) {
    if (!full.endsWith(".tsx")) {
      continue;
    }
    const source = readFileSync(full, "utf8");
    for (const topic of [
      ...captured(source, ANCHOR_LITERAL),
      ...captured(source, HELP_TOPIC_PROP_LITERAL),
    ]) {
      found.push({ topic, file: rel(full) });
    }
  }
  return found;
}

// ---------------------------------------------------------------------------
// T-3006: the panel census.
// ---------------------------------------------------------------------------

/** Resolves a relative import specifier the way Vite does, or undefined for
 * a bare package name / an asset with no module behind it. */
function resolveImport(fromFile: string, spec: string): string | undefined {
  if (!spec.startsWith(".")) {
    return undefined;
  }
  const base = resolve(dirname(fromFile), spec);
  for (const candidate of [`${base}.tsx`, `${base}.ts`, join(base, "index.tsx"), join(base, "index.ts")]) {
    if (existsSync(candidate) && statSync(candidate).isFile()) {
      return candidate;
    }
  }
  return undefined;
}

/** Every module reachable from the SPA entry point, static or lazy. This is
 * what makes "is this a panel of the product" a question about the shipped
 * tree rather than about the filesystem: `grafana/MetricsPanel.tsx` is the
 * render body of an out-of-repo Grafana plugin and nothing in the app
 * imports it, so it is not a vnprox panel and cannot be made to sprout a
 * vnprox help drawer. */
function reachableModules(): Set<string> {
  const reachable = new Set<string>();
  const queue = [join(SRC, "main.tsx")];
  while (queue.length > 0) {
    const file = queue.pop();
    if (file === undefined || reachable.has(file)) {
      continue;
    }
    reachable.add(file);
    const source = readFileSync(file, "utf8");
    for (const spec of captured(source, /(?:from|import)\s*\(?\s*["']([^"']+)["']/g)) {
      const target = resolveImport(file, spec);
      if (target !== undefined) {
        queue.push(target);
      }
    }
  }
  return reachable;
}

/** The naming convention this codebase already uses for "a surface inside a
 * screen". Derived from the tree, not from a list of files: adding
 * `web/src/foo/BarPanel.tsx` puts it in the census on the next run.
 *
 * A basename that is *only* the suffix (`components/Drawer.tsx`) is a
 * generic primitive with no product surface of its own — the panel built on
 * it carries the topic — so the prefix must be non-empty. */
const PANEL_SUFFIXES = [
  "Panel",
  "Wizard",
  "Section",
  "Card",
  "Drawer",
  "Planner",
  "Viewer",
  "View",
] as const;

function isPanelShaped(file: string): boolean {
  if (!file.endsWith(".tsx")) {
    return false;
  }
  const base = file.slice(file.lastIndexOf("/") + 1, -".tsx".length);
  return PANEL_SUFFIXES.some((suffix) => base.length > suffix.length && base.endsWith(suffix));
}

interface CensusEntry {
  readonly file: string;
  readonly topics: readonly string[];
}

/** Every panel-shaped module the app actually renders, with the help topics
 * it declares. */
function panelCensus(): CensusEntry[] {
  const reachable = reachableModules();
  return sourceFiles()
    .filter((full) => isPanelShaped(full) && reachable.has(full))
    .map((full) => {
      const source = readFileSync(full, "utf8");
      const topics = [
        ...new Set([...captured(source, ANCHOR_LITERAL), ...captured(source, HELP_TOPIC_PROP_LITERAL)]),
      ];
      return { file: rel(full), topics };
    })
    .sort((a, b) => a.file.localeCompare(b.file));
}

/** Surfaces whose topics claim to document a thing you can point at in the
 * running app. A `concept` or `reference` topic documents an idea and is
 * legitimately reached from `seeAlso` and the browse index only. */
const PLACEABLE_SURFACES: readonly HelpSurface[] = ["panel", "dialog"];

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

  it("walks the import graph from main.tsx and reaches the app", () => {
    // If this resolver ever stops following imports, every panel falls out
    // of the census and the forward check below certifies an empty set.
    const reachable = reachableModules();
    expect(reachable.size).toBeGreaterThanOrEqual(300);
    expect([...reachable].map(rel)).toContain("App.tsx");
    expect([...reachable].map(rel)).toContain("topology/InspectorPanel.tsx");
    // Reached only through React.lazy / dynamic import, so this also pins
    // that the `import(…)` form is still followed.
    expect([...reachable].map(rel)).toContain("layout/Sidebar.tsx");
  });

  it("finds a plausible number of panels, including known ones", () => {
    const census = panelCensus();
    expect(census.length).toBeGreaterThanOrEqual(50);
    const files = census.map((c) => c.file);
    expect(files).toContain("topology/InspectorPanel.tsx");
    expect(files).toContain("governance/PoliciesPanel.tsx");
    expect(files).toContain("sdn/wizards/VlanZoneWizard.tsx");
    expect(files).toContain("settings/TokensSection.tsx");
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

describe("help coverage — every panel has help (T-3006)", () => {
  it("gives every panel the app renders a help topic", () => {
    // The forward half. A new `*Panel.tsx` that the app imports and that
    // says nothing about its own help fails here, by name, the first time
    // the suite runs — which is the whole point: T-2005 shipped an entire
    // surface with no topics because the inventory could not see it.
    const missing = panelCensus()
      .filter((entry) => entry.topics.length === 0)
      .map((entry) => `${entry.file} (no <HelpAnchor topic> and no helpTopic prop)`);
    expect(missing).toEqual([]);
  });

  it("registers every topic a panel declares", () => {
    const dangling: string[] = [];
    for (const entry of panelCensus()) {
      for (const topic of entry.topics) {
        if (getHelpTopic(topic) === undefined) {
          dangling.push(`${entry.file}: topic="${topic}" is not registered`);
        }
      }
    }
    expect(dangling).toEqual([]);
  });

  it("anchors every component that accepts a helpTopic prop", () => {
    // Declaration integrity. `helpTopic="…"` counts as a declaration in the
    // census above only because the component receiving it is known to
    // render an anchor from it. Without this, `helpTopic="anything"` in a
    // module with no `?` at all would satisfy the census — an allowlist
    // with extra steps.
    const unanchored: string[] = [];
    for (const full of sourceFiles()) {
      if (!full.endsWith(".tsx")) {
        continue;
      }
      const source = readFileSync(full, "utf8");
      if (!/\bhelpTopic\s*\??\s*:\s*string\b/.test(source)) {
        continue;
      }
      if (!/<HelpAnchor[^>]*?\stopic=\{\s*helpTopic\s*\}/.test(source)) {
        unanchored.push(`${rel(full)}: takes a helpTopic prop but never renders <HelpAnchor topic={helpTopic}>`);
      }
    }
    expect(unanchored).toEqual([]);
    // Anti-vacuity: the indirection this rule polices must still exist.
    const wrappers = sourceFiles().filter(
      (full) => full.endsWith(".tsx") && /\bhelpTopic\s*\??\s*:\s*string\b/.test(readFileSync(full, "utf8")),
    );
    expect(wrappers.map(rel)).toContain("settings/platformCommon.tsx");
  });
});

describe("help coverage — every topic describes something real (T-3006)", () => {
  it("places every panel and dialog topic at a `?` in the shipped tree", () => {
    // The reverse half, and the answer to T-3004-followup-01: the gate
    // proved every screen HAS a topic and proved nothing about a topic
    // having a screen. `ipv6-planning` documented an IPv6 planning grid
    // that was never built and `topology-paint-modes` documented a
    // backup-path map overlay that does not exist; both passed every check
    // in this file because a topic could live on `seeAlso` links alone.
    //
    // This is not a claim that the prose is accurate — no test can check
    // that. It is the weaker, checkable claim that the surface the topic
    // says it documents is one an operator can actually put a cursor on.
    const placed = new Set(anchoredTopics().map((a) => a.topic));
    const unplaced = allHelpTopics()
      .filter((t) => PLACEABLE_SURFACES.includes(t.surface))
      .filter((t) => !placed.has(t.id))
      .map((t) => `${t.id} (surface: ${t.surface}) — no <HelpAnchor topic="${t.id}"> anywhere in web/src`);
    expect(unplaced).toEqual([]);
  });

  it("still has panel and dialog topics to check", () => {
    // A ratchet, and the reason the check above cannot be dodged. The only
    // way to make an unplaceable topic pass is to change its `surface` to
    // something this check ignores — which drops this count and fails here,
    // naming nothing but making the reclassification impossible to do
    // quietly. Raise the floor when the real number goes up; never lower
    // it to accommodate a demotion.
    const placeable = allHelpTopics().filter((t) => PLACEABLE_SURFACES.includes(t.surface));
    expect(placeable.length).toBeGreaterThanOrEqual(60);
    expect(placeable.map((t) => t.id)).toContain("path-simulator");
  });

  it("makes every headless topic name the route or CLI that does reach it", () => {
    // `headless` is the escape valve for "the daemon has it, this build
    // has no screen for it" — and an escape valve nobody checks becomes a
    // dumping ground. A topic that claims this surface has to say how an
    // operator actually gets at the thing, or it is just a panel topic
    // with the gate switched off.
    const vague: string[] = [];
    for (const topic of allHelpTopics()) {
      if (topic.surface !== "headless") {
        continue;
      }
      const blob = [topic.summary, ...topic.sections.map((s) => `${s.heading}\n${s.body}`)].join("\n");
      if (!/\/api\/v1\/|vnproxctl/.test(blob)) {
        vague.push(`${topic.id}: says there is no screen but names no route or CLI verb that reaches it`);
      }
      if (!/no screen|not in the web UI|never existed|has never existed/i.test(blob)) {
        vague.push(`${topic.id}: marked headless but never says so in its prose`);
      }
    }
    expect(vague).toEqual([]);
  });

  it("has headless topics to check", () => {
    const headless = allHelpTopics().filter((t) => t.surface === "headless");
    expect(headless.length).toBeGreaterThanOrEqual(4);
    expect(headless.map((t) => t.id)).toContain("ipv6-planning");
  });
});

// ---------------------------------------------------------------------------
// T-2202-followup-02, absorbed by T-3006.
//
// Phase 22's scope boundary said in as many words that "100% coverage" did
// not include form fields, and that docs/features/change-management.md §5's
// "every field has inline help written for non-networking-experts" was a
// separate, larger piece of work. This is the enforcement half of closing
// it: writing the help was the easy part, keeping it is what needs a test.
// ---------------------------------------------------------------------------
describe("help coverage — entity editors have field-level help (T-2202-followup-02)", () => {
  /** Every `<Field …>` opening tag under the entity editors, with whether
   * it carries a `help` prop. */
  function editorFields(): { file: string; tag: string; hasHelp: boolean }[] {
    const dir = join(SRC, "changesets", "editors");
    const found: { file: string; tag: string; hasHelp: boolean }[] = [];
    for (const entry of readdirSync(dir)) {
      if (!entry.endsWith(".tsx") || entry.endsWith(".test.tsx")) {
        continue;
      }
      const source = readFileSync(join(dir, entry), "utf8");
      // Only the modules that *consume* `Field`. EditorDialog.tsx defines
      // it and mentions `<Field>` in its own doc comments, which is not a
      // form field anybody fills in.
      if (!/import\s*\{[^}]*\bField\b[^}]*\}\s*from\s*"\.\/EditorDialog"/.test(source)) {
        continue;
      }
      // `<Field` through its closing `>`, tolerating the multi-line form
      // the longer help strings force. Nested braces in a `help={cond ? …}`
      // expression mean a naive `[^>]*` would stop early, so this consumes
      // to the first `>` that ends a tag at brace depth zero.
      for (let i = source.indexOf("<Field"); i !== -1; i = source.indexOf("<Field", i + 1)) {
        if (/[A-Za-z]/.test(source[i + "<Field".length] ?? "")) {
          continue; // <FieldHelp>, not <Field>
        }
        let depth = 0;
        let end = i;
        for (let j = i; j < source.length; j++) {
          const ch = source[j];
          if (ch === "{") depth++;
          else if (ch === "}") depth--;
          else if (ch === ">" && depth === 0) {
            end = j;
            break;
          }
        }
        const tag = source.slice(i, end + 1);
        found.push({ file: `changesets/editors/${entry}`, tag, hasHelp: /\shelp[=\s]/.test(tag) });
      }
    }
    return found;
  }

  it("finds the fields it is meant to be checking", () => {
    const fields = editorFields();
    expect(fields.length).toBeGreaterThanOrEqual(25);
    expect(fields.map((f) => f.file)).toContain("changesets/editors/BondEditor.tsx");
    expect(fields.map((f) => f.file)).toContain("changesets/editors/BridgeEditor.tsx");
    expect(fields.map((f) => f.file)).toContain("changesets/editors/VlanEditor.tsx");
    expect(fields.map((f) => f.file)).toContain("changesets/editors/InterfaceEditor.tsx");
  });

  it("gives every editor field inline help", () => {
    const bare = editorFields()
      .filter((f) => !f.hasHelp)
      .map((f) => `${f.file}: ${f.tag.replace(/\s+/g, " ").slice(0, 90)} — no help= prop`);
    expect(bare).toEqual([]);
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
