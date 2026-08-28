# Architecture tour, for a stranger

You have never seen this repository before. `docs/architecture.md` is the authoritative technical
design — dense, and written for someone who already knows the shape of the system. This page is
the on-ramp: get something running in one command, then walk the one request that matters most
(a network change, browser to Proxmox and back) so the rest of the codebase has somewhere to hang.

No Proxmox cluster required. vnprox develops against `internal/pvemock`, a fixture-driven mock of
the Proxmox VE API, and `cmd/pvemock` runs it standalone. `make dev` wires the mock server,
`vnproxd`, and the Vite dev server together; that combination — never a real PVE node — is what
`make check`, and every test in this repository, actually run against.

## 1. Get running

Two prerequisites, both pinned so your build matches what CI would run if it were funded (see
"A note on CI" in `CONTRIBUTING.md`):

- **Go**, at the version `scripts/lib/versions.sh` names as `GO_VERSION_EXPECTED` (currently in that
  file — don't copy the number here, it drifts; that file is the single source of truth). A newer
  local `go` still builds via `GOTOOLCHAIN` auto-download, but that needs network access.
- **Node 22**, via [nvm](https://github.com/nvm-sh/nvm) — not your system Node, if you have one.
  `scripts/lib/versions.sh`'s `NODE_MAJOR` names the pinned major the same way.

From a clean clone, this is the one command (copy the whole block — it's `&&`-chained on purpose,
so it stops at the first real problem instead of ploughing past it):

```bash
. scripts/lib/versions.sh && \
  { command -v nvm >/dev/null 2>&1 || { export NVM_DIR="$HOME/.nvm"; [ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"; }; } && \
  (nvm ls "$NODE_MAJOR" >/dev/null 2>&1 || nvm install "$NODE_MAJOR") && \
  nvm use --delete-prefix "$NODE_MAJOR" && \
  go mod download && \
  (cd web && npm ci) && \
  make dev
```

What each piece does, and what to expect:

| Step | What it does | What "it worked" looks like |
|---|---|---|
| `. scripts/lib/versions.sh` | Loads `GO_VERSION_EXPECTED`/`NODE_MAJOR` so nothing below hardcodes a second copy | No output |
| nvm bootstrap | Loads nvm if installed; if `nvm` truly isn't on the machine at all, install it first — [nvm's own install script](https://github.com/nvm-sh/nvm#installing-and-updating) — then re-run this block | — |
| `nvm install`/`nvm use` | Gets Node 22 active in this shell | `node --version` reports `v22.x.x` |
| `go mod download` | Populates the Go module cache | No output on success |
| `npm ci` (in `web/`) | Installs the frontend's exact locked dependencies | `added N packages` |
| `make dev` | Starts `cmd/pvemock` on `:8006`, `vnproxd` on `:8007` against `testdata/dev.toml`, and the Vite dev server on `:5173` | Vite prints `ready in`, and `curl -sk https://127.0.0.1:8007/api/v1/health` returns `{"status":"ok",...}` |

Then open **http://localhost:5173** — the Vite dev server, which is where the UI lives in dev mode
(`vnproxd` itself only serves the built SPA when one exists, i.e. after `make build`; `make dev`
runs `go run ./cmd/vnproxd` with no build step). `web/vite.config.ts` proxies `/api` (and the
WebSocket) straight through to `https://127.0.0.1:8007`, so the browser only ever talks to one
origin. Log in with `root` / `vnprox-mock` / realm `pam` — the mock fixture's built-in superuser,
the same credential `docs/features/demo-mode.md` documents for `vnproxd --demo`. `Ctrl-C` stops all
three processes; `make dev`'s own `trap 'kill 0' EXIT` is what makes one `Ctrl-C` enough.

**This was run end to end while writing this page** (T-3810), in a throwaway `git worktree` off a
clean checkout: `go mod download` (cache already warm) and `npm ci` (373 packages, ~15s) both
succeeded; Vite reported "ready" within about a second of `make dev` starting, and `vnproxd`'s
`/api/v1/health` returned `200 {"status":"ok",...}` within about ten seconds (it does more startup
work — collectors, key generation, findings' first pass — before it opens its listener). If any
step above behaves differently for you, that's a real bug in this page — say so.

Two things that trip people up:

- **`make dev` doesn't run `npm ci` for you.** It assumes `web/node_modules` already exists (it
  runs `npm run dev` directly). Skip the `npm ci` step once and the error is a Vite module-not-found,
  not an obviously-missing-dependencies message.
- **Ports 8006/8007/5173 need to be free.** If another `make dev` (yours or, on a shared box,
  someone else's) is already running, this one won't start cleanly. `make ports` shows what's
  holding what.

If you'd rather see a built, non-hot-reloading product with zero setup at all — not the dev loop
above, but useful for a first look — `vnproxd --demo` (after `make build`) runs the whole product
against a synthetic cluster baked into the binary. See `docs/features/demo-mode.md`.

## 2. One request, start to finish

The request that defines this product: a user drags a NIC into a bond, and vnprox changes real
Proxmox network config safely. Following it end to end touches most of the architecture.

```
Browser (React SPA, web/src/)
  │  1. User edits the topology canvas. Nothing is sent yet — the edit becomes
  │     an Op in the in-memory change drawer (web/src/changesets/).
  │
  │  2. "Review & apply" — the SPA POSTs the ops to the daemon.
  ▼
internal/api/  (HTTP router + handlers, docs/api.md is the exact contract)
  │  3. Handler hands the ops to internal/change — this is the ONLY path
  │     into it. Nothing else in this codebase is allowed to touch Proxmox
  │     network config; see "Invariant 1" below.
  ▼
internal/change/  (the change engine: stage → validate → diff → apply → confirm/rollback)
  │  4. STAGE   — ops become a draft Changeset (internal/change/changeset.go),
  │               persisted to vnprox's own SQLite store (app-owned data —
  │               see "Invariant 2" below).
  │  5. VALIDATE — internal/change/validate*.go runs a layered pipeline:
  │               schema → referential/projection → safety → advisory,
  │               against an internal/inventory.Snapshot (the normalized
  │               graph built from what internal/collect last read from
  │               Proxmox — never vnprox's own SQLite, which holds no shadow
  │               copy of network config to validate against).
  │  6. DIFF     — the applier computes the exact per-node file/API diff
  │               this changeset would produce, which is what "Review &
  │               apply" showed the user before they clicked apply.
  │  7. APPLY    — internal/pve (the PVE API client) and internal/host
  │               (direct netlink / /etc/network/interfaces writes for what
  │               the PVE API doesn't cover) make the real change. A
  │               pre-apply snapshot is written first, so rollback has
  │               something to restore.
  ▼
internal/pve/ + internal/host/  →  the mock PVE server in dev (internal/pvemock),
                                    a real pveproxy + this node's kernel in production
  │
  │  8. CONFIRM/ROLLBACK — the daemon starts a countdown (confirm_timeout_default
  │     in config). If the user's session doesn't confirm connectivity before
  │     the deadline (persisted in changesets.confirm_deadline, re-armed on
  │     daemon restart — it survives a crash mid-window), internal/change
  │     rolls back to the pre-apply snapshot automatically. No action from
  │     the user is required for a bad change to undo itself.
  ▼
Back to the browser via the WS hub (internal/api's WS layer) — changeset.status
broadcasts push the new state to every connected client watching this changeset,
not just the one that submitted it.
```

Two packages worth knowing before you go further because nearly everything else in `internal/`
feeds one of them: **`internal/inventory`** is the normalized model of "what does this cluster's
network actually look like right now" (built by `internal/collect` polling Proxmox and this node's
own host state); **`internal/change`** is the only thing allowed to turn a proposed edit into a
real one. `docs/architecture.md` §2's component diagram is the full picture once these two anchor
it for you.

## 3. `internal/` vs `web/src/`: what lives where

`internal/` is the Go backend — one process (`vnproxd`) that runs identically on every node in the
cluster. Read `docs/architecture.md` §2's package table for the full list; the packages you'll touch
most as a first-time contributor:

- **`internal/api/`** — HTTP routes and handlers. If you're adding a new field to an API response,
  this is usually where the wiring lives (plus `docs/api.md`, which the response shape must match
  exactly).
- **`internal/change/`** — the change engine (§2 above). Touch this only with real care; it carries
  the product's core safety guarantee, and `docs/development.md` targets ≥90% test coverage here
  for exactly that reason.
- **`internal/pvemock/`** — the mock PVE server every test and `make dev` runs against. Fixtures
  live in `testdata/clusters/` as YAML.
  `docs/development.md` §"The mock PVE server" is the detail.
- **`internal/doctor/`** — `vnproxctl doctor`'s preflight checks. Small, self-contained, table-driven
  — a good first PR (see `docs/first-change.md`, which builds one).
- **`internal/findings/`** — the unified health-findings stream (drift, LLDP, certs, HA, and more) —
  another good place to look for a first-change-shaped task once you've done the tutorial.

`web/src/` is the React + TypeScript SPA (Vite, strict TS, TanStack Query for server state, zustand
for canvas state — `docs/development.md` §"Tech stack" has the full list). It talks to `internal/api`
over `/api/v1` and a WebSocket, never directly to Proxmox. Top-level folders are mostly one per
feature area (`changesets/`, `sdn/`, `ipam/`, `firewall/`, `topology/`, and so on); `help/` is the
online-help system every new screen/panel must register with — see `docs/development.md`
§"Online help is part of the change, not a follow-up".

## 4. The invariants that will bite you if you don't know them

These are `CLAUDE.md`'s ground rules, restated with the "why" a newcomer needs and not just the
rule. Full reasoning for each is in the linked ADR — read the ADR rather than taking this page's
word for it.

1. **The change engine is the only mutation path.**
   `internal/change/` is the sole way anything in this codebase writes to Proxmox network config.
   There is no second code path, no "just this once" direct `pve.Client` write from a handler. See
   [ADR 0004](adr/0004-change-engine-is-the-sole-mutation-path.md).

2. **Proxmox is the source of truth; vnprox's SQLite store is app-owned only.**
   vnprox never persists a shadow copy of PVE config as if it were authoritative — the store holds
   sessions, changesets, snapshots, audit, and layout, not a cached "what does the network look
   like" that could drift from reality. When you need "what does the network look like right now,"
   the answer comes from `internal/inventory`, built fresh from a poll, never from the store. See
   [ADR 0005](adr/0005-proxmox-is-source-of-truth-sqlite-is-app-owned-only.md).

3. **Everything is cluster-aware.** vnprox runs on every node as a symmetric peer — there is no
   elected leader, and any node's UI can view and manage the whole cluster (`internal/peer/` is how
   one node reaches another for node-local data like LLDP or live stats). A feature that reads or
   writes node state has to work when that node is a peer being reached from elsewhere, not only
   when it's the node you're browsing. See
   [ADR 0006](adr/0006-peerless-symmetric-cluster-no-elected-leader.md).

The full ADR set (`docs/adr/`) covers the rest of the load-bearing decisions — language/framework
choice (0001, 0002), how PVE API writes are authenticated (0003), the default port (0007), SDN scope
(0008), supported PVE versions (0009), and the two frozen-API decisions (0010, 0011). Skim
`docs/adr/README.md` for the index; read one in full the first time a task touches the area it
covers.

## Next

Ready to make an actual change: `docs/first-change.md` walks a small, real one from an empty diff
to a green `make check`.
