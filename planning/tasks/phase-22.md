# Phase 22 — Online help (arc: "a shipped product answers its own questions")

**Premise.** vnprox has 25 routed screens, four wizards, a change engine with
irreversible-looking consequences, and an in-app help surface that consists of
exactly one dialog listing keyboard shortcuts. Everything a user needs to know
lives in `docs/` — on disk, in a git repo, on a machine they are probably not
looking at while a commit-confirm countdown is running. That is a documentation
set, not online help.

**Goal.** 100% of the product's screens answer "what is this, and what do I do
here?" without leaving the browser — and the coverage is enforced by a test that
fails the build when a new screen ships without help, rather than asserted in a
report.

---

## T-2201 — Help engine

**kind:** implementation
**depends on:** —

Build `web/src/help/` — the content model, registry, and lookup that everything
else in this phase consumes.

- `types.ts` — `HelpTopic` (id, title, surface, summary, sections, steps,
  seeAlso, docRef, keywords). Content is typed TypeScript, bundled into the
  SPA: no runtime fetch, no new API surface (v3.0 platform freeze), works with
  the daemon unreachable — which is exactly when a user needs the rollback
  instructions.
- `registry.ts` — merges the content modules, rejects duplicate ids at module
  load, exposes `getHelpTopic` / `allHelpTopics` / `searchHelp`.
- `inline.ts` — a two-rule inline formatter (`**bold**`, `` `code` ``) so help
  prose can emphasise UI labels and config keys without adding a markdown
  dependency to the locked stack (`docs/development.md`).
- `store.ts` — zustand store holding open state, the current topic, and a back
  stack (so `seeAlso` navigation is reversible).

**Acceptance**

1. `getHelpTopic` returns `undefined` for an unknown id, never throws.
2. Duplicate topic ids fail at import time with the offending id named.
3. `searchHelp` matches title, summary, section headings/bodies, and keywords;
   results rank exact-title matches first.
4. The inline formatter round-trips plain text unchanged and never emits
   `dangerouslySetInnerHTML`.

## T-2202 — Help content

**kind:** implementation
**depends on:** T-2201

Write the content. Every topic is sourced from an existing repo doc, and names
that doc in `docRef` — help that invents behaviour is worse than no help.

- One topic per routed screen (25).
- Concept topics for the things the UI assumes you understand: the change
  engine's stage → validate → diff → apply → confirm sequence, commit-confirm
  and unattended rollback, protected interfaces, snapshots/time machine,
  findings, drift, permissions, read-only mode, cluster awareness.
- Platform topics for the v2.0/v3.0 opt-ins: federation, MCP/AI operators,
  plugins, tenants, HA, the hub's trust gate, embeds, switch push, OIDC, PBS.
- Panel topics for the surfaces inside a screen that carry their own risk or
  their own vocabulary: path simulator, raw editor, zone wizards, IPAM grid and
  external sync, redundancy wizard, bulk reattach, capture, alert rules,
  changeset review, snapshot restore.

**Acceptance**

1. Every topic names a `docRef` that exists on disk.
2. No topic contains placeholder text.
3. Content states the safety boundaries accurately where they apply — in
   particular that AI operators and plugins can stage but never apply, and that
   a switch push cannot be remotely rolled back.

## T-2203 — Coverage gate

**kind:** validation
**depends on:** T-2201, T-2202

The claim is "100%". This card is what makes the claim checkable.

`web/src/help/coverage.test.ts` derives the screen inventory from the shipped
source rather than from a hand-maintained list:

- Parse `src/App.tsx` for every `path="…"` literal. Every one (bar the `*`
  catch-all) must map to a topic that exists.
- Parse `src/layout/NavRail.tsx` for every nav destination; same requirement.
- Every `<HelpAnchor topic="…">` in the tree must resolve to a registered topic.
- Every `seeAlso` id must resolve.
- No orphans: every topic must be reachable from a route mapping, an anchor, or
  the browse index.
- Content quality floor: non-empty title, summary ≥ 60 chars, ≥ 2 sections, each
  section body ≥ 80 chars, no `TODO`/`TBD`/`Lorem`/`coming soon`.

**Anti-vacuity.** A source-parsing test that silently matches nothing passes
trivially. Each parse asserts a floor on what it found and asserts a known
sentinel is among the results (`/topology` for the router, `/audit` for the nav
rail) — if the regex ever stops matching the real file, the test fails loudly
instead of reporting full coverage of an empty set.

**Acceptance**

1. Deleting any one route topic fails the suite, naming the route.
2. Adding a route to `App.tsx` with no help fails the suite, naming the path.
3. The sentinel assertions fail if the parse returns an empty set.

## T-2204 — Help surface and entry points

**kind:** implementation
**depends on:** T-2201, T-2202

- `HelpPanel` — right-hand drawer: the current screen's topic, a search box
  across all topics, a browse index grouped by surface, and back navigation.
- `HelpAnchor` — the inline `?` affordance placed next to a panel heading,
  opening the panel directly at that panel's topic.
- `HelpButton` in the top bar, and `F1` bound app-wide.
- The existing `?` shortcut and its dialog are left exactly as they are; the
  keyboard topic links to them. Additive, so no existing test changes meaning.

**Acceptance**

1. The panel opens on the current route's topic from any screen.
2. Escape closes; focus returns to the invoking control.
3. Search finds a topic by a word that appears only in its body.
4. `seeAlso` navigation pushes and Back pops.
5. Axe finds no violations on the open panel.

## T-2205 — Documentation and gate wiring

**kind:** implementation
**depends on:** T-2203

- `docs/user-guide.md` gains a section on the help surface.
- `docs/development.md` gains the rule the gate enforces: a new route ships with
  a help topic in the same change.
- Playwright spec for the panel, alongside the vitest coverage (noting
  `T-1806-bug-01`: the e2e suite is not run by any automated gate, so vitest
  carries the enforcement).
