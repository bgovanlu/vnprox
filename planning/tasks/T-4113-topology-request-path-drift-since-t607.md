# T-4113 · `GET /topology` has drifted ~5× slower since T-607, at unchanged scale

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
