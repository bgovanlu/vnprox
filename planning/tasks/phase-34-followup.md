# Phase 34 follow-ups

## T-3406-followup-01 · Pre-existing page-local contrast and ARIA defects
**model:** sonnet-5 · **size:** L · **depends:** Phase 34 (T-3406) · **context:** `web/e2e/quarantine.json` (the 40 quarantined tests name the exact routes), `web/e2e/a11y.spec.ts`, `web/e2e/demo.spec.ts`, `docs/development.md` "Visual language (Phase 34, T-3401)"

**Deadline: the quarantine expires 2026-09-17.** After that date `make check` fails whether or
not the tests do (`internal/e2egate`'s `TestRepoQuarantineIsValid`, T-2505). This card exists to
be done before then, not to be renewed.

**Where this came from.** Phase 34's close-out (T-3406) was the first time this app's axe suite
ran *every routed page* across light, dark, and demo-amber. It found six genuine Phase-34
regressions, which T-3406 fixed. It also found roughly nineteen routes' worth of defects that
**pre-date Phase 34** — every sampled instance git-blames to well before it. Those were
quarantined rather than fixed, because repairing fifteen unrelated feature pages is not a
redesign close-out's job, and folding them in would have hidden how much of the sweep's output
the redesign was actually responsible for.

That distinction is the reason this is a card and not a footnote: the suite was previously
scoped narrowly enough that these never surfaced. They are not new, and they are not the
redesign's — but they are real, and they now have a date attached.

**Objective:** Clear the quarantined a11y failures at their source, then delete the quarantine
entries. Do not extend the expiry.

**Deliverables:**
- Fix the two defect classes the sweep identified:
  1. Bare `text-slate-500`/`text-slate-400` used as body or label text with no dark-mode pairing
     (or a pairing that fails in one of the two themes) — across Audit, History, Settings and
     its four sub-pages, Incidents, Edge, Flows, Blueprints, SDN, Governance, Firewall,
     Management, IPAM, Analysis, Tools, and Config-as-code.
  2. `AuditPage`'s `aria-expanded` on a bare `<tr>`, which is not a role that takes it.
- Remove each fixed test's entry from `web/e2e/quarantine.json` in the same change that fixes it,
  so the file never claims more debt than exists.
- Where a fix is a shared-component change rather than a page-local one, prefer the shared fix —
  but check the *other* theme and the demo-amber accent before declaring it done. T-3406 found a
  case (the `bg-accent-600/10 text-accent-700` selection wash) where a 10%-opacity wash barely
  darkens no matter which step it is mixed from, so the obvious fix did not work.

**Acceptance criteria:**
1. `web/e2e/quarantine.json` contains no `T-3406-followup-01` entries.
2. The full e2e suite passes with those tests live, in all three variants (light, dark,
   demo-amber).
3. `make check` green.

**Explicitly not in scope:** re-running the two historically flaky axe specs (`axe: Dashboard`,
`axe: Topology (Switch view)`) to chase their machine-load flake. Those have their own history
(33%/47% flake rates under load, passing in isolation) and are a separate problem from a real
contrast defect.

---

## Deferred from Phase 34, not yet carded

- **Sidebar identity chip has no cluster name to show.** Neither `GET /auth/me` (username, realm)
  nor `GET /health` (status, version, demo flag) carries an instance or cluster name, so the chip
  renders the product name plus demo state. If a name is ever added to either response,
  `IdentityChip` in `web/src/layout/Sidebar.tsx` is the single wiring point. Needs a backend
  decision first — deliberately not invented client-side.
- **Stripe-style user-pinned "Shortcuts" section** — listed as a stretch item in the Phase 34
  preamble and not built. Requires app-owned per-user state; would follow the `layouts`-table
  precedent (app-owned data only, never a shadow copy of PVE config).
