# Implementation plan — Arc 6 (earned)

**Roadmap:** [`../docs/roadmap-earned.md`](../docs/roadmap-earned.md) ·
**Cards:** [`tasks/phase-29.md`](tasks/phase-29.md) — phases 30–33 cards are authored when
their phase begins, from the roadmap's card summaries, so each phase's cards can bake in what
the previous phase learned.

Twenty-five cards across five phases. Release cuts: `v4.1` after Phase 29, `v4.2` after 30,
`v4.3` after 31, `v4.4` after 32, `v5.0` after 33.

## Order

Phases run in order — Phase 29 first because several of its items are live defects or open
security gaps in the deployed release. Within Phase 29, cards run in two waves chosen by file
ownership and by the one-migration-per-wave rule:

| Wave | Card | Why here |
|---|---|---|
| 1 | `T-2901` PWA/embed un-break | Live defect in production; owns `internal/api/middleware.go` |
| 1 | `T-2902` peer write parity + audit IP | Owns `internal/peer/` + **migration 0047**; the arc's biggest safety fix |
| 1 | `T-2904` hub install hardening | Independent file set (`cmd/vnproxd/hubinstall.go`, `internal/api/hub.go`) |
| 1 | `T-2906` docs truth pass | Docs-only; runs alongside anything |
| 2 | `T-2903` bearer read_only + token expiry | Owns `internal/auth/middleware.go` (incl. the CSRF constant-time fix, moved here from T-2905 for file ownership) + **migration 0048** |
| 2 | `T-2905` hardening punch list | Everything else in `internal/auth` + daemon/webhook/prober/packaging items |

Phase 30's cards are written ([`tasks/phase-30.md`](tasks/phase-30.md), 2026-08-16) and run in
two waves, again by file ownership. Every card is UI-only, so no migration number is claimed by
either wave:

| Wave | Card | Why here |
|---|---|---|
| 1 | `T-3005` canary apply UI | Owns `web/src/changesets/ReviewApplyScreen.tsx`; unblocks the review-screen work `T-3002` also needs |
| 1 | `T-3001` config-as-code cockpit | Owns `web/src/drift/`; independent of the others |
| 1 | `T-3004` analysis surfaces | Six independent read-mostly areas; touches nobody else's files |
| 2 | `T-3002` governance surfaces | Also touches `ReviewApplyScreen.tsx` (policy verdicts, break-glass) — must follow `T-3005` |
| 2 | `T-3003` platform panel | Owns `web/src/settings/`; surfaces `T-2903`/`T-2905` semantics, so it wants Phase 29 settled |
| 2 | `T-3006` help completion + panel-aware gate | Gates every panel the five cards above add, so it runs last by construction |

Phase 31's cards are written ([`tasks/phase-31.md`](tasks/phase-31.md), 2026-08-16) and run in
four waves. Contention is exactly two files — `internal/change/op.go` and
`internal/change/validate_schema.go` — so the rule is **one op-adding card per wave**:

| Wave | Card | Why here |
|---|---|---|
| 1 | `T-3101` SDN Fabrics | Owns `validate_schema.go` and the SDN object graph; P0, and every SDN-shaped card waits on it |
| 1 | `T-3105` restore fidelity | Owns `restore_ops.go` + the inventory Bond model; disjoint from T-3101 |
| 2 | `T-3102` controllers | Owns the wave's op-const block |
| 2 | `T-3103` firewall fidelity | Owns `internal/fw/` + `inventory/entity.go`; disjoint from T-3102 |
| 3 | `T-3104` IPAM completion | Owns the wave's op-const block |
| 4 | `T-3106` localization | Touches every component the waves above wrote; cannot overlap with any |

Migration numbers are claimed per wave as usual (0049–0052), but **none is expected**: every
object in Phase 31 is PVE-owned, and vnprox never persists PVE config as authoritative state.

Waves within phases 32–33 are decided when their cards are written; the standing rules below
apply to all of them.

**One rule specific to Phase 31, and it generalizes:** before modelling a PVE object, run
`pvesh usage` against it on pvecube (PVE 9.2.4) and check the capture in. Phase 31's scoping
found the repo's model of SDN Fabrics wrong in `pvemock`, in `docs/compatibility.md`, in the
compatibility matrix's only enforced check, and in the roadmap card itself — all four agreeing
with each other because all four were written from the same Proxmox release notes rather than
from the API. Do not model from `pvemock`, from `docs/`, or from release notes.

**One rule specific to Phase 30, because it inverts the usual instinct:** these are assembly
cards over routes that already exist and are contract-frozen. Every card asserts
`docs/openapi.json` is byte-identical before and after. An agent that reaches for a new route
has misread its scope — it reports that rather than adding one.

## Cross-cutting rules for this arc

- **Exactly one schema migration number per wave**, claimed in the plan before the wave starts
  (0047 → T-2902, 0048 → T-2903). Every new migration adds its `versionSeeds` fixture — the
  `migrate_fromeach_test.go` loop is never relaxed, narrowed, or skipped; fix the seed, not the
  harness.
- **Sub-agents do not run `make check`, `make e2e`, or Playwright.** The orchestrator runs the
  full gate per wave on the merged state. Agents run only the focused package tests for files
  they touched.
- **Every fix ships with the test that proves the old behavior was wrong.** A CSP fix ships a
  browser-level assertion that a service worker registers; the peer-write fix ships a test that
  watches an unvalidated protected-path write get refused; the bearer fix ships a test where a
  write-scoped token is refused in a read-only deployment.
- **A card that makes a documented claim true (or false) updates that doc sentence in the same
  commit.** T-2906 owns the broad truth pass but explicitly does not own sentences that
  T-2901/2902/2903 are in the middle of making true.
- **API compatibility is additive-only.** Removing a request field is done by ignoring it with
  an explicit error path, never by breaking the schema (see T-2904's `trustUnsigned`).
- **Standing constraints carried from prior arcs:** a11y suppressions are never expanded;
  `web/src/help/coverage.test.ts` is never weakened; hub signature verification never becomes
  optional; the service worker never caches `/api/*`; push payloads never carry
  cluster-identifying content; new `/api/v1` routes land in `docs/api.md` + `docs/openapi.json`
  in the same commit.

## Risk register

| Risk | Card | Mitigation |
|---|---|---|
| Loosening CSP for the PWA accidentally widens it beyond `'self'` | `T-2901` | AC pins the exact directive list; a test asserts the full header string, not substrings |
| Embed frame-ancestors relaxation enables clickjacking on non-embed routes | `T-2901` | Relaxation is per-route for `/embed/*` only, behind a config allowlist defaulting to `'self'`; a test asserts app routes still send `DENY` |
| Receiving-side peer validation breaks legitimate distributed rollback (which must restore even "protected" content) | `T-2902` | Restore of a snapshot the receiving node itself staged is exempted by provenance, not by skipping validation; AC covers the rollback path explicitly |
| Audit `ip` column migration breaks the from-each corpus | `T-2902` | Seed rule above; the column is nullable so pre-0047 rows stay valid |
| Token `expiresAt` breaks existing automation overnight | `T-2903` | Expiry applies to newly minted tokens; existing tokens get no retroactive expiry, and the release note says how to rotate |
| Hub endpoint containment check has a symlink hole | `T-2904` | Containment is asserted on the fully resolved path (`EvalSymlinks`) of both root and endpoint |
| The punch list card sprawls | `T-2905` | Each item is one bounded mechanical change with its own AC; anything that grows a design question is split out and reported, not absorbed |
| Docs pass rewrites history instead of recording it | `T-2906` | Stale snapshots are corrected in place only where they state current fact; delivery history stays, with dated correction notes — the status-matrix double-negation trap is the cautionary example |

## What this arc deliberately does not do

The roadmap's "Not carried" table is binding: no post-confirm revert, no management-IP
re-addressing, no certificate renewal, no git-history rewrite (unless T-3302 forces the
question), no full non-root operation, no suppression semantics. If a card seems to need one
of these, that is a finding for the report, not a decision to make inline.
