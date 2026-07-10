# Phase 0 audit — Foundations (T-001…T-006)

**Date:** 2026-07-09 · **HEAD:** a5cab4c · **Auditor:** Claude (6 parallel audit agents, one per task pair)

**Method:** every acceptance criterion on every task card was verified against the actual code
and test assertions (not test names), with the relevant tests executed (`go test -race -count=1`
per package, `npx tsc --noEmit`, `npx eslint .`, `npx vitest run`, `make check`/`lint`/`build`/`deb`,
`systemd-analyze verify`, plus live runs: the daemon against `testdata/dev.toml`, the pvemock README
curl walkthrough, the built binary's SPA embed). `make check` at HEAD: **exit 0**.

**Verdict: phase 0 passes.** All acceptance criteria are met in substance; every finding below is
minor. T-004 (pvemock) is exemplary — every criterion has a dedicated, correctly-skeptical test and
the README walkthrough reproduces exactly against a live server, including 403s, failure injection,
and rollback.

## Criteria summary

| Task | AC1 | AC2 | AC3 | AC4 | Notes |
|---|---|---|---|---|---|
| T-001 scaffolding/Makefile/CI | PASS¹ | PASS | PASS | — | ¹ toolchain drift caveat (F-05) |
| T-002 daemon skeleton | PASS | PASS | PASS | PASS | criterion 1 verified live + piecewise tests; no single e2e test (F-03) |
| T-003 SQLite store | PASS | PASS | PASS | PASS | all 7 data-model tables, rigorous at-rest-encryption test (WAL-checkpointed raw-byte assert) |
| T-004 pvemock | PASS | PASS | PASS | PASS | walkthrough executed live; endpoint surface complete vs card + README |
| T-005 React shell | PASS | PASS | PASS | PASS | WS reconnect test is a true integration test (real server restart, same port) |
| T-006 packaging | PASS / NOT-VERIFIABLE² | PASS | PASS | PASS | ² `systemctl start` half needs a systemd container / hardware (documented in packaging/test/deb-install.sh:11-15 and README) |

Ground rules (all six tasks' code): slog-only logging, `%w`-wrapped errors, app-owned-data-only
schema, approved deps only, zero `any`/non-null assertions in T-005 scope — all clean.

## Findings

All minor; none blocks phase-2 work. Ordered by recommended fix priority.

### F-01 · minor · T-003 — metric_samples prune loop is implemented but never wired into the daemon
`MetricSampleRepo.RunPruneLoop` (`internal/store/metrics.go:130`) exists, matches the run-group
actor contract, and is tested — but nothing outside `internal/store` calls it; `cmd/vnproxd/server.go`'s
run group registers cert-watch, HTTPS, auth renewal, and collectors only, despite metrics.go:129's
comment "the daemon wires it up." A long-running daemon grows `metric_samples` unboundedly once
metrics start flowing.
**Fix:** register the prune loop in `runDaemon`'s run group (one line + repo construction), or
explicitly assign the wiring to the metrics-collector task card.

### F-02 · minor · T-002 — CSP `connect-src` looser than the security doc
`internal/api/middleware.go:66` emits `connect-src 'self' wss: ws:`. Scheme-only sources allow
WebSocket connections to *any* host, and `ws:` additionally allows plaintext — contradicting
docs/security.md's "strict CSP (self-only; no inline script; WS to self)". Same-origin `wss://` is
already permitted by `'self'` in modern browsers. `TestSecurityHeaders` asserts nothing about
`connect-src`, so it slipped through.
**Fix:** `connect-src 'self'` and extend the test to assert no bare `ws:`.

### F-03 · minor · T-004/Makefile — `make dev` claims to start pvemock but doesn't
The recipe (Makefile:47-56) echoes "starting mock PVE, vnproxd, and the Vite dev server" and
docs/development.md promises "backend against pvemock", but only vnproxd + Vite are launched;
vnproxd's PVE collector loops on `dial tcp 127.0.0.1:8006: connection refused` until `make mockpve`
is run separately (observed live).
**Fix:** spawn `cmd/pvemock` inside the `dev` recipe (the `trap 'kill 0'` teardown already exists).

### F-04 · minor · T-006 — packaged binaries are group-writable (0775)
`packaging/Makefile:37-38` builds straight into the package root, inheriting the host umask;
`dpkg-deb --root-owner-group` fixes ownership but not mode, so the shipped .deb contains
`-rwxrwxr-x root:root /usr/bin/vnproxd` and `vnproxctl` (confirmed via `dpkg-deb -c`; `vnprox-setup`
is correctly 0755 because it's `install -m 0755`ed). Group-writable root-owned binaries in /usr/bin
violate Debian policy (lintian would flag).
**Fix:** `chmod 0755` in the packaging Makefile or build to a temp path and `install -m 0755`.

### F-05 · minor · T-001 — toolchain drift vs the "only Go 1.23" criterion and docs
Post-T-001 commit ae3cc81 bumped go.mod to `go 1.25.0` and CI pins Go 1.26.5
(`.github/workflows/ci.yml:19,43`), while the task card, docs/development.md:9, and CLAUDE.md still
say Go 1.23+. A Go-1.23-only host now builds solely via GOTOOLCHAIN auto-download (needs network;
fails air-gapped).
**Fix:** update docs/development.md's stated minimum to match go.mod, or note the
toolchain-download dependency.

### F-06 · minor · T-001 — ci.yml missing legs described in docs/development.md
docs/development.md:64 specifies "`make check` matrix (amd64; arm64 build-only), frontend build,
`make deb` artifact upload" and Go/npm caching. Current ci.yml has `make check` + arm64 build-only;
no production frontend build (`make check` runs vitest/eslint/tsc, not `npm run build`), no
`make deb` artifact upload, no npm cache. The card's literal deliverable ("ci.yml running make
check") is met — this is a doc-contract gap.
**Fix:** add `make build`/`make deb` + artifact-upload steps and `cache: npm`.

### F-07 · minor · T-006 — shipped conffile omits the documented `[storage]` section
docs/deployment.md:63-66's reference `/etc/vnprox/vnprox.toml` shows `[storage] db_path /
session_key_file`; `packaging/config/vnprox.toml` omits the section. Behavior unaffected (code
defaults match the doc paths exactly), but operators reading the shipped file won't discover the knobs.
**Fix:** add the section (commented is fine) to the packaged conffile.

### F-08 · minor · T-002 — acceptance criterion 1 has no automated end-to-end test
No test invokes the daemon with `testdata/dev.toml`, yet dev.toml:4-10's comment references "this
file's own acceptance test". Behavior is verified piecewise (router tests + real-SIGTERM drain test
in `cmd/vnproxd/main_test.go:122`) and was confirmed live during this audit, so this is a
coverage/comment-accuracy gap only.
**Fix:** add an integration test running `runDaemon` against dev.toml on an ephemeral port, or
correct the comment.

### F-09 · info · T-006 — `systemctl start` not regression-protected
The scripted container test deliberately skips the systemd half of AC1 (stock debian:12 lacks
systemd PID 1); a one-off manual verification with `--systemd=always` is recorded in
packaging/README.md:96-103. Honest and documented — but "apt install → systemctl start serves
health" remains **needs hardware/systemd-container validation**.

### F-10 · info · T-005 — first `make build` failed transiently; Node version drift
One `npm ci && npm run build` failure ("Cannot find module .../.bin/tsc", partial node_modules);
clean on retry. Host runs Node 20.19.2 / npm 9.2.0 while docs assume Node 22 (package.json engines
only demands >=20.19.0). Likely environmental; watch for recurrence.

### F-11 · info · T-005 — WS push envelope was an open contract assumption
`web/src/api/ws.ts:9-16` documents that docs/api.md doesn't pin the server-push JSON shape and
assumes flat `{"event": ...}`. T-106 has since built to that shape, but the API doc still doesn't
pin it — close the loop in docs/api.md.

### F-12 · info · cross-cutting — npm audit advisory below the gate
`dompurify <=3.4.10` (moderate, via `monaco-editor`) shows in `npm audit`; `make check` gates at
high so it passes. Track for a monaco bump.

---

# Remediation appendix (2026-07-10)

Every finding was addressed; verification: full `make check` green, `make deb` inspected, live
`make dev` bring-up, `npm audit` clean. Details in `planning/reports/audit-remediation.md`.

| Finding | Outcome |
|---|---|
| F-01 prune loop unwired | **Fixed** — registered in runDaemon's run group (hourly); exercised by the new dev.toml e2e test |
| F-02 loose CSP | **Fixed** — `connect-src 'self'`; TestSecurityHeaders now rejects bare `ws:`/`wss:` sources |
| F-03 `make dev` missing pvemock | **Fixed** — dev recipe spawns cmd/pvemock under the existing trap; verified live (collector converges, no connection-refused spin) |
| F-04 0775 packaged binaries | **Fixed** — chmod 0755 in packaging/Makefile; verified via dpkg-deb -c |
| F-05 toolchain-drift docs | **Fixed** — docs/development.md states Go 1.25+ (go.mod) + GOTOOLCHAIN auto-download caveat |
| F-06 missing CI legs | **Fixed** — npm cache + `package` job (make build + make deb + artifact upload) added to ci.yml |
| F-07 conffile [storage] | **Fixed** — explicit section matching code defaults; verified inside the built .deb |
| F-08 no dev.toml e2e test | **Fixed** — TestRunDaemon_DevConfigServesHealth runs the real runDaemon against dev.toml; dev.toml comment now accurate |
| F-09 systemctl-start unverified | **Open (by nature)** — tracked in planning/reports/needs-hardware-validation.md |
| F-10 transient npm failure | **No change** — environmental; not reproduced |
| F-11 WS envelope unpinned | **Fixed** — docs/api.md pins subscribe semantics (set-replacing) and the flat event envelope with examples |
| F-12 dompurify advisory | **Fixed** — npm override to dompurify 3.4.11; `npm audit`: 0 vulnerabilities; build verified |
