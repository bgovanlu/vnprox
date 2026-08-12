# Phase 25 — Proof that runs itself

**Roadmap:** [`docs/roadmap-adopted.md`](../../docs/roadmap-adopted.md) ·
**Plan:** [`../implementation-plan-adopted.md`](../implementation-plan-adopted.md)

Context for every card in this phase: `docs/architecture.md`, `docs/development.md`,
`docs/api.md`, `docs/data-model.md`.

This phase exists because `project-status.md` ranks "117 unvalidated items" as the P0 open item
and marks it **human only**. Every card here is an attempt to make that label wrong.

**A note that binds this whole phase:** no card is complete because a report says a check ran. It
is complete when a command emits an artifact that somebody else can re-run and disagree with.

---

## T-2501 · Self-executing hardware validation suite ★

**kind:** implementation · **depends on:** T-2502
**context:** `docs/status-matrix.md` §2, `planning/validation/`, `internal/doctor/`,
`planning/reports/needs-hardware-validation.md`

The hardware-validation figure is 12 of 130 because validating an item means a human reading a
checklist line, doing the thing, and writing down what happened. That does not scale, does not
repeat, and cannot be delegated to a user who wants to help.

`internal/doctor` already proves the shape works: ten checks, each with a broken fixture proving
it can fail, and remediation enforced structurally by `Report.Validate()`. Generalise it from
"is this daemon healthy" to "does this cluster exhibit the behaviour we claim".

- New `internal/verify` package: a `Check` carries an ID matching a `status-matrix.md` row, a
  `Run(ctx, deps) Result`, and a **required** `Evidence` field — the command output, API response,
  or captured state the verdict rests on.
- `vnproxctl verify --suite=hardware [--only=ID,...] [--out=report.json]`. Exits non-zero on any
  failure. Refuses to run against a mock endpoint unless `--allow-mock` is passed, so a green run
  cannot be produced by accident.
- Suites: `hardware` (needs real PVE), `multinode` (needs 2+ nodes, skips loudly with a named
  reason otherwise), `destructive` (failure injection; requires `--i-understand`).
- The report is a signed, timestamped JSON artifact naming the vnprox version, PVE version,
  kernel, and NIC models. `status-matrix.md`'s `HW` column becomes generated from it.
- A check that cannot run states **why** and counts as `skipped`, never as `passed`.

**Acceptance**

1. Every check ID resolves to a real `status-matrix.md` row; a test fails if a check names a row
   that does not exist, **and** if a matrix row claiming `V` has no check backing it.
2. Each check has a fixture that makes it **fail**, asserted individually — not a single
   "all checks pass" test. A check with no failing fixture fails the build.
3. `skipped` is never conflated with `passed`: a run where every check skips exits non-zero and
   the report's summary says `0 passed`.
4. Running against `pvemock` without `--allow-mock` refuses with a message naming the flag,
   proven by driving the real CLI, not the internal function.
5. `--out` writes a report that round-trips through the parser and whose signature verifies; a
   byte flipped anywhere in it fails verification.
6. `--only` with an unknown ID is an error naming the unknown ID, not a silent empty run.
7. At least 20 checks ship, covering every feature area currently marked `B` (blocked) in the
   matrix, each stating its own hardware precondition.

## T-2502 · Record/replay real PVE traffic into fixtures ★

**kind:** implementation · **depends on:** —
**context:** `internal/pvemock/`, `internal/pve/client.go`, `docs/development.md`

`T-2108` found four defects sitting under green unit tests whose fixtures **invented the shape
the code expected**. That is not a testing accident; it is the structural consequence of
hand-writing every fixture. The mock is only as correct as our guess about PVE.

Add a record mode to the PVE client and a replay backend to `pvemock`, so a fixture can be
*observed* rather than imagined.

- `VNPROX_PVE_RECORD=<dir>` on the real client writes each request/response pair as a cassette:
  method, path, query, status, body, and the PVE version that produced it.
- **Redaction is mandatory and structural.** Cassettes pass through the same redactor
  `T-1902`'s support bundle uses before hitting disk. A recorded ticket, password, or key fails
  the write rather than being written.
- `pvemock` gains a replay backend that serves cassettes, matching on method + path + normalised
  query, and **fails loudly on an unmatched request** rather than falling through to a synthetic
  default — an unmatched request is the exact bug class this card exists to catch.
- `make record` documents the operator flow; recorded cassettes land in `internal/pvemock/testdata/cassettes/<pve-version>/`.
- A drift check: a test runs the existing hand-written fixtures and the recorded cassettes through
  the same parser and reports fields present in one and absent in the other.

**Acceptance**

1. A recorded cassette replays to a byte-identical response body.
2. Recording a response containing a PVE ticket, a `password` field, or a private key **fails the
   write** and names the field. Asserted for all three, separately.
3. An unmatched request in replay mode returns a distinctive error and fails the test; a fixture
   that "works" only because of a fallback default is impossible to write.
4. Query-string matching is order-independent but value-sensitive, proven by a pair of cases.
5. The fixture-vs-cassette drift check reports at least one real divergence on the current tree,
   or states in the report that none exists — the outcome is recorded either way.
6. Replay mode requires no network; the test suite passes with outbound networking disabled.

## T-2503 · Opt-in compatibility telemetry

**kind:** implementation · **depends on:** T-2501
**context:** `docs/security.md`, `internal/api/`, `T-2501`'s report format

One cluster validated by us is an anecdote. A hundred clusters reporting which checks pass on
which PVE version, kernel, and NIC is the compatibility matrix `T-2103` needs and cannot produce
from a single dev box.

- Off by default. `[telemetry] enabled = false` in config; nothing is sent, and no endpoint is
  contacted, until an operator sets it.
- The payload is a `T-2501` report reduced to: check IDs and verdicts, vnprox version, PVE
  version, kernel version, NIC driver names, node count. **No hostnames, no addresses, no MACs,
  no guest names, no cluster name.**
- `vnproxctl telemetry preview` prints the exact bytes that would be sent. This is the primary
  trust surface and is not optional.
- An `install-id` is a random ULID generated locally, resettable with one command, and is the only
  correlator.

**Acceptance**

1. With telemetry unset or `false`, no outbound request is made — asserted by a transport that
   fails the test if called, not by inspecting config.
2. `telemetry preview` output and the bytes actually transmitted are the **same buffer**, proven
   by a test that captures both and compares; they cannot drift.
3. A payload containing any hostname, IP, MAC, guest name, or cluster name fails a structural
   check before send. One test per field class.
4. Resetting the install-id produces a different ULID and the old one is not recoverable from the
   store.
5. Send failure is non-fatal and never blocks or delays a `verify` run; asserted with a transport
   that hangs.
6. `docs/security.md` gains a section stating exactly what is collected, which a test asserts
   lists every field in the payload struct — adding a field without documenting it fails the build.

## T-2504 · Nightly soak and resource-leak gate

**kind:** implementation · **depends on:** —
**context:** `internal/collect/collector.go`, `cmd/vnproxd/server.go`, `docs/performance.md`

2,594 Go tests establish that each unit behaves. None of them establish that the daemon survives
a week. The failure modes this arc cares about — a goroutine per collection cycle, an unbounded
table, a slow leak in the flow ring — are invisible to every test we have because they need time.

- `make soak` runs `vnproxd` against `pvemock` under synthetic churn (nodes flapping, guests
  created and destroyed, findings appearing and clearing) for a configurable duration, default 30
  minutes locally and 8 hours nightly.
- Samples goroutine count, heap, RSS, open file descriptors, and every bounded table's row count
  on a fixed interval.
- **Fails on trend, not on threshold.** A linear regression over the second half of the run with a
  positive slope beyond a stated tolerance fails, naming the metric. A high-but-flat value passes.
- Emits the sample series as an artifact so a failure is diagnosable without a re-run.

**Acceptance**

1. A deliberately leaked goroutine (a test-only build tag spawning one per cycle) fails the gate
   and the failure message names goroutines.
2. A deliberately unbounded table fails the gate and names the table. Both leaks are separate
   fixtures; neither is detected by any existing test, asserted by running the existing suite
   against the leaky build and observing it pass.
3. A flat-but-high goroutine count passes — the gate measures slope, proven with a fixture that
   allocates once at startup and holds.
4. The run is deterministic enough to be re-run: the churn generator is seeded, and the seed is in
   the artifact.
5. Duration is configurable and a 60-second run exercises every sampler at least twice.

## T-2505 · E2E sharding, isolation, and flake quarantine

**kind:** implementation · **depends on:** —
**context:** `web/e2e/`, `web/playwright.config.ts`, `planning/tasks/phase-24.md` (T-2409),
branch `t-2409-e2e-store-isolation`

**This card subsumes the open `T-2409`, and inherits its unfinished investigation.** The branch
achieves per-spec store isolation and is blocked: 87 passed / 4 failed / 2 skipped in 16.3 min
against a 9.1-min, 89-passed baseline — **+79% wall clock against a +25% budget**, with four
reproducible, order-dependent failures. Two hypotheses are already refuted and recorded on the
phase-24 card: cold-daemon warm-up (implemented as `pveCollectorReady`, did not fix it) and CPU
contention (refuted by an idle-machine rerun reproducing the wall clock within 0.4 min).

Do not re-derive those two. Start from the datum that `scale.spec.ts › v2 canvas` **passes alone
in 41.3s and times out at 120s in-suite** — the cause is order-dependent state, not load.

- Bisect which preceding spec file's isolated daemon causes each of the four failures. The
  bisection is the deliverable even if the fix is small.
- Shard the suite across workers so wall clock is bounded by the slowest shard, not the sum.
- Quarantine: a spec marked flaky runs but does not fail the gate, and appears on a trend list
  with its flake rate over the last N runs. **A quarantine has an expiry**; an expired quarantine
  fails the build.

**Acceptance**

1. The four order-dependent failures are each explained by a named cause, or the card closes with
   AC1 recorded as *unexplained* and the bisection data attached — an unexplained outcome is
   legitimate and recorded, never faked closed.
2. Full-suite wall clock is within +25% of the 9.1-min baseline, **or** the budget is renegotiated
   in this file with the measurement that justifies it.
3. `--repeat-each=2` passes — the criterion `T-2409` never ran.
4. Sharding is proven to isolate: a spec that corrupts global state fails only its own shard.
5. A quarantined spec's failure does not fail the gate; an expired quarantine does, proven with a
   fixture whose expiry is in the past.
6. The flake trend list is generated from run history, not hand-maintained.

### T-2505 AC2 — the budget, restated against a named host (2026-08-12)

**The +25% budget was never host-qualified, and a wall-clock budget that does not name its machine
is not a budget.** `T-2505-input-02` is the evidence: the same commit is 89/0/2 here and 87/2/2 on a
hosted runner, and this box is 32-core/62 GB against a runner's 2-4 core/~16 GB. So AC2 is restated
rather than merely met:

> **AC2 (restated).** The suite's wall clock is measured on the **development host** (32-core,
> 62 GB, otherwise idle) with `make e2e`, which runs all four shards concurrently. The budget is
> **within +25% of the serial baseline measured on the same host on the same day** — 9.92 min
> (`89 passed / 0 failed / 2 skipped`, commit `d80f771`), i.e. **≤ 12.4 min**. CI does **not** run
> four shards on one runner; it runs one shard per matrix leg, so the hosted number is a per-leg
> figure and is deliberately not compared against this one.

Measured, same host, same day, machine otherwise idle:

| Arrangement | Wall clock | Result |
|---|---|---|
| Serial (pre-T-2505, `workers: 1`) | **9.92 min** | 89 passed / 0 failed / 2 skipped |
| Four concurrent shards, run 1 | 4.6 min | 90 passed / 1 failed / 2 skipped |
| Four concurrent shards, run 2 | 5.0 min | 89 / 2 / 2 |
| Four concurrent shards, run 3 (as shipped) | **5.5 min** | 90 passed / 0 failed / **1 quarantined-failing** / 2 skipped, gate PASS |

**−45% against a +25% budget.** The slowest shard sets the clock, and shard-1 is the long pole
because the quarantined `scale.spec.ts` test burns its full 120s timeout inside it; removing that
one timeout would put shard-1 at ~3.5 min. The run-to-run spread (4.6 → 5.5 min) is itself worth
recording: four concurrent shards contend for one machine, so this number is noisier than the serial
one was.

**One shard on two cores** (`taskset -c 0,1`, the closest local analogue of a hosted runner leg) is
recorded in the delivery record below — that, not the number above, is what should be compared with
the pipeline.

## T-2506 · Performance regression budget gate

**kind:** implementation · **depends on:** T-2505
**context:** `docs/performance.md`, `internal/collect/sim_bench_test.go`, `web/e2e/scale.spec.ts`

`docs/performance.md` states render and collection budgets. Nothing fails when one is exceeded, so
they are aspirations with a document, not budgets.

- Machine-readable budgets in one file, referenced by both the Go benchmarks and the Playwright
  scale spec, so there is a single source.
- The gate compares against the budget, not against the previous run — a slow drift that never
  regresses more than 5% in one step still fails once it crosses the line.
- Reports headroom on every run, so an approaching budget is visible before it breaks.

**Acceptance**

1. A deliberately slowed code path fails the gate and names the budget it exceeded.
2. Budgets live in exactly one file; a test fails if `performance.md`'s stated numbers and the
   machine-readable file disagree.
3. The gate is threshold-based, proven by a fixture that degrades 4% per step across five steps
   and fails at the crossing rather than at any single step.
4. Noise does not fail the gate: the measurement is a median of N runs with N stated, and a test
   asserts a single slow outlier passes.
5. Headroom is reported for every budget on every run, including passing ones.

---

## Follow-ups filed during wave 1 (2026-08-10)

These were found while implementing and verifying the wave, not by a card. They are recorded
here so they are not re-derived; none is a blocker for the rest of the arc.

### T-2502-followup-01 · `pvemock`'s list endpoints are order-nondeterministic

**kind:** defect · **found by:** T-2502's cassette-freshness gate

`/cluster/resources`, `/cluster/sdn/vnets` and `/cluster/sdn/zones` build their arrays by ranging
over Go maps, so the same request returns the same elements in a different order on roughly one
run in three. T-2502 contained the blast radius by comparing canonicalised bodies (arrays sorted)
in the freshness gate, with a comment saying why; replay itself is still byte-exact.

This matters beyond the gate: any test asserting on a list's *order* against pvemock is
accidentally asserting on Go's map iteration seed. Fix by sorting at the handler, on a stable key
(`id` where present), so the mock is deterministic by construction.

**Acceptance**

1. The same request to each of the three endpoints returns byte-identical bodies across 100 runs
   in one process and across 10 separate processes.
2. The sort key is stated per endpoint, and a fixture with deliberately unsorted input proves the
   handler orders it rather than the fixture happening to be ordered.
3. T-2502's freshness gate can then compare raw bodies; the canonicalisation is removed, not left
   as dead tolerance.

### T-2505-input-01 · load-sensitive test flakes, observed twice independently

**kind:** evidence · **feeds:** T-2505

Two independent observations during wave 1, both under full-parallel load on a 32-core machine
with three agents running:

- `internal/collect`'s `TestGolden_OVSLab` blew its 3-second convergence deadline on one of two
  full uncached `go test ./...` runs (reported by T-2502's agent; passes 5/5 alone).
- A full `go test` sweep against T-2504's leaking build reported 3 package failures once, then 0
  across three subsequent runs.

**Third and fourth observations (wave 2, 2026-08-10).** T-2501's agent reported `make check`
exiting 2 on load-dependent 5s vitest timeouts (`scaleLab.render`, `IpamPage` — a different pair
each run, all passing in isolation), while two other agents saturated the machine. That agent
touched zero files under `web/`. On a quiet machine the same frontend suite passes on clean main
(224 files / 1,566 tests), and the combined wave-2 merge gate — which contains all of T-2501,
T-2602 and T-2704 — is green with zero failures.

**That is now four independent sightings across two waves, in both the Go and the TypeScript
suites, every one of them under CPU pressure and every one of them passing when quiet.** The
conclusion this repository should act on is not "these two specs are flaky" but "this suite has
deadline-based tests whose deadlines are tight enough to be load-sensitive", which is a property
of the suite, not of any spec.

### T-2505-input-02 · the hosted runner fails e2e specs this host passes — a hardware-class datum

**kind:** evidence · **feeds:** T-2505 · **recorded:** 2026-08-11

The `CI` workflow's `e2e` job on commit `4968bf3` reported **87 passed / 2 failed / 2 skipped**:

- `e2e/guest-interior.spec.ts:23` — guest interior tab opt-in toggle
- `e2e/user-guide-tasks.spec.ts:73` — LACP bond create draft

The **same commit, same suite, same command** (`make e2e`) on the development host reports
**89 passed / 0 failed / 2 skipped** — the documented baseline — with those two specs green at
3.5s and 13.7s respectively. Full local run: 9.5 min, well inside budget.

The two machines differ by roughly an order of magnitude in parallelism: this host is **32-core /
62 GB**; a standard GitHub-hosted `ubuntu-latest` runner is **2–4 core / ~16 GB**.

**Why this matters to T-2505 specifically.** T-2409 concluded its regression was *order-dependent,
not load-dependent*, on the evidence that an idle-machine rerun reproduced the wall clock within
0.4 min. `T-2505-input-01` already noted that this rules out load as the cause of the *slowdown*
but not of the *failures*. This sighting is the first where the **same commit passes on fast
hardware and fails on slow hardware with no code difference at all** — which is the cleanest
available separation of the two hypotheses, because order is held constant and only the machine
changes.

The practical consequence for T-2505's AC2 (wall clock within +25% of the 9.1-min baseline): the
baseline and the budget are **host-relative**, and a card that measures them on a 32-core box is
not measuring what CI experiences. T-2505 should either state which host its budget is defined
against, or express the budget in a host-independent way. Measuring on this host alone will
produce a green card and a red pipeline.

**Not yet done:** these two specs have not been run under artificial CPU restriction (e.g.
`taskset -c 0,1`) to confirm the mechanism directly. That is the cheap next experiment and it
belongs to T-2505, not here.

**Why this is recorded on T-2505 rather than fixed here:** T-2409's regression was characterised
as *order-dependent, not load-dependent*, on the evidence that an idle-machine rerun reproduced
the wall clock within 0.4 min. That evidence rules out load as the cause of the *slowdown*. It
does not rule out load as a cause of the *four failures*, and these two sightings show this
repository does contain deadline-based tests that fail under CPU pressure. T-2505 should test
that hypothesis explicitly before concluding, and should not treat it as already refuted.

---

## T-2505 — delivery record (2026-08-12)

Shipped: four-shard e2e suite (`web/e2e/shards.json` + `shards.ts`), `cmd/e2egate`
(quarantine + expiry + run-history flake trend), `scripts/e2e-shards.sh`, a four-leg CI matrix
with a required `e2e-gate` job, and six new registry rows.

| AC | State | Evidence |
|---|---|---|
| 1 | ◐ **One failure bisected, mechanism NOT explained** | See below. Recorded as unexplained, with the bisection attached, exactly as the AC permits |
| 2 | ● Met, budget restated against a named host | 5.5 min vs a 9.92-min same-day serial baseline: **−45%** against a +25% budget |
| 3 | ✗ **NOT met, and now explained** | Run for the first time. 10 failures; the reason is precise and is not sharding |
| 4 | ● Met, with a mutation | Canary/witness pair; green isolated, red when two shards share a store |
| 5 | ● Met, with a past-dated fixture | Five gate cases run against real shard reports |
| 6 | ● Met | Trend computed from `var/e2e-runs/runs.jsonl`; nothing hand-maintained |

### AC1: what the bisection found, and what it did not

**The four failures T-2409 reported no longer exist as a set** — they were properties of that
branch's per-spec-daemon design, not of this repository. Two of them are explained outright:

- **`user-guide-tasks.spec.ts` × 2 (IPAM reserve, firewall macro rule).** T-2409's branch applied
  `isolatedStore({ config: "testdata/dev-scale.toml" })` at **file** scope. On `main` only that
  file's *first* `describe` runs against the scale stack (`test.use({ baseURL: ...:28007 })`); the
  SDN/IPAM/firewall `describe` runs against three-node-vlan. The conversion silently moved four
  tests onto a fixture they were never written for. Named cause: **a fixture regression introduced
  by the isolation conversion, not order-dependence.** `SPEC_STACKS` in `web/e2e/shards.ts` records
  that this file needs *both* stacks, with a comment naming the trap.
- **`history.spec.ts › History playback` (run 2 only).** Same class: `history.spec.ts` drives the
  flow stack (`58007`), and appeared only in the run where the branch's per-file daemon replaced it.
  Not reproduced here; the spec is green in every run below.

**`scale.spec.ts › scale-lab (v2 canvas renderer)` is bisected and unexplained.** It is
**quarantined** (`web/e2e/quarantine.json`, expires 2026-09-15, `T-2505-followup-01`).

| Tests run, in file order | Result | Reproductions |
|---|---|---|
| `:258` alone | passes, 14.2s | 1/1 |
| `:258` alone under `taskset -c 0,1` | passes, 19.5s | 1/1 |
| `:127` → `:258` | passes, 33s total | 1/1 |
| `:174` → `:258` | passes, 58s total | 1/1 |
| `:127` → `:174` → `:258` | **`:258` times out at 120s** | **4/4** |
| the full 91-test serial suite (`:127`,`:174`,`:258` in it) | passes, 13.3s | 1/1 |

What that rules out, with the evidence:

- **Not the sharding change.** It reproduces on **unmodified `main`'s** `playwright.config.ts`
  (restored from `d80f771` and run with `--config`), same 4/4 shape.
- **Not CPU.** It passes on two cores in isolation, and reproduces on an otherwise-idle 32-core box.
- **Not order-dependent app state.** The scale stack's SQLite store contains **no layout row** after
  a failing run — nothing was persisted for `:258` to inherit. (`TopologyPage` only persists
  `positions`/`activeLayers`/`vlanFilter`; pan and zoom are not persisted at all.)
- **Not the daemon's warm-up.** `:258` passes against a daemon of the same age when the preceding
  tests are removed.

So it needs **both** predecessors and neither alone, in a process where each test already gets a
fresh browser context — which points at cumulative state in the shared **browser process**, not in
vnprox. That is a hypothesis, not a finding, and it is written here as one. The remaining puzzle,
stated plainly because it is the part that does not fit: the full 91-test serial suite runs all
three tests in that order and **passes**, so more preceding work makes it *better*, not worse.

`T-2505-followup-01` carries it.

### AC3: `--repeat-each=2` — run at last, and it fails

`E2E_ARGS="--repeat-each=2" scripts/e2e-shards.sh`, all four shards, idle machine, 8.8 min:
**168 passed / 10 failed / 2 quarantined-failing / 6 skipped.** The criterion `T-2409` never ran has
now been run, and the answer is no.

The reason is one sentence, and it is not sharding: **most of this suite's specs assume a fresh
store, and `--repeat-each=2` runs each spec twice against one daemon.** The cleanest demonstration is
the spec that says so in its own name:

```
onboarding.spec.ts, --repeat-each=2, shard-2, idle machine
  repeat 1  full walkthrough ...  PASS  5.5s
  repeat 1  skip/dismiss/resume   PASS 11.8s
  repeat 2  full walkthrough ...  FAIL 34.8s   <- describe title: "onboarding walkthrough (fresh DB, ...)"
```

The ten failures are that shape: `onboarding`, `changesets`, `mgmt-redundancy` (apply → confirm →
committed), `history` (seeds and commits a changeset), `alert-rules` (a finding transition only fires
once), `simulator`, `flows`, `responsive-triage` (confirms a pending changeset), `guest-interior`.
Every one of them mutates app-owned state and then asserts on a starting condition its own first
repeat destroyed.

**What this means for the arc, stated plainly.** `--repeat-each` needs isolation *per run of a spec*.
Sharding gives isolation *per shard*, which is a different and weaker guarantee — it was never going
to satisfy this criterion, and saying otherwise would have been the tidy answer rather than the true
one. The construct that does satisfy it is `T-2409`'s per-spec daemon on branch
`t-2409-e2e-store-isolation`, whose blocker was cost: +79% wall clock, 16.3 min serial.

**That blocker is now much smaller than it was.** 16.3 min of serial per-spec isolation spread over
four shards is ~4-5 min — inside the budget restated above, on the same host. Combining the two is the
obvious next card, and it is the one that closes AC3. It is not attempted here because it would mean
re-verifying every measurement in this record against a second, larger change.

### AC4: sharding isolates, proven by breaking it

`web/e2e/aa-shard-canary.spec.ts` (shard-1) creates a real changeset in its shard's store and
records the row id; `web/e2e/zz-shard-witness.spec.ts` (shard-2) asks *its* daemon for the same id
and requires a 404. It refuses to be vacuous: it waits for the writer's marker and fails if it never
arrives, and in a whole-suite run — where the two specs legitimately share one stack — it inverts
into a positive control requiring the row to **be** visible.

The mutation: `shardVarDir()` changed to return one directory for every shard.

```
CONTROL   shard-1 writer ✓   shard-2 witness ✓
MUTATION  shard-1 writer ✓   shard-2 witness ✘
  changeset 01KZV807SEK09FMB2E7HX7C1TN was created by shard-1 on https://127.0.0.1:8007 and is
  visible on shard-2's own daemon https://127.0.0.1:21007: the two shards are sharing a store, so
  a spec that corrupts state does not fail only its own shard
```

### AC5: the quarantine, and its expiry

Five cases, run through `cmd/e2egate` against the **real** shard reports of a run that really did
fail `scale.spec.ts`:

| Case | Verdict |
|---|---|
| No quarantine | FAIL — both failures named |
| Live quarantine (expires 2026-09-15) | the scale failure **tolerated**, reported as `QUARANTINED` |
| Same entry back-dated to 2026-08-01 | FAIL, `EXPIRED ... fix it or re-triage it` |
| Back-dated entry on a test that **passed** | FAIL — an expiry that only bites when the test fails is not a deadline |
| Two of four shard reports missing | FAIL, `NOREPORT shard shard-3 produced no report` |

`internal/e2egate` additionally table-tests malformed entries (reason under 20 characters, no ticket,
unparseable or >42-day expiry, duplicates) and refuses to honour them, and
`TestRepoQuarantineIsValid` re-checks the shipped file against the real clock on every `make check`.

### A defect this work found in the suite, and fixed

`saved-views.spec.ts › T-907 annotations` reloads the page immediately after clicking "Pin note".
The note list re-renders from the mutation's own result, so the assertion can pass while
`POST /annotations` is still in flight, and the reload then discards the write. That is the
mechanism behind the "failed once in three full runs, passes in isolation" flake recorded on
`t-2409-e2e-store-isolation`; it surfaced immediately once four shards ran at once. Both the create
and the delete now wait for the server's response.

### T-2505-followup-01 · `scale.spec.ts › v2 canvas` times out after its two file-mates

**kind:** defect · **found by:** T-2505's bisection · **quarantined until:** 2026-09-15

Everything known is in the AC1 table above: 4/4 reproducible, needs both preceding tests in the
file, reproduces on unmodified `main`, not CPU, not persisted app state, and yet green inside the
full serial suite. The next experiment is the one this card ran out of room for: restart the browser
between the three tests (three separate Playwright invocations against one long-lived daemon) and
see whether the failure follows the browser or the daemon. If it follows the browser, this is a
Playwright/Chromium resource story and the fix is a spec-level one; if it follows the daemon, the
store check above was looking in the wrong place.

**Do not close it by re-running until it is green.** It is deterministic in the arrangement above.

### The load hypothesis, tested rather than assumed — and half-confirmed

`T-2505-input-02` asked for one experiment: re-run the two specs the hosted runner fails under
`taskset -c 0,1`. Run, with a control at each step, on an otherwise-idle machine:

| What | 32 cores | 2 cores (`taskset -c 0,1`) |
|---|---|---|
| `guest-interior.spec.ts:23` alone | passes 3.8s | **passes** 4.7s |
| `user-guide-tasks.spec.ts:73` alone | passes 13.6s | **passes** 17.5s |
| `scale.spec.ts:258` alone | passes 14.2s | **passes** 19.5s |
| **shard-4 entire (20 tests)** | passes, 3/3 runs | **fails, 2/2 runs — a different test each time** |

The single-spec probes refute the simple version of the hypothesis: two cores alone do not break
these specs. The whole-shard runs confirm the version `T-2505-input-01` actually stated. Run 1 failed
`guest-interior.spec.ts:23` — **one of the exact two specs the hosted runner failed on `4968bf3`**.
Run 2 failed `a11y.spec.ts:106 › axe: Topology (Graph view, v1)` instead, and passed
`guest-interior`. Both failures were `toBeVisible` assertions that ran out their 30-second budget,
and both specs pass on the same two cores when run alone.

**That is not a set of flaky specs; it is a suite whose deadlines are a function of the machine.**
Two changes came out of it:

1. **Deadlines now scale with `availableParallelism()`** (`web/playwright.config.ts`): ×2.5 under 4
   cores, ×1.5 under 8, unchanged above. It honours CPU affinity, so a `taskset` run and a
   cpuset-restricted container both read the cores they can actually use. A deadline only costs time
   when it fires, so a passing run on a big machine is unaffected. With it, shard-4 on two cores went
   green in 2.8 min.
2. **A real defect, not a deadline** — see `T-2505-followup-02`. The scaled deadline did **not** fix
   `guest-interior`, which failed again on the next two-core run after waiting the full 75 seconds.
   Raising a deadline would have hidden it; the daemon log shows it is not slowness at all.

### T-2505-followup-02 · the guest-interior panel never refetches after its toggle is enabled

**kind:** defect · **found by:** T-2505's two-core reproduction · **reproduces:** `taskset -c 0,1`
running shard-4 whole, 2 of 3 runs

The daemon log for a failing run, in order, over 60ms:

```
GET  /api/v1/guests/guest:pve1:200/interior-toggle   200
GET  /api/v1/guests/guest:pve1:200/interior          404      <-- toggle still off
PUT  /api/v1/guests/guest:pve1:200/interior-toggle   200      <-- spec turns it on
GET  /api/v1/guests/guest:pve1:200/interior-toggle   200
(nothing further)
```

The interior read is issued **once**, before the toggle is on, and correctly 404s. Enabling the
toggle never invalidates that query, so the panel keeps rendering the error branch — "Could not read
this guest's interior right now — it may be unreachable, or no live PVE session is available" —
until the tab is remounted. On a fast machine the initial GET lands after the toggle and the bug is
invisible.

This is a **frontend cache-invalidation defect in the guest-interior panel**, not a timing
tolerance, and it is the most likely explanation for one of the two specs the hosted runner failed on
`4968bf3`. Out of scope for T-2505, which is why it is written down here rather than patched: the
fix belongs with an owner of that panel, and the spec should assert the refetch happened rather than
waiting longer for a render that is never coming.
