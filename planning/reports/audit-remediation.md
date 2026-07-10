# Phase 0/1 audit remediation — completion report

**Date:** 2026-07-10 · **Base:** a5cab4c · **Scope:** every finding in
`audit-phase-0.md` (12) and `audit-phase-1.md` (21), plus one bug found during remediation.

## How the work was organized

Five fix agents in three dependency-ordered waves, territories chosen so no two agents touched the
same files:

1. **Wave 1 (parallel):**
   *infra/phase-0* (Makefile, packaging, CI, docs, `internal/api` middleware, `cmd/vnproxd`,
   `internal/store` wiring) · *pvemock+auth+pve-client* (`internal/pvemock`, `internal/pve`,
   `internal/auth`, `testdata/clusters`) · *inventory+collect* (`internal/inventory`,
   `internal/collect`).
2. **Wave 2:** *topology backend* (`internal/topology`, `internal/api`) — consumes wave 1's new
   inventory APIs (`Snapshot.RawSource`, optional booleans).
3. **Wave 3:** *web* (`web/`) — consumes wave 2's pinned JSON shapes; added the Playwright e2e
   suite.
4. **Wave 4 (orchestrator):** percent-encoded-ref fix, decisions record, hardware-validation
   checklist, audit-file appendices, this report, full `make check`.

Per-finding outcomes are in the **remediation appendix** at the bottom of each audit file.
Long-lived documentation produced:

- `planning/reports/phase-0-1-decisions.md` — decisions of record (D-01…D-10), acknowledged
  deviations (V-01…V-05), and the new comment-hygiene rule.
- `planning/reports/needs-hardware-validation.md` — everything only a real PVE cluster or a real
  dev machine can confirm, as a check-off list.
- `docs/testing/topology-render-verification.md` and `docs/testing/topology-performance.md` —
  the T-107 verification artifacts (how to run, executed record, environment caveats).
- `docs/api.md` — now pins the WS envelope, `GET /inventory/{ref}` `rawSource`, and the
  `/topology` `staleness` shapes.

## Behavior changes to know about

- **Collector:** departed cluster nodes are retired from inventory (empty scoped polls per PVE
  source); the interfaces(5) file is now ingested (`FromInterfaces`), so `host-interfaces` is a
  live top-precedence source for declared fields on the local node.
- **Inventory:** `Snapshot.RawSource(ref)` returns per-source raw text; `vlanAware`/`stp`/`linkUp`
  gained `…Set` companions — unset means "not reported" (no merge win, no conflict; UI shows
  unknown, and an unreported NIC link is no longer paintable as down).
- **PVE client:** IPAM reads added; ticket renewal uses ticket-as-password and drops the plaintext
  password after the first successful renewal (password fallback retained).
- **pvemock:** API-token auth, single-step TOTP, ticket TTLs, `GET /access/permissions`, IPAM
  routes, and new fixture users/tokens (`totp-user@pve`, `sdn-only@pve`, `vm-user@pve`,
  `root@pam!daemon`) — all opt-in via fixture fields; defaults preserve prior behavior.
- **API:** `/topology` gains an optional `staleness` section (source stale after 3 consecutive
  failures); `/inventory/{ref}` gains `rawSource` and now accepts percent-encoded refs; CSP
  tightened to `connect-src 'self'`.
- **Web:** demo auth stub is now opt-in (`VITE_AUTH_STUB=true`); real login is the default dev
  flow. Staleness banner + stale-band greying; inspector Raw source tab shows genuine raw config
  (provenance moved to its own tab); WS delta payloads are runtime-validated.
- **Daemon/packaging/CI:** metric-prune loop wired (hourly); `make dev` starts pvemock; .deb
  binaries 0755; CI adds npm cache, a package job (build + deb artifact), and a 60s parser fuzz
  job; dompurify advisory closed via npm override (npm audit: 0 vulnerabilities).

## What was NOT fully closed (tracked, not hidden)

All in `planning/reports/needs-hardware-validation.md`: real-PVE confirmation of token auth
semantics, ticket-as-password window, `/access/permissions` tree shape, two-step NeedTFA, IPAM wire
shapes, PUT-as-JSON on oldest supported PVE, ticket-expiry margins; `systemctl start` from the
.deb under real systemd; real netlink/LLDP/bond readers on PVE hardware; the 60fps criterion on a
GPU-composited machine (headless measurement committed: ~35 fps mean with a 60 fps idle control —
an environment floor, not a verdict); and the hand-extended `host-interfaces` half of the captured
detail fixture.

## Verification

- `make check` (lint + typecheck + full Go and Vitest suites) — green at completion.
- `-race` on every touched Go package; full 30s inventory stress soak race-clean.
- Playwright e2e (opt-in `npm run e2e`): 2/2 against a freshly booted real stack (pvemock +
  vnproxd + production build, real login).
- `make deb` + `dpkg-deb` inspection; live `make dev` bring-up/teardown; fuzz smoke run.
- Frontend: `tsc --noEmit`, `eslint` (0), `vitest` 100/100 (up from 66).
