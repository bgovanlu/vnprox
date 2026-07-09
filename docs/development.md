# Development guide

Standards for all implementation work. `CLAUDE.md` summarizes the rules; this document is the detail.

## Tech stack (locked — see architecture §10)

| Layer | Choice | Version |
|---|---|---|
| Backend | Go | 1.23+ |
| Router | net/http + `chi` | v5 |
| DB | SQLite via `modernc.org/sqlite` (pure Go, no cgo) | — |
| Netlink | `github.com/vishvananda/netlink` | — |
| WS | `nhooyr.io/websocket` | — |
| IDs | ULID (`oklog/ulid`) | — |
| Frontend | React + TypeScript strict + Vite | 18 / 5.x |
| Canvas | `@xyflow/react` + `elkjs` | 12+ |
| Data | TanStack Query v5, zustand | — |
| UI | Tailwind CSS + Radix primitives | 4 / — |
| Charts | `recharts` (inspector sparklines/history) | — |
| Editor | Monaco (raw interfaces editor, lazy-loaded) | — |

Adding any other dependency requires a justification note in the task report. Prefer stdlib.

## Repo layout

As specified in architecture §2. Additional top-level: `Makefile`, `packaging/`, `testdata/` (shared fixtures).

## Make targets (contract — CI and agents rely on these)

```
make build      # vnproxd binary with embedded SPA (runs web build first)
make dev        # backend against pvemock + Vite dev server, hot reload
make test       # go test ./... && vitest run
make lint       # golangci-lint + eslint + tsc --noEmit
make check      # lint + test + govulncheck + npm audit --audit-level=high
make deb        # build the .deb into dist/
make mockpve    # run the mock PVE server standalone on :8006
```

## The mock PVE server (`internal/pvemock`)

The single most important dev asset: an HTTP server faithfully imitating the PVE API surface vnprox uses (`/access/ticket`, `/cluster/*`, `/nodes/*/network`, `/nodes/*/qemu|lxc`, SDN + firewall endpoints, task polling), driven by YAML fixture files in `testdata/clusters/` — e.g. `single-node.yaml`, `three-node-vlan.yaml`, `evpn-lab.yaml`, `messy-brownfield.yaml` (drift, conflicts, stale configs on purpose). It simulates: ticket auth + CSRF, permission-dependent 403s, task lifecycle with delays, `interfaces.new` staging semantics, and SDN pending/apply state. Host-level reads (`internal/host`) are interfaced so fixtures can back them too (`host.Reader` implementations: `real` and `fixture`).

Every feature must work against at least `single-node.yaml` and `three-node-vlan.yaml` before it is done.

## Go standards

- `golangci-lint` config in repo; no lint suppressions without a comment explaining why.
- Table-driven tests; `internal/change` and `internal/sim` target ≥90% coverage (they are the safety/truth cores), elsewhere be reasonable.
- Context-first APIs (`func (c *Client) Nodes(ctx context.Context) ...`); all I/O cancellable.
- Errors wrapped with operation context; sentinel errors in each package's `errors.go`.
- No global state except the wired-in-main dependency graph. Interfaces defined where consumed.
- Concurrency: collectors and timers use a supervised run-group; every goroutine has an owner and a shutdown path. Rollback timers must survive daemon restart (persisted deadline in `changesets.confirm_deadline`, re-armed on startup — test this).

## TypeScript standards

- `strict: true`, `noUncheckedIndexedAccess: true`; no `any`, no non-null assertions without a comment.
- API types generated from a single `web/src/api/types.ts` that mirrors `docs/api.md` (hand-maintained in v1; a generation task exists in P6 backlog).
- Components: function components only; server state via TanStack Query only (no fetch in components); canvas state in zustand.
- Testing: Vitest + Testing Library on logic-bearing components (drawer, validators display, simulator result rendering); Playwright smoke suite (P6) against `make dev` + mock PVE.

## CI (GitHub Actions)

`ci.yml`: on PR/push — `make check` matrix (amd64; arm64 build-only), frontend build, `make deb` artifact upload. `release.yml`: on tag — build, sign .deb, publish to the apt repo, GitHub release with changelog. Keep runtimes <10 min; cache Go/npm.

## Definition of done (every task)

1. Acceptance criteria on the task card all pass.
2. `make check` green.
3. Works against the required pvemock fixtures.
4. New API routes/types match `docs/api.md`/`docs/data-model.md` exactly (or the doc was updated in the same change with a flagged note).
5. Report written (per `CLAUDE.md`).
