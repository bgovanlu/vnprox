# Phase 16 — Network intelligence

Goal: turn vnprox from a tool that reports state into one that exercises judgment. By Phase 15 the
product sees more of the network than any single tool in the stack — flows, baselines-in-waiting,
topology, config, drift. Phase 16 turns that corpus into opinions: what's abnormal, what's risky,
what breaks next. Every opinion stays advisory or staged: anomaly findings are explainable
statistics, never a black-box verdict; the microsegmentation planner proposes and dry-runs, it
never enforces; the failure simulator predicts, it never induces; capacity forecasts warn ahead of
a threshold, they don't act on it. No card in this phase introduces a second mutation path —
microsegmentation policy lands as ordinary `fw.*` changeset ops (`docs/data-model.md` §3), exactly
like every other firewall edit in the product.

Dependency shape: three cards are independent entry points that need only shipped surfaces —
**T-1601** (flow baselining, builds on T-1002/T-1003's flow pipeline and T-1007's history
retention), **T-1605** (rogue-service detection, builds on T-805's ARP/neighbor enrichment and
Phase 14's T-1404 IPv6 RA visibility), and **T-1606** (capacity forecasting, builds on T-1004's host
sampling, T-1007's history, and T-405's IPAM). The microsegmentation pair is a strict two-step
pipeline: **T-1602** (the strong-model synthesis/dry-run *core*, consuming T-1601's baselines and
T-1006's firewall log analytics) must land and clear its heavyweight review before **T-1603** (the
review/dry-run UX) can build on its output shape. **T-1604** (failure-impact simulation, also a
strong-model core) is this phase's other design-critical dependency magnet — it needs Phase 13's
path-simulator lineage, Phase 15's Ceph awareness, Phase 14's WireGuard tunnel model, and Phase 11's
scheduled-changeset machinery all landed first, since its verdict is what T-1103's unattended
maintenance-window pre-flight will trust. **T-1607** (posture score & report) closes the phase,
folding T-1604's SPOF inventory, T-1601's anomaly rate, and T-1602's segmentation coverage into one
explainable score, extending T-605's config-doc export.

Origin: `docs/roadmap-universal.md`'s Phase 16 section (v2.4) and the companion design document's
§1 Phase 16 task list, §2 model policy, §3 review policy, §5 fixture families, §6 standing
constraints, and §7 Phase 16 safety notes. This phase's exit demo: the planner proposes a 6-rule
policy for a NAS guest from 30 days of flows; dry-run shows zero would-have-blocked legitimate
flows; enforcement staged and confirmed through an ordinary changeset. The failure simulator flags
that one switch takes down both corosync links — the SPOF that federation-era growth quietly
introduced — and that verdict is what the next unattended maintenance window's pre-flight check
consults before it dares apply anything alone.

---

## T-1601 · Flow baselining & anomaly findings
**model:** sonnet-5 · **size:** L · **depends:** T-1002 (flow ingestion), T-1003 (flow explorer/map), T-1007 (history retention) · **context:** `docs/features/monitoring.md` §2 §3, `docs/api.md` (Flows, Findings), `docs/data-model.md` §2 (`flow_samples`, `finding_events`), `internal/flow/`, `internal/findings/`, `internal/store/`

**Objective:** Learn per-guest/per-segment traffic baselines (talkers, ports, volumes, time-of-day
shape) over the retained flow window and raise findings on statistically significant deviation — a
new outbound port from a DB guest, a 10× volume spike, a guest talking to a subnet it never
touched. Every anomaly is **explainable**: it names the baseline it deviated from and the
deviation's magnitude, never a bare "this looks weird." Not a black-box/ML IDS, not packet
inspection, not a SIEM ingest path — the explicit non-goal boundaries this card must not cross.

**Deliverables:**
- New `internal/baseline` package: `Learn(flowSamples, ref, window) Profile` computing, per
  guest/segment `Ref`, a statistical summary — top talkers, observed port set, an hourly
  time-of-day volume histogram, and mean/stddev byte-volume per hour-bucket — over a configurable
  learning window (default 14 days, capped by whatever `flow_samples` actually retains per
  `[flows] retention_minutes`/`max_rows`, docs/data-model.md §2).
- A new bounded app-store table `baseline_profiles` (ref, profile_json, window_start, window_end,
  updated_at) — baselines must survive past `flow_samples`' own pruning (a learned shape outlives
  the raw flows it was learned from), so this is app-owned summary data, not a shadow copy of raw
  flow rows; explicit age cap (`[baseline] profile_retention_days`, default 90) and a prune loop
  mirroring `metric_samples`' existing pattern.
- `Detect(Profile, recentFlows) []Anomaly`: three deviation classes — `new_port` (a src/dst pair
  using a port never seen in the learning window), `volume_spike` (a ref's trailing-window volume
  ≥ a configurable multiple, default 10×, of its baseline mean+stddev for that hour-bucket),
  `new_subnet` (a ref communicating with a CIDR never observed in the learning window). Each
  `Anomaly` carries `{baselineWindow, baselineValue, observedValue, deviationFactor}` — the
  machine-checkable "names its baseline and the deviation" contract.
- New findings producer, `source: "baseline"`, `check` values `new_port`\|`volume_spike`\|
  `new_subnet`; `detail` is a plain-English rendering of the `Anomaly` struct above (never a bare
  "anomalous" string); `severity: "warning"`; `fixable: false`, `docsLink` set. Standard hysteresis
  (2 cycles) before firing, matching every other continuously-recomputed producer.
- **Fixture family (new, this card's deliverable per design §5): multi-day synthetic flow-baseline
  corpora** under `internal/baseline/testdata/` — at least one "clean 14-day baseline + one
  injected anomaly of each class" corpus and one "no anomaly, pure noise" corpus, both reused
  verbatim by T-1602's planner tests.
- `docs/api.md` `GET /findings`'s source enum documentation gains `baseline`; `docs/data-model.md`
  gains `baseline_profiles`.

**Acceptance criteria:**
1. Against the clean-corpus fixture, `Learn` over the 14-day window followed by `Detect` on the
   same window's own flows raises zero anomalies (a baseline never flags its own training data).
2. Against the injected-anomaly corpus, `Detect` raises exactly one finding per injected anomaly,
   each with `check` matching the injected class and `detail` naming the correct baseline value,
   observed value, and deviation factor (golden test asserting the structured fields, not just
   finding count).
3. `volume_spike` threshold is table-tested at 5×/10×/20× multiples against a synthetic step-change
   series, proving the finding fires only once the configured multiple is crossed.
4. `baseline_profiles` prune loop test: profiles older than `profile_retention_days` are pruned on
   the same tick-based cadence `metric_samples`' existing prune loop uses; a profile is never lost
   before `flow_samples`' own window closes on the flows it was learned from.
5. A guest with no flow history in the learning window produces no baseline and no anomaly findings
   (cold-start is silent, not a false positive).
6. `docs/api.md` + `docs/data-model.md` updated; `make check` green.

---

## T-1602 · Microsegmentation planner core ★
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-1601 (baselines), T-1006 (fw log analytics), T-205 (change engine), T-503 (path-simulator evaluation engine — the dry-run reuses it) · **context:** `docs/features/firewall.md` §5 §6 (the simulator's honesty contract, binding precedent for this card's own), `docs/features/change-management.md` §2, `docs/api.md` (Firewall, Changesets), `docs/data-model.md` §3 (`fw.*` op group), `internal/fw/`, `internal/fwlog/analytics.go`, `internal/baseline/` (T-1601), `internal/change/`

**Objective:** From observed flows, compute the **minimal** firewall policy that preserves
observed-good traffic — "these N rules cover 30 days of traffic; everything else was noise" — per
guest/security-group, plus the semantics of what counts as observed-good and what a monitor-only
**dry-run** means. Policy is staged through ordinary `fw.rule.create`/`fw.group.create` changeset
ops (`docs/data-model.md` §3's existing op vocabulary — no new op type). This card owns synthesis
and dry-run semantics only; the review UI and the would-have-blocked reporting surface are T-1603.
**This is a review checkpoint** (design doc §3): its safety-analysis section must be present and
cross-referenced by test names before the card counts as done, and a heavyweight review of dry-run
soundness gates T-1603's dispatch.

**Safety analysis (required, T-703/T-1103-level rigor):**
- **What "observed-good" means.** A flow counts as observed-good only if it appears in the learning
  window **and** was not itself flagged by T-1601's own anomaly detector during that window — an
  anomalous flow is deliberately excluded from what the planner treats as legitimate, so a single
  transient compromise or misconfiguration inside the training window can never get itself
  legitimized into a proposed "allow" rule. This exclusion is load-bearing and must be provable by
  test, not asserted in prose.
- **What the minimal-covering-set algorithm may not do.** Rules are collapsed by
  `(proto, port, peer-subnet)` grouping up to a configurable coverage threshold (default 99.5% of
  observed-good bytes); the last-mile long tail is deliberately left uncovered rather than
  generating a rule per rare flow, and the planner's report states the coverage percentage and the
  uncovered flow count explicitly — never silently rounds a policy up to "covers everything."
- **What dry-run soundness requires.** `DryRun(proposedRules, corpus) Report` replays every flow in
  a given corpus (training or held-out) against the proposed ruleset using the same evaluation
  engine `internal/sim` already uses for path simulation (no second, divergent firewall evaluator),
  and classifies each flow `wouldAllow`\|`wouldBlock`. A flow that `wouldBlock` and was
  observed-good is a **would-have-blocked** flow — the exact signal T-1603's UI must surface before
  anyone enforces. The report is proven sound against the training corpus itself (self-consistency:
  zero would-have-blocked on the exact data the rules were derived from) before this card counts as
  done; a held-out day's corpus is a second, independent proof point.
- **No auto-enforcement, ever.** `internal/microseg`'s only write path is handing a computed
  `[]change.Op` to `change.Service.Create` as a **draft** — never `Apply`. A regression test asserts
  the package imports nothing from `internal/change` beyond the op-construction types.

**Deliverables:**
- New `internal/microseg` package: `Propose(guestOrGroupRef, flowCorpus, baselineProfile,
  fwlogAnalytics) Proposal{rules []fw.RuleView, coveragePct, uncoveredFlowCount}` — the minimal
  covering-set synthesis described above, informed by T-1601's baseline (what's observed-good) and
  T-1006's `internal/fwlog/analytics.go` (which existing rules already cover this traffic, so the
  planner doesn't propose a rule PVE already effectively has).
- `DryRun(Proposal, flowCorpus) Report{wouldAllow, wouldBlock []FlowRef, coveragePct}` per the
  safety analysis above.
- `Stage(Proposal) []change.Op`: emits `fw.rule.create` ops (existing op group) targeting the
  guest's or security group's ruleset — no new op type, no new mutation path.
- Reuses T-1601's flow-baseline corpora fixtures (`internal/baseline/testdata/`) for both synthesis
  and dry-run tests — the reuse this card's fixture-family entry in design §5 calls for.

**Acceptance criteria:**
1. Against T-1601's clean 14-day corpus for a NAS-guest fixture, `Propose` returns a ruleset with
   coverage ≥99.5% and a rule count materially smaller than the raw distinct-flow-tuple count
   (golden test asserting both the rule count and coverage percentage).
2. Self-consistency: `DryRun(Propose(corpus), corpus)` reports zero `wouldBlock` entries among
   observed-good flows (the training-corpus soundness proof).
3. Held-out proof: `DryRun` against a second day's corpus (not used in `Propose`) reports a bounded,
   explicitly-stated `wouldBlock` count — not required to be zero, but every entry must be traceable
   to the stated uncovered-flow tail, not a synthesis bug (manual-review-shaped assertion backed by
   a golden fixture).
4. Anomaly-exclusion proof: injecting one of T-1601's anomaly-corpus flows into the training window
   and re-running `Propose` never produces a rule that would allow that specific flow — table test
   against each of T-1601's three anomaly classes.
5. `Stage` emits only `fw.rule.create`/`fw.group.create` ops; a static-import-boundary test proves
   `internal/microseg` never references `change.Service.Apply`/`Confirm`.
6. Safety analysis section present in the report, cross-referenced by the test names in AC1–5.
7. `docs/api.md` gains a `POST /microseg/propose` + `POST /microseg/dry-run` route pair
   (netRead-gated for propose, since it's read-only synthesis; dry-run likewise); `docs/data-model.md`
   notes the reused `fw.*` op group carries this card's traffic without a schema change; `make check`
   green.

---

## T-1603 · Microsegmentation review & dry-run UX
**model:** sonnet-5 · **size:** M · **depends:** T-1602, T-1003 (flow explorer), T-1006 (fw analytics UI) · **context:** `docs/features/firewall.md`, `docs/features/change-management.md` §1 (drawer), `docs/api.md` (`POST /microseg/propose`/`dry-run` from T-1602, Changesets), `web/src/flows/` (T-1003), `web/src/firewall/` (T-1006's analytics UI)

**Objective:** A reviewable suggested-ruleset presentation per guest/security-group and a
monitor-only dry-run mode reporting *would-have-blocked* flows before anyone enforces — the UX half
of the T-1602/T-1603 review checkpoint. Staging the reviewed policy uses the ordinary
ChangesetDrawer flow (`docs/architecture.md` §8); this card owns no synthesis logic and introduces
no enforcement path outside the changeset.

**Deliverables:**
- `web/src/microseg/` (new directory): `MicrosegPlanner.tsx`, launched from a guest's or security
  group's inspector — calls `POST /microseg/propose`, renders the proposal as "these N rules cover
  X% of 30 days of traffic" with the uncovered-flow-count stated plainly (per T-1602's coverage
  contract, never rounded to "everything").
- `DryRunReport.tsx`: calls `POST /microseg/dry-run`, renders would-allow/would-block flow counts
  and a table of the would-have-blocked flows (reusing `web/src/flows/FlowExplorer.tsx`'s row
  renderer from T-1003, not a new flow table) — the reviewer-facing proof surface.
- "Stage as changeset" action: hands `Stage`'s `[]Op` (already computed server-side by T-1602) into
  the existing drawer basket via the same draft-accumulation path every other editor uses — no new
  apply affordance.
- Vitest + Testing Library: proposal rendering (coverage %, rule list, uncovered count), dry-run
  report rendering (would-block table population), and that "Stage" is disabled until a dry-run has
  been run at least once for the current proposal (the UI-level guardrail against staging a policy
  no one has dry-run).
- `web/e2e` Playwright: propose → dry-run → review would-block table → stage → drawer review → apply,
  against a pvemock fixture extended with T-1601's flow-baseline corpus.

**Acceptance criteria:**
1. Vitest: `MicrosegPlanner` renders coverage percentage and uncovered-flow count from a fixed
   `Proposal` fixture without rounding either to 100%/0.
2. Vitest: `DryRunReport` renders every `wouldBlock` entry from a fixed `Report` fixture in the
   flow-table format, and zero entries render zero rows (not a missing/error state).
3. Vitest: "Stage as changeset" is disabled until `DryRun` has been called at least once for the
   current proposal; re-proposing invalidates the prior dry-run's staged-enablement.
4. Playwright e2e against the NAS-guest fixture from T-1602: propose returns the golden 6-rule
   policy, dry-run shows zero would-have-blocked, staging opens the drawer with exactly those `fw.*`
   ops, apply succeeds and the ruleset reflects the staged rules on the next `GET /firewall/rulesets`.
5. Playwright e2e negative case: a proposal whose dry-run against a held-out corpus reports a
   nonzero would-block count renders those flows distinctly (visually flagged) before the stage
   action is reachable.
6. `docs/features/firewall.md` gains a microsegmentation section describing the propose → dry-run →
   stage flow; `make check` green.

---

## T-1604 · Failure impact simulation core ★
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-503 (path simulator engine — the design document's task table cites `T-501`; corrected here, see Card-author notes), T-1201 (federation topology), T-1503 (Ceph awareness), T-1401 (tunnels), T-1103 (scheduled changesets) · **context:** `docs/features/firewall.md` §5 §6 (the simulator's "no silent approximation" honesty contract, binding precedent), `docs/architecture.md` §4 §5, `internal/sim/`, `internal/topology/mgmtpath.go` (`ResolveMgmtPaths`), `docs/api.md` (`/protected-interfaces/status`), `planning/tasks/phase-11.md` T-1103 (the pre-flight hook point), `planning/tasks/phase-7.md` T-703 (mgmt-path resolver precedent)

**Objective:** "What breaks if X dies?" for any node, bond, switch, uplink, or tunnel — computed
from **real topology**: guests losing connectivity, VLANs stranded, quorum/Ceph risk, mgmt-path
loss. A single-point-of-failure inventory feeds a standing dashboard score. This is the pre-flight
check T-1103's maintenance-window scheduler runs automatically before unattended applies — so a
wrong verdict here green-lights an unsafe unattended apply, which is why this card is both a strong
executor and a heavyweight review checkpoint (design §3) before T-1103 or T-1701 (Phase 17) may
wire into it. Pure simulation only — it never induces a failure.

**Safety analysis (required, T-703/T-1103-level rigor):**
- **False-negative bias, by construction.** Where topology data is incomplete or a dependency
  hasn't landed for a given deployment (no Ceph, no WireGuard tunnels, single-cluster with no
  federation), the simulator must report the affected impact dimension as **"not evaluated"**,
  exactly like `internal/sim`'s existing caveat contract (`docs/features/firewall.md` §5/§6) — never
  a confident "no impact" for a dimension it cannot actually assess. A silently-omitted risk
  category is indistinguishable from "checked and safe" to a caller, which is the one failure mode
  this card cannot ship with.
- **Quorum-risk computation.** A node/link removal's quorum impact is computed from the corosync
  ring topology (reusing T-702/T-703's mgmt-path resolver for ring membership), not merely counted
  by name — a removal that drops reachable quorum-voting nodes below `floor(N/2)+1` is `quorumRisk:
  true` regardless of whether the removed element carries the label "corosync."
- **mgmt-path loss uses the shared resolver, not a second implementation.** Impact on a node's
  management path is computed by calling `internal/topology.ResolveMgmtPaths` against the
  post-failure topology, the same function `GET /protected-interfaces/status` and the mgmt-path
  interlock (T-703) already use — one shared truth, not a parallel notion of "management
  connectivity" that could silently disagree with the interlock's own.
- **The T-1103 hook is additive, not an override.** Wiring this simulator into the scheduler's
  pre-flight check gives it one more reason to abort an unattended apply; it must never grant a
  bypass of T-1103's existing `touchesMgmtPath` exclusion or any other existing safety gate — a
  changeset the failure-impact model rates "safe" that also touches the mgmt path still gets T-1103's
  unconditional exclusion.

**Deliverables:**
- New `internal/failsim` package: `Simulate(inventory.Snapshot, target Ref) Impact{disconnectedGuests
  []Ref, strandedVlans []Ref, quorumRisk bool, cephRisk bool, mgmtPathLoss []string /* node names
  */, notEvaluated []string, severity}` — computed by removing `target` from a snapshot copy of the
  inventory graph and recomputing connected components (guests reachable to their gateway/uplink),
  cross-referencing quorum per the safety analysis above, and Ceph public/cluster-network isolation
  via T-1503's Ceph-network read model where present (else `notEvaluated: ["ceph"]`).
- SPOF inventory: `Inventory(inventory.Snapshot) []SPOFEntry{ref, impact Impact}` — every node,
  bond, switch (T-1205's switch model where present), uplink, and WireGuard tunnel (T-1401's tunnel
  model where present) whose `Impact` is nonzero, backing a new dashboard tile
  (`GET /failsim/spof-score`: `{score, entries: [SPOFEntry], generatedAt}`).
- `POST /changesets/{id}/preflight-impact` (or equivalent hook consumed by T-1103's scheduler,
  documented in `docs/api.md`): given a changeset's touched refs, returns the `Impact` of the
  worst-affected touched entity — T-1103's scheduler calls this at `windowStart` re-validation
  (`planning/tasks/phase-11.md` T-1103's existing re-validation step) and aborts on `quorumRisk` or
  nonempty `mgmtPathLoss`, additive to its existing exclusion, per the safety analysis above.
- `docs/api.md` new Failure-impact-simulation section; `docs/data-model.md` notes `Impact`/
  `SPOFEntry` shapes are computed, never persisted (pure over the live inventory snapshot, same
  "never a shadow copy" rule every read-model in this arc follows).

**Acceptance criteria:**
1. Golden `Impact` computations against `three-node-vlan` and `evpn-lab` for a node-removal, a
   bond-removal, and an uplink-removal scenario each — asserting the exact `disconnectedGuests`/
   `strandedVlans` sets.
2. Quorum test: removing enough nodes from a fixture's corosync ring to drop below `floor(N/2)+1`
   sets `quorumRisk: true`; removing one node from a 5-node fixture with redundant rings does not.
3. mgmt-path-loss test reusing T-702's fixture: a node whose only management carrier depends on the
   removed bond reports that node in `mgmtPathLoss`, verified against the same
   `ResolveMgmtPaths` output `GET /protected-interfaces/status` would return post-failure.
4. `notEvaluated` proof: against a fixture with no Ceph/WireGuard data present, `Simulate` reports
   `ceph`/`tunnels` in `notEvaluated` rather than a false `cephRisk: false`/no tunnel impact.
5. SPOF inventory golden test on `three-node-vlan`: the dashboard-tile response names every element
   whose removal has nonzero impact and excludes every purely-redundant element.
6. T-1103 pre-flight integration test: a scheduled changeset touching a SPOF bond fires the hook at
   `windowStart`, the scheduler aborts the unattended apply on `quorumRisk`/`mgmtPathLoss`, audited
   distinctly from T-1103's existing `touchesMgmtPath` exclusion; a changeset with a clean impact
   verdict still respects `touchesMgmtPath` unconditionally (proving additive-not-override).
7. Safety analysis section present in the report, cross-referenced by the test names in AC1–6; a
   grep-able feature→evaluated|not-evaluated inventory mirrors `internal/sim`'s own honesty-audit
   convention (T-503 AC3).
8. `docs/api.md` + `docs/data-model.md` updated; `make check` green.

---

## T-1605 · Rogue-service detection
**model:** sonnet-5 · **size:** M · **depends:** T-805 (ARP/neighbor IPAM enrichment), T-1404 (IPv6 RA visibility) · **context:** `docs/features/ipam.md` §1, `docs/api.md` (Findings, `Cell.sources`), `internal/neighbor/`, `internal/findings/`

**Objective:** Detect rogue DHCP servers, unexpected IPv6 RAs, ARP/ND spoofing, and unknown MACs on
protected segments — entirely from data the collectors already gather (T-805's neighbor/ARP
enrichment, T-1404's RA visibility, the existing DHCP lease/reservation views). Raised as
high-severity findings; never active mitigation — blocking a suspected rogue stays a
human-confirmed fix, exactly like every other detection-only producer in the unified stream.

**Deliverables:**
- New findings producer, `source: "rogue"`, all four checks `severity: "error"` (the stream's most
  severe tier — mapped onto the existing three-level `error`\|`warning`\|`info` vocabulary,
  `docs/api.md`'s `GET /findings` shape, not a new severity value), `fixable: false`, `docsLink` set,
  hysteresis-exempt (a spoofed/rogue signal is a security event, not a noisy counter to debounce):
  - `rogue_dhcp_server`: a DHCP-offering source (from raw lease-file/DHCP-traffic observation on a
    DHCP-enabled SDN subnet) whose MAC/address does not match the subnet's own configured PVE SDN
    DHCP range owner (`GET /sdn/dhcp`'s existing config-truth view).
  - `unexpected_ra`: an IPv6 Router Advertisement observed on a segment from a source that is not a
    known PVE-configured RA source (T-1404's RA visibility feed) — additive once T-1404 lands; this
    card's own tests may stub that feed's interface but the check is a real no-op until T-1404
    actually ships it.
  - `arp_spoof_suspected`: the same IP resolving to more than one MAC (or the reverse) within a short
    trailing window on T-805's neighbor-table observations, above a debounce-free churn threshold.
  - `unknown_mac_protected_segment`: a MAC joining a VLAN/segment flagged protected (reusing the
    existing protected-interfaces concept's vocabulary, extended to arbitrary operator-flagged
    segments via a small new `protected_segments` config list) that matches no known
    guest/PhysNic/LLDP-neighbor MAC in the inventory graph.
- `internal/findings/health_rogue.go` (or a small `internal/rogue` package if the detection logic
  needs its own state beyond a single poll cycle, e.g. the ARP-churn window) — composed into the
  existing findings engine the same way every other health check is (`internal/findings/engine.go`),
  no second findings pipeline.
- Fixtures: extend T-805's neighbor-table fixtures with a spoofed-MAC scenario; extend the DHCP
  fixture set with a rogue-offer scenario; a protected-segment config fixture with both a known and
  an unrecognized MAC join.
- `docs/api.md` `GET /findings` source enum documentation gains `rogue`; `docs/features/ipam.md` §1
  notes the new detection checks alongside T-805's existing enrichment description.

**Acceptance criteria:**
1. `rogue_dhcp_server` fires exactly once against the rogue-offer fixture and not at all against a
   clean fixture (golden test); `refs` names the offending MAC/interface, not the whole subnet.
2. `unexpected_ra` is a documented no-op (never fires, never errors) against a fixture with no
   T-1404 RA feed wired, and fires against a fixture with a stubbed unexpected-RA source once that
   interface is populated (both states table-tested).
3. `arp_spoof_suspected` fires against the spoofed-MAC neighbor-table fixture within the churn
   window and does not fire against normal DHCP-renewal MAC/IP reassignment (a distinguishing
   negative test — renewal is not spoofing).
4. `unknown_mac_protected_segment` fires for the unrecognized MAC and not for the known one in the
   protected-segment fixture; a segment not listed in `protected_segments` never fires this check
   regardless of unknown MACs on it.
5. All four checks are `fixable: false` and hysteresis-exempt (table test asserting no debounce
   delay); a regression test confirms zero changeset ops or write routes are introduced by this
   card (mirrors T-1206 AC4's zero-write-surface pattern).
6. `docs/api.md` + `docs/features/ipam.md` updated; `make check` green.

---

## T-1606 · Capacity forecasting
**model:** sonnet-5 · **size:** M · **depends:** T-1004 (host-local flow sampling / host sampling), T-1007 (history retention), T-405 (IPAM) · **context:** `docs/data-model.md` §2 (`metric_samples`' 24h ring, the retention precedent this card extends), `docs/features/monitoring.md` §2, `docs/roadmap-universal.md` (Phase 16's "one deliberate retention extension"), `internal/metrics/`, `internal/ipam/`

**Objective:** Trend link/segment utilization and IPAM pool consumption against history; raise a
finding when a growth curve crosses capacity within a configured horizon ("vmbr1 uplink full in
~5 weeks"). **This is the arc's one deliberate retention extension** — downsampled long-term
aggregates, explicitly bounded and exportable, never a raw-data warehouse. Every other card in this
arc stays within existing bounded-retention rules; this card's aggregate table is the sole,
named exception.

**Deliverables:**
- New bounded app-store table `capacity_aggregates` (ref, kind: `"link"`\|`"ipam_pool"`, bucket_at
  — daily rollup timestamp, avg_utilization, max_utilization, created_at) — a **downsampled** daily
  rollup computed from `metric_samples`' 24h ring (for links) and `internal/ipam`'s live allocation
  counts (for pools) before the source data prunes, so trend history outlives the raw 24h window it
  was summarized from. Explicit, documented bound: `[capacity] aggregate_retention_days` (default
  400 — roughly 13 months, enough for year-over-year trend without being unbounded), pruned on the
  same tick-based cadence every other bounded table uses.
- `GET /capacity/export?format=csv|json` — the export path this retention extension is required to
  carry (design §6 rule 4): the full aggregate history for a given ref, bounded by the same
  retention window, never a live-data dump.
- New `internal/capacity` package: `Forecast(aggregates []Aggregate, horizonDays) Projection{crossesAt
  *time.Time, confidence}` — a simple linear trend fit over the daily rollups; `crossesAt` is nil
  when the trend is flat or decreasing (no false-positive forecast on stable utilization).
- New findings producer, `source: "capacity"`, `check` values `capacity_link_forecast`\|
  `capacity_ipam_forecast`; `detail` names the projected exhaustion date and the horizon used;
  `severity: "warning"`; `fixable: false`, `docsLink` set; fires only when `crossesAt` falls inside
  the configured horizon (default 90 days).
- Daily rollup job: a supervised goroutine (per `docs/development.md`'s Go standards — owned,
  shutdown path) computing yesterday's bucket once per day, restart-safe (idempotent on a bucket
  that already exists for that day).
- `docs/data-model.md` new table entry explicitly flagged as the retention exception, cross-
  referencing the `flow_samples`/`metric_samples` bounded-retention precedent for contrast;
  `docs/api.md` new Capacity section.

**Acceptance criteria:**
1. Rollup job test: after `metric_samples`' 24h ring prunes a day's raw counters, the corresponding
   `capacity_aggregates` row for that day still exists and matches the pre-prune computed values
   (the survive-past-source-pruning property).
2. Forecast golden test against a synthetic linearly-growing-utilization fixture: `crossesAt` falls
   inside the fixture's known threshold-crossing date within a stated tolerance; against a flat/
   stable fixture, `crossesAt` is nil and no finding fires.
3. IPAM pool forecast golden test: a subnet fixture with steadily increasing allocation count over
   the rollup history produces a `capacity_ipam_forecast` finding naming the pool and projected
   exhaustion date; a subnet with stable allocation does not.
4. `GET /capacity/export` round-trips CSV and JSON against a seeded aggregate history, bounded to
   exactly the retention window (rows older than `aggregate_retention_days` are absent).
5. Retention-bound test: aggregates older than `aggregate_retention_days` are pruned on the next
   prune tick; the rollup job is idempotent when re-run for an already-computed day (no duplicate
   rows).
6. `docs/data-model.md` explicitly documents this table as the arc's one retention exception, with
   the export path named; `docs/api.md` updated; `make check` green.

---

## T-1607 · Posture score & report
**model:** sonnet-5 · **size:** M · **depends:** T-1604 (SPOF inventory), T-1601 (anomaly rate), T-1602 (segmentation state), T-605 (config-doc export) · **context:** `docs/features/blueprints.md` §3 §4 (T-605's export precedent this card extends), `docs/data-model.md`, `docs/api.md` (`GET /export/doc`, Findings), `internal/docexport/` (T-605's package), `internal/failsim/` (T-1604), `internal/baseline/` (T-1601), `internal/microseg/` (T-1602), `internal/fw/` (resolved firewall view, T-501)

**Objective:** One periodically-computed network security/resilience score with **named**
contributing factors — SPOF count, unsegmented guests, exposed ports, anomaly rate, drift hygiene —
never an opaque single number. A scheduled, exportable report extending T-605's config-doc export:
the management-legible progress artifact that turns findings into a trend line an operator can show
someone else.

**Deliverables:**
- New `internal/posture` package: `Score(inventory.Snapshot, findings, spofInventory,
  segmentationState) Posture{overall int, factors: []Factor{name, weight, value, contribution}}` —
  five named factors:
  - **SPOF count** — from T-1604's `Inventory` (fewer/lower-impact SPOFs score higher).
  - **Unsegmented guests** — the fraction of guests with no T-1602-staged-and-applied
    microsegmentation policy (a guest with a *proposed but unstaged* policy still counts
    unsegmented — only applied coverage counts, matching the "never opaque, never aspirational"
    contract).
  - **Exposed ports** — guest-scope firewall rules (via `internal/fw`'s existing resolver, T-501)
    whose resolved evaluation order permits inbound from `0.0.0.0/0`/`::/0` with no narrower rule
    ahead of it.
  - **Anomaly rate** — T-1601's `source: "baseline"` finding count over a trailing window,
    normalized per guest.
  - **Drift hygiene** — the existing `GET /drift` open-finding count (already-shipped `internal/drift`,
    no new detection logic), normalized cluster-wide.
  - Each factor's `weight`/`value`/`contribution` are independently inspectable fields, not folded
    into `overall` opaquely — the "never a single number with no factors" contract is a card
    deliverable, tested directly.
- New bounded app-store table `posture_scores` (id, computed_at, overall, factors_json) — one row
  per scheduled computation, retention bound mirroring `finding_events`' 24h-class window scaled to
  this report's actual cadence (default: keep the last 90 computations, whichever is smaller by
  count or 400 days by age).
- `GET /posture` (latest score + factors), `GET /posture/history` (bounded trend), and
  `GET /export/posture?format=md|html` extending T-605's `internal/docexport` renderer with a new
  section (score, factor table, trend sparkline) — reusing the existing export machinery's
  Markdown/HTML dual-format contract, not a parallel renderer.
- A scheduled computation job (supervised goroutine, default daily) alongside T-1606's rollup job
  pattern.
- `docs/api.md` new Posture section; `docs/data-model.md` `posture_scores` table entry;
  `docs/features/blueprints.md` §4 updated to note the posture-report extension of config-doc export.

**Acceptance criteria:**
1. Golden score computation against a curated fixture with known SPOF count, known
   segmented/unsegmented guest split, known exposed-port count, known anomaly finding count, and
   known open-drift count — every factor's `value`/`contribution` matches expectation exactly.
2. Contract test: `Posture.factors` is never empty and every factor has a non-empty `name` —
   asserted as a standing regression, not just checked on the golden fixture (the "never opaque"
   guarantee).
3. Segmentation factor test: a guest with a T-1602 proposal that was dry-run but never staged still
   counts as unsegmented; a guest with an applied microseg changeset counts as segmented.
4. `GET /export/posture?format=md` and `?format=html` golden test against the curated fixture,
   verifying the score/factor table/trend section renders and the HTML is standalone (no external
   requests, mirroring T-605 AC3's CSP-style check).
5. Retention test: `posture_scores` is bounded by count and age per the stated defaults; the
   scheduled job is idempotent (re-running the same day's computation does not duplicate a row).
6. `docs/api.md`, `docs/data-model.md`, `docs/features/blueprints.md` updated; `make check` green.

---

## Card-author notes

- **T-1604's stated dependency `T-501` is corrected to `T-503`.** The design document's task table
  (§1) and its dependency graph (§4) cite `T-501 (path simulator)` as a Phase 16 dependency for
  T-1604 (and, elsewhere, for T-1404/T-1505 in Phases 14/15, out of this file's scope). `T-501` is
  actually **"Firewall read: rulesets, objects, resolved view"** (`planning/tasks/phase-5.md`); the
  actual path simulator engine is **T-503** (`internal/sim`, `planning/tasks/phase-5.md`), which
  itself depends on T-501. This is not a roadmap-vs-design conflict — `docs/roadmap-universal.md`
  does not name task IDs — so this file uses the corrected `T-503` reference in T-1604's `depends:`
  line rather than propagating the design document's apparent typo.
- **Design §4's parallelism note is inconsistent with §1's T-1605 dependency line.** §4 lists T-1605
  among Phase 16's "independent roots" that "can begin as soon as the shipped flow/metrics surfaces
  are available," but §1's own task table gives T-1605 a hard `depends: T-1404` (Phase 14's IPv6 RA
  visibility, not yet shipped at the start of Phase 16). This file follows §1 (the authoritative
  task list) and keeps `T-1404` as a real dependency; the `unexpected_ra` check is scoped as an
  additive no-op until T-1404 lands, so T-1605's other three checks can still be built and tested
  independently in the spirit of §4's parallelism intent, without contradicting §1's stated
  dependency.
- No other design/roadmap conflicts were found for Phase 16. `docs/roadmap-universal.md`'s Phase 16
  bullets, P0/P1 markers, and exit demo match the design document's task list one-for-one.
- All `docs/` references cited above were verified against the current repository state
  (`docs/architecture.md`, `docs/api.md`, `docs/data-model.md`, `docs/development.md`,
  `docs/features.md`, `docs/features/monitoring.md`, `docs/features/change-management.md`,
  `docs/features/firewall.md` §5/§6's honesty-contract framing, `docs/features/ipam.md`,
  `docs/features/blueprints.md`, `docs/security.md`) and the `internal/` package layout (`ls
  internal/`). No cited doc section was invented; `internal/federation` (T-1201) and `internal/
  switchdrv`/Phase 14–15 packages referenced as dependencies do not yet exist in this repository —
  consistent with this phase sequencing after them per the design document's own dependency graph.
