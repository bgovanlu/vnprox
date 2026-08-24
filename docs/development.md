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
| Icons | `lucide-react` (Phase 34 — per-icon named imports only, never the barrel) | — |
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

### The vitest suite has the same flake visibility (`T-3708`)

`cmd/e2egate` (above) gives the e2e suite a quarantine with hard expiries and a flake trend computed
from run history. The vitest suite — 2,281 tests as of this writing, which gates **every push**
through the pre-push `make ci` hook — had none of it until `T-3708`. The gap showed up for real:
`TenantsPanel.test.tsx` timed out on a `findByRole` under `make ci`'s concurrent load, refused a
push, then passed 3/3 alone and 295/295 in-suite immediately after. Nothing recorded that the test
was load-sensitive rather than broken (fixed in `2cd48367` — see the comments in
`web/src/test/setup.ts` and `web/vite.config.ts`'s `testTimeout` for the two-part fix and why the
second part was needed), so the next occurrence would have been diagnosed from scratch.

`cmd/vitestgate` closes that gap **using the same mechanism**, not a second one:
`internal/vitestgate` parses vitest's own JSON reporter output into `internal/e2egate`'s
`Outcome`/`ShardReport` shape (vitest is not sharded — one process, one report, so the whole run
becomes a single `e2egate.ShardReport`) and hands it to `e2egate.Evaluate`, `e2egate.Trend` and the
quarantine logic unmodified, imported rather than reimplemented. Only the presentation strings
differ (`vitestgate: PASS`, not `e2e gate: PASS`) — reusing `Verdict.Summary()`'s exact text would
have made a vitest failure print as if it were an e2e one.

```
make test                          go test ./... && vitest run, gated by vitestgate
make vitest-trend                  per-test flake rate over the recorded vitest run history
go run ./cmd/vitestgate gate       what `make test` actually runs after vitest
```

**vitest's own exit code is not the verdict**, the same relationship the e2e shards have to
`cmd/e2egate`: `make test` runs `npm run test` (vitest writes `web/test-results/vitest.json` via the
`json` reporter configured in `web/vite.config.ts`, alongside the human-readable `default` reporter)
and does **not** abort on a nonzero exit — `cmd/vitestgate gate` reads that report and decides pass
or fail: an unquarantined failure fails the build, an **expired** quarantine fails the build whether
or not its test failed, and every run is appended to `var/vitest-runs/runs.jsonl` (gitignored, like
`var/e2e-runs/`) so the flake trend is computed rather than curated.

**Quarantine** lives in `cmd/vitestgate/quarantine.json` — not under `web/`, so that this mechanism's
own config travels with the tool that reads it. Same rules as the e2e quarantine: file, the test's
full title (describe ancestry joined by `internal/e2egate.TitleSeparator`, `" › "`), a reason of at
least 20 characters, a ticket, an expiry no more than 42 days out.
`internal/vitestgate`'s `TestRepoQuarantineIsValid` re-checks that file against the real clock on
every `make check`. It ships seeded with the `TenantsPanel.test.tsx` entry from the incident above —
not because the test is currently flaky (the underlying fix landed in `2cd48367`), but so the record
starts non-empty and the hard-expiry mechanism has something real to expire: it is due `2026-09-20`,
at which point it fails the build until someone re-triages it.

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

## Validating against real PVE (T-3704)

`internal/pvemock` fixtures are a guess until checked against a real node (see record/replay,
above). There are two real things to check against, and they answer different questions —
picking the wrong one either risks the user's live cluster or wastes an afternoon building a lab
for something the real cluster already answers.

### `vnprox-dev` — the real cluster, for almost everything

pvecube (192.168.1.9, root SSH) and pve001 (192.168.1.7, **no SSH credentials, no authorisation to
modify it**) have been a quorate two-node corosync cluster named `vnprox-dev` since 2026-08-18.
This was not known for five days after it happened — `CLAUDE.md` said "one real node, no cluster"
the whole time, and an audit filed 156 validation items and classified 17 features "unprovable" on
that stale premise. See `planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt` for the
`pvecm status` transcript that corrected it, and `CLAUDE.md`'s "Real PVE access" section for the
rule that follows from it: **if a limit here blocks you, verify it on the node before you accept
it.**

Use `vnprox-dev` directly, read-only, for: cross-node validation, peer round-trips and fan-out,
drift between nodes, mixed-version peering (pve001 runs an older vnproxd than pvecube — this is
live, not simulated), and per-node API divergence (the two nodes disagreed on `/nodes/{node}/sdn/zones`
the first time anyone checked — see the transcript above). `pvesh get /nodes/pve001/...` and
`pvesh get /cluster/resources` reach pve001 through pvecube's own API; that is enough for most
validation without ever touching pve001 directly.

**It is the user's live cluster, and pve001 is not ours to change.** Two rules, not one each:
- **Read-only unless a task explicitly says otherwise.** A mutating `pvesh` call against pvecube is
  a change to real network config, outside the change engine.
- **Never anything on pve001 requiring root.** We have no SSH credentials for it and no
  authorisation to modify it — it is observable through pvecube's `pvesh`, and reachable as a
  vnprox peer, and that is the extent of it.

### The disposable lab — only for what `vnprox-dev` must never be used for

`scripts/pve-lab.sh` builds and tears down two nested PVE 9.2 guests on pvecube (`pve-lab-1`,
`pve-lab-2` — 2 vCPU / 4 GiB / 32 GiB each, clustered **with each other**, on an isolated bridge
with no physical uplink), for the destructive subset that cannot be run against `vnprox-dev`:
partition behaviour, quorum loss, killing a node mid-rollback, deliberately corrupting drift. It
is a fixture, not an environment — teardown is as scripted as build and safe to run twice.

```
scripts/pve-lab.sh up       # preflight (capacity, VMIDs, ISO checksum), create bridge + both VMs
scripts/pve-lab.sh join     # form the corosync cluster over SSH once both are installed (best-effort)
scripts/pve-lab.sh status   # qm status for both VMIDs + bridge presence
scripts/pve-lab.sh down     # stop + destroy both VMIDs, remove the bridge — idempotent
```

Run it **on pvecube** (it shells out to `qm`/`pvesh`/`pvesm`), using the
`proxmox-ve_9.2-1.iso` already staged at `/var/lib/vz/template/iso/` — the script checks its byte
count before booting it and refuses to re-download. See the script's own header for the full
option list (VMIDs, bridge name, addressing, sizing) and for why the OS install step is left
manual rather than scripted with `proxmox-auto-install-assistant`: that tool isn't installed on
pvecube, and guessing its answer-file schema from documentation instead of the node is exactly the
mistake `CLAUDE.md` already warns about (Phase 31's SDN Fabrics defect).

**⚠️ `scripts/pve-lab.sh` has been written and reviewed but never executed.** Its preflight checks
were validated against real read-only output from pvecube (`pvesm status`, `free -m`,
`/etc/network/interfaces`, `pvesh get /cluster/resources`), but no code path that creates, joins,
or destroys a guest has run for real. Treat the first real run as a test of the script, not a
formality.

**The quorum choice, and why it matters.** A two-node corosync cluster has no quorum tie-break
without either `quorum { two_node: 1 }` or a qdevice. The script sets **neither** — it deliberately
mirrors `vnprox-dev`'s own `corosync.conf`, which also has no `two_node` line. Consequence: losing
either lab node drops the *survivor* out of quorum too, the same as it would on `vnprox-dev` today.
That means the lab can demonstrate "both sides lose quorum on partition" — but **cannot**
demonstrate a lone survivor continuing with `two_node: 1`, which is a different, common two-node
recipe this lab does not use. If a future task needs that scenario, it's a one-line
`corosync.conf` edit after `join`; the script's header spells out exactly which line.

**What the lab still cannot answer, even once built:**
- **Three-or-more-node quorum** (a survivor holding quorum against two failed peers, or any
  behaviour that needs a majority of more than two votes) — two nodes cannot demonstrate it.
- **Physical behaviour** — nested guests have no real NICs: no bond failover, no LACP against a
  real switch, no media type beyond `PORT_TP`.

File anything blocked by one of those two — or by needing root on pve001 — in
`planning/reports/needs-hardware-validation.md` with the specific reason, not a bare "needs
hardware": that bare phrasing is what let 156 items sit for months against a limit (no second
node) that stopped being true on 2026-08-18 and wasn't checked for five days.

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

  **`web/e2e/` is wired into the gate and is BLOCKING (T-1806-bug-01 and T-2108, both closed
  2026-08-07).** Read "CI" throughout this document as "the gate": the GitHub Actions workflows
  have been `disabled_manually` since 2026-08-13 (Actions billing exhausted), so the jobs described
  below are executed by `scripts/ci-local.sh` on the dev host and by `make check`/`make e2e`, not
  on push.
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

## Visual language (Phase 34, T-3401)

The cockpit's look is modelled on a dashboard idiom the product owner picked as the reference:
neutral surfaces, a single saturated accent used sparingly, hairline borders in place of shadows,
and generous radii. Concretely, for any new UI:

- **Accent is for one thing per screen.** `accent-*` marks the active nav item, the primary
  action, and focus rings. It is not a decoration; a screen with four accent-coloured elements
  has no primary action. Status colours (emerald healthy, amber warning, red destructive) are a
  separate vocabulary and never substitute for the accent.
- **The accent is an alias, always.** Components reference `accent-*`, never `indigo-*`. This is
  what lets demo mode re-tint the entire app by re-pointing eleven custom properties
  (`html.demo` in `index.css`, T-2801) instead of touching components. A raw colour utility in a
  component is a demo-mode bug waiting to happen; `index.css.test.ts` guards the token layer but
  cannot see a hardcoded `bg-indigo-600` at a call site.
- **Page actions are pills** (`Button` `shape="pill"`, `--radius-pill`). In-form and in-table
  buttons keep the default `rounded-md` — the pill marks "this acts on the whole page".
- **Tabs are underlined**, not boxed or pill-segmented: muted label, accent underline on the
  active one. Segmented controls stay segmented where they select a *mode* rather than a
  sub-page (the topology Graph/Switch toggle is a mode, not a tab).
- **Sidebar is grouped and labelled.** Sections carry a small muted uppercase label; groups
  collapse. Items are icon + label, never a letter glyph.
- **Borders before shadows.** A hairline `border-slate-200`/`dark:border-slate-800` separates
  surfaces; shadows are reserved for genuinely floating layers (dialog, dropdown, toast).
- **Every surface defines both themes.** Light-first designs that omit a `dark:` pairing inherit
  whatever the parent had, which is how this codebase has produced four separate WCAG AA
  contrast defects. Both themes, plus the demo-amber accent, are gated by axe — a contrast claim
  in a task report that is not backed by an axe run is not evidence.

## CI

> **Decision, 2026-08-18 (T-3301): hosted GitHub Actions is retired, not paused.** The three
> workflows (`CI`, `Packaging matrix`, `Release`) stay `disabled_manually` on purpose —
> `scripts/ci-local.sh` (via `make ci` for the fast subset) is the permanent gate, not a stopgap
> while billing is down. This closes the question the 2026-08-13 note below left open: the choice
> was restore billing / self-host a runner / formalize local CI, and it's the third one. Reasons:
> a self-hosted runner is another always-on service to operate and secure for a single-maintainer
> project, and restoring Actions billing re-introduces the same "red X independent of the diff"
> failure mode this note already documented once. If that calculus changes (a team forms, hosted
> matrix testing across OSes becomes worth the cost), revisit — but don't restore Actions
> reflexively just because billing gets fixed.
>
> **Enforcement:** `.githooks/pre-push` runs `make ci` on every `git push` and refuses the push on
> failure. One-time setup after cloning: `make install-hooks` (sets `core.hooksPath`). Deliberate
> bypass is `git push --no-verify` — git's own escape hatch, nothing extra layered on top, so
> there's exactly one way around the gate and it's on the record in shell history. This is *not*
> enforced via GitHub branch protection's `required_status_checks`: those checks are posted by the
> now-permanently-disabled workflows, so requiring them would never be satisfiable and would lock
> every push to `main` — see "Branch protection on `main`" below for what protection this repo
> does use instead.
>
> **A tag does not publish anything by itself.** `Release` runs on `v*` tags; with it disabled,
> tagging builds no artifact and cuts no GitHub release. Build the .deb locally from a clean
> worktree at the tag (`git worktree add <dir> vX.Y.Z && cd <dir> && make deb`) so the version
> string has no `+dirty` suffix — see the v3.5.0 cut for the pattern — then run the release
> publish step manually (T-3301's apt-repo push, `packaging/build-apt-repo.sh`) before tagging is
> considered done.
>
> **The 2026-08-13 note this decision resolves, kept for the history:** all three workflows went
> `disabled_manually` (`gh workflow list --all` confirmed it) after billing was exhausted on
> 2026-08-11 — every triggered job failed with *"The job was not started because recent account
> payments have failed,"* 37 of the last 50 runs, on commits that were green locally. That was a
> red X on healthy work, not a signal, and left the failure state meaningless. If a future decision
> ever reverses T-3301 and restores hosted CI:
>
> ```
> gh workflow enable "CI" && gh workflow enable "Packaging matrix" && gh workflow enable "Release"
> ```
>
> **The 2026-08-11 note this replaces, kept because its lesson outlives the funding state:** that
> note said to treat the pipeline as unavailable while runs still appeared in `gh run list` and
> still completed. Do not infer from a green or red hosted run that the pipeline is funded, and do
> not infer from "unfunded" that nothing ran. **Check, don't assume, in both directions** — which
> now cuts the other way too: workflows being *disabled* is also a thing to verify rather than
> remember, because someone will re-enable them.
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

> **These workflows do not currently run.** `CI`, `Packaging matrix` and `Release` were set to
> `disabled_manually` on 2026-08-13 after GitHub Actions billing was exhausted on 2026-08-11.
> The job definitions below are still accurate as *definitions*, and `scripts/ci-local.sh`
> executes all seven of them sequentially on the dev host — that is the gate that actually runs.
> Nothing runs on push.

`ci.yml` defines five jobs, nominally on every PR and push to `main`:

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

- **Go**: `1.26.6` (`actions/setup-go`'s `go-version`, identical across `ci.yml`, `packaging-matrix.yml`, `release.yml`); `go.mod` floors at `go 1.25.0`. Bumped from `1.26.5` on 2026-08-14: `govulncheck` failed `make check` on five stdlib advisories (GO-2026-6218 `net/url`, GO-2026-6090 `crypto/tls`, GO-2026-6089 `net/http`, GO-2026-5972 `encoding/asn1`, GO-2026-5026 `net/http`/idna), every one of them reachable from vnprox code and every one fixed in `1.26.6`. Since the workflows are disabled, the version that actually matters is the one on the dev host that builds the `.deb` — check `go version` before cutting a release.
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

### Branch protection on `main`

**Status, 2026-08-18 (T-3301): applied.** `gh api repos/bgovanlu/vnprox/branches/main/protection`
returns 200: force-push and branch deletion are disabled, and `enforce_admins` is on so the rule
applies to the repo owner too. This deliberately does **not** set `required_status_checks` — those
checks are posted by the `CI`/`Packaging matrix`/`Release` workflows, which stay
`disabled_manually` by the decision recorded above (T-3301); requiring a status check that nothing
ever posts would make every push to `main` unsatisfiable, not safer. `.githooks/pre-push`
(`make install-hooks`) is the actual gate — see the CI section above. It also does not set
`required_pull_request_reviews`: this is a single-maintainer repo and direct pushes to `main` are
the normal workflow, not an exception to route around.

Current settings, and the command that produced them (re-run if the repo ever needs re-protecting,
e.g. after a GitHub-side reset):

```sh
gh api --method PUT repos/bgovanlu/vnprox/branches/main/protection \
  --input - <<'EOF'
{
  "required_status_checks": null,
  "enforce_admins": true,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF
```

If the CI decision above is ever reversed and hosted Actions comes back, add
`required_status_checks` back with the real job names (`ci.yml`'s `check`, `cross-arm64`, `fuzz`,
`package`) and consider `required_pull_request_reviews` once there's more than one contributor —
neither belongs in this config while it would be unsatisfiable or would lock out the only
maintainer. Verify anytime with `gh api repos/bgovanlu/vnprox/branches/main/protection` (200, not
404).

### Adding a new required or advisory check

- **Required** (blocks merge): add a step to an existing `ci.yml` job, or a new job whose name is added to `required_status_checks.contexts` above (needs a repeat of the branch-protection call). Keep the job deterministic — see the `fuzz`-job anchoring above for what "independent of the diff" looks like when it goes wrong.
- **Advisory** (visible, doesn't block): add a job without adding its name to `required_status_checks.contexts`. Say so in the job's own comment, so the next person doesn't assume it gates merges.

### `make check` and concurrent worktrees on one machine

`golangci-lint` acquires a file lock on start and, by default, **exits with a hard error** ("parallel golangci-lint is running") if another instance already holds it, instead of waiting. This repo's own orchestration convention (`planning/implementation-plan-proven.md` and prior arcs' plans) explicitly tells the orchestrator to run concurrent tasks in **separate git worktrees**, each running its own `make check` — a sanctioned pattern that collides with golangci-lint's default lock behavior every time two such `make check` runs overlap in wall-clock time, independent of anything in either task's diff (T-1806 found this as a real collision between two sibling agents' worktrees, not a repo test flake).

The fix: `make lint`/`make check` now invoke `golangci-lint run --allow-serial-runners ./...` (both the local-binary and `go run ...@version` code paths in the `lint` target) instead of the default `--allow-parallel-runners=false` behavior. `--allow-serial-runners` makes a second concurrent invocation **wait for the lock and then run**, rather than erroring out immediately — verified locally by launching two `golangci-lint run --allow-serial-runners ./...` invocations against this repo at the same time and confirming both exit 0 (the second's start is simply delayed until the first releases the lock). If you see the old "parallel golangci-lint is running" error, you're on a golangci-lint invocation that bypassed this flag (e.g. calling the binary directly outside `make lint`).

## Online help is part of the change, not a follow-up (T-2205, written down 2026-08-16)

**A new routed screen ships with a help topic, and a new panel ships with a `?` anchor, in the
same change.** `web/src/help/coverage.test.ts` enforces both, and it fails the build — it is not
advisory.

`T-2205`'s card claimed this rule was written here. It was not: until 2026-08-16 the string
"help" appeared in this document **zero times**, so for four arcs the gate enforced a rule that
existed only in the gate. It is written now because `T-3006` went looking for it.

What the gate actually checks, so the rule and the test say the same thing:

| Direction | Assertion |
|---|---|
| Screen → topic | Every route in `App.tsx`/`Sidebar.tsx` has a `ROUTE_HELP` entry. The route inventory is **derived from the source**, never hand-maintained. |
| Panel → topic | Every panel-shaped component reachable in the import graph from `main.tsx` declares a topic, via `<HelpAnchor topic="…">` or by handing a literal to a wrapper that renders one. |
| Topic → surface | Every topic whose own `surface` is `panel` or `dialog` is placed at a `?` **somewhere**. A topic describing a screen nobody built has nowhere to be placed and fails. |
| Field → help | Every `<Field>` in the entity editors carries a `help=` prop. |

The third row is the one worth understanding before adding a topic. It cannot check that prose is
*accurate* — no test can — but it checks the decidable half: the surface a topic claims to
document has to be one you can put a cursor on. That check found four topics describing features
this app has never had (`ipv6-planning`'s grid, `topology-paint-modes`' backup overlay,
`ipam-external-sync`, `ipam-cross-cluster`, `scheduled-apply`), each backed by a real daemon route
with no frontend caller.

Those became `surface: "headless"`, browsed under **"Not in the web UI yet"**. That surface is a
deliberate escape hatch and is fenced as one: a `headless` topic **must** name the `/api/v1` route
or `vnproxctl` verb that reaches it, **and** must say in its own prose that there is no screen.
Demoting a real panel topic to dodge the reverse check trips a count floor. Do not reach for
`headless` to make a check go quiet — reach for it when the honest thing to tell a reader is
"this exists, and not here".

## Definition of done (every task)

1. Acceptance criteria on the task card all pass.
2. `make check` green.
3. Works against the required pvemock fixtures.
4. New API routes/types match `docs/api.md`/`docs/data-model.md` exactly (or the doc was updated in the same change with a flagged note).
5. **A new screen has a help topic; a new panel has a `?` anchor.** Enforced by
   `web/src/help/coverage.test.ts` — see the section above.
6. Report written (per `CLAUDE.md`).
