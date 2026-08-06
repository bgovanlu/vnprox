# Phase 20 — Sharper daily use (v3.3)

Goal: finish what the build left open, and make the everyday paths pleasant. Smaller cards than
the prior two phases, high daily contact. Two of them close gaps the product has carried since
v2.0: a federation registry with full CRUD routes and no UI at all, and a scattering of
well-specified follow-ups that implementation reports flagged and nobody picked up.

Dependency shape: mostly independent roots. **T-2001** (federation UI) gates **T-2003** (change
review) only because the review surface should absorb the cluster-scoping concepts the editor
introduces. **T-2004** (a11y and design-system pass) precedes **T-2005** (PWA), since shipping a
new surface before the audit means auditing it twice. **T-2002** and **T-2006** can start
immediately.

This phase can run alongside Phase 19 — it shares no packages with the operability work.

Exit demo: a second cluster is attached entirely through the UI; a colleague reviews and comments
on the resulting changeset from a phone, approves it, and confirms it after apply.

---

## T-2001 · Federation cluster editor UI ★
**model:** sonnet-5 · **size:** M · **depends:** — · **context:** `internal/api/federation.go` (the existing CRUD routes, capability gating, audit), `web/src/api/federation.ts`, `web/src/topology/federation/`, `web/src/settings/SettingsPage.tsx`, `planning/reports/T-1201.md`, `planning/reports/T-1407-followups.md`, `docs/api.md` §Federation

**Objective:** `/federation/clusters` has full CRUD, audit coverage, and capability gating — and
no UI whatsoever. Attaching a cluster today means hand-crafting a `POST` with a credential in it.
This is the largest UI-versus-API gap in the product and it gates the multi-cluster story v2.0 was
named after.

**Deliverables:**
- A federation cluster management surface (a Settings section, following `SettingsPage.tsx`'s
  existing `Section` pattern rather than inventing a new page shape): list attached clusters with
  status, attach, edit, detach.
- An attach/edit form covering both credential kinds (ticket username/password/realm, and PVE API
  token), with the credential write-only — never rendered back, matching the API's own guarantee
  that it is never returned.
- The tunnel linkage surfaced read-only per cluster: `wgTunnelId` with `wgTunnelSource`
  ("explicit" vs. "peer"), including the non-obvious consequence that clearing an explicit
  override does not unlink a cluster that still has a tagged WireGuard peer. This is
  `wgTunnelSource`'s first real consumer.
- Detach confirmation naming what is lost (aggregated views, cross-cluster IPAM conflict
  detection) — detaching is cheap to do and annoying to undo.
- `netWrite` capability gating and CSRF handled the way every other mutating surface does; the
  form is disabled with the standard tooltip when the capability is missing.

**Acceptance criteria:**
1. A cluster can be attached, edited, renamed, re-credentialed, and detached entirely through the
   UI; Playwright spec end to end against the mock.
2. No credential value is ever rendered into the DOM after submission — asserted explicitly,
   including after an edit that leaves the credential unchanged.
3. `wgTunnelSource` is displayed, and a peer-derived linkage is visibly distinguished from an
   explicit one, with the "clearing this will not unlink it" consequence stated in the UI.
4. Without `netWrite`, mutating controls are disabled with the standard missing-capability
   tooltip; the read view still works.
5. Vitest for the form logic, Playwright for the flow; `make check` green.

---

## T-2002 · Flagged-follow-up burndown ★
**model:** sonnet-5 · **size:** M · **depends:** — · **context:** `planning/reports/T-505.md` (deep link), `planning/reports/T-504.md` (`RuleRef.RulesetRef`), `planning/reports/T-1603.md` (security-group inspector), `planning/reports/T-1307.md` (per-step audit), `web/src/firewall/`, `internal/sim/`, `internal/diagnose/`

**Objective:** A backlog sweep of small, well-specified gaps that implementation reports left
behind, each already scoped by its own report. Individually minor; collectively the difference
between "the feature exists" and "the feature is finished."

**Scope — exactly these four, each with its report as the specification:**
1. **Firewall rule deep link does not focus its target** (T-505). `deeplink.ts` builds
   `/firewall?scope=…&ref=…&pos=…&origin=…` by stable identity, but the firewall pages never read
   those query params, so the link navigates and then abandons the user on an unfiltered page.
   Wire the pages to read the params and focus the matching row.
2. **`internal/sim`'s `RuleRef.RulesetRef` is unpopulated for `origin: "guest"`** (T-504), which
   is the single most common deny case, forcing a frontend workaround in `deeplink.ts`. Populate
   it — or, if the field is genuinely never meant to be authoritative, remove it from the wire
   contract and delete the workaround. Do not leave it half-true.
3. **No security-group inspector surface** (T-1603), so the microsegmentation planner can only be
   launched per-guest even though groups are where the policy actually lives. Add a group
   inspector and the group-scoped launch point.
4. **No per-step audit granularity in the diagnosis ladder** (T-1307): a ladder run is audited as
   one event, so which step reached which conclusion is not recoverable afterwards.

**Acceptance criteria:**
1. A firewall deep link navigates *and* focuses the referenced rule; a link to a rule that no
   longer exists degrades gracefully with a message rather than an empty highlight.
2. `RuleRef.RulesetRef` is either populated for guest-origin rules (with the `deeplink.ts`
   workaround removed) or removed from the contract entirely — the report argues which and why.
3. The microseg planner launches from a security group, producing the same proposal shape the
   guest-scoped launch does.
4. A diagnosis ladder run writes per-step audit rows; a reviewer can reconstruct which step
   concluded what.
5. Each of the four has a regression test; the four source reports are updated to note the
   follow-up closed; `make check` green.

---

## T-2003 · Change review: approvals, comments, side-by-side diff ★
**model:** sonnet-5 · **size:** L · **depends:** T-2001 · **context:** `internal/change/`, `internal/tenant/` (the request-changeset approval queue — the model this generalizes), `web/src/changesets/`, `docs/features/change-management.md`, `docs/api.md` §Changesets

**Objective:** The changeset is the product's unit of work and its review surface is thin. This is
where a *team*, as opposed to a single admin, actually lives.

**Deliverables:**
- Per-op comments on a changeset, with author and timestamp, surviving validate/diff cycles.
- An explicit approval step, generalizing the tenant request-changeset queue rather than building
  a second mechanism: configurable whether approval is required, by whom, and whether the author
  may approve their own.
- A side-by-side before/after **config** diff alongside the existing semantic diff — operators
  reason in `/etc/network/interfaces` terms even when the semantic diff is more accurate.
- A shareable review link that opens the changeset in review mode.
- Audit rows for comment, approve, and reject, consistent with existing changeset lifecycle
  auditing.

**Safety note for the executor:** approval is an authorization surface. Whether a changeset may
apply must be decided **server-side** from the stored approval state — never from a client
assertion, and never by the frontend simply not rendering the apply button. `planning/reports/T-402.md`
already flagged capability enforcement that lived only in the frontend; do not add a second
instance.

**Acceptance criteria:**
1. Comments persist across validate and diff, and are attributed; deleting an op does not orphan
   its comment silently.
2. With approval required, apply is refused **server-side** for an unapproved changeset —
   including via direct API call with the UI bypassed, and via `vnproxctl`.
3. Self-approval is permitted or refused per configuration, and the refusal path is tested.
4. The config diff matches what apply would actually write, for a fixture changeset spanning
   node-file and SDN ops.
5. Comment, approve, and reject are audited; Playwright covers the review flow; `make check` green.

---

## T-2004 · Accessibility and design-system second pass ★
**model:** sonnet-5 · **size:** M · **depends:** — · **context:** `web/e2e/a11y.spec.ts`, `web/src/components/`, `planning/tasks/phase-9.md` (the first pass), `docs/development.md` (UI conventions)

**Objective:** Phase 9 did the first accessibility and design-system pass. The surface has roughly
doubled since — capture, diagnose, microseg, planner, hub, embed, edge, IPv6, k8s, conntrack,
WireGuard wizard, federation — and the second pass is overdue.

**Deliverables:**
- Keyboard reachability for every wizard, inspector, dialog, and the map's interactive elements;
  focus management and focus trapping in dialogs; visible focus indicators throughout.
- Contrast audit across both themes, including chart and map colors, which the first pass largely
  did not cover.
- Screen-reader labeling for the topology canvas and its controls — the hardest surface in the
  product and the one most likely to have been skipped.
- A documented reduced-motion path (`prefers-reduced-motion`) for map transitions and animations.
- Component-set consolidation: any surface that grew its own one-off input, button, or dialog
  instead of using `web/src/components/` gets folded back in.
- `web/e2e/a11y.spec.ts` extended to cover every page added since Phase 9, not just the original
  set.

**Acceptance criteria:**
1. Every route is reachable and operable by keyboard alone; the Playwright a11y spec asserts it
   per route rather than on a sample.
2. Automated a11y checks pass on every page; violations that are deliberately accepted are listed
   with a reason rather than suppressed silently.
3. Contrast meets WCAG AA in both themes, charts and map included.
4. `prefers-reduced-motion` is honored; asserted in a spec.
5. One-off components are consolidated or the report names each survivor and why; `make check`
   green.

---

## T-2005 · Mobile PWA with push ★
**model:** sonnet-5 · **size:** M · **depends:** T-2004 · **context:** `planning/reports/T-909.md` (responsive triage layout), `web/src/`, `internal/api/` (event stream, T-1104), `docs/roadmap-universal.md` Phase 17 exit demo, `docs/security.md`

**Objective:** T-909 shipped a responsive triage layout; Phase 17's own exit demo describes an
on-call human confirming a changeset from their phone. Make that real: an installable PWA with
web-push for new critical findings and for changesets awaiting confirm.

**Deliverables:**
- PWA manifest, service worker, and installability, with an offline shell that fails honestly —
  a stale topology presented as current would be dangerous, so cached views are labeled with their
  age.
- Web-push subscription management tied to the existing event stream (T-1104), with per-category
  opt-in: critical findings, awaiting-confirm changesets, drift.
- A push notification deep-links into the triage layout with the relevant changeset or finding
  focused.
- Push subscriptions treated as credentials at rest and revocable per device; subscriptions are
  per-session and die with it.

**Safety note:** a push that lets someone confirm a changeset from a lock screen must not weaken
the confirm authorization. Confirming still requires an authenticated session with the capability
— the notification is a deep link, never an action token.

**Acceptance criteria:**
1. The app is installable and passes an installability audit; the offline shell labels cached data
   with its age and never presents stale topology as live.
2. A critical finding and an awaiting-confirm changeset each generate a push to a subscribed
   device (fixture-driven), deep-linking to the right view.
3. Confirming from a notification requires a real authenticated session with the capability; a
   notification alone can never confirm — explicit test.
4. Subscriptions are listable and revocable per device, and are cleaned up when the session ends.
5. Playwright coverage for the flow; `make check` green.

---

## T-2006 · Localization (i18n) ★
**model:** sonnet-5 · **size:** L · **depends:** — · **context:** `web/src/`, `web/src/wireguard/strings.ts` and `web/src/sdn/wizards/strings.ts` (the existing extracted-strings precedent), `docs/development.md`

**Objective:** No i18n scaffolding exists; every user-facing string is inline English. Proxmox's
user base is heavily German-speaking. Extraction is cheap now and gets more expensive with every
screen added — this is a card whose cost rises monotonically until it is done.

**Deliverables:**
- An i18n framework choice (argued in the report against `docs/development.md`'s dependency
  policy — this is a new major frontend dependency and needs a note either way).
- String extraction across the app, following the `strings.ts` precedent the wizards already set:
  keys grouped by feature, with context notes for translators where a string is ambiguous.
- A German translation as the first locale, with locale detection and an explicit override in
  Settings.
- Pluralization, number, and date/time formatting handled by the framework rather than by
  hand-rolled helpers.
- A lint rule or test that fails on a new inline user-facing string, so the extraction does not
  immediately rot.

**Acceptance criteria:**
1. Every user-facing string renders from the catalog; a test asserts no inline literals remain in
   the covered surfaces.
2. Switching to German changes the whole UI, including validation messages, findings copy, and
   toast text — the places translations usually get missed.
3. Pluralization and date/number formatting are locale-correct, table-driven per locale.
4. A newly added inline string fails CI.
5. `make check` green; `docs/development.md` documents how to add a string and a locale.

---

## Card-author notes

- **These cards were written before Phase 18 ran** (D4). T-2002's four items are specified from
  their source reports and are stable, but Phase 18's burndown may add UI-visible bugs that belong
  in this phase; expect to extend T-2002's scope rather than to open a fifth card for each.
- **T-2003's approval work is an authorization surface**, not a UI feature, and should be reviewed
  as one. AC2 is the acceptance criterion that matters.
- **T-2006 is sized L and is mostly mechanical**, but the framework choice is a real dependency
  decision under `CLAUDE.md`'s "no new major dependencies without a note" rule. The executor should
  raise it in the report rather than deciding silently.

---

## T-2003-bug-01 · Nav-rail navigation silently stops working after the inspector closes

**Severity:** High — a user-facing dead end in the primary navigation, reachable by an ordinary
sequence, with no error shown. The user clicks a nav item, the URL changes, and the page simply
does not.

**Found by:** T-2003, while hardening a `changesets.spec.ts` locator. Its original
`getByText("app01/net0")` assertion could false-positive-match a stale Topology graph node label
instead of the Guests page it meant to assert on — so the bug had been masked by a weak locator.
Tightening it to a heading-based assertion exposed the real failure. See
`planning/reports/T-2003.md` §4 and §6.

**Reproduction:** spotlight search → open an entity inspector → `Escape` to dismiss it → click any
nav-rail `<Link>`. `window.location` updates, but `<Routes>`'s rendered `Outlet` content does not
swap; the DOM keeps showing the previous page (Topology). Root-caused with a throwaway debug spec.

**Not caused by T-2003** — independently verified to reproduce on pristine `main` via `git stash`,
and none of the involved files are touched by that card. It is pre-existing and of unknown age.

**Why it survived this long:** nothing in CI runs the Playwright suite (`T-1806-bug-01`), and the
one spec that traversed this path asserted on text loose enough to match the *stale* page it was
supposed to have navigated away from. A test that passes because the app did not navigate is worse
than no test — this is a concrete instance of that failure mode, and worth remembering when
`T-1806-bug-01` is addressed: turning the suite on is necessary but not sufficient if the
assertions are this weak.

**Scope for whoever picks this up:** find why the route change does not re-render (a likely
candidate is focus/portal state left behind by the inspector dialog interfering with router
context, but that is a hypothesis, not a diagnosis). Fix the cause, add a regression spec that
asserts on a *heading*, and audit sibling specs for the same too-loose-locator pattern.

---

**Update, 2026-08-06 — the documented reproduction no longer reproduces.**

Re-checked as part of landing the e2e gate (`T-1806-bug-01`). A regression spec now exercises the
exact sequence this card describes — top-bar Search → type `vmbr0` → click the first result →
assert the inspector really opened (`dialog "vmbr0"`, not merely "a dialog is visible") → `Escape`
→ click a nav-rail link — and asserts on **headings**, including that Topology's heading is *gone*:
`web/e2e/nav-after-inspector.spec.ts`.

It passes. Navigation works, twice in a row, on `6c0957e`.

**This is a finding, not a fix — nothing here was changed to make it pass.** Two readings remain
open, and this card stays open until one is settled:

1. It was fixed incidentally between being filed and now (phase 22 touched `AppShell` and Radix
   focus handling; phase 23 did not).
2. The real precondition differs from the documented one. The card notes it was found inside
   `changesets.spec.ts`, so the trigger may involve the changeset drawer rather than spotlight
   alone — and this card's root cause was never actually recorded, only hypothesised
   ("focus/portal state ... but that is a hypothesis, not a diagnosis").

Whoever picks it up should try reading 2 first, from the `changesets.spec.ts` context. Two locator
mistakes were made and corrected while writing the spec above, both worth avoiding: the inspector
in this flow exposes **no** close button (its `Close <entity>` control belongs to the stacked-pane
variant), and it is *not* reliably the only element with `role="dialog"` on the page.
