# Phase 0 — Foundations

Goal: a repo where every later task can build, test, run, and package. Contracts (Make targets, layout, fixtures) freeze at the end of this phase.

---

## T-001 · Repo scaffolding, Makefile, CI
**model:** sonnet-5 · **size:** S · **depends:** — · **context:** `docs/development.md`, `docs/architecture.md` §2

**Objective:** Create the repository skeleton exactly per the documented layout, the Make targets contract, and GitHub Actions CI.

**Deliverables:** Go module `github.com/bgovanlu/vnprox`; directory tree per architecture §2 with placeholder `doc.go`/`README` stubs per package; `Makefile` implementing all documented targets (targets whose subjects don't exist yet succeed as no-ops with a notice); `golangci-lint` + eslint + tsconfig configs; `.github/workflows/ci.yml` running `make check` on push/PR; `planning/reports/` directory with `.gitkeep`.

**Acceptance criteria:**
1. Fresh clone + `make check` passes in CI and locally with only Go 1.23 and Node 22 installed.
2. All Make targets from `docs/development.md` exist and exit 0.
3. `go vet ./...` and `tsc --noEmit` run as part of `make lint`.

---

## T-002 · Daemon skeleton
**model:** sonnet-5 · **size:** M · **depends:** T-001 · **context:** `docs/architecture.md` §2 §9, `docs/security.md` (Transport), `docs/deployment.md` (config file)

**Objective:** `vnproxd` starts, loads config, serves HTTPS with security headers, serves an embedded SPA placeholder, shuts down gracefully.

**Deliverables:** `cmd/vnproxd` (flags: `--config`, `--version`); `internal/config` parsing/validating the documented TOML (unknown keys warn); TLS with PVE-cert reuse and explicit-path override, cert hot-reload on SIGHUP + file-watch; `internal/api` router (chi) with middleware stack (request id, slog logging, panic recovery, security headers per `docs/security.md`), `/api/v1/health` endpoint, embed.FS SPA serving with SPA-fallback routing; supervised run-group + graceful shutdown; structured logging throughout.

**Acceptance criteria:**
1. `vnproxd --config testdata/dev.toml` serves `https://localhost:8007/api/v1/health` → `{"status":"ok","version":...}`.
2. Response headers include HSTS, CSP, X-Frame-Options per security doc (asserted in a Go test).
3. SIGTERM exits 0 within 3s with in-flight requests drained (test with a slow handler).
4. Config with invalid `listen` or missing cert paths fails fast with a clear error.

---

## T-003 · SQLite store & migrations
**model:** sonnet-5 · **size:** M · **depends:** T-002 · **context:** `docs/data-model.md` §2

**Objective:** The store layer: schema migrations and typed repositories for all documented tables.

**Deliverables:** `internal/store` with embedded forward-only migrations (`0001_init.sql` implementing every table in the data model doc); WAL mode, foreign keys, busy-timeout; repository types per table with context-first methods; ULID generation; session-secret encryption helpers (AES-256-GCM, key file per security doc); metric_samples pruning job (24h); `kv` schema-version handling with refusal to open a newer-schema DB.

**Acceptance criteria:**
1. Opening a fresh DB creates all tables; reopening is idempotent; a DB with a higher schema version is refused with a clear error.
2. Repository round-trip tests for every table (insert → get → list → update where applicable).
3. Encrypted fields are unreadable in raw DB bytes (test asserts ciphertext ≠ plaintext) and decrypt correctly.
4. Concurrent writers (10 goroutines × 100 inserts) complete without `SQLITE_BUSY` surfacing to callers.

---

## T-004 · Mock PVE server & fixtures
**model:** sonnet-5 · **size:** L · **depends:** T-001 · **context:** `docs/development.md` (mock PVE section — the spec), `docs/architecture.md` §1 §4

**Objective:** The development linchpin: an HTTP server imitating the PVE API surface vnprox uses, driven by YAML cluster fixtures.

**Deliverables:** `internal/pvemock` server implementing: `POST /access/ticket` (+CSRF, bad-password 401, permission model per fixture users), `GET /cluster/status|resources`, `GET/PUT /nodes/{n}/network` with `interfaces.new` staging + reload-task semantics, qemu/lxc config GET/PUT, SDN endpoints (zones/vnets/subnets CRUD + pending/apply + status), firewall endpoints (all three scopes + options/aliases/ipsets/groups), task API with configurable latency/failure injection; fixtures `testdata/clusters/{single-node,three-node-vlan,evpn-lab,messy-brownfield}.yaml` as characterized in `docs/development.md`; `host.Reader` fixture backing (interfaces file content, netlink-equivalent state, lldp JSON, stats) sourced from the same YAML; `make mockpve` target.

**Acceptance criteria:**
1. `make mockpve` + `curl` walkthrough documented in the package README works (ticket → authenticated reads → staged network write → reload task completes).
2. Fixture-declared permissions produce 403s exactly like PVE (verified for at least: non-root read-only user denied a network PUT).
3. All four fixtures load and self-validate (referential integrity check on load).
4. Failure injection: a fixture flag makes `PUT /nodes/{n}/network` reload task fail; state rolls back to pre-staging (mirroring ifupdown2 semantics).

---

## T-005 · React app shell
**model:** sonnet-5 · **size:** M · **depends:** T-001 · **context:** `docs/development.md` (TS standards, stack), `docs/user-guide.md` §6, `docs/api.md` (conventions, auth, WS)

**Objective:** The SPA skeleton every UI task builds inside.

**Deliverables:** Vite + React 18 + TS strict app in `web/`; routing (Topology, SDN, Firewall, IPAM, History, Audit, Tools, Settings — placeholder pages); app layout (nav rail, top bar, theme toggle dark-default); Tailwind 4 + Radix setup with a small shared component set (Button, Dialog, Drawer, Table, Toast, EmptyState); TanStack Query client with the documented error envelope handling; WS client with reconnect/backoff and a subscription API; login page + session bootstrap against `/auth/me` (works against T-002's stub until T-105 lands — feature-flag the real login); keyboard shortcut framework with the documented bindings; Vitest setup with example tests.

**Acceptance criteria:**
1. `make dev` serves the shell; all routes render placeholders; dark/light toggle persists.
2. `tsc --noEmit` and eslint clean under strict settings; example Vitest tests pass.
3. WS client survives server restart (reconnects, resubscribes — integration-tested against a test WS server).
4. Production build embeds into `vnproxd` (`make build`) and serves correctly with SPA fallback.

---

## T-006 · Packaging skeleton
**model:** sonnet-5 · **size:** M · **depends:** T-001 · **context:** `docs/deployment.md`, `docs/security.md` (Host footprint)

**Objective:** Installable artifact from day one: .deb, hardened systemd unit, installer + setup + ctl stubs.

**Deliverables:** `packaging/` with deb build (nfpm or dpkg-deb + `make deb`): binary → `/usr/bin/vnproxd`, unit → systemd dir, default config → `/etc/vnprox/vnprox.toml` (conffile), postinst creating `/var/lib/vnprox` + key generation, prerm/postrm per deployment doc (purge semantics); systemd unit with the documented hardening directives; `install.sh` implementing the documented flow with port-conflict detection (PBS check) — cluster rollout + PVE token steps stubbed with clear TODO markers pointing at T-606; `vnproxctl` Go binary skeleton (`status` talking to the local health endpoint; other subcommands print "available after <task>").

**Acceptance criteria:**
1. `make deb` produces an installable package: in a Debian 12 container, `apt install ./dist/*.deb` → `systemctl start vnprox` serves the health endpoint.
2. `apt remove` keeps config+data; `apt purge` removes them (container test script in `packaging/test/`).
3. Unit file passes `systemd-analyze verify` and includes every hardening directive from the security doc.
4. `install.sh` on a host with a fake listener on 8007 detects the conflict and prompts for an alternative port.
