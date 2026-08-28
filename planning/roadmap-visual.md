# Roadmap: the visual product — 100 enhancements, Phases 42–51

**Written 2026-08-28.** The brief: vnprox is a *visual* networking tool for Proxmox, and today
the visuals are its weakest part. The engine room is deep — a change engine, a topology model,
overlays, flow ingestion, a federation view — but the surface is lack-luster: the accent color is
an alias of Tailwind's stock indigo (`web/src/index.css` says so in its own comment), there is no
typeface, no motion system, no chart theme, the shared component library is ten generic primitives
(`web/src/components/`), and most of the 28 routed pages (`web/src/pages/`) render tables for
domains that are inherently spatial — IPAM, firewall policy, DHCP leases, conntrack, cabling.

This roadmap is 100 enhancements whose common thread is **making the product visibly excellent**:
a real design language, a canvas worth screenshotting, and a picture-first counterpart for every
table-first page. It continues the phase numbering after
[`roadmap-open-source.md`](roadmap-open-source.md)'s Phases 38–41.

## Honest baseline (what exists on 2026-08-28)

Grounded in the tree, not in aspiration:

| Area | What's there | What's missing |
|---|---|---|
| Canvas | `@xyflow/react` + `elkjs`, LOD (`topology/lod.ts`), minimap, 8+ overlays (MTU, STP, SNMP, recency, Ceph, diff, preview, annotations), blast-radius focus, saved views, replay scaffolding | Node/edge design is functional boxes-and-lines; overlays compete for the same visual channels; no motion; no label collision handling; dark canvas is the light palette inverted |
| Design system | Tailwind 4 CSS-first theme, `accent-*` alias layer, demo-amber variant with contrast tests, density toggle | No identity (stock indigo), no typeface, no elevation system, no motion tokens, no semantic status scale shared across pages |
| Charts | `recharts` in a handful of tiles | Unthemed defaults; no sparkline primitive; no heatmap/treemap/matrix/timeline idioms at all |
| Physical layer | `SwitchFaceplate.tsx`, `CablingPlanView.tsx`, port media detection, LLDP data | Faceplate is the only physical rendering; cabling is a list; no node faceplate, no rack view |
| Tables | `components/Table.tsx` everywhere | IPAM, firewall, conntrack, ports, DHCP, WireGuard, audit, certs are table-first with no visual counterpart |
| Gates | axe a11y suite per routed page, contrast unit tests, Playwright e2e harness | **No screenshot/visual regression gate** — visual quality can silently rot, which is partly how it got here |

## Design principles for every card below

1. **The map is the product's poster.** Any trade between canvas quality and anything else on this
   roadmap resolves toward the canvas.
2. **Every visual encodes something true.** No decoration that isn't data: color = status, width =
   rate, position = topology, motion = change. If a flourish encodes nothing, it doesn't ship.
3. **Tables stay; pictures are added.** The visual counterpart of a table-first page complements
   it (toggle or side-by-side), never replaces the operator's ability to sort/filter/copy.
4. **Both themes, deliberately.** Dark mode is designed, not inverted. Every card's acceptance
   includes both themes plus the demo-amber accent.
5. **Reduced motion is first-class.** Every animation has a reduced-motion rendering that conveys
   the same information statically.
6. **Data comes from what the daemon already serves** unless the card says otherwise — these are
   frontend/rendering cards; new backend surface is called out explicitly and is rare.
7. **Perf budgets apply to beauty.** Canvas work runs under the T-4107 envelope gates
   (`perf/budgets.json`); a visual that blows `topology.project_at_envelope_ms` doesn't ship at
   envelope scale, it ships with an LOD rule.

---

## Phase 42 — Design language (T-4201–T-4210)

The foundation everything else builds on. Runs first; nothing in 43–51 starts before T-4201/02/04
land, because they define the tokens the rest consume.

- **T-4201 · A real visual identity (L)** — Commission the palette: a named brand accent (literal
  hex values in `@theme`, exactly where `index.css`'s comment says a brand hex belongs), a
  hue-biased neutral ramp replacing raw slate, a wordmark/logo for TopBar, favicon, and login.
  Re-run the demo-amber contrast derivation against the new accent (`index.css.test.ts` already
  enforces the alias sets match).
- **T-4202 · Typography system (M)** — A characterful UI face and a proper monospace for
  addresses, MACs, and counters, self-hosted (CSP forbids CDNs); type-scale tokens; `tabular-nums`
  wherever digits align (metrics, counters, IPs — today they wobble).
- **T-4203 · Elevation & surface system (M)** — Tokenized surface levels (page, card, popover,
  overlay) with dark-mode elevation done by lightening surfaces, not by shadows that vanish on
  dark grounds. Sweep Dialog/Drawer/Inspector/tiles onto the tokens.
- **T-4204 · Semantic status scale (M)** — One tokenized health scale — ok / degraded / critical /
  unknown / stale — consumed by `StatusDot`, finding badges, tiles, and canvas glyphs alike.
  Today each page picks its own emerald/amber/rose; drift between pages reads as bugs.
- **T-4205 · Network pictogram set (L)** — Custom glyphs for bridge, bond, VLAN, VXLAN, fabric,
  zone, WireGuard peer, physical NIC, replacing generic lucide stand-ins. Designed to work at
  three sizes: inline icon, canvas node glyph, empty-state illustration seed.
- **T-4206 · Motion foundation (M)** — Duration/easing tokens; enter/exit primitives for
  Drawer/Dialog/Toast/Inspector; a single `prefers-reduced-motion` gate all animation flows
  through instead of per-component checks.
- **T-4207 · Density system completion (S)** — `components/density.ts` exists; extend
  comfortable/compact through every Table consumer and the inspector, persisted per user.
- **T-4208 · Component library growth (M)** — Segmented control, Badge/Chip, Stat, KeyValue list,
  Skeleton, Progress, Banner variants — promoted from the hand-rolled per-page versions that
  currently diverge (firewall's scope pills vs. SDN's status chips vs. topology's badges).
- **T-4209 · Empty states with illustrations (S)** — `EmptyState.tsx` is text-only; give each
  domain an illustration built from the T-4205 pictograms plus exactly one next action.
- **T-4210 · Visual regression gate (M)** — Playwright screenshot suite per routed page × both
  themes × demo accent, wired into `scripts/ci-local.sh` beside the axe suite. This is the card
  that stops every other card on this roadmap from regressing silently. **Do this first or
  second.**

## Phase 43 — Canvas rendering (T-4301–T-4310)

The map itself. Depends on Phase 42's tokens and pictograms.

- **T-4301 · Node redesign (L)** — Entity cards with pictogram, status stripe, and port chips;
  LOD-aware degradation full card → glyph → dot (extend `lod.ts`'s existing thresholds).
- **T-4302 · Edge rendering overhaul (L)** — Rounded orthogonal routing; parallel-link bundling so
  bond members read as one braided edge; width classes by link speed; media styling (copper
  solid, fiber accent-tinted, virtual dashed) from `portMedia.ts`.
- **T-4303 · Group capsules (M)** — Soft rounded hulls for node membership, VLAN groups, and SDN
  zones with tinted washes and labels-on-border, replacing the current rectangular groupings.
- **T-4304 · Canvas ground & depth (S)** — Subtle dot grid, focus vignette, and z-layering rules
  so overlays read as planes above the map rather than clutter within it.
- **T-4305 · Animated flow particles (M)** — Directional particles along edges in traffic mode,
  density proportional to rate (`flowEdges.ts` already computes per-edge rates); static
  arrows+width under reduced motion. Must hold 60fps at the 8/300 fixture and degrade by LOD at
  envelope scale.
- **T-4306 · Label engine (L)** — Collision-avoiding placement, halo text for legibility over
  edges, fade-by-zoom. Labels never overlap at any zoom level; `messyBrownfield.render.test.tsx`'s
  fixture is the acceptance scene.
- **T-4307 · Overlay visual grammar (L)** — Assign each overlay a distinct channel — hue, hatch,
  halo, badge, edge ornament — with a composable legend, so MTU + STP + recency can be on
  simultaneously and still read. Today they compete for fill color.
- **T-4308 · Selection & focus hierarchy (M)** — Distinct hover / selected / multi-selected /
  blast-radius treatments; `blastRadiusFocus.ts`'s dimming becomes a designed spotlight rather
  than an opacity drop.
- **T-4309 · Port-level endpoints (M)** — Edges terminate at rendered port stubs on the node
  border, VLAN tags as tick marks on the stub — visually reconciling the canvas with
  `SwitchFaceplate`'s per-port world.
- **T-4310 · Dark-canvas art direction (M)** — Design the dark map deliberately: deep ground,
  luminous edges, glow-based status. The screenshot most people will ever see of this product is
  the dark canvas; it should look like a product, not an inverted diagram.

## Phase 44 — Canvas interaction & choreography (T-4401–T-4410)

- **T-4401 · Zoom choreography (M)** — Semantic-zoom crossfades between LOD levels (no popping),
  eased zoom-to-fit, double-click-to-focus an entity.
- **T-4402 · Layout pinning with animated reflow (M)** — Keep elkjs auto-layout, add user pinning;
  every relayout animates positions instead of jumping (saved per view in `savedViews`).
- **T-4403 · Hover peek cards (M)** — Rich hover previews — mini sparkline, status, key facts —
  before committing to the inspector.
- **T-4404 · App-wide command palette (M)** — Promote `SpotlightSearch` from canvas-only to a ⌘K
  palette over entities, pages, and actions, with type glyphs and fuzzy match.
- **T-4405 · Context menu redesign (S)** — `ContextMenu.tsx` grouped and icon-led, with
  destructive actions carrying an inline scope preview ("affects 3 guests").
- **T-4406 · Drag-to-connect staging (L)** — Drag port-to-port to stage a change through the
  existing changeset op builders, with a ghost-edge preview during the drag. Staging only —
  the change engine remains the sole mutation path.
- **T-4407 · Marquee multi-select (M)** — Lasso/marquee selection with a floating bulk-action bar.
- **T-4408 · Inspector stack choreography (M)** — `InspectorStack` gets animated push/pop,
  breadcrumbs, and a resizable/dockable panel.
- **T-4409 · First-load build-up (S)** — Nodes settle and edges draw in on first canvas load — the
  loading state *is* the choreography; instant render under reduced motion.
- **T-4410 · Touch & trackpad refinement (M)** — Pinch-zoom, inertial pan, two-finger gestures;
  the canvas must be operable from a tablet in a rack aisle.

## Phase 45 — Telemetry made visible (T-4501–T-4510)

- **T-4501 · Sparkline primitive (M)** — One cheap canvas-drawn mini-chart used uniformly in
  nodes, hover cards, table cells, and tiles.
- **T-4502 · Interface utilization heatmap (M)** — Node × interface grid colored by rate and
  error count: the one-screen answer to "where is my traffic."
- **T-4503 · Charts with thresholds (M)** — Rate charts gain p95 bands and threshold annotation
  lines sourced from the alert rules the app already stores.
- **T-4504 · Latency mesh matrix (M)** — Render `latMeshQueries`' node-to-node data as a
  sorted heat matrix, plus on-canvas heat tinting in latency mode.
- **T-4505 · Error visual language (S)** — Drops/errors as red tick marks on charts and edge
  ornaments on canvas — errors become visible before anyone reads a counter.
- **T-4506 · Horizon charts for history (M)** — Compact horizon-chart idiom for long-window,
  many-series views on the history page.
- **T-4507 · Live top-talkers ranking (S)** — `TopTalkersTile` grows into a full view with
  animated rank transitions on each live tick.
- **T-4508 · Legible counters (S)** — `tabular-nums`, delta arrows, and rate-of-change coloring
  across Ports/Conntrack/SNMP tables.
- **T-4509 · Service-class visual identity (S)** — `serviceClassBreakdown` rendered as a
  donut-plus-ribbon with QoS class colors from the semantic scale.
- **T-4510 · One recharts theme (M)** — Grid, axes, tooltip, and palette themed once for both
  modes; today every chart ships recharts defaults.

## Phase 46 — Addressing & segmentation visuals (T-4601–T-4610)

- **T-4601 · Subnet treemap (M)** — IPAM subnets as a proportional treemap colored by
  utilization; click drills into T-4602's grid.
- **T-4602 · Address heat grid (M)** — A /24 as a 16×16 cell grid (allocated / free / reserved /
  conflict); `CellDetailDialog` already anchors per-cell detail.
- **T-4603 · VLAN fabric ribbon (L)** — VLANs as horizontal ribbons crossing nodes and bridges,
  showing where each VLAN is trunked, where it dead-ends, and where nodes disagree.
- **T-4604 · IPv6 prefix tree (M)** — Collapsible radix-tree of delegated prefixes with segment
  coloring, growing out of `IPv6SegmentsPanel`.
- **T-4605 · SDN zone map (L)** — `SdnPage`'s tree gains a map: zones as regions, vnets as lanes,
  gateways as glyphs — and the same regions render as a topology overlay.
- **T-4606 · Overlay/underlay split view (L)** — EVPN/VXLAN as a two-plane view tying VTEPs on the
  overlay plane to their underlay paths beneath.
- **T-4607 · DHCP lease timeline (S)** — Leases on a horizontal time axis with expiry-countdown
  coloring; `DhcpView` stays as the table behind it.
- **T-4608 · Fabric mesh view (M)** — `FabricsView` renders the fabric's routed mesh with
  per-link protocol state from the Phase 31 SDN-fabric model.
- **T-4609 · Overlap visualizer (S)** — Overlapping/conflicting subnets drawn as intersecting
  spans with the collision region highlighted — today a conflict is a table row.
- **T-4610 · Visual next-free picker (S)** — `NextFreePicker` becomes "click a free cell in the
  grid," not a text suggestion box.

## Phase 47 — Security & policy visuals (T-4701–T-4710)

- **T-4701 · Policy matrix (L)** — Source-zone × dest-zone grid of effective allow/deny; a cell
  click lists the rules that decide it. The firewall page's visual counterpart.
- **T-4702 · Rule-hit heat (M)** — Rules tinted by live hit counters; dead rules visibly cold —
  the "why do we have 400 rules" view.
- **T-4703 · Packet-walk animation (M)** — The simulator's verdict path animated step-by-step
  through the resolved chain, the deciding rule highlighted.
- **T-4704 · Ruleset DAG (M)** — `CompiledRulesetPage` renders chains and jumps as a graph, not
  scrolling text (both engines — the page already knows iptables vs nftables per T-3718).
- **T-4705 · Microseg planner on canvas (L)** — `MicrosegPlanner` draws proposed segments as
  lassoed groups on the topology with the flows a policy would cut highlighted; `DryRunReport`
  shows in place.
- **T-4706 · Conntrack live grid (M)** — Connections as state-colored dots with aging animation
  instead of a raw table; filter by verdict/state visually.
- **T-4707 · Staged-change blast radius (M)** — `previewOverlay` grows a quantified "this change
  touches N guests on M nodes" ring treatment before apply.
- **T-4708 · Firewall log ribbon (M)** — `fwlog` as a rate ribbon where deny spikes are visible
  at a glance, with drill-in to the matching rule.
- **T-4709 · Certificate expiry radial (S)** — Certs on a radial time arc by days-to-expiry;
  the table remains for copy/paste.
- **T-4710 · WireGuard mesh view (M)** — Peers as a mesh graph, handshake freshness as pulse
  decay, tunnels drawn onto the main topology.

## Phase 48 — Time, history & change (T-4801–T-4810)

- **T-4801 · Global time scrubber (L)** — A timeline scrubber over the canvas replaying topology
  history (the `topology/replay` scaffolding's completion) with play/pause/speed.
- **T-4802 · Diff as morph (L)** — Animate between two snapshots — entities fade in/out, edges
  re-route — as the primary diff rendering; `TopologyDiffPanel`'s list becomes the detail view.
- **T-4803 · Changeset storyboard (M)** — Each staged op as a card with a mini before/after
  canvas thumbnail; the changeset review becomes a picture story.
- **T-4804 · Apply choreography (M)** — Apply/confirm/rollback as a stepper with live per-node
  status and a rollback countdown ring — the product's core safety loop, finally visible.
- **T-4805 · Drift ghosts (M)** — Expected state drawn as ghost outlines against actual on the
  canvas, per node — drift becomes something you *see*.
- **T-4806 · Audit stream (S)** — `AuditPage` as an actor/entity-grouped event stream with icons
  and a change-frequency sparkline.
- **T-4807 · Incident bands (M)** — Incidents as annotated bands over metric charts; the
  postmortem view links each event to the graph state at that moment.
- **T-4808 · Diff minimap (S)** — `DiffView` gains a minimap of where changes cluster.
- **T-4809 · Snapshot gallery (S)** — Named snapshots as rendered thumbnails (via the map export
  path), not rows.
- **T-4810 · "Since you last looked" (M)** — Per-user delta summary on login: a one-screen visual
  of what changed, feeding off the same diff machinery.

## Phase 49 — Dashboard & wayfinding (T-4901–T-4910)

- **T-4901 · Dashboard redesign (L)** — Tiles get a visual identity (Stat + sparkline + status
  stripe), drag-resize on `DashboardGrid`, per-tile chart types.
- **T-4902 · Cluster health hero (M)** — One glanceable strip — nodes, quorum, links, findings —
  above the fold on the dashboard.
- **T-4903 · Navigation IA redesign (M)** — The 28 pages regrouped task-first; sidebar sections
  (`sidebarGroupsStore` exists) with a collapsed icon-rail mode.
- **T-4904 · Contextual page headers (S)** — `PageHeader` gains breadcrumbs, node/cluster scope
  chips, and live status — applied consistently everywhere.
- **T-4905 · Search results with previews (S)** — Entity results as mini-cards with type glyph,
  status, and a locate-on-map action.
- **T-4906 · Findings triage board (M)** — Severity-grouped board with entity context and
  locate-on-map, complementing the findings table.
- **T-4907 · Skeleton loading (S)** — Per-page skeletons replace spinners; the canvas uses
  T-4409's progressive reveal as its loading state.
- **T-4908 · Designed failure states (S)** — `ErrorBoundary` and `OfflineShellBanner` visuals
  with concrete retry affordances, matching the design language.
- **T-4909 · Notification center (M)** — Toast history as a grouped, icon-led drawer; nothing
  important vanishes after 5 seconds anymore.
- **T-4910 · Tours with spotlight masking (S)** — `tour/` overlays get real spotlight cutouts and
  animated callouts anchored to live elements.

## Phase 50 — The physical world (T-5001–T-5010)

vnprox already knows more about the physical layer than it draws.

- **T-5001 · Faceplate fidelity (M)** — `SwitchFaceplate` port shapes by media (RJ45 / SFP /
  SFP+ / QSFP), activity blink from live counters, per-port VLAN color band.
- **T-5002 · Node faceplate (M)** — Each PVE node's NICs as a rear-panel faceplate, bond and
  bridge membership color-coded — the node's physical identity page.
- **T-5003 · Cabling diagram (M)** — `CablingPlanView` as an actual patch diagram with drawn
  cable runs, not a list of pairs.
- **T-5004 · Rack elevation (L)** — Optional user-arranged rack layout, nodes and switches in
  U-positions, inter-device links drawn. Layout is app-owned data (SQLite), like canvas layout
  already is.
- **T-5005 · LLDP adjacency map (M)** — Chassis/port neighbors as a physical adjacency drawing;
  undiscovered NICs render as unknown stubs (T-3719 made them visible in tables — now draw them).
- **T-5006 · Port-state glyph set (S)** — One glyph language for up / down / blocked /
  half-duplex, shared by faceplates, canvas port stubs (T-4309), and tables.
- **T-5007 · Bond compound rendering (M)** — A bond as a compound plug with member ports;
  active-backup state or LACP hash distribution (`LacpHashSection`'s data) drawn as a flow split.
- **T-5008 · True physical view (L)** — `ViewModeToggle` grows a physical mode — faceplates +
  cables — sharing selection with the logical view (`viewParity.test.ts` extends to cover it).
- **T-5009 · Link health on the cable (M)** — Speed mismatch, MTU mismatch, and optic light
  levels (where SNMP serves them) drawn on the cable run itself.
- **T-5010 · Printable physical sheets (S)** — Print stylesheets + export for rack and cabling
  views: the sheet you take into the aisle.

## Phase 51 — Presentation, export & theming (T-5101–T-5110)

- **T-5101 · Map export as documentation (M)** — `ExportMapMenu` produces high-res PNG/SVG with
  legend, title block, and timestamp — network-documentation-grade output.
- **T-5102 · Shareable view URLs (S)** — Saved views become fully URL-addressable (overlays,
  zoom, selection) for pasting into a ticket.
- **T-5103 · Embed mode polish (S)** — `embed/` gets a chrome-less themed mode sized for wikis
  and Grafana panels (`grafana/` already exists).
- **T-5104 · Theme editor with guardrails (M)** — User-chosen accent within automated contrast
  checks — the same enforcement pattern `index.css.test.ts` applies to demo amber, generalized.
- **T-5105 · Colorblind-safe & high-contrast modes (M)** — Overlay hues verified
  deuteranopia-safe; pattern fills as a redundant channel wherever hue is the only encoding
  (a direct requirement of principle 2).
- **T-5106 · State-of-the-network report (M)** — One-click PDF: map render, findings summary,
  drift status, top talkers — the thing an operator forwards to their boss.
- **T-5107 · Presentation mode (S)** — Full-screen canvas auto-cycling saved views for a NOC
  wall display.
- **T-5108 · Long-string layout sweep (S)** — i18n visual QA against the longest translations so
  the design survives German; feeds the T-4210 screenshot suite.
- **T-5109 · First-run experience (M)** — A designed welcome flow: connect, discover, and watch
  the first map assemble via T-4409's build-up — the moment that sells the product.
- **T-5110 · Design language documented (S)** — `docs/design-language.md`: tokens, chart theme,
  canvas grammar, pictogram usage — so open-source contributors extend the identity instead of
  diluting it.

---

## Sequencing

```
Phase 42 (design language)  ── first, alone; everything consumes its tokens
   ├── T-4210 (visual regression gate) lands before any restyle ships
Phase 43 (canvas rendering) ── second; the flagship
Phase 44 (interaction)      ── overlaps 43's tail
Phases 45–47 (telemetry, addressing, security) ── parallelizable per domain
Phase 48 (time & change)    ── after 43 (reuses node/edge rendering for diffs)
Phase 49 (wayfinding)       ── parallelizable; T-4901 after T-4501 (sparkline)
Phase 50 (physical)         ── after T-4309 (port stubs) and T-5006's glyphs
Phase 51 (presentation)     ── last; exports and polish over finished visuals
```

Cross-roadmap dependencies: **T-4113** (the `GET /topology` drift) should be fixed before
Phase 43 makes the canvas heavier; **T-4305/T-4802** run under the T-4107 envelope gates.

## Execution model

Same as `roadmap-open-source.md`: card stubs per phase in `planning/tasks/phase-4x.md`,
implemented by sub-agents against the mock fixtures, verified where possible against the deployed
node via the Playwright harness, every merge behind a green `make check` — plus, once T-4210
lands, a green screenshot suite. Phase 42 items T-4201/T-4205 (identity, pictograms) are design
work first and build work second; they are the two cards where a human eye on the direction is
worth a round-trip before implementation starts.

## Delivery status

| Phase | Items | Status |
|---|---|---|
| 42 — Design language | T-4201–T-4210 | **in progress** — see below |
| 43 — Canvas rendering | T-4301–T-4310 | not started |
| 44 — Canvas interaction | T-4401–T-4410 | not started |
| 45 — Telemetry visible | T-4501–T-4510 | not started |
| 46 — Addressing & segmentation | T-4601–T-4610 | not started |
| 47 — Security & policy | T-4701–T-4710 | not started |
| 48 — Time & change | T-4801–T-4810 | not started |
| 49 — Dashboard & wayfinding | T-4901–T-4910 | not started |
| 50 — Physical world | T-5001–T-5010 | not started |
| 51 — Presentation & export | T-5101–T-5110 | not started |

### Phase 42 detail

| Card | Status |
|---|---|
| T-4201 identity | done — signal azure (OKLCH 224), literal hex, ramp solved so no call site moved |
| T-4202 typography | done — IBM Plex Sans/Mono, self-hosted, latin + latin-ext |
| T-4203 elevation | tokens done; primitive adoption in the same wave |
| T-4204 status scale | tokens done; call-site adoption in the same wave |
| T-4205 pictograms | done — 23 glyphs in `web/src/icons/`, kinds derived from `internal/inventory/ref.go` |
| T-4206 motion | tokens done + one global reduced-motion gate; primitive adoption in the same wave |
| T-4207 density | not started — scoped: the seam works, the new primitives just never joined it (below) |
| T-4208 component library | done — 8 primitives in `web/src/components/`, 88 tests; forced `--color-status-on-solid` |
| T-4209 empty states | in progress — `emptystate/EmptyIllustration.tsx` + adoption across 58 call sites |
| T-4210 visual gate | done — `web/e2e/visual.spec.ts`, route list derived from `App.tsx` |

**Defects this phase found, all filed rather than absorbed:**

- **T-4211** — formalising an amber `degraded` put it ~22deg from demo mode's amber accent, so in
  demo mode a *selected* row and a *degraded* row nearly match. Introduced by T-4204.
- **T-4212** — the axe sweep's hand-kept route list has drifted from `App.tsx`: `/guest` and
  `/wireguard` have never been accessibility-checked. Found by T-4210 deriving its own inventory
  from source instead of copying that list.
- **T-4213** — the visual gate's 2% diff tolerance was justified by unstoppable clocks, but
  Playwright 1.61 ships `page.clock.install()`. The tolerance is wide enough to hide a real
  regression and should shrink once timestamps are frozen.
- **T-4214** — the accent got a ramp but never got semantic aliases, so 21 call sites across 19
  files still hand-pick a step per theme, one recipe copy-pasted verbatim into nine of them.
  Found by reading `EmptyIllustration.tsx`, whose comment claims "the accent ramp is pre-resolved
  for both themes" one line above `text-accent-600 dark:text-accent-400`.

Two of those four (T-4211, T-4214) are the same defect at different addresses: a role that needs
different values per theme, left as a *value* token rather than a *role* token, so the conditional
reappears at every call site. `--color-status-on-solid` was the third instance and got fixed
inline because a component forced it. The general lesson for the phases below: **a design token
that cannot re-point is not a design token, it is a constant with a nice name** — and the tell is
always a `dark:` variant surviving in code that claims not to need one.

**T-4207 is smaller than the card implies.** The density seam itself is fine —
`components/density.ts` gives both a per-instance prop and an ambient `DensityProvider`, the
prop wins, and 19 sites already provide it. What is missing is only that the T-4208 primitives
did not join it: of the eight new components, `SegmentedControl` and `KeyValue` read
`useDensity`, and `Badge`, `Chip`, `Stat`, `Progress` and `Banner` do not, though all five have
padding or type sizes that should respond. `Skeleton` is the one genuine exception — it borrows
the dimensions of whatever it stands in for, so it has no scale of its own to compress. So
T-4207 is five components joining an existing seam, not a system to design.

Worth noticing *why* it happened: T-4208's card asked for eight primitives and said nothing
about density, so eight primitives is what it got. The seam was three months old and invisible
from inside that task. A component-library card should name the seams a new component is
expected to read, or the library grows a second way of doing everything already solved.

**The lesson worth carrying into Phase 43,** because it will recur on the canvas: on this phase's
palette work the *measurement disagreed with the screen twice*. The amber that satisfied the
hue-separation metric rendered as olive; the dark-mode critical it liked rendered as salmon.
Rendering four candidates and looking at them is what settled it, and the metric — not the palette
— turned out to be the thing that was wrong. Every card below that claims a visual property should
produce an image of that property, not only a number.
