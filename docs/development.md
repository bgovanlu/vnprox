# Development guide

Standards for all implementation work. `CLAUDE.md` summarizes the rules; this document is the detail.

## Tech stack (locked — see architecture §10)

| Layer | Choice | Version |
|---|---|---|
| Backend | Go | 1.25+ (go.mod: `go 1.25.0`; CI pins a newer patch release) |
| Router | net/http + `chi` | v5 |
| DB | SQLite via `modernc.org/sqlite` (pure Go, no cgo) | — |
| Netlink | `github.com/vishvananda/netlink` | — |
| WS | `nhooyr.io/websocket` | — |
| IDs | ULID (`oklog/ulid`) | — |
| Compression | zstd via `github.com/klauspost/compress/zstd` (pure Go; snapshot blob storage — docs/data-model.md §2's `content_zstd` names the format, and stdlib has no zstd) | — |
| Frontend | React + TypeScript strict + Vite | 18 / 5.x |
| Canvas | `@xyflow/react` + `elkjs` | 12+ |
| Data | TanStack Query v5, zustand | — |
| UI | Tailwind CSS + Radix primitives | 4 / — |
| Charts | `recharts` (inspector sparklines/history) | — |
| Editor | Monaco (raw interfaces editor, lazy-loaded) | — |

Adding any other dependency requires a justification note in the task report. Prefer stdlib.

Toolchain note: the module requires Go 1.25+ (`go 1.25.0` in go.mod). A host with an older `go` binary still builds, but only via `GOTOOLCHAIN` auto-download of a matching toolchain — which needs network access and therefore fails air-gapped. Install Go 1.25+ natively for offline builds.

## Repo layout

As specified in architecture §2. Additional top-level: `Makefile`, `packaging/`, `testdata/` (shared fixtures).

## Make targets (contract — CI and agents rely on these)

```
make build      # vnproxd binary with embedded SPA (runs web build first)
make dev        # backend against pvemock + Vite dev server, hot reload
make test       # go test ./... && vitest run
make lint       # golangci-lint + eslint + tsc --noEmit
make check      # lint + test + govulncheck + npm audit (gated by web/audit-allowlist.json)
make deb        # build the .deb into dist/
make mockpve    # run the mock PVE server standalone on :8006
make ports      # every port this repo's tooling binds, and what is holding them now
```

### Ports

This repo's dev and test tooling binds ~21 ports, and it assumes it has them to itself. Before
adding a bind — a new e2e stack, a packaging test, a mock server — register it in
[`testdata/dev-ports.tsv`](../testdata/dev-ports.tsv); `make check` fails on an unregistered or
double-claimed port. See [`docs/testing/port-registry.md`](testing/port-registry.md) for the policy
and `make ports` for live status with the holding PID.

Why it is enforced rather than documented: five collisions in a single phase, the fourth of which
was the fix for the third (`T-1807-bug-01` → `T-1807-bug-02`). Each one first presented as a
product defect.

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

  **`web/e2e/` runs in CI and is BLOCKING (T-1806-bug-01 and T-2108, both closed 2026-08-07).**
  For three arcs the Playwright suite was run by nothing: no `make` target, no CI job. Task reports
  across those arcs cite passing e2e specs as evidence, and none of those claims had been
  re-checked since the day it was written.

  Turning it on found **29 failures against 59 passes** across 14 spec files. Triaging that backlog
  took the suite to **89 passed / 0 failed / 2 skipped** (the two skips are `microseg`'s own
  documented `test.skip`s), and the `e2e` job is now required like every other.

  What the backlog actually contained is the argument for keeping it that way. It was not 29 stale
  locators: it included `T-1304`'s guest-interior API rejecting **every request a browser ever
  made**, a render loop that left the app unable to navigate away from the Topology page at all,
  an SDN zone wizard that could not be completed, an LLDP trunk check that warned on every
  neighbour while naming none of them, and four WCAG AA contrast defects. Several of those had
  **green unit tests sitting on top of fixtures that invented the shape the code expected** — which
  is the failure mode no amount of unit testing can catch, and the reason this suite exists.

  Three lessons from how these were found, worth carrying into any future spec:

  - A spec that passes because the app *did not* navigate is worse than no spec. `T-2003-bug-01`
    hid behind exactly that — a `getByText` loose enough to match the stale page. Assert on
    headings, and assert the origin's heading is *gone*.
  - A conditional step (`if (await x.isVisible())`) turns a regression into a silent skip. Every
    step in a spec should be unconditional, or the spec is lying about its coverage.
  - A regression spec that does not actually reproduce the bug is not evidence the bug is gone.
    `T-2003-bug-01`'s first regression spec passed against the live defect for two months because
    it exercised the wrong precondition — the one the *report* named, not the one that mattered.
    Reproduce first, then assert.

## CI

> **GitHub Actions is currently unfunded for this repository, so no workflow runs.** The
> `.github/workflows/` definitions below are kept accurate and will run again when funding is
> restored, but **today the gate that matters is `make ci` on a development host** — it runs the
> exact same four jobs (`make check`, the arm64 cross-build, all seven fuzz targets, and the
> package build). Treat a red `make ci` the way you would treat a red pipeline. Do not read the
> absence of a failing check on a commit as evidence that anything passed.
>
> `make e2e` is still deliberately **not** part of `make ci`: it needs a downloaded Chromium and a
> set of free ports (`make ports`), so a developer running `make ci` on a laptop should not pay for
> it. It is green (89 passed / 0 failed / 2 skipped) and is a required job in `ci.yml`; run it
> locally when touching UI flows.

### Workflow definitions (GitHub Actions)

`ci.yml` runs five jobs on every PR and push to `main`:

| Job | What it does | Required? |
|---|---|---|
| `check` | `make check` (gofmt, go vet, golangci-lint, go test, vitest, govulncheck, npm audit — see below) | **Required** |
| `cross-arm64` | `GOOS=linux GOARCH=arm64 go build ./...` — build-only; internal/host's netlink/ioctl code must at least compile for arm64 Proxmox nodes even though the CI fleet is amd64-only | **Required** |
| `fuzz` | Every untrusted-input parser's fuzz target, 60s each (T-604) — see `docs/security-verification.md`'s fuzz inventory | **Required** |
| `package` | `make build` (production frontend) + `make deb`, artifact uploaded | **Required** |
| `e2e` | `make e2e` — Playwright against pvemock + vnproxd + the production SPA. Uploads traces on failure | **Required** (blocking since 2026-08-07, `T-2108`) |

Local equivalents, in the order of decreasing frequency you should run them:

```
make check   # every change — lint, typecheck, 4,058 tests, govulncheck, npm audit
make ci      # before pushing — check + arm64 cross-build + 7 fuzz targets + package
make e2e     # when touching UI flows — Playwright; green, and required in CI
```

`release.yml`: on tag — build, sign .deb, publish to the apt repo, GitHub release with changelog. Keep runtimes <10 min; cache Go/npm.

### Toolchain pinning

Every job pins the same versions so a CI run is reproducible and a local `make check` matches it exactly:

- **Go**: `1.26.5` (`actions/setup-go`'s `go-version`, identical across `ci.yml`, `packaging-matrix.yml`, `release.yml`); `go.mod` floors at `go 1.25.0`.
- **Node**: `22` (`actions/setup-node`'s `node-version`, identical across all workflows); `web/package.json`'s `engines.node` floors at `>=20.19.0`.
- **golangci-lint**: `v2.12.2` (`Makefile`'s `GOLANGCI_LINT_VERSION`, invoked via `go run .../golangci-lint@$(GOLANGCI_LINT_VERSION)` when no local binary is on `PATH` — same version locally and in CI).
- **govulncheck**: `v1.5.0` (`Makefile`'s `GOVULNCHECK_VERSION`, same `go run ...@version` pattern).

To bump any of these: change the one place listed above (Makefile variable or the `go-version`/`node-version` fields — `grep` the repo for the old value to catch every workflow file) and re-run the acceptance check below.

### The `fuzz` job's anchored `-fuzz` regexes

`go test -fuzz=<pattern>` matches every fuzz target *in the package under test* whose name matches `<pattern>` as an **unanchored** regexp, and refuses to fuzz at all if more than one matches ("will not fuzz, -fuzz matches more than one fuzz test"). `internal/host` alone has five fuzz targets (`FuzzParse`, `FuzzParseBGPSummary`, `FuzzParseEVPNVNI`, `FuzzParseLLDP`, `FuzzParseDHCPLeases`); an unanchored `-fuzz=FuzzParse` matched all five and failed the job on every run, independent of the diff. Every `fuzz` job step now anchors its pattern (`-fuzz='^FuzzParse$'`, etc.) — including steps whose package currently has only one match, so a future sibling fuzz target can't silently break it again. When adding a new fuzz target, anchor its CI invocation the same way.

### `npm audit`: allowlisted advisories, not a permanent red X

`make check`'s web step no longer runs bare `npm audit --audit-level=high` — vnprox's transitive dependency tree carries several high-severity advisories with no available non-breaking fix (see `web/audit-allowlist.json`), so that command failed the build on every run regardless of the diff, which is worse than not running it: a permanent red X trains everyone to ignore CI.

Instead, `web/scripts/check-audit-allowlist.mjs` runs `npm audit --json`, extracts every high/critical **root** advisory (a package the advisory names directly, not one merely depending on a vulnerable package), and checks each against `web/audit-allowlist.json`. The build fails if:

- a high/critical advisory is found that **is not** in the allowlist (a genuinely new vulnerability), or
- an allowlisted advisory's `expires` date has passed (forces the entry to be revisited, not accumulate forever).

Each allowlist entry is `{id, package, rationale, expires}` — `id` is the GHSA identifier, `rationale` argues concretely why the advisory doesn't apply to vnprox's usage (or why no non-breaking fix exists yet), `expires` is an ISO date (`YYYY-MM-DD`) after which the entry stops being honored. **To accept a new advisory**: add an entry with a real rationale and a near-term expiry (few months out, not years). **To renew one that's about to expire**: re-confirm the rationale still holds and bump `expires`; don't just push the date out reflexively. **To remove one**: delete the entry once `npm audit` stops reporting it (the script warns — non-fatally — about stale entries it can no longer find, as a nudge to clean up).

The pure matching/expiry logic lives in `web/scripts/auditAllowlist.mjs` and is unit-tested (`web/scripts/auditAllowlist.test.mjs`) against fixture JSON, independent of a real `npm audit` invocation — including the case of an expired entry failing the build.

### Branch protection on `main` — requires repo-admin action

CI being green is only a gate if the platform enforces it. As of this writing `main` has **no branch protection** (`gh api repos/bgovanlu/vnprox/branches/main/protection` returns 404) — any push bypasses required checks entirely. This requires a repo admin to apply; no CI workflow or agent can do it from within the repo. Run:

```sh
gh api --method PUT repos/bgovanlu/vnprox/branches/main/protection \
  --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["check", "cross-arm64", "fuzz", "package"]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "required_approving_review_count": 1
  },
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF
```

This requires all four `ci.yml` job names as required status checks (`strict: true` means the branch must be up to date with `main` before merging, so a required check can't be satisfied by a stale run), forbids force-push and branch deletion, and applies status-check requirements to admins too (`enforce_admins`) so the rule can't be quietly bypassed. Adjust `required_approving_review_count` to the team's actual review process — 1 is a floor, not a recommendation. Verify afterwards with `gh api repos/bgovanlu/vnprox/branches/main/protection` (should return 200, not 404).

### Adding a new required or advisory check

- **Required** (blocks merge): add a step to an existing `ci.yml` job, or a new job whose name is added to `required_status_checks.contexts` above (needs a repeat of the branch-protection call). Keep the job deterministic — see the `fuzz`-job anchoring above for what "independent of the diff" looks like when it goes wrong.
- **Advisory** (visible, doesn't block): add a job without adding its name to `required_status_checks.contexts`. Say so in the job's own comment, so the next person doesn't assume it gates merges.

### `make check` and concurrent worktrees on one machine

`golangci-lint` acquires a file lock on start and, by default, **exits with a hard error** ("parallel golangci-lint is running") if another instance already holds it, instead of waiting. This repo's own orchestration convention (`planning/implementation-plan-proven.md` and prior arcs' plans) explicitly tells the orchestrator to run concurrent tasks in **separate git worktrees**, each running its own `make check` — a sanctioned pattern that collides with golangci-lint's default lock behavior every time two such `make check` runs overlap in wall-clock time, independent of anything in either task's diff (T-1806 found this as a real collision between two sibling agents' worktrees, not a repo test flake).

The fix: `make lint`/`make check` now invoke `golangci-lint run --allow-serial-runners ./...` (both the local-binary and `go run ...@version` code paths in the `lint` target) instead of the default `--allow-parallel-runners=false` behavior. `--allow-serial-runners` makes a second concurrent invocation **wait for the lock and then run**, rather than erroring out immediately — verified locally by launching two `golangci-lint run --allow-serial-runners ./...` invocations against this repo at the same time and confirming both exit 0 (the second's start is simply delayed until the first releases the lock). If you see the old "parallel golangci-lint is running" error, you're on a golangci-lint invocation that bypassed this flag (e.g. calling the binary directly outside `make lint`).

## Definition of done (every task)

1. Acceptance criteria on the task card all pass.
2. `make check` green.
3. Works against the required pvemock fixtures.
4. New API routes/types match `docs/api.md`/`docs/data-model.md` exactly (or the doc was updated in the same change with a flagged note).
5. Report written (per `CLAUDE.md`).
