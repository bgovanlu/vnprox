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

**Why this is recorded on T-2505 rather than fixed here:** T-2409's regression was characterised
as *order-dependent, not load-dependent*, on the evidence that an idle-machine rerun reproduced
the wall clock within 0.4 min. That evidence rules out load as the cause of the *slowdown*. It
does not rule out load as a cause of the *four failures*, and these two sightings show this
repository does contain deadline-based tests that fail under CPU pressure. T-2505 should test
that hypothesis explicitly before concluding, and should not treat it as already refuted.
