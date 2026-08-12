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

As specified in architecture §2. Additional top-level: `Makefile`, `packaging/`, `testdata/` (shared
fixtures), `perf/` (T-2506's single budgets file, read by both a Go test and a Playwright spec —
repository-level data rather than either language's, which is why it is not under `internal/` or
`web/`).

## Make targets (contract — CI and agents rely on these)

```
make build      # vnproxd binary with embedded SPA (runs web build first)
make dev        # backend against pvemock + Vite dev server, hot reload
make test       # go test ./... && vitest run
make lint       # golangci-lint + eslint + tsc --noEmit
make check      # lint + test + govulncheck + npm audit (gated by web/audit-allowlist.json)
make deb        # build the .deb into dist/
make mockpve    # run the mock PVE server standalone on :8006
make record     # record a real PVE cluster's API responses into cassettes (T-2502)
make record-mock# re-record the checked-in mock cassettes after a pvemock handler change
make ports      # every port this repo's tooling binds, and what is holding them now
make soak       # resource-leak gate: real daemon + pvemock under seeded churn, fails on trend
make perf       # performance budget gate against perf/budgets.json (also runs inside make check)
```

### The soak / resource-leak gate (`make soak`, T-2504)

`make soak` boots the real `runDaemon` against `pvemock` in-process, drives it with a **seeded**
churn generator (guests created and destroyed, cluster members flapping offline and back, the
`orphan_vnet` finding appearing and clearing, continuous read traffic, draft-changeset
create/validate/discard), and samples goroutines, live heap, RSS, open file descriptors, and
**every** table's row count on a fixed interval.

It **fails on trend, not on threshold**: the verdict is a least-squares slope over the *second half*
of the run (so warm-up is not scored as a leak), and a metric fails only if that slope exceeds its
per-minute tolerance *and* projects a real absolute rise across the observed window. A high-but-flat
value passes. The failure message names the metric — and for a table, names the table.

| Knob | Default | Notes |
|---|---|---|
| `SOAK_DURATION` | `3m` | Nightly: `8h`. A longer local run: `30m`. |
| `SOAK_INTERVAL` | `5s` | Sampling interval. |
| `SOAK_CHURN` | `2s` | Churn interval. |
| `SOAK_SEED` | `0` (clock-derived) | Recorded in the artifact; pass it back to replay the churn sequence. |
| `SOAK_FIXTURE` | `three-node-vlan.yaml` | Any fixture under `testdata/clusters/`. |
| `SOAK_ARTIFACTS` | `var/soak/<timestamp>` | `samples.csv` + `report.json`. |
| `LEAK` | *(unset)* | One of `goroutine`, `table`, `flat` — see below. |

Every run writes `samples.csv` (the full series) and `report.json` (the seed, the config, and a
per-metric verdict), so a failure is diagnosable without a re-run.

**The gate ships with fixtures that make it fire.** `cmd/vnproxd/soakleak.go` is compiled only
under the `soakleak` build tag, which nothing this repo ships, tests, lints, or packages ever sets;
`make soak LEAK=<mode>` adds it. `LEAK=goroutine` leaks one goroutine per PVE collection cycle and
**must fail**, naming `goroutines`. `LEAK=table` grows an unpruned table and **must fail**, naming
`table.soak_leak_unbounded`. `LEAK=flat` allocates 500 goroutines and 64 MiB once at startup and
holds them, and **must pass** — that is the proof the gate measures slope rather than magnitude.
Measured results for all four runs are in [`performance.md`](performance.md) §12.

### The performance budget gate (`make perf`, T-2506)

`docs/performance.md` used to state render and collection budgets that nothing failed on, which
makes them aspirations with a document. The budgets now live in **one machine-readable file**,
[`perf/budgets.json`](../perf/budgets.json), read by both measurement sites:

| Site | Reads it via | Runs in | Budgets |
|---|---|---|---|
| `internal/collect/sim_bench_test.go` | `internal/perfbudget` | `make check`, `make perf` | `sim.*` |
| `web/e2e/scale.spec.ts` | `web/perf/budgets.ts` | `make e2e` | `topology.scale.*` |

Three properties are worth knowing before you touch it:

- **It compares against the budget, never against the previous run.** A drift that never regresses
  5% in one step still fails once it crosses the line. `perfbudget.Evaluate` is not given a previous
  run at all, so there is nothing a step-over-step comparison could be made against.
- **Every verdict is a median of N**, with N declared per budget in the file (5 for the Go budgets,
  3 for the browser ones). A gating budget may not declare fewer than 3 — `Validate` rejects the
  file. One slow sample is noise and does not move a median.
- **Every budget's headroom is printed on every run, passing ones included**, and a budget that
  stops being measured is a failure (`perfbudget.Missing`) rather than a silent pass.

**A budget is host-relative, and the file says so.** `T-2505-input-02` is the evidence: the same
commit passes here and fails on a hosted runner with no code difference, because this box is 32-core
and the runner is 2–4. So each budget declares its normalisation — `calibrated` (a fixed Go CPU
kernel measured on the machine doing the measuring, against the reference host's 42.3 ms),
`cores` (T-2505's own `availableParallelism` ladder, shared with `web/playwright.config.ts`'s
`slowFactor`), or `absolute` (which **may not gate**; `Validate` refuses). Both factors are floored
at 1.0, so **normalisation only ever loosens**: the documented limit is the tightest the gate ever
gets, and a slower machine trades sensitivity for not producing false failures. See
[`performance.md`](performance.md) §13.2 for the full consequence, and §13.8 for what this costs.

**The document and the file cannot disagree.** `performance.md` §13.3 carries the budget table
between `<!-- perf-budgets:begin -->` markers, and `internal/perfbudget`'s
`TestDocTableMatchesBudgets` compares it against `perf/budgets.json` on every `make check`, in both
directions — an edit to either side alone reddens the tree. **To change a budget: edit
`perf/budgets.json` (including its `why`, which must say where the number came from) and the table
in §13.3 together.**

**The gate ships with fixtures that make it fire**, the same arrangement as `make soak LEAK=`:
`internal/sim/perfslow_on.go` puts real extra work inside `Engine.Simulate` and is compiled only
under the `perfslow` build tag, which nothing this repo ships, tests, lints or packages ever sets.

```
make perf                      measure and gate; prints the headroom table
make perf PERF_SLOW=always     every sample slowed — must FAIL, naming sim.simulate_10k_wall_ms
make perf PERF_SLOW=outlier    one of five samples slowed — must PASS
```

Measured results for all three are in [`performance.md`](performance.md) §13.7.

### Ports

This repo's dev and test tooling binds ~21 ports, and it assumes it has them to itself. Before
adding a bind — a new e2e stack, a packaging test, a mock server — register it in
[`testdata/dev-ports.tsv`](../testdata/dev-ports.tsv); `make check` fails on an unregistered or
double-claimed port. See [`docs/testing/port-registry.md`](testing/port-registry.md) for the policy
and `make ports` for live status with the holding PID.

Why it is enforced rather than documented: five collisions in a single phase, the fourth of which
was the fix for the third (`T-1807-bug-01` → `T-1807-bug-02`). Each one first presented as a
product defect.

`T-2505` added six rows (`21006`-`23007`): the e2e suite's default three-node-vlan stack now has one
copy per shard. Those ports are written as literals in `web/e2e/shards.ts` precisely so the scan can
see them — computing them as `base + shard * stride` would have hidden every shard's binds from the
registry, which is the failure mode the registry exists to prevent.

### The e2e suite runs in shards (`T-2505`)

`web/e2e/shards.json` says which spec files run in which of the four shards; `web/e2e/shards.ts`
turns that into ports, generated daemon configs and Playwright `webServer` entries. Each shard is a
**separate Playwright process with its own pvemock/vnproxd stacks, SQLite store and interfaces
sandbox**, so the suite's wall clock is the slowest shard rather than the sum of every spec, and a
spec that corrupts global state can only corrupt its own shard's.

```
make e2e                          all four shards concurrently, then the gate
make e2e-whole                    the pre-T-2505 arrangement: one process on 8006/8007
make e2e-trend                    per-test flake rate from recorded run history
scripts/e2e-shards.sh shard-2     one shard
E2E_ARGS="--repeat-each=2" scripts/e2e-shards.sh
```

**The shards' exit codes are not the verdict.** `cmd/e2egate` reads every shard's JSON report and
decides: an unquarantined failure fails the build, an **expired** quarantine fails the build whether
or not its test failed, a shard that produced no report at all fails the build, and every run is
appended to `var/e2e-runs/runs.jsonl` so the flake trend is computed rather than curated.

**Quarantine** lives in `web/e2e/quarantine.json`. An entry needs the file, the test's full title, a
reason of at least 20 characters, a ticket, and an expiry no more than 42 days out.
`internal/e2egate`'s `TestRepoQuarantineIsValid` re-checks that file against the real clock on every
`make check`, so an entry that quietly expires reddens the tree without anyone running the e2e suite.

**Adding a spec:** put its file name in exactly one shard in `shards.json`, and if it needs a
fixture other than three-node-vlan, name that stack in `SPEC_STACKS`. A spec file in no shard is a
hard error at config load — a spec that silently stops running is what `T-2108` spent an arc
recovering from, and the check caught exactly that mistake while this manifest was being written.

**Local four-up is not the CI arrangement.** Four concurrent shards is a 32-core developer machine's
setting; `ci.yml` runs **one shard per runner** across a four-leg matrix, with a required `e2e-gate`
job that collects the reports. See `T-2505-input-02` for why that distinction is load-bearing.

## The mock PVE server (`internal/pvemock`)

The single most important dev asset: an HTTP server faithfully imitating the PVE API surface vnprox uses (`/access/ticket`, `/cluster/*`, `/nodes/*/network`, `/nodes/*/qemu|lxc`, SDN + firewall endpoints, task polling), driven by YAML fixture files in `testdata/clusters/` — e.g. `single-node.yaml`, `three-node-vlan.yaml`, `evpn-lab.yaml`, `messy-brownfield.yaml` (drift, conflicts, stale configs on purpose). It simulates: ticket auth + CSRF, permission-dependent 403s, task lifecycle with delays, `interfaces.new` staging semantics, and SDN pending/apply state. Host-level reads (`internal/host`) are interfaced so fixtures can back them too (`host.Reader` implementations: `real` and `fixture`).

Every feature must work against at least `single-node.yaml` and `three-node-vlan.yaml` before it is done.

### Record/replay (`T-2502`)

Every fixture above is a **guess**. `T-2108` found four defects sitting under green unit tests
whose fixtures invented the shape the code expected — the structural consequence of hand-writing
each one. Record mode is the alternative:

```
make record PVE_URL=https://pve1.lab:8006 PVE_VERSION=8.3.5 \
            PVE_TOKEN='vnprox@pve!daemon=<uuid>' PVE_NODE=pve1 [PVE_INSECURE=1]
```

`VNPROX_PVE_RECORD=<dir>` (plus `VNPROX_PVE_VERSION`) on any `pve.Client` writes each
request/response pair as a **cassette** into `<dir>/<pve-version>/`: method, path, normalised
query, status, and the response body verbatim. Cassettes land in
`internal/pvemock/testdata/cassettes/<pve-version>/`; see that directory's README.

Three properties are not negotiable, and each has a test that makes it fire:

- **Refusal, not redaction.** A response body containing a PVE ticket, a `password` field or a
  private key **fails the write** and names the field — via the same redactor
  (`internal/redact`) `T-1902`'s support bundle uses. A cassette with a hole where a ticket used
  to be no longer describes what PVE returned, which is the only thing it has over a
  hand-written fixture. Consequence: **record with an API token, never a password** — a
  ticket-auth client's first call is a login, and its response body is a credential.
- **No fallback on replay.** `pvemock.NewReplayServer(dir)` serves cassettes and nothing else. An
  unmatched request returns HTTP 599 with `ErrNoCassette` and, with `WithUnmatchedFailer(t)`,
  fails the test on the spot. A fixture that passes only because the mock invented an answer
  cannot be written.
- **Request headers and bodies are never recorded.** Not redacted — never collected.

`internal/pvecassette.Drift` compares a fixture-driven run against a cassette set and reports
every field present in one and absent in the other; `TestFixtureCassetteDrift` runs it and logs
the outcome either way.

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

> **Status, 2026-08-11: Actions billing is exhausted; treat the hosted pipeline as unavailable and
> run `scripts/ci-local.sh` instead.** Note carefully what this does *not* mean — as of this
> writing runs still appear in `gh run list` and still complete. Do not infer from a green or red
> hosted run that the pipeline is funded, and do not infer from "unfunded" that nothing ran.
> **Check, don't assume, in both directions** — that is the durable lesson from the correction
> below, and it survives the funding status changing again.
>
> **The 2026-08-08 (T-2410) correction this replaces, kept because its lesson still applies:** this
> note once said Actions was unfunded and that no workflow ran. That was wrong, and it was
> expensive — `T-1806-bug-02` sat unexplained for two days with "reproduce under runner-like
> conditions" as its next step, while the runner's own log, one `gh api .../jobs/<id>/logs` call
> away, contained the answer. Before writing off a CI signal as absent, run `gh run list`.
>
> **`scripts/ci-local.sh` is now the gate that matters.** It reproduces every job in both
> workflows — `check`, `e2e`, `cross-arm64`, `fuzz`, `package`, the 10-leg `packaging-matrix`, and
> `cluster-ssh` — pins Node to the major the workflows pin (via nvm; it *fails* rather than
> silently using the system Node), refuses a dirty tree unless `ALLOW_DIRTY=1`, and prints one
> line per job. `make ci` remains the fast container-free subset for a working tree.
>
> ```
> scripts/ci-local.sh                # every job
> scripts/ci-local.sh check e2e      # only the named jobs
> FUZZTIME=10s scripts/ci-local.sh fuzz
> ```
>
> **Run the jobs sequentially, and do not run anything else heavy alongside them.** The runner does
> this by default for a reason: on 2026-08-11 a `go test ./internal/api/` issued concurrently with
> the `fuzz` job killed a fuzz worker outright — "fuzzing process hung or terminated unexpectedly
> while minimizing: EOF" — and Go saved the input it happened to be holding as a corpus entry. That
> input did not reproduce any failure; the *process* died, not the parser. Run alone, `fuzz` passes
> in ~500s. A fuzz failure of that shape is a resource verdict on the host, not a finding about the
> code, and the saved corpus entry should be deleted rather than committed.
>
> **A local green is not a hosted green.** On commit `4968bf3` the hosted `e2e` job failed two
> specs that pass on this host (see `T-2505-input-02` in `planning/tasks/phase-25.md`). This box is
> 32-core; a hosted runner is 2–4. Any timing- or budget-based criterion measured here is
> host-relative and will not predict the pipeline.
>
> `make e2e` is still deliberately **not** part of `make ci`: it needs a downloaded Chromium and a
> set of free ports (`make ports`), so a developer running `make ci` on a laptop should not pay for
> it. Since `T-2505` it runs four shards concurrently — **90 passed / 0 failed / 1
> quarantined-failing / 2 skipped in 5.5 min** on this host, against the 9.9-min serial baseline the
> same day — and the `e2e-gate` job is what is required in `ci.yml`. Run it locally when touching UI
> flows.

### Workflow definitions (GitHub Actions)

`ci.yml` runs five jobs on every PR and push to `main`:

| Job | What it does | Required? |
|---|---|---|
| `check` | `make check` (gofmt, go vet, golangci-lint, go test, vitest, govulncheck, npm audit — see below) | **Required** |
| `cross-arm64` | `GOOS=linux GOARCH=arm64 go build ./...` — build-only; internal/host's netlink/ioctl code must at least compile for arm64 Proxmox nodes even though the CI fleet is amd64-only | **Required** |
| `fuzz` | Every untrusted-input parser's fuzz target, 60s each (T-604) — see `docs/security-verification.md`'s fuzz inventory | **Required** |
| `package` | `make build` (production frontend) + `make deb`, artifact uploaded | **Required** |
| `e2e` | **T-2505:** a four-leg matrix, one shard per runner. Uploads each shard's report (and traces on failure). A leg exits 0 even when its tests failed — it is not the verdict | Not required on its own |
| `e2e-gate` | `cmd/e2egate` over all four reports: unquarantined failures, expired quarantines and missing shard reports each fail it | **Required** (the blocking successor to `e2e`, `T-2108` → `T-2505`) |

Local equivalents, in the order of decreasing frequency you should run them:

```
make check   # every change — lint, typecheck, 4,058 tests, govulncheck, npm audit
make ci      # before pushing — check + arm64 cross-build + 7 fuzz targets + package
make e2e     # when touching UI flows — Playwright, sharded; the gate decides pass/fail
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
