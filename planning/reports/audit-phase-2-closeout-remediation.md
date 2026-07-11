# Phase 2 close-out audit — remediation record

**Date:** 2026-07-10 · **Base:** `main` at the close-out audit (`dfd5082`), fix
branch `fix-t207-e2e` off `ad702e2` · **Audit:** `planning/reports/audit-phase-2-closeout.md`

The close-out audit of T-206/T-207/T-208 found T-206 and T-208 sound and one
MAJOR (F-01) plus two MINORs (F-02, F-03). Dispositions below.

## F-01 · MAJOR · T-207 — FIXED

**Finding:** the AC1 Playwright walkthrough was red on the integrated `main`
tree. T-207's step 7 asserted a `failed`-to-apply outcome banner, correct at
T-207's base (a non-root daemon writing the real `/etc/network/interfaces`
fails at the stage step). After the remediation's F-22 `dev_interfaces_dir`
sandbox merged into `main`, the same apply instead **succeeds** into the
commit-confirm window (`awaiting_confirm` + countdown), so the assertion was
stale and the walkthrough failed 1/2. The report's "2/2 pass" was true at its
base but false as-integrated.

**Root cause, two parts:**
1. A stale assertion — the test encoded the pre-sandbox failure behavior.
2. A latent test-isolation defect the stale assertion had masked: the e2e
   webServer resets the SQLite DB per run but **not** the `var/dev-host`
   sandbox. Because an apply now genuinely succeeds, the committed change
   (e.g. `vmbr77`) persists in `var/dev-host/interfaces` across runs (the dev
   NodeAgent only re-seeds the fixture when it is *missing*), so the
   walkthrough would pass once and then fail on the next run against a
   polluted base file.

**Fix (`web/e2e/changesets.spec.ts`, `web/playwright.config.ts`):**
- Rewrote step 7 to drive the real happy path the sandbox now enables:
  **apply → countdown banner (`role="alert"` with Confirm/Roll back) → page
  reload mid-window → countdown still present → Confirm → `committed` outcome
  banner**. This is exactly AC1's "apply → countdown → confirm" and AC3's
  mid-window reload survival, both previously deferred to hardware validation.
- Removed the now-obsolete `runningAsRoot` skip guard: the sandbox writes to
  `var/dev-host` with a no-op `ifreload`, so the apply never touches the real
  machine regardless of uid.
- The countdown banner locator was corrected from `role="status"` to
  `role="alert"` — the `applying` and terminal-outcome states use
  `role="status"`, but the `awaiting_confirm` countdown uses `role="alert"`
  (see `CountdownBanner.tsx`).
- The webServer command now `rm -rf var/dev-host` alongside the existing DB
  reset, restoring a deterministic starting state each run.

**Verified:** the full stack (pvemock → vnproxd with `testdata/dev.toml` →
production SPA) was built and run. `changesets.spec.ts` passes **2/2**, and the
walkthrough was run **3 consecutive times** to confirm the sandbox-reset makes
it deterministic (it was pass-once-then-fail before). `tsc --noEmit` and
`eslint` clean on the edited files. No source/product code changed — this is a
test + test-harness fix only. Report `planning/reports/T-207.md` corrected
(AC1, AC3, "How I tested", deviations) to match.

**Residual (unchanged, honestly documented):** the real `ifreload` execution
still needs hardware validation; the full round-trip is otherwise e2e-covered.

## F-02 · MINOR · T-206 — ACCEPTED (no change)

The card's "side-by-side/unified diff viewer" shipped unified-only. This is an
under-delivered deliverable but was honestly disclosed as a deviation in the
T-206 report, and unified diff satisfies the AC's functional intent (diff
between any two snapshots / vs. live renders correctly). Left as a documented
deviation; a side-by-side view is a low-risk future polish, not a correctness
gap. No blocker for v0.5 beta.

## F-03 · MINOR · cross-task — DEFERRED

HTTP apply-contract wire assertions (202 / 409 `changeset_locked` / 422 with
findings / 404) remain unasserted at the transport layer (originally deferred
F-11 from the checkpoint audit). The behaviors are covered at the service
layer; only the thin HTTP mapping is untested end-to-end. Re-deferred — a
small, well-scoped follow-up best folded into an early Phase 3 API hardening
pass rather than reopening Phase 2. Recorded here so it is not lost.

## Note — `internal/change` coverage drift

The close-out audit measured `internal/change` at 87.7%, below the 90.2% the
package held at the T-205 checkpoint. This is not a regression in tested code:
T-206 (restore/inverse-synthesis) and T-208 (`iface.raw.replace` + raw
validation) added statements to the package after T-205's ≥90% acceptance
criterion was met and measured. No active build gate enforces the 90% floor
package-wide. Flagged for awareness; raising raw-op and restore-path coverage
back over 90% is a reasonable Phase 3 hygiene item, not a Phase 2 blocker.
