# Phase 32 — Proven on iron

**Roadmap:** [`docs/roadmap-earned.md`](../../docs/roadmap-earned.md) ·
**Plan:** [`../implementation-plan-earned.md`](../implementation-plan-earned.md)

Context for every card in this phase: `docs/architecture.md`, `docs/development.md`,
`planning/reports/needs-hardware-validation.md`, `planning/reports/blocked-validation.md`.

> **This card file was authored retroactively on 2026-08-19, by the 2026-08-19 debt sweep
> (`planning/tasks/debt-sweep-2026-08-19.md`, item 5), not at the start of the phase.**
> `planning/implementation-plan-earned.md` states phase cards for 30–33 are "authored when their
> phase begins, from the roadmap's card summaries" — that did not happen here. Phase 32's four
> cards (`T-3201`–`T-3204`) were executed and their results recorded directly in
> `docs/roadmap-earned.md`'s prose, `planning/reports/blocked-validation.md`, and
> `planning/reports/needs-hardware-validation.md`, with no card file ever written. This file
> reconstructs what a Phase 32 card file would have said, **after the fact**, from the real
> evidence: git commits, `planning/reports/blocked-validation.md`, `planning/reports/T-3202-scenarios.md`,
> `planning/reports/T-3203.md`, and direct inspection of the current tree (not from
> `docs/roadmap-earned.md`'s own summary of Phase 32, which this reconstruction checks rather than
> trusts — see the T-3202 section below for a case where that summary overstates what shipped).
> Every "done" / "partial" / "not done" mark below was checked against a commit, a report file, or
> the current source — not copied from another document.

The organising rule, from the arc roadmap: **nothing goes public carrying a safety claim that has
never been observed working.** This phase stands up the project's first real multi-node PVE
cluster and uses it to test the two claims that mattered most and had never been checked: that
cross-node behaviour works at all, and that commit-confirm actually self-heals a real lockout.

---

## T-3201 · Second node + the blocked register: cross-node validation for real

**Priority:** P0 · **Owns:** `planning/reports/blocked-validation.md` (new),
`internal/host/corosync.go`, `internal/auth/caps.go`, `cmd/vnproxd/certwire.go`,
`packaging/bin/vnprox-setup`, `packaging/systemd/vnprox.service`

**Objective, from the roadmap:** stand up a second PVE node beside `pvecube` and finally write
`planning/reports/blocked-validation.md` — the register `T-1803` named as Arc 4's authoritative
ledger of what remains unproven, referenced by `implementation-plan-proven.md:26` and never
created. With two nodes: peer API round trips, node-vs-node drift, distributed rollback timers,
federation transport, cross-node presence fan-out, the two remaining `doctor` checks
(`clock_skew`/`peer_secret`), and `T-1906-bug-01` (the stale-IP-SAN certificate failure) all
become observable for the first time.

### What was actually delivered — status: **done**

Verified against `planning/reports/blocked-validation.md` and commits `044f74bb` ("change+test:
T-3201 — real two-node cluster validation, first in project history") and `257741ab` ("fix: close
T-3201's four real-hardware findings, verified live on both nodes").

A real second node (`pve001`, 192.168.1.7) was joined to `pvecube` in a real corosync cluster
(`vnprox-dev`, PVE 9.2.10, quorate 2/2). `planning/reports/blocked-validation.md` was written —
the register Arc 4 named and never created — with the evidence-per-claim discipline the file's own
header states ("proof is not self-report... every 'proven' line below carries the artifact").

**Proven, with evidence (§1 of the register):**
1. Peer API round trips work, bidirectionally, sustained — 971 successful round trips in one
   sampled hour.
2. `T-1906-bug-01` (stale IP SAN on the peer cert) — confirmed already mitigated: verification
   goes by PVE node name, not dial address, so the stale SAN is never consulted.
3. `vnproxctl doctor --live` run against both real nodes — `clock_skew`/`peer_secret` confirmed
   still `skip` (missing server-side code, not a second-node precondition — `T-2406-followup-01`/
   `-02` remain genuinely open).
4. Real 2-node corosync ring output captured.
5. T-2805's presence/lock cross-node fan-out gap confirmed still unfilled (absence of code, not a
   behaviour needing traffic to observe).
6. Certificate cluster inventory correct on both nodes.

**Four real, previously-unknown bugs found on first contact with a second node, all fixed and
confirmed on real hardware (commit `257741ab`):**
1. `internal/host.ParseCorosyncStatus` could not parse real knet-transport output at all —
   `corosync_link_degraded` was a silent permanent no-op on every real PVE cluster since 6.x.
   Fixed: a second parser branch for the `LINK ID`/`nodeid:` shape.
2. `vnprox@pve!daemon`'s PVE token is cluster-wide but each node's on-disk copy is independent —
   regenerating it on one node silently breaks every other node's copy with no detection. Fixed:
   `vnprox-setup` now detects cluster peer count and warns explicitly rather than silently
   regenerating; the daemon logs a named hint on `ErrPVEAuth` failures.
3. `internal/certs.NewService` nil-pointer panic on a fresh install with no PVE token yet (a Go
   typed-nil-interface gotcha). Fixed: `certClusterFactsFor` nil-checks the concrete pointer
   before boxing it into the interface.
4. `pve_privileges` under `--live` failed on every correctly-provisioned install, because the
   check reused the *operator* privilege list against the daemon's own deliberately read-only
   token. Fixed: `internal/auth.DaemonTokenPrivileges()`, a separate list mirroring
   `vnprox-setup`'s actual grant, pinned against it by a test that reads the setup script's own
   source.

**New gap found, not fixed (explicitly out of scope, and still open — see `T-3204`'s worklist
item 8):** `packaging/install.sh`'s multi-node SSH rollout re-runs `vnprox-setup` on every node
but never copies the first node's PVE token file, so a fresh multi-node install hits the
warn-and-stop path bug 2 above added. Confirmed still true by reading `packaging/install.sh`
directly (2026-08-19) — no `scp`/token-copy step exists in the multi-node rollout section.

### Acceptance — reconstructed and checked against evidence

1. A second PVE node is joined and reachable — **done**, `vnprox-dev` cluster, quorate 2/2.
2. `planning/reports/blocked-validation.md` exists and every claim in it carries an artifact —
   **done**, verified by reading the file (§1–§3, every entry has a `journalctl`/`openssl`/`ping`/
   command-and-output block).
3. Cross-node presence, drift, corosync, and doctor-live gaps are each either proven or filed as
   still-blocked, never left ambiguous — **done**, §3 of the register states the honest boundary
   explicitly.
4. Any real bug found on first hardware contact is fixed and regression-tested, not just noted —
   **done** for all four bugs above; each has a named unit test.

**Needs hardware validation still:** everything in blocked-validation.md §3 (failure injection
beyond Scenarios 1/5 — see `T-3202` below; distributed rollback under partial multi-node failure;
federation transport; HA/3+-node quorum; SDN Fabrics/Controllers/IPAM convergence on real
hardware).

---

## T-3202 · Failure-injection proof of commit-confirm + validation burndown

**Priority:** P0 · **Owns:** `cmd/vnproxd/changeagent.go`, `planning/reports/T-3202-scenarios.md`
(new)

**Objective, from the roadmap:** T-1804 verbatim — break connectivity mid-change on real hardware
and watch commit-confirm self-heal. Also: the record/replay hardware half (`T-2502-followup-01` —
are real PVE list responses order-stable?), first real-hardware cassettes replacing the
mock-recorded ones, a hardware-validated row in the compat matrix (`T-2103`'s open half), and
closing `T-1904-followup-01` (`install.sh` aborts, not reports, on failing doctor).

### What was actually delivered — status: **partial, and narrower than `docs/roadmap-earned.md` currently claims**

Verified against `planning/reports/T-3202-scenarios.md`, commits `69fc944b`, `fa95217c`,
`18516075`, `19d835bb`, and direct inspection of the current tree.

**Done, with evidence:** T-1804's own headline scenarios were run live and passed.
- **Scenario 1** (management-link lockout on `pve001`, an `iface.update` re-addressing the live
  `vmbr0`): PASSED. SSH became unreachable within seconds of apply; the node-local timer restored
  the interface unattended at exactly its 180s deadline; live state confirmed byte-identical to
  pre-change.
- **Scenario 5** (firewall-only lockout on `pvecube`, T-1805's sealed-PVE-ticket unattended revert
  — "the headline result, T-1804 AC3 names as the card's headline result"): PASSED on the third
  attempt, finding and fixing two real bugs live:
  1. `GET /nodes/{node}/firewall/status` does not exist on real PVE 9.2.10 (only `log`/`options`/
     `rules` do); `fw_verify` was hard-failing (and rolling back) every firewall-touching apply on
     any real node. Fixed: a 501 from that route now degrades to an unverified-OK result instead
     of propagating.
  2. A node whose firewall in/out policy was never explicitly set reports back no `policy_in`/
     `policy_out` field at all; the rollback's restore sent empty strings for both anyway, which
     real PVE rejects with 400 — breaking the rollback's own restore step on the common case of a
     freshly-provisioned node. Fixed with the same non-empty guard the adjacent
     `PolicyForward`/`LogLevelForward` restore already used.
  Both fixes are regression-tested (`TestFirewallCompileStatus_501DegradesToUnverifiedOK`,
  `TestRestoreFirewallScope_OmitsEmptyPolicyInOut`, `cmd/vnproxd/changeagent_test.go`) and
  confirmed live: cluster/node firewall state after restoration was byte-identical to pre-test.

**Explicitly deferred, and still outstanding — not a gap in this reconstruction, a gap in the
work itself:** `planning/reports/T-3202-scenarios.md`'s own "Scenarios deferred" section lists
four of T-1804's six scenarios as not run this session:
- **Scenario 2** — `vnproxd` killed inside the confirm window (crash recovery re-arming the
  timer from the DB).
- **Scenario 3** — node hard-reset inside the confirm window.
- **Scenario 4** — confirm window expiring with no session alive at all.
- **Scenario 6** — apply interrupted mid-step (between a write and its `ifreload`).

These are recorded in `planning/reports/blocked-validation.md` §3 as still genuinely blocked, not
silently dropped — but they are real, open work, and any reader treating "T-3202 done" as "T-1804
closed" would be wrong.

**Not delivered, contrary to what `docs/roadmap-earned.md`'s Phase 33 prose currently implies —
checked directly against the code and docs, 2026-08-19, not assumed from the roadmap:**
- **`T-1904-followup-01` (install.sh aborts, not reports, on failing doctor) is still open.**
  `docs/roadmap-earned.md`'s `T-3202` paragraph says this card "closes `T-1904-followup-01`: `install.sh`
  aborts (not reports) on failing doctor — its blocker was resolved in Arc 4 and nobody went
  back." **This is false as of 2026-08-19.** `packaging/install.sh` (around its step-9 self-check)
  still reads `if vnproxctl doctor; then ... else warn ...` — a report, not an abort — and the
  comment immediately above it is unchanged: *"It reports; it does not abort... Turning this into
  a hard gate is tracked as `T-1904-followup-01`."* Nothing in the T-3202 commits touches
  `packaging/install.sh`. This item is **not done** and should be treated as still open.
- **The compat matrix's hardware-validated row (`T-2103`'s open half) was not added.**
  `docs/compatibility.md`'s generated matrix (2026-08-19) still shows all three PVE-version rows
  as `validation: mock`, and the document's own closing note still reads "a hardware-validated
  row, **if one is ever added here**, would need its own explicit column" — phrased as not yet
  done. **Not done.**
- **No real-hardware cassette replaced the mock-recorded ones.** `internal/pvemock/testdata/cassettes/`
  contains only `mock-three-node-vlan` (the same cassette `docs/project-status.md` §6.4 already
  noted "no cassette here is from real PVE hardware yet"). No new cassette directory was added by
  any T-3202 commit. **Not done** — `T-2502-followup-01`'s hardware half remains open.

### Acceptance — reconstructed and checked against evidence

1. At least one scenario testing a genuine, deliberately-triggered network lockout self-heals on
   real hardware with evidence, not narrative — **done**: Scenario 1.
2. The scenario T-1804 names as the headline result (a firewall-only lockout exercising the
   sealed-ticket revert path) self-heals on real hardware — **done**: Scenario 5, third attempt,
   two real bugs found and fixed along the way.
3. Every scenario not run is named explicitly as still open, with why — **done**: the four
   deferred scenarios are stated plainly in the scenarios report and cross-referenced in the
   blocked register.
4. Record/replay gains a real-hardware cassette — **not done**.
5. The compat matrix gains a hardware-validated row — **not done**.
6. `install.sh` becomes a hard gate on doctor failure — **not done**.

**What the next agent must know:** treat `T-3202` as closing *only* Scenarios 1 and 5 of T-1804's
six, plus the two firewall bugs those scenarios found. The three items above (cassette, compat
row, install.sh abort) are real, uncarded debt — they should be filed as a followup rather than
assumed done because `docs/roadmap-earned.md`'s prose says so.

---

## T-3203 · Scale & performance on real cluster data

**Priority:** P1 · **Owns:** `planning/reports/T-3203-harness/` (new),
`web/scripts/measure-real-hardware-perf.mjs` (new), `internal/topology/collapse_physical.go`

**Objective, from the roadmap:** T-1808 verbatim — real per-node port counts and guest densities,
then re-derive `DefaultPhysicalCollapseThreshold = 8` (provisional since T-1907). Re-baseline the
performance budgets on hardware rather than the 32-core dev host.

### What was actually delivered — status: **done, within a stated and honest scope limit**

Verified against `planning/reports/T-3203.md` and commit `3df0b7eb` ("planning: T-3203 —
scale/perf on real cluster data, T-1808 verbatim").

Two standalone, committed, read-only measurement harnesses were built
(`planning/reports/T-3203-harness/measure-api.sh`, `web/scripts/measure-real-hardware-perf.mjs`)
and run against the real two-node cluster:
- Real `GET /topology` latency (20 samples): p50 77.1ms / p95 83.2ms against the cluster's real 28
  entities — compared honestly against the synthetic 300-guest fixture's p50 60.1ms, with the
  divergence (+24–28%) explained as fixed per-request overhead dominating at both scales rather
  than entity count, not hand-waved away.
- Real collector poll-duration histograms read from `GET /api/v1/metrics`.
- `DefaultPhysicalCollapseThreshold = 8` re-derived against real per-node physical port counts (4
  and 6 on the two real nodes) — both under 8, so the threshold is unchanged, but this is now a
  real measurement rather than a provisional guess.

**The report states its own limit up front, and this reconstruction repeats it rather than
smoothing it over:** this cluster (2 nodes, a handful of guests) is roughly two orders of
magnitude below `docs/performance.md`'s documented scale target (8 nodes × 6 NICs, 300 guests, 40
VNets). Nothing in `T-3203` claims to validate that target — `docs/performance.md` §1–§5's
synthetic-fixture measurement remains the only evidence for the documented target itself, and a
denser real chassis to stress the collapse threshold's upper end remains unavailable.

### Acceptance — reconstructed and checked against evidence

1. Real API/collector timing is measured and published, compared against the synthetic numbers
   with divergences stated rather than rounded away — **done**.
2. `DefaultPhysicalCollapseThreshold` is re-derived against real hardware data — **done**
   (unchanged at 8, now on real evidence).
3. The report states plainly what this cluster's scale can and cannot validate — **done**.
4. Harnesses are committed, standalone, and re-runnable — **done**.

**Needs hardware validation still:** the documented 8-node/300-guest/40-VNet scale target itself,
and the collapse threshold's upper end on a denser real chassis — both explicitly out of this
card's reach with the hardware available.

---

## T-3204 · Test-debt closure: quarantine, flake, isolation, frozen-payload guards

**Priority:** P1 · **Owns:** `web/e2e/quarantine.json`, `web/e2e/simulator.spec.ts`,
`web/e2e/isolate.ts` (new), `internal/mcp/`, `internal/plugin/`, `internal/api/flows.go`

**Objective, from the roadmap:** root-cause the quarantined `scale.spec.ts` ordering failure
(quarantine expires 2026-09-15); the `simulator.spec.ts` T-504 AC5 flake; revive the parked
`t-2409-e2e-store-isolation` branch to meet T-2505's unmet AC3; and `T-2002-bug-01`'s
field-removal regression guards for frozen MCP/plugin-SDK/event-stream payloads.

### What was actually delivered — status: **partial, four independent threads at different states**

Verified against commit `ef8abec4` ("test: close accumulated e2e/contract debt — quarantine,
flake, isolation, frozen payloads (T-3204)") and `web/e2e/quarantine.json`'s current content.

1. **`scale.spec.ts` quarantine — narrowed, not fixed.** The "shared browser process" hypothesis
   the original entry implied was refuted (three separate Chromium processes against one
   long-lived daemon still reproduce the hang 4/4). Root cause narrowed to a single
   `page.mouse.move()` CDP call that stops returning once the daemon is old enough; server-side
   resource exhaustion was ruled out by pprof sampling. Two concrete candidate mechanisms and a
   next step are recorded in the quarantine entry itself. **The quarantine entry is still
   present and still expires 2026-09-15** — this is exactly `debt-sweep-2026-08-19.md`'s worklist
   item 1, and it remains open after `T-3204`, not closed by it.
2. **`simulator.spec.ts` T-504 AC5 flake — fixed.** Root cause: `traceFromContextMenu`'s
   5s/60s timeouts were literal, never inheriting the suite's own `slowFactor`/`coresFactor`
   scaling under concurrent-shard contention. Fixed, and the file also gained its own isolated
   daemon.
3. **Per-spec store isolation — partial, and deliberately not the full T-2409 revival.** New
   `web/e2e/isolate.ts` scopes isolation to the exact 8 files T-2505's `--repeat-each=2` run found
   unsafe, reusing each stack's already-registered read-only pvemock fixture rather than
   isolating that too. The parked `t-2409-e2e-store-isolation` branch itself is untouched — its
   approach was measured and rejected (proven +79% wall-clock cost, and its hash-derived ports
   don't satisfy `internal/devports`' literal-port registry gate), not merged. `debt-sweep-
   2026-08-19.md`'s worklist item 10 (`T-2409` built but unmerged on its own branch) is therefore
   still accurate and still open — `T-3204` solved the underlying test-flake problem a different
   way, it did not land T-2409.
4. **Frozen-payload regression guards — done.** 8 MCP tool payloads, 5 plugin-SDK interfaces, and
   the event-stream schema all gained guards. Found and fixed a real, previously-unguarded bug
   along the way: `flows.query` returned `store.FlowSample`'s bare Go field names instead of the
   documented `flow.Record` shape.

**One more real bug found and fixed as a byproduct**, not part of the original scope: 
`web/src/changesets/policyVerdict.ts` crashed on every changeset review for a cluster with no
configured policy set (the common case) — a Go nil slice marshalling as JSON `null`, not `[]`.

### Acceptance — reconstructed and checked against evidence

1. The `scale.spec.ts` quarantine's mechanism is narrowed with committed evidence — **done**; the
   quarantine itself is **not removed** and still expires 2026-09-15.
2. `simulator.spec.ts`'s flake is root-caused and fixed — **done**.
3. T-2505's unmet `--repeat-each=2` AC is addressed for the files it actually found unsafe —
   **partial**: the 8 known-unsafe files are isolated; the parked branch and its own two unmet ACs
   remain unmerged, exactly as `debt-sweep-2026-08-19.md` item 10 already tracks.
4. `T-2002-bug-01`'s frozen-payload guards exist for MCP tools, plugin-SDK interfaces, and the
   event stream — **done**.

**What the next agent must know:** do not read "T-3204 shipped" as "the scale.spec.ts quarantine
is closed" or "T-2409 is merged" — neither is true, and both are already tracked correctly as open
items in `debt-sweep-2026-08-19.md` (items 1 and 10).

---

## Phase 32 — delivery record (reconstructed 2026-08-19)

| Card | State | Note |
|---|---|---|
| `T-3201` | ● Done | Second node stood up; `blocked-validation.md` written with evidence per claim; four real bugs found on first hardware contact, all fixed and regression-tested. One new gap found and left open: `install.sh`'s multi-node rollout doesn't copy the PVE token file (debt-sweep item 8) |
| `T-3202` | ◐ Partial | T-1804's two highest-priority scenarios (1, 5) passed live, with two real firewall bugs found and fixed. Scenarios 2/3/4/6 explicitly deferred, still open. **`docs/roadmap-earned.md`'s claim that this card also closed `T-1904-followup-01` is false as of 2026-08-19** — `install.sh` still reports, not aborts. The compat-matrix hardware row and a real-hardware cassette were also not delivered |
| `T-3203` | ● Done | Real API/collector/frontend timing measured against the real cluster, honestly compared against the synthetic fixture; collapse threshold re-derived (unchanged). Scope limit (2 nodes vs. the documented 8-node/300-guest target) stated plainly, not implied |
| `T-3204` | ◐ Partial | Flake fixed, frozen-payload guards shipped. The `scale.spec.ts` quarantine is narrowed but still present and still expires 2026-09-15 (debt-sweep item 1). Per-spec isolation covers the 8 known-unsafe files only — `T-2409` itself remains parked, unmerged (debt-sweep item 10) |

**What this reconstruction found that the roadmap's own prose did not say plainly:** `T-3202`'s
scope, as described in `docs/roadmap-earned.md`'s Phase 32 paragraph, bundles a real, load-bearing
result (Scenarios 1 and 5 passing live) with three smaller items that were never actually
delivered (the compat-matrix hardware row, a real-hardware cassette, and `install.sh`'s abort
behaviour) and states all of them as accomplished in one breath. Whoever next touches
`docs/roadmap-earned.md`'s Phase 32 section should correct that paragraph to match this card's
findings, or at minimum not repeat the `T-1904-followup-01` claim without re-checking it.
