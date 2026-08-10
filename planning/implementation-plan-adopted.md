# Implementation plan — Arc 5 (adoptable, not just proven)

**Roadmap:** [`../docs/roadmap-adopted.md`](../docs/roadmap-adopted.md) ·
**Cards:** [`tasks/phase-25.md`](tasks/phase-25.md), [`tasks/phase-26.md`](tasks/phase-26.md),
[`tasks/phase-27.md`](tasks/phase-27.md), [`tasks/phase-28.md`](tasks/phase-28.md)

Twenty-five cards across four phases. Two release cuts: `v3.2` after Phase 26, `v4.0` after
Phase 28.

## Order

Phases run in order. Within a phase, cards are ordered so the highest-value independent work
lands first and nothing waits on a card that might stall.

| Step | Card | Why here |
|---|---|---|
| 1 | `T-2502` record/replay | Everything in Phase 25 rests on fixtures being observed rather than imagined |
| 2 | `T-2501` verify suite | The arc's centrepiece; needs T-2502's cassettes to be worth anything |
| 3 | `T-2505` e2e sharding | Subsumes the open T-2409; unblocks T-2801's e2e-asserted demo mode |
| 4 | `T-2504` soak gate | Independent; long-running, so start it early and let it accumulate history |
| 5 | `T-2503` telemetry | Needs T-2501's report format to reduce |
| 6 | `T-2506` perf budgets | Small; last in the phase because T-2505 changes how the suite runs |
| 7 | `T-2601` policy-as-code | Phase 26's foundation; two later cards consume it |
| 8 | `T-2602` canary apply | Independent of policy; the largest single safety gain in the arc |
| 9 | `T-2603` finding rollback | Needs a staged apply to interrupt |
| 10 | `T-2604` two-person rule | Consumes T-2601's op classes |
| 11 | `T-2605` topology preview | Independent, read-only, lowest risk in the phase |
| 12 | `T-2701` git sync | Phase 27's foundation; the substrate under T-2101's Terraform provider |
| 13 | `T-2704` topology diff | Independent read surface; T-2703 needs it |
| 14 | `T-2702` changeset → PR | Closes T-2701's loop |
| 15 | `T-2703` drift to git | Needs both directions to exist first |
| 16 | `T-2705` MCP staging | Needs T-2601 so a staged op can be policy-checked |
| 17 | `T-2706` compliance | Needs T-2601's policies as evidence sources |
| 18 | `T-2803` registry | Independent of everything; can start any time in Phase 28 |
| 19 | `T-2801` install + demo | The phase's headline; needs T-2505's suite to assert it |
| 20 | `T-2804` incident mode | Assembly of shipped parts; needs T-2704 |
| 21 | `T-2805` presence + locking | Independent |
| 22 | `T-2807` scheduled digests | Small; pure reuse of T-2407's delivery path |
| 23 | `T-2806` annotations | Independent, frontend-weighted |
| 24 | `T-2808` in-app assistant | Needs T-2705 |
| 25 | `T-2802` hosted demo | Last: it publishes T-2801's dataset, so it inherits every fix |

`T-2502` is deliberately first in the whole arc. Writing a validation suite (`T-2501`) that
asserts against hand-written fixtures would prove only that our imagination is self-consistent —
which is precisely the defect class `T-2108` found four instances of.

## Cross-cutting rules for this arc

- **New routes go in `docs/api.md` and `docs/openapi.json` in the same commit.** `T-2405`'s gate
  enforces the second; nothing enforces the first, so it is a rule.
- **Every guard ships with a fixture that makes it fire.** This arc adds more gates than any
  previous one (policy, canary, rollback trigger, two-person, budgets, soak, compliance). A gate
  with only a passing fixture is worse than no gate, because it is trusted.
- **Nothing here becomes a second write path.** Three cards add entry points to staging
  (`T-2701`, `T-2703`, `T-2705`); all three stage and none apply. `T-2705` enforces this at the
  type level, and that pattern is the reference for the other two.
- **No new major dependency without a note in the report.** Three cards are at genuine risk of
  wanting one — `T-2701`/`T-2702` (git and host APIs) and `T-2601` (policy evaluation). See the
  risk register.
- **`make check` before each commit**, `make e2e` before deploy, `vnproxctl verify` before a tag
  once `T-2501` exists.
- **Hardware validation is a deliverable, not a follow-up.** From `T-2501` onward, a card touching
  a matrix row marked `B` states whether it moved that row and why not if it did not.

## Risk register

| Risk | Card | Mitigation |
|---|---|---|
| Policy engine grows into a scripting language and a new dependency | `T-2601` | Card specifies declarative data over existing op/inventory shapes, explicitly no interpreter. If a rule genuinely needs logic, that is a decision to record, not to make silently |
| Git integration pulls in a large dependency or shells out to `git` | `T-2701` | Decide explicitly and record it; a read-only fetch of one file is a narrow requirement and should be sized against it, not against general git support |
| Canary apply leaves a changeset in an unrecoverable half-state | `T-2602` | AC4 requires daemon-restart recovery from the store; the paused state is persisted, not in-memory |
| Auto-rollback fires on an unrelated finding and trains people to disable it | `T-2603` | Attribution is scoped to T-2404's `Impact`, off by default, and the rollback names the finding that caused it |
| `T-2505` inherits T-2409's unexplained regression and stalls the phase | `T-2505` | AC1 makes "unexplained, with the bisection data attached" a legitimate close, as `T-2410`'s AC4 did |
| Demo mode diverges from real behaviour and becomes a lie | `T-2801` | The demo dataset is a repository fixture and doubles as a test corpus, so divergence breaks tests |
| Compliance reporting is read as certification | `T-2706` | Ship the format with one general profile and no certification claim; `unmapped` can never render as `pass` |
| Telemetry damages trust more than the data helps | `T-2503` | Off by default, `preview` prints the exact bytes, and the preview and the transmission are the same buffer by construction |
| The arc is 25 cards and Phase 21 is still unstarted | — | Phase 28 depends on `T-2102`'s apt repository for the install path; if Phase 21 has still not landed when Phase 28 starts, `T-2801` falls back to the tarball path and says so |

## What this arc deliberately does not do

Restated from the roadmap because it is the thing most likely to erode under pressure: **no sixth
networking domain.** At 91% feature delivery and 9% hardware validation, a new feature area
widens the gap this arc exists to close. If a card in this arc starts growing a new networking
capability, that is a signal the card is wrong, not that the arc's scope should expand.
