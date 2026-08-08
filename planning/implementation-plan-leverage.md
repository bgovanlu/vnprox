# Implementation plan — Phase 24 (operator leverage)

**Roadmap:** [`../docs/roadmap-leverage.md`](../docs/roadmap-leverage.md) ·
**Cards:** [`tasks/phase-24.md`](tasks/phase-24.md)

Ten cards, one phase, no release cut of its own — Phase 24 lands on the `v3.1` line alongside
Phases 22 and 23, which are shipped but unreleased.

## Order

Chosen so the highest-value, most independent work lands first and nothing waits on a card that
might stall.

| Step | Card | Why here |
|---|---|---|
| 1 | `T-2402` finding ack | Unblocks two others; the largest single reduction in daily noise |
| 2 | `T-2408` batch fix | Small once ack's identity handling exists |
| 3 | `T-2404` blast radius | Independent; feeds T-2403's summaries |
| 4 | `T-2401` scheduled snapshots | Independent backend; feeds T-2403's history sources |
| 5 | `T-2403` entity history | Merges the two above plus audit |
| 6 | `T-2405` OpenAPI + gate | Must come after every new route above, or the gate churns |
| 7 | `T-2407` quiet hours | Independent |
| 8 | `T-2406` `doctor --live` | Independent; validated on `pvecube` at deploy |
| 9 | `T-2409` e2e isolation | Harness work; run the full suite after |
| 10 | `T-2410` packaging root cause | Investigation, timeboxed; may end unexplained |

`T-2405` is deliberately last among the API cards. Writing the completeness gate before the
routes it must cover would mean editing the document five times.

## Cross-cutting rules for this phase

- **New routes go in `docs/api.md` in the same commit**, and — from step 6 onward — in
  `docs/openapi.json`, which the gate enforces.
- **Every new guard gets mutation-tested.** The `T-2108` triage found four defects sitting under
  green unit tests whose fixtures invented the shape the code expected; a test that cannot fail is
  the thing this phase is least allowed to add.
- **No new third-party dependency** without a note in the report. Nothing in these ten needs one.
- **`make check` before each commit**, `make e2e` before deploy.

## Risk register

| Risk | Card | Mitigation |
|---|---|---|
| Ack semantics on a cleared-then-returned finding are genuinely ambiguous | `T-2402` | Card requires the test to *state* the intended behaviour, so it is a decision rather than an accident |
| Blast radius over-claims and trains people to ignore it | `T-2404` | Every disruption verdict carries its reason; no verdict without one |
| OpenAPI gate becomes a chore that gets disabled | `T-2405` | Document is hand-maintained but *mechanically checked*, so the cost is only paid when the contract really changes |
| `T-2410` stays unexplained | `T-2410` | AC4 makes "unexplained" a legitimate, recorded outcome rather than a reason to fake a close |
| Scheduled snapshots fill the disk | `T-2401` | Off by default, content de-duplicated, retention ceiling, and the existing store-size finding already covers the tail |
