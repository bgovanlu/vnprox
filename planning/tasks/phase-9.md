# Phase 9 — Cockpit UI & UX

Goal: one cockpit, any scale, any operator. v1's frontend grew page-by-page across six phases
(Phases 0–6); Phase 9 is a deliberate, frontend-only consolidation pass that makes the topology
map the true center of the product and brings the whole surface up to one design bar. Per
`docs/roadmap-next.md`'s framing, this phase renders data that already exists — no new backend
capabilities, with the single documented exception of T-907's additive app-owned layout/store
surface (saved views + annotations). Everything else is a pure UI/UX swap or addition over
frozen contracts: `internal/topology`'s projection output shape (`web/src/topology/projection.ts`)
does not change, and `GET /topology`'s `{nodes[], edges[]}` wire contract is untouched.

T-901 is the load-bearing dependency for this phase: it replaces the Graph view's rendering
engine and, in doing so, must open the accessibility seam that T-902/T-905/T-906/T-907 build on.
T-903 and T-904 are independent of T-901 and can run in parallel with it (T-903 targets the
current DOM-rendered entities; it does not require the canvas engine to exist). T-905 and T-909
close the phase: T-905 is the WCAG pass gated on T-901's seam, and T-909 is the responsive layout
gated on both the dashboard (T-904) and the design system (T-905).

Per-card testing rule: every card names Vitest + Testing Library coverage for its logic-bearing
components and at least one `web/e2e` Playwright scenario, following this repo's existing
spec-file naming convention (`web/e2e/<area>.spec.ts`, e.g. `topology.spec.ts`, `perf.spec.ts`,
`scale.spec.ts`, `mgmt-redundancy.spec.ts`). Visual/perf claims cite a measurable proxy (a
snapshot, an axe run, a frame-time percentile from the existing rAF sampler) — never "looks
right." Backend rule: every card except T-907 makes zero changes under `internal/` or `docs/api.md`
routes; state that explicitly. T-907 is scoped to additive app-owned store/API surface only
(`layouts`-table-adjacent data) — never a shadow copy of PVE config.

---

## T-901 · Topology renderer v2: canvas/WebGL core
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-107 · **context:** `docs/features/topology.md` §2 §3 §4, `docs/development.md` (frontend stack — `@xyflow/react`/`elkjs` are the current Graph-view engine), `web/src/topology/projection.ts`, `web/src/topology/toFlowElements.ts`, `web/src/topology/TopologyCanvas.tsx`, `web/src/topology/EntityNode.tsx`/`EntityEdge.tsx`, `web/e2e/perf.spec.ts`, `web/e2e/scale.spec.ts`

**Objective:** Replace the Graph view's DOM/SVG entity rendering (today's React Flow node-link
canvas) with a canvas/WebGL engine, behind a feature flag, without touching the topology
projection contract — this is a pure rendering-layer swap. Switch view (`SwitchView.tsx`/
`SwitchFaceplate.tsx`) is untouched; it is already an appropriate DOM rendering for a faceplate
and is out of scope. The v1 Graph renderer remains selectable for one release as the fallback.

**Deliverables:**
- New `web/src/topology/TopologyCanvasV2.tsx` (or equivalent), consuming the exact same
  `toFlowElements.ts` output the v1 renderer does — no new projection/shape.
- Feature flag (Settings > Experimental or a `localStorage`-backed toggle in `store.ts`) selecting
  v1 vs v2 for the Graph view; the Switch/Graph segmented control is unaffected either way.
- Parity: layer toggles, VLAN filter (dim/grey), selection, hover-chain highlight, badges
  (including the amber `mgmt`/`corosync`/`mgmt-path` trio and the finding-dash overlay from
  T-602/T-702), drag-and-drop editing (NIC→bond, guest NIC→bridge → draft op in the changeset
  drawer), and saved layouts (`GET/PUT /layouts/{name}`, `internal/api/layouts.go`) all work
  identically under v2.
- Accessibility bridge: a parallel DOM roving-focus layer kept in sync with the canvas (one
  accessible proxy element per visible entity, `aria-label` per entity, updated on pan/zoom/
  selection) — this is the seam T-905 (WCAG pass) and T-903 (palette-driven map navigation, once
  it adopts v2) build on. Canvas v2 must not be a pure `<canvas>` pixel blob with zero DOM
  accessibility surface.
- Hit-testing: click/hover resolve to the correct entity at any zoom level, including overlapping
  badge chips.

**Acceptance criteria:**
1. Feature-flag toggle switches the Graph view between v1 and v2 renderers at runtime; both read
   the same `GET /topology` response with no re-fetch.
2. Golden parity: `three-node-vlan` and `evpn-lab` fixtures render the same selected-entity set,
   badge set, and VLAN-filter dim set under v2 as the existing v1 snapshot tests assert
   (`threeNodeVlan.render.test.tsx`, extended per-renderer or duplicated for v2).
3. Drag-and-drop editing against `single-node` produces an identical draft changeset op under v2
   as v1 (NIC-onto-bond and guest-NIC-onto-bridge cases).
4. A layout saved under v1 (`PUT /layouts/{name}`) renders node positions identically under v2,
   and vice versa — round-trip test.
5. Accessibility bridge: a Vitest test asserts every visible canvas entity has a corresponding
   accessible DOM node (role + `aria-label`) that stays in sync across pan/zoom/selection changes.
6. Perf budget: `web/e2e/scale.spec.ts`'s existing rAF frame-delta sampler, run against v2 on the
   `scale-lab` fixture (`testdata/genscale`, 8 nodes × 6 NICs, 300 guests, 40 VNets) — p95 frame
   time ≤ 20ms attached as the `frame-stats` artifact and transcribed into `docs/performance.md`
   with the same headless/software-rasterized caveat the existing perf specs document (a
   measurable proxy, not a hardware guarantee).
7. `internal/topology` and `web/src/topology/projection.ts`'s exported types are unchanged
   (diff-empty, stated in the report); zero backend changes.
8. `make check` green.

---

## T-902 · Level-of-detail scale semantics
**model:** sonnet-5 · **size:** L · **depends:** T-901 · **context:** `docs/features/topology.md` §4 (scale targets, the T-607-flagged physical-layer-collapse gap), `web/src/topology/expand.ts`, `web/src/topology/scaleLab.render.test.tsx`, `web/e2e/scale.spec.ts`

**Objective:** Zoom-driven level-of-detail on the v2 renderer: full faceplates when zoomed in,
per-node summary capsules when zoomed out (closing the physical-layer collapse gap flagged at
T-607 in `docs/features/topology.md` §4), edge bundling for guest-dense bridges, and a minimap
with a viewport rectangle.

**Deliverables:**
- `web/src/topology/lod.ts`: LOD threshold config (named zoom bands, e.g. full / simplified /
  capsule) and the entity-set transform per band, documented in `docs/features/topology.md` §4
  alongside the existing scale-target text (this card resolves the T-607-flagged gap — update
  that section rather than leaving it dangling).
- Per-node summary capsule component for the lowest zoom band: collapses a node's physical layer
  (NICs + bonds) into one capsule showing counts/aggregate status, expanding back to full
  faceplate detail above the threshold.
- Edge bundling: a bridge whose guest-NIC edge count exceeds the existing collapse threshold
  (`internal/topology/collapse.go`'s `DefaultCollapseThreshold`, mirrored client-side) renders one
  bundled edge with a count badge instead of N individual edges; unbundles on zoom-in or click.
- Minimap: scaled overview with a viewport rectangle tracking pan/zoom; dragging the rectangle
  pans the main canvas.

**Acceptance criteria:**
1. LOD thresholds and per-band content are documented in `docs/features/topology.md` §4, and that
   section's T-607-flagged-gap paragraph is updated to reflect the resolution (or explicitly
   re-scoped if genscale-scale still exceeds it — state which in the report).
2. Deterministic snapshot tests (`web/src/topology/lod.test.ts` / `lod.render.test.tsx`) assert
   the rendered entity set at each defined zoom band against the `scale-lab` fixture (the same
   fixture `scaleLab.render.test.tsx` already exercises).
3. Edge bundling: a `scale-lab` bridge with guest count over threshold renders one bundled edge
   with a correct count badge; a test asserts unbundling on click restores per-guest edges.
4. Minimap: renders an overview with a viewport rectangle that tracks pan/zoom; a Playwright
   scenario (extend `web/e2e/scale.spec.ts` or add `web/e2e/lod.spec.ts`) drags the minimap
   rectangle and asserts the main canvas viewport moved.
5. Perf: the LOD transform does not regress T-901's frame budget at any zoom band — the same rAF
   sampler run across bands, attached to the perf artifact, still meets the p95 ≤ 20ms budget.
6. Zero backend changes: `internal/topology` untouched.
7. `make check` green.

---

## T-903 · Command palette & keyboard-first navigation
**model:** sonnet-5 · **size:** M · **depends:** T-107 · **context:** `docs/features/topology.md` §2 (spotlight search), `web/src/keyboard/shortcuts.ts`, `web/src/keyboard/useKeyboardShortcuts.ts`, `web/src/keyboard/ShortcutHelpDialog.tsx`, `web/src/topology/SpotlightSearch.tsx`

**Objective:** A ⌘K/Ctrl+K command palette unifying the existing `/` spotlight entity search with
an action registry ("edit vmbr0", "new VLAN zone", "open drafts", "simulate path from <entity>"),
so every page action gets a palette verb, plus roving keyboard focus across map entities and an
extended shortcut help overlay. Depends only on T-107 (not T-901) — it targets whichever Graph/
Switch DOM entities are currently mounted, so it works against the v1 renderer today and adopts
T-901/T-905's a11y bridge automatically once that lands, with no rework required here.

**Deliverables:**
- `web/src/keyboard/CommandPalette.tsx`: merges `SpotlightSearch`'s fuzzy entity search
  (`GET /inventory/search`) with a static+dynamic action list in one `⌘K` dialog.
- `web/src/keyboard/actions.ts`: a `usePaletteActions` registry hook; pages register verbs on
  mount and unregister on unmount. At minimum Topology, SDN, Changesets/drafts, and Simulator
  pages register at least one verb each (edit `<bridge>`, new VLAN zone, open drafts, simulate
  path from `<entity>`).
- `shortcuts.ts` gains the `⌘K`/`Ctrl+K` binding, dispatched through the existing
  `useKeyboardShortcuts` mechanism; `ShortcutHelpDialog.tsx` lists it and every currently
  registered palette verb that has a shortcut.
- Roving focus: arrow-key navigation across topology entities (current DOM-rendered Graph and
  Switch views) moves both visual and programmatic focus in an order consistent with visual
  adjacency.

**Acceptance criteria:**
1. `⌘K`/`Ctrl+K` opens the palette; typing a bridge name surfaces both the spotlight entity result
   and its registered action ("Edit vmbr0") against the `three-node-vlan` fixture.
2. `usePaletteActions` registry: a Vitest test asserts actions from two simultaneously-mounted
   pages merge without collision, and unmounting one page removes only its actions.
3. `ShortcutHelpDialog` renders the palette binding plus at least the four named verbs' shortcuts
   where bound.
4. Roving focus: a Vitest test on the topology entity list asserts arrow-key navigation advances
   focus in visual-adjacency order and Enter activates the same action a click would (selection →
   inspector open).
5. `web/e2e/command-palette.spec.ts` (new, following `topology.spec.ts`'s naming convention):
   logs in against `three-node-vlan`, opens the palette via keyboard only, selects an action, and
   asserts the expected navigation/drawer opened.
6. Zero backend changes.
7. `make check` green.

---

## T-904 · Home dashboard
**model:** sonnet-5 · **size:** M · **depends:** T-601, T-602, T-305, T-702 · **context:** `docs/features/monitoring.md` §1 §3 §5, `docs/features/topology.md` §3 (management-path status), `docs/api.md` (`/findings`, `/changesets`, `/protected-interfaces/status`, `/metrics/live`, `/audit`), `web/src/findings/queries.ts`, `web/src/pages/ToolsPage.tsx`

**Objective:** A network-at-a-glance landing page that becomes the default route: open findings by
severity, drift status, pending/awaiting-confirm changesets, mgmt-path redundancy per node
(Phase-7's `GET /protected-interfaces/status`), top talkers, and recent audit entries — every tile
deep-linking into its owning page. Read-only; built entirely on routes that already exist.

**Deliverables:**
- `web/src/dashboard/DashboardPage.tsx` plus one component per tile under `web/src/dashboard/`:
  findings-by-severity (`GET /findings`, grouped), drift status (`/findings?source=drift`),
  pending/awaiting-confirm changesets (`GET /changesets`, client-filtered to non-terminal
  statuses), mgmt-path redundancy per node (`GET /protected-interfaces/status`, counting
  non-`redundant` nodes), top talkers (per `docs/features/monitoring.md` §3: rank guest-NIC refs
  on the busiest bridge(s) by `GET /metrics/live` rates — a client-side computation over an
  existing route, no new backend aggregation), and recent audit entries (`GET /audit?limit=5`).
- `App.tsx`'s index route changes from `<Navigate to="/topology" replace />` to the dashboard;
  `NavRail.tsx` gains a "Home" entry.
- Each tile is a link/button to its source page (`/tools` for findings/drift, `/changesets`,
  `/management`, `/topology`, `/audit`).

**Acceptance criteria:**
1. `/` (index route) renders `DashboardPage`; `NavRail`'s Home entry highlights when active.
2. Each tile renders correct data against `three-node-vlan` (`DashboardPage.test.tsx`, one Vitest
   case per tile, mocking the underlying TanStack Query hooks — following `TopBar.test.tsx`'s
   pattern).
3. Deep-links: a Testing Library test asserts clicking each tile navigates to its owning route.
4. Empty states: against `single-node` with no open findings/pending changesets, tiles render an
   explicit "all clear"-style empty state, never blank or an unhandled error.
5. Read-only: the page issues no mutating request; it mounts under the same `netRead` capability
   gate as the topology page (stated and verified in the report — no POST/PUT/DELETE call in the
   dashboard's query code).
6. `web/e2e/dashboard.spec.ts` (new): logs in against `three-node-vlan`, asserts the landing page
   is the dashboard, and exercises at least one tile's deep-link end to end.
7. Zero backend changes: no new API routes; `docs/api.md` untouched by this card.
8. `make check` green.

---

## T-905 · Design system & accessibility pass
**model:** sonnet-5 · **size:** L · **depends:** T-901 · **context:** `docs/features/topology.md` §3 (badge vocabulary), `web/src/components/`, `web/src/layout/ThemeToggle.tsx` (existing dark-theme store), `web/e2e/topology.spec.ts-snapshots`

**Objective:** Consolidate the component set into one documented library (density modes,
consistent form/table/drawer patterns), complete dark-theme coverage (map included, via T-901's
new renderer), and reach WCAG 2.1 AA: full keyboard navigation including map roving focus wired
through T-901's accessibility bridge, screen-reader labels for every map entity and badge, and
reduced-motion support. Automated axe checks wired into the Playwright e2e suite.

**Deliverables:**
- Component contract pass over `web/src/components/` (`Button`, `Dialog`, `Drawer`, `EmptyState`,
  `ErrorBoundary`, `Table`, `Toast`, `Tooltip`): a density-mode prop/variant (compact/comfortable)
  and a consistent form/table/drawer interaction pattern, each documented in the component's own
  doc comment per this repo's existing convention (no new doc-generation tooling — not in
  `docs/development.md`'s locked stack).
- Dark theme completion: the existing `useThemeStore`/`ThemeToggle.tsx` theme now also drives the
  v2 canvas renderer's draw colors in both Graph and Switch views.
- Keyboard nav: every interactive map entity reachable via Tab/arrow roving focus (consuming
  T-901's a11y bridge, not reimplementing it) with a visible focus ring; Enter/Space activates the
  entity the way a click would.
- Screen-reader labels: every map entity and every badge chip (including the `mgmt`/`corosync`/
  `mgmt-path` trio) carries an `aria-label` naming kind, identity, status, and badge list.
- Reduced motion: `prefers-reduced-motion: reduce` disables pan/zoom easing and status-pulse
  animations (unconfirmed-changeset pulse, drift dash), falling back to static equivalents.
- `@axe-core/playwright` added as a dev dependency (flagged in the report per CLAUDE.md's
  "no new major dependencies without a note" — it is a test-only dependency, not shipped) and
  wired into a new `web/e2e/a11y.spec.ts`.

**Deliverables note:** zero backend changes.

**Acceptance criteria:**
1. Density mode: a Vitest test on `Table.tsx` (or the shared density context) asserts
   compact/comfortable render distinctly.
2. Dark theme: a Playwright screenshot comparison (following the `topology.spec.ts-snapshots`
   pattern) of the v2 map canvas in dark vs. light mode against `three-node-vlan`.
3. Keyboard nav: `web/e2e/a11y.spec.ts` includes a keyboard-only traversal scenario reaching and
   activating a map entity with no mouse input.
4. Screen-reader labels: a snapshot test asserts `aria-label` content for every entity/badge type
   against `three-node-vlan` and `evpn-lab` (covering the VTEP mesh/peer badges).
5. Reduced motion: a Vitest test mocking `prefers-reduced-motion: reduce` asserts pan/zoom easing
   and pulse animations are disabled.
6. Automated axe: `web/e2e/a11y.spec.ts` runs against Dashboard, Topology (both Graph and Switch
   views), SDN, Firewall, IPAM, the changeset drawer, and Settings, asserting zero serious/critical
   violations; any suppressed rule carries a reason comment in the spec.
7. `make check` green (note any new lint rule from the added dev dependency in the report).

---

## T-906 · Map export (SVG/PNG) + print stylesheet
**model:** sonnet-5 · **size:** S · **depends:** T-901 · **context:** `docs/features/topology.md` §4 (T-607-flagged export gap), `docs/api.md` (`GET /export/doc` — the existing config-doc export, for contrast), `web/src/pages/ToolsPage.tsx`

**Objective:** A dedicated export-the-map control (SVG and PNG) honoring the current layer
toggles, VLAN filter, and zoom/viewport — distinct from `ToolsPage.tsx`'s existing config-document
export (`GET /export/doc`), which exports prose, not the rendered map. Plus a print stylesheet.
This is the T-607-flagged gap ("physical layer collapses... export... " scope: dedicated map
export was never built in v1).

**Deliverables:**
- An "Export map" control on the Topology page toolbar (both Graph and Switch views), rendering
  the current view's visible entities (post layer-toggle/VLAN-filter/collapse state) to SVG,
  with a PNG rasterization option.
- A print stylesheet (`@media print`) for the topology page: legible layout, no interactive chrome
  (toolbars, drawers), current filter/legend state printed as a caption.
- Client-side only: no new backend route (contrast with `GET /export/doc`, which stays as-is).

**Acceptance criteria:**
1. Export control present on both Graph and Switch views; clicking it with the VLAN filter and
   one layer toggled off produces an SVG containing only the filtered/toggled entity set (asserted
   by parsing the exported SVG's entity count against `three-node-vlan`).
2. PNG export produces a non-empty image blob of the same filtered scene (byte-size/dimension
   sanity check, not pixel-exact).
3. Print stylesheet: a Playwright scenario emulates `print` media and asserts toolbar/drawer
   chrome is hidden while map entities remain visible.
4. `web/e2e/map-export.spec.ts` (new): logs in against `three-node-vlan`, applies a layer toggle
   and VLAN filter, triggers SVG export, and asserts the download contains the filtered set.
5. Vitest coverage on the export-serialization logic (`web/src/topology/export.ts` or equivalent)
   independent of the browser download mechanism.
6. Zero backend changes.
7. `make check` green.

---

## T-907 · Saved views & annotations
**model:** sonnet-5 · **size:** M · **depends:** T-901 · **context:** `docs/data-model.md` §2 (`layouts` table), `docs/api.md` (document the new routes here per docs/development.md's definition-of-done #4), `internal/api/layouts.go`, `internal/store/layouts.go`

**Objective:** Named presets of layer+filter+zoom+selection state, shareable as URLs, plus
sticky-note annotations pinned to map entities — persisted in the app-owned layout store. This is
the phase's **only** permitted backend surface, and it stays strictly app-owned data (no shadow
copy of PVE config): saved views/annotations are vnprox-local UI state, never authoritative
network configuration.

**Deliverables:**
- Saved views: a named preset capturing `{layers, vlanFilter, zoom, viewport, selection, view:
  "graph"|"switch"}`, built on the existing per-user `layouts` table/`GET|PUT /layouts/{name}`
  route (`internal/api/layouts.go`, `internal/store/layouts.go`) — reuse rather than fork this
  mechanism where the shape fits.
- Shareable URLs: a saved view's state round-trips through the URL's query string (so a link works
  even for a viewer who hasn't saved the view themselves — the URL, not just the stored name,
  carries the state), documented in `docs/api.md` alongside a new/extended route if the existing
  per-user `layouts` shape can't carry cross-user sharing (flag this design choice explicitly in
  the report — do not silently fork the `layouts` table without documenting why).
- Annotations: sticky notes pinned to a map entity ref, with free-text content, persisted
  additively (new store table or an extension of `layouts` — document the choice and update
  `docs/data-model.md` §2 in the same change per CLAUDE.md's data-structure rule).
- New/extended API routes documented in `docs/api.md` exactly per this task's additions (route
  names, JSON shapes, capability gate — matching the existing `layouts` routes' `netRead` gate
  precedent unless a reason for a stricter gate is stated).

**Acceptance criteria:**
1. Saving a view against `three-node-vlan` (specific layers off, a VLAN filter set, zoomed/panned)
   and reloading it restores the exact same rendered state — a Vitest test round-trips the
   captured state through save/load.
2. A saved view's shareable URL, opened in a fresh session with no prior `layouts` row for that
   viewer, renders the same filtered/zoomed state (state lives in the URL, not only server-side).
3. Annotations: pinning a sticky note to an entity ref persists it; reloading the topology page
   re-renders the note at the same entity; deleting it removes it — Vitest + one
   `web/e2e/saved-views.spec.ts` (new) end-to-end case against `three-node-vlan`.
4. New/changed routes and table shapes are documented in `docs/api.md` and `docs/data-model.md` in
   this same change, per CLAUDE.md's contract rule — no undocumented schema drift.
5. Store data is unambiguously app-owned: no field in the new schema duplicates PVE-authoritative
   configuration (stated and spot-checked in the report).
6. `make check` green.

---

## T-908 · Inspector v2
**model:** sonnet-5 · **size:** M · **depends:** T-107, T-601 · **context:** `docs/features/monitoring.md` §1 §2, `web/src/topology/InspectorPanel.tsx`, `web/src/topology/MetricsTab.tsx`, `web/src/topology/metricsQueries.ts`

**Objective:** Pinnable multiple inspectors, side-by-side entity compare (two bonds, two nodes'
bridges), and inline sparkline history in metrics tabs — building on the existing single-entity
`InspectorPanel.tsx`/`MetricsTab.tsx` (which already renders one entity's recharts sparkline from
`GET /metrics/history`) rather than replacing it.

**Deliverables:**
- Pin control on `InspectorPanel`: pinning keeps an inspector open as a persistent panel while
  selecting a new entity opens an additional (not replacing) inspector, up to a reasonable cap.
- Side-by-side compare: selecting two same-kind entities (e.g. two Bonds, or two nodes' `vmbr0`)
  while pinned renders a compare layout — fields and metrics tabs aligned column-wise.
- Every pinned/compared inspector's Metrics tab keeps its own live `MetricsTab` sparkline
  (`GET /metrics/history`, `recharts`), so compare mode visually diffs two entities' sparklines
  side by side.

**Acceptance criteria:**
1. Pinning an inspector and selecting a second entity against `three-node-vlan` shows two open
   inspectors simultaneously; unpinning/closing one leaves the other intact (Vitest test on the
   inspector container's pin state).
2. Compare mode: selecting two Bonds on `three-node-vlan` renders a side-by-side layout with
   matching field rows aligned; a mismatched-kind pair (Bond vs Bridge) either disables compare or
   states why, not a broken layout (test both).
3. Each pane's Metrics tab renders its own sparkline from `GET /metrics/history` independently
   (mocked in `InspectorPanel.metrics.test.tsx`-style tests, extended for the two-pane case).
4. `web/e2e/inspector-compare.spec.ts` (new): opens two inspectors against `three-node-vlan`,
   pins one, selects a second entity, and asserts both remain visible with distinct metrics data.
5. Zero backend changes: `GET /metrics/live`/`GET /metrics/history` called unchanged, just from
   multiple mounted `MetricsTab` instances.
6. `make check` green.

---

## T-909 · Responsive triage layout
**model:** sonnet-5 · **size:** M · **depends:** T-904, T-905 · **context:** `docs/api.md` (changesets apply/confirm/rollback), `web/src/dashboard/DashboardPage.tsx` (T-904), `web/e2e/changesets.spec.ts`

**Objective:** A read-only tablet/phone layout for on-call triage: dashboard, findings, and
changeset confirm/rollback actions only — commit-confirm from a phone is the target scenario.
Every other mutation (staging new ops, running wizards, editing entities) stays desktop-only, with
an explicit UI affordance stating so rather than silently hiding or half-rendering controls.

**Deliverables:**
- Responsive breakpoints applied to `AppShell`/`NavRail`/`DashboardPage` collapsing to a
  narrow-viewport layout below a defined width; the reachable page set on narrow viewports is
  restricted to Dashboard, Findings (`/tools`), and the changeset detail/confirm/rollback view.
- Every other route, when navigated to on a narrow viewport (including via a direct link),
  renders an explicit "desktop only" affordance instead of a broken/cramped attempt at the full
  UI — stating what the user can do instead (e.g. "open on a desktop to edit; confirm/rollback
  from here").
- Changeset confirm/rollback controls (the countdown, typed-acknowledgement block for
  `touchesMgmtPath` changesets, confirm/rollback buttons) are fully usable at narrow viewport
  width — this is the one write path this card must not degrade.

**Acceptance criteria:**
1. At a phone-width viewport, Dashboard and Findings render a usable (not just shrunk) layout —
   Playwright viewport-emulation scenario against `three-node-vlan`.
2. A changeset with `touchesMgmtPath: true` in `awaiting_confirm` status, opened at phone width,
   renders the full ack block and confirm/rollback controls, and a confirm action succeeds end to
   end — extends `web/e2e/mgmt-redundancy.spec.ts`'s or `changesets.spec.ts`'s existing scenario
   with a narrow-viewport variant rather than duplicating its setup.
3. Navigating to a desktop-only route (e.g. the SDN wizard) at phone width shows the explicit
   "desktop only" affordance with actionable copy, not a broken layout — Vitest test on the
   route-guard component plus one Playwright assertion.
4. No new write capability is exposed at narrow viewport width beyond confirm/rollback (stated and
   spot-checked in the report — grep for mutating calls reachable only from the narrow layout).
5. `web/e2e/responsive-triage.spec.ts` (new): phone-width viewport, logs in against
   `three-node-vlan`, reaches Dashboard → Findings → a pending changeset → confirms it.
6. Zero backend changes.
7. `make check` green.
