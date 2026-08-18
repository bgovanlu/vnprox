# Phase 34 — Stripe-style cockpit shell

Goal: replace the app shell's visual language with the Stripe-dashboard idiom the product owner
has picked as the reference (screenshot reviewed 2026-08-18): a light, grouped, collapsible
sidebar with section labels and a pinned bottom item; a slim top bar with a rounded search
field; the demo banner restyled as Stripe's dark "You're testing" test-mode bar; page bodies led
by a large title, pill-shaped action buttons, and underlined tab navigation; an indigo accent on
otherwise-neutral surfaces.

Like Phase 9, this is a **frontend-only** consolidation pass: zero changes under `internal/`,
`docs/api.md` routes, or any wire contract. It renders exactly the data today's shell renders.
The one dependency change is an icon library (see "Decisions" below), which must be recorded in
`docs/development.md`'s approved-deps table in the same change that adds it.

## What the Stripe reference gives us, mapped to vnprox

| Stripe element (screenshot) | vnprox mapping |
|---|---|
| Dark navy "Sandbox / You're testing" top banner | `DemoBanner` restyled as a full-width dark bar above the shell |
| Light sidebar, account switcher chip at top | Sidebar with instance identity chip (cluster name, or "demo") |
| Flat primary items (Home, Balances, …) | Home, Topology, Guests, Management |
| "Shortcuts" section (user-pinned) | Pinned favorites (stretch — see T-3402) |
| Section labels + collapsible groups ("Products" → Payments ▸ sub-items) | "Network", "Operate", "Automate" groups (see T-3402) |
| Pinned bottom item (Developers) | Settings |
| Rounded search field | Existing spotlight-search trigger, restyled |
| Big page title + pill buttons ("+ Add funds") | Shared `PageHeader` component (T-3404) |
| Underlined tabs (Payouts / Top-ups) | Restyled Radix tabs (T-3404) |
| Indigo/violet accent, generous radii, subtle borders | Token change only — accent alias re-pointed (T-3401) |

## Decisions (do not re-litigate; flag if blocked)

- **Accent**: the `accent-*` alias layer in `web/src/index.css` re-points from Tailwind's blue
  scale to the indigo scale (closest to Stripe's #635BFF). The alias mechanism itself — and the
  `html.demo` amber override that re-tints the whole app in demo mode (T-2801) — is untouched.
- **Icons**: `lucide-react` (tree-shakeable, ESM, permissive license) replaces the single-letter
  glyph stand-ins. This is the phase's only new dependency; add it to `docs/development.md`'s
  frontend stack table in the same commit.
- **Dark mode stays.** Stripe is light-first; vnprox has a working class-based dark mode with
  axe-verified contrast (T-905/T-2004 history). Every new surface defines both modes, and the
  phase-closing axe pass (T-3406) gates on both, plus the demo-amber variant.
- **No router changes.** Paths, `DesktopOnlyRoute` gating, and the narrow-viewport reachable
  set (`/`, `/tools` — T-909) are preserved exactly.

## Preserved load-bearing details (each has bitten this project before)

- `nav[aria-label="Primary"]` and bare `header` are selectors in the print stylesheet
  (`index.css` T-906) and in e2e specs — the new sidebar/topbar keep those accessible names.
- The sidebar keeps `relative z-50` (T-909: `CountdownBanner` is `fixed z-40` and previously
  painted over the nav, making items unclickable).
- The findings-count badge (T-602) survives on the Tools item, with its T-2004 contrast fix
  (dark text on amber, no opacity).
- Icon-only rail below `sm` keeps explicit `aria-label`s on links (hidden-label accessible-name
  computation, T-909).
- All contrast claims are axe-verified, not eyeballed — this codebase has three separate commits
  fixing "looked fine" colors that failed WCAG AA.

Per-card testing rule (same as Phase 9): Vitest + Testing Library for logic-bearing components,
at least one `web/e2e/*.spec.ts` Playwright scenario per card, axe for anything that changes
color or chrome, and the existing bundle budget (`web/src/build/bundleSplit.test.ts`) must keep
passing. UI verification runs against the real dev-host Playwright harness, not screenshots of
minified builds.

Dependency shape: T-3401 unblocks everything; T-3402 and T-3403 run in parallel after it;
T-3405 precedes T-3404 (the page-header pattern consumes the restyled Button/Tabs); T-3406
closes the phase.

---

## T-3401 · Design tokens: Stripe-inspired foundation
**model:** sonnet-5 · **size:** S · **depends:** — · **context:** `web/src/index.css`, `docs/development.md` (frontend stack), `web/src/components/Button.tsx` (current accent usage)

**Objective:** Establish the phase's visual vocabulary as tokens only — accent, radii, borders,
shadows, type scale — so every later card styles against names, not raw values.

**Deliverables:**
- `accent-*` alias re-pointed blue → indigo in `@theme`; `html.demo` amber override verified
  still effective (it overrides the same custom properties, so it should be untouched — prove
  it with a test, don't assume).
- New semantic tokens in `@theme`: `--radius-pill` (full), surface/border/muted-text roles for
  both modes if not expressible with existing slate utilities.
- A short "visual language" section appended to `docs/development.md`: pill buttons for page
  actions, underlined tabs, section-labeled sidebar, when to use borders vs shadows.

**Acceptance criteria:**
1. `make check` passes with the accent swap; no component file changes needed for the re-tint
   (the alias layer absorbs it).
2. A Vitest test asserts the demo-mode override still wins over the new base accent (computed
   style or class-application check, matching however T-2801's test did it).
3. axe contrast run on the existing shell with the new accent shows no new violations in either
   theme.

## T-3402 · Sidebar: grouped, collapsible, iconed, pinned-bottom
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-3401 · **context:** `web/src/layout/NavRail.tsx` + test, `web/src/lib/useNarrowViewport.ts`, `web/src/findings/queries.ts`, `index.css` print rules, `web/e2e` nav-touching specs

**Objective:** Replace `NavRail` with a Stripe-style `Sidebar`: light surface, grouped sections
with muted section labels, collapsible groups with chevrons, real icons, an instance-identity
chip at top, Settings pinned at the bottom.

**Deliverables:**
- `web/src/layout/Sidebar.tsx` (NavRail.tsx retired in the same change — no dead component left
  behind), rendering:
  - Identity chip at top: cluster/instance name (from the session/health data the shell already
    has), "demo" labeled distinctly in demo mode — Stripe's account-switcher slot, minus the
    switching (single instance today; do not build a fake switcher).
  - Flat primary items: Home, Topology, Guests, Management.
  - **Network** group: SDN, Firewall, IPAM, Ports, Edge, Flows, Conntrack.
  - **Operate** group: History, Incidents, Audit, Analysis, Tools (keeps findings badge).
  - **Automate** group: Config as code, Governance, Blueprints, Hub.
  - Settings pinned at the bottom, visually separated (Stripe's "Developers" slot).
- Groups collapse/expand; state persisted in `localStorage`; a group containing the active
  route auto-expands on navigation.
- `lucide-react` icons per item (added to `docs/development.md` deps table); glyph letters gone.
- Narrow viewport: same reachable-set filter (`/`, `/tools`), icon-only, aria-labels intact.
- `aria-label="Primary"`, `relative z-50` preserved; groups use the WAI-ARIA APG disclosure
  pattern — a trigger button carrying `aria-expanded`, with the group's items unmounted while
  collapsed. (This sentence originally read "collapsed groups still expose their items to the
  accessibility tree", which was wrong: under the APG pattern a collapsed panel is *removed*
  from the tree, and that is what axe expects. Corrected during implementation rather than
  after, so it does not mislead T-3406's axe sweep.)

**Acceptance criteria:**
1. Vitest: grouping renders all 21 routes exactly once; active-route auto-expand works;
   collapse state round-trips localStorage; findings badge renders on Tools with count.
2. e2e: navigate to a route inside a collapsed group via expand→click; countdown-banner overlap
   scenario (the T-909 regression) still passes.
3. axe: zero violations on the sidebar in light, dark, and demo-amber, at both widths.
4. Bundle budget test passes with lucide-react (per-icon imports only — no barrel import).

## T-3403 · Top bar + demo test-mode bar
**model:** sonnet-5 · **size:** M · **depends:** T-3401 · **context:** `web/src/layout/TopBar.tsx` + test, `web/src/demo/DemoBanner.tsx`, `web/src/layout/OfflineShellBanner.tsx`

**Objective:** Restyle the top bar to the reference — rounded search field, quieter ghost
actions, account dropdown as a chip — and restyle `DemoBanner` as Stripe's dark full-width
test-mode bar.

**Deliverables:**
- TopBar: search trigger becomes a rounded-full field (same behavior: opens spotlight via
  topology store); Assistant/Help/?/theme/account controls regrouped Stripe-style (icon buttons
  with tooltips where the label adds nothing). `header` element and all existing aria-labels
  and keyboard entry points (`/`, `?`, F1) unchanged.
- DemoBanner: dark navy full-width bar (reads correctly in both themes), same mount point and
  normal-flow layout (the collision constraints documented in `AppShell.tsx` still apply), same
  "renders nothing outside demo mode" contract. OfflineShellBanner restyled to match the new
  banner family but visually distinct from the demo bar.

**Deliverables — explicitly out:** no search relocation into the sidebar (Stripe has done both;
the top bar placement is what the screenshot shows in the content column and it avoids
touching the narrow-width rail).

**Acceptance criteria:**
1. Existing TopBar/DemoBanner/OfflineShellBanner Vitest suites pass with only styling-level
   assertion updates (behavioral assertions unchanged).
2. e2e: `/` opens search from a non-topology page and lands on topology with spotlight open
   (existing behavior, re-proven under the new markup).
3. axe: both themes, demo bar included.

## T-3404 · PageHeader + pill actions + underlined tabs, rolled out to every page
**model:** sonnet-5 · **size:** L · **depends:** T-3405 · **context:** `web/src/pages/*.tsx` (24 files), `web/src/components/Button.tsx`, Radix tabs usages (`grep @radix-ui/react-tabs`), `web/src/help/useHelpForRoute.ts`

**Objective:** One shared header pattern — large title, optional description line, pill action
buttons right-aligned, optional underlined tab row — replacing each page's ad-hoc `<h1>`/toolbar
markup, so the content column reads like the reference's "Balances" screen.

**Deliverables:**
- `web/src/components/PageHeader.tsx` (title, actions slot, tabs slot) and a restyled shared
  `Tabs` wrapper over the existing Radix dep (underline indicator, muted inactive labels).
- All routed pages migrated. Pages with existing tab-like sub-navigation adopt the shared Tabs;
  pages with none simply get title + actions. Login and the topology full-bleed map keep their
  own layouts (explicitly exempt — the map is not a document page).
- No behavior changes: every action button keeps its handler, test id, and accessible name.

**Acceptance criteria:**
1. Every migrated page's existing Vitest suite passes; a new PageHeader suite covers
   title/actions/tabs slots and tab keyboard navigation (arrow keys, Radix defaults).
2. e2e smoke: one representative tabbed page (SDN or Firewall) switches tabs and deep-links.
3. axe on three representative pages, both themes.

## T-3405 · Core component restyle
**model:** sonnet-5 · **size:** M · **depends:** T-3401 · **context:** `web/src/components/` (Button, Table, Dialog, Drawer, EmptyState, Toast, Tooltip) + tests, `density.ts`

**Objective:** Bring the shared primitives to the reference's look: pill-option buttons, quieter
tables (muted uppercase-free headers, hairline row borders, no zebra), softer
dialogs/drawers/toasts (larger radii, subtle shadow, hairline border).

**Deliverables:**
- Button gains a `pill` shape option (default shape unchanged so 200+ call sites don't churn);
  primary variant uses the new accent.
- Table restyled per reference (header row muted, generous row height at comfortable density —
  `density.ts` compact mode preserved).
- Dialog/Drawer/Toast/Tooltip/EmptyState restyled tokens-only; no API changes.

**Acceptance criteria:**
1. All existing component Vitest suites pass unmodified except explicit style assertions.
2. Visual claims cited via axe + a Playwright screenshot of the components in both themes
   attached to the report (proxy, not proof — the axe run is the gate).
3. Density toggle still changes table metrics (existing test keeps passing).

## T-3406 · Phase close-out: full regression pass
**model:** sonnet-5 · **size:** M · **depends:** T-3402, T-3403, T-3404 · **context:** `web/e2e/` full suite, `web/src/build/bundleSplit.test.ts`, `index.css` print rules, `docs/user-guide.md` + `web/src/tour/tourScript.ts` + `web/src/onboarding/` (chrome references)

**Objective:** Prove the redesign changed pixels and nothing else.

**Deliverables:**
- Full `web/e2e` Playwright run on the dev host; every failure triaged as regression (fix) or
  stale selector (update with a note).
- axe sweep: every routed page × light/dark × demo-amber accent.
- Print stylesheet re-verified (nav + header + drawer still hidden under the new markup).
- Guided tour (T-2802), onboarding walkthrough, and help-panel content re-checked against the
  new chrome — any step that names or highlights a moved control is updated.
- `docs/user-guide.md` screenshots/descriptions of the shell updated.
- Bundle budget: final numbers recorded in the report; budget test passing.

**Acceptance criteria:**
1. `make check` green; full e2e suite green on the dev host.
2. axe sweep report shows zero violations, attached to the phase report.
3. Tour and onboarding e2e scenarios pass against the new shell.
