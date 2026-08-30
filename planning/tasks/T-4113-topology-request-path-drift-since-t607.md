# T-4113 · `GET /topology` has drifted ~5× slower since T-607, at unchanged scale

**Status:** done. The card's own hypothesis was wrong about the actionable cost — see the outcome.
**Found by:** T-4107's scale-envelope measurement, 2026-08-28 · **size:** M ·
**depends:** — · **affects:** every UI page load; the provisional p95 bar in
`docs/performance.md` §1

## The observation

T-4107 needed a baseline to compare its 50-node/5,000-guest envelope against, so it measured the
**pre-existing** 8-node/300-guest `scale-lab.yaml` fixture on the same host, same harness, same day:

| Measurement | Source | Result |
|---|---|---|
| `GET /api/v1/topology`, 8 nodes / ~300 guests | `docs/performance.md` §1, recorded at **T-607** | **60.1 / 67.2 ms** |
| `GET /api/v1/topology`, 8 nodes / ~304 guests | `BenchmarkAPIAtScale/GetTopology`, **2026-08-28** | **p50 ~333–345 ms** |

**Roughly a 5× regression at unchanged scale**, against a documented provisional bar of
p95 &lt; 300 ms — which the current number now exceeds outright.

This is not caused by T-4107, and T-4107 did not fix it: that card's scope was the scale envelope,
not an audit of what has accumulated on `handleTopology`'s request path in the intervening phases.
It is flagged here, in `perf/budgets.json`'s `api.topology_at_envelope_ms` entry, and in
`docs/development.md`'s scale-envelope section, rather than absorbed silently into a new ceiling.

## Why it went unnoticed

There was no gate on this path. `docs/performance.md` §1's figure is a **recorded observation from
T-607**, not an asserted budget, so nothing failed when the number moved. Every phase since has
added collectors, findings producers, drift checks, QoS and PBS work to the daemon — all of which
run concurrently with a request on the same CPUs.

T-4107's own evidence points at that as the mechanism rather than data volume: **16× fewer guests
costs only ~15% less wall clock**, which is the opposite of what a data-volume-bound cost looks
like. The request path is contending with the daemon's own background work, not with the size of
the graph.

That also means **tiling would not fix it** — T-4107 measured and rejected tiling for exactly this
reason.

## Deliverables

- Profile a `GET /topology` request against the 8/300 fixture and attribute the time: how much is
  the handler, how much is background contention, how much is serialisation. Guessing between
  those three would waste the fix.
- Decide whether the T-607 number is still the right bar. It may not be — the daemon legitimately
  does much more than it did then. If the bar moves, move it **deliberately, with the reason
  recorded**, exactly as `perf/budgets.json`'s entries do; do not quietly restate the observation.
- **Add a gate at the 8/300 scale**, whatever bar is chosen. The absence of one is why five phases
  of drift went unmeasured, and adding the envelope gate without this one leaves the same hole at
  the scale most installs actually run at.
- If background contention dominates, consider whether collector scheduling should yield during a
  request — but measure first; that is a real design change and should not be speculative.

## Acceptance criteria

1. The time in a `GET /topology` request at 8/300 is attributed across handler, contention and
   serialisation, with the measurement recorded.
2. A `perfbudget` gate exists at that scale, with its ceiling's basis written down.
3. `docs/performance.md` §1 either matches current reality or explains why the T-607 figure is kept
   as history — it must not read as a current claim while being 5× off.

---

## Outcome

| | p50 | p95 | `handleTopology` share of CPU |
|---|---|---|---|
| T-607 (2025, recorded) | 60.1 ms | 67.2 ms | — |
| Before this card | **382 ms** | **439 ms** | **42.19%** |
| After | **116–124 ms** | **134–148 ms** | **17.16%** |

Three consecutive runs after the fix, load average 7.7 of 32: p50 123.4 / 124.4 / 116.3, p95 147.4
/ 147.8 / 134.3. The remaining ~2x over T-607 is the daemon genuinely doing more than it did then.

### AC1 — attributed, and the attribution contradicted the card

This card's own analysis said *"the request path is contending with the daemon's own background
work, not with the size of the graph"*, and warned that "guessing between those three would waste
the fix". Profiling was the right instruction and the guess was wrong about the **actionable** part.

A CPU profile of the request attributed:

| | share |
|---|---|
| `inventory.Ref.String` | **24.7%** cum |
| `sort.partition_func` | **26.7%** cum |
| `inventory.Snapshot.All` | **34.3%** cum |
| `encoding/json` | **1.3%** — never the cost |

`Snapshot.All` sorted on **every call**:

```go
sort.Slice(out, func(i, j int) bool { return out[i].GetRef().String() < out[j].GetRef().String() })
```

`Ref.String` is a five-operand concatenation and the comparator builds two of them, so an n-entity
snapshot cost ~2·n·log(n) allocations *per call* — ~10,800 entities at this fixture, and `All()` is
called many times by a request and again by every findings producer. Background contention is real
and is most of what remains, but it was not the part anyone could act on.

### The fix, and the correction inside it

The sort moved out of the read path. A snapshot is immutable and published by atomic pointer swap,
so the order cannot change — the work was being repeated for a value that could not differ.

**The first version put it in the publish path and was wrong.** Four places construct a `state`
(three in `graph.go`, one in `project.go`); filling the field at one of them left `All()` returning
an empty slice everywhere else. The compiler said nothing, because a missing struct field is just
the zero value, and **ten preview tests failed**. So it is computed lazily under a `sync.Once`
instead: *a field every construction site must remember is a field the next construction site will
forget.* Race-tested, including the concurrent-reader path.

### AC2 — the gate, and proof it bites

`api.topology_at_scale_ms`, 250 ms, `cores`-scaled, at `cmd/vnproxd/scale_bench_test.go`.
**Deliberately stricter than `docs/performance.md`'s 300 ms prose bar**, so the gate fires before
the documented promise is broken rather than at the moment it is. Currently 127–139 ms — 45-49%
headroom.

Verified by reverting the fix and re-running: **319.9 ms, FAIL, −27.9% headroom.** The gate catches
the exact regression it was written for.

### AC3 — the document

`docs/performance.md` §1 now carries **two** `GET /topology` rows, T-607's and today's, with the
drift and its cause written between them. The old row is kept as history rather than overwritten,
because the point is not the number — it is that a recorded observation nothing asserts will
eventually be wrong while still reading as a current claim. §13's budget table gained the new
entry, which `TestDocTableMatchesBudgets` required before it would pass.
