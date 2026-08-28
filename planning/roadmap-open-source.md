# Roadmap — fifty enhancements on the road to open source

**Written:** 2026-08-27. **Source basis:** the shipped-feature inventory and backlog survey run the
same day (docs/features.md, docs/architecture.md, planning/tasks/phase-{0..37}.md,
docs/audit-matrix-2026-08-23.md, planning/reports/needs-hardware-validation.md).

**Intent (owner's words):** *a visual networking tool for Proxmox that will eventually be open
sourced.* Every phase below serves one of two goals: make the product worth adopting, or make the
project possible to adopt. Phase 38 is the second goal; 39–41 are the first.

> **Ground-truth correction (2026-08-27, from the card-grounding pass):** "eventually open
> sourced" understates the present. `bgovanlu/vnprox` is **already public on GitHub** —
> Apache-2.0, license decided 2026-08-06, LICENSE/NOTICE/CONTRIBUTING/SECURITY files present,
> full history visible including every `192.168.1.x` reference. GitHub Pages is `built`, but
> `docs.vnprox.com` has no DNS record, so the site is unreachable by name. Phase 38 is therefore
> not a pre-publication gate: it closes out a **live** exposure and makes an already-visible
> project adoptable. Individual card corrections are folded into the tables below and detailed
> in `planning/tasks/phase-38.md`'s own ground-truth section. This is the project's recurring
> lesson applied to its own roadmap: the first draft of this file asserted "the project cannot
> be open sourced by pushing public" without checking; `gh repo view` took one second.

**What this roadmap is not.** None of the 50 items below duplicates a shipped feature, an open
task card, or a disclosed gap (T-607, T-3002, T-3005, T-1407, the Arc 6 items). Those existing
items are sequenced in **Wave 0** below but are not counted among the 50. Every item respects the
frozen platform surfaces (D10: MCP tool manifest additive-only and never an apply tool; plugin SDK
v1 additive-only; WS events envelope additive-only) and the permanent scope boundaries in
docs/features.md (vnprox never replaces the PVE firewall engine, is not a metrics warehouse, does
no payload inspection, never owns another cluster's config).

---

## Wave 0 — the debt gate (existing cards, not part of the 50)

New feature work does not start while two quarantines are running out the clock. In priority
order, with the dates that force it:

| Item | Why it gates | Forcing date |
|---|---|---|
| `scale.spec.ts` v2-canvas hang (T-2505-followup-01) | quarantine expires; 3 investigation rounds failed, next step is a Playwright trace, not a fourth patch | **2026-09-15** |
| T-3713 simulator trace-path flake | quarantine expires; root cause known (`waitForLayout` weak settle condition) | **2026-09-22** |
| ~~Tenant mutation routes not scoped to membership~~ | **CLOSED — was already fixed.** Re-derived 2026-08-27: commit `4713d72c` scoped all five routes 35 min after the debt sweep filed the finding; the sweep doc was never updated, so it read as open for eight days. Carded retroactively as T-3714. No code change needed. | ✅ done |
| T-3712 duplicate peer-neighbor polls | 401-per-cycle noise; masks real replay alarms | hygiene |
| ~~T-3101-followup-01 `sdn.apply` surfaces foreign pending changes~~ | **CLOSED — already implemented.** Found 2026-08-28 while reading `internal/apidoc`: `internal/change/apply_sdn_foreign.go` names the card in its own header, with `GET /changesets/{id}/sdn-foreign-pending`, the `/ack` route, `web/src/changesets/sdnForeignPendingGate.ts`, the review-screen gate and tests. The debt sweep recorded it as open and was never corrected. | ✅ done |
| T-3406-followup-02 badge-class consolidation (5 duplicated definitions) | third recurrence of the same defect class | prevention |
| ~~`AlertRules.tsx` local filter union (12-of-17 sources defect twin)~~ | **CLOSED — already fixed.** Re-derived 2026-08-27: `AlertSourceFilterValue = FindingSource` and `SOURCE_LABELS: Record<FindingSource, string>` make a missing source a compile error, not a silent `undefined`. Test strengthened to derive expectations exhaustively so it stays that way. | ✅ done |
| `planning/implementation-plan.md` stops at Phase 6 | this roadmap makes the staleness worse unless fixed; fold phases 7–41 into the index | docs |

**Owner decisions already pending, restated:** public DNS for `registry./apt./demo.vnprox.com`
(deferred by owner — gates the hosted half of T-4008/T-4009, the Sigstore end-to-end run, the
ADR/docs site at `docs.vnprox.com`, and a working `security@` mailbox).

> ### ⚠️ Unresolved conflict: is hosted CI retired or being restored?
>
> Two live positions contradict each other, and several cards in this file were written assuming
> the second without knowing about the first:
>
> - **`docs/development.md`, T-3301, dated 2026-08-18:** *"hosted GitHub Actions is retired, not
>   paused."* The three workflows stay `disabled_manually` **on purpose**; `scripts/ci-local.sh`
>   is "the permanent gate, not a stopgap while billing is down." It explicitly warns: *"don't
>   restore Actions reflexively just because billing gets fixed."* Reasons given: a self-hosted
>   runner is another always-on service for a single maintainer to secure, and hosted Actions
>   re-introduces the "red X independent of the diff" failure mode.
> - **The owner, later, in the 2026-08-25 session:** chose "You'll get Actions running again" when
>   asked which blocked items they would clear.
>
> The later statement may simply supersede T-3301 — but it may also have been made without T-3301
> in view, since that decision lives in a CI section nobody re-reads. **This roadmap does not
> assume either way.**
>
> **Open-sourcing changes the calculus, which is worth putting in front of the decision.** T-3301
> was reasoned from a solo-maintainer, effectively-private project. On a public repo, local CI
> cannot validate a *contributor's* pull request: the maintainer must fetch every fork branch and
> run the gate by hand, or merge on trust. That is a real adoption barrier and a real security
> boundary (running an untrusted fork's code locally is worse, not better, than running it in a
> disposable hosted runner). T-3301's own text anticipates exactly this: *"If that calculus
> changes (a team forms…), revisit."* Open-sourcing is that change.
>
> Until this is answered, **T-3808 is contested rather than blocked**, and T-4108 (nightly lab
> burndown) and the first genuine Sigstore keyless signature inherit the same uncertainty.

> **Wave 0 audit result (2026-08-27, amended 2026-08-28).** Executing this gate found that **four
> of its eight items were already done** and had survived only as uncorrected records: the tenant
> privilege gap
> (closed by `4713d72c` thirty-five minutes after the sweep filed it), the AlertRules source union,
> — from the Phase 38 grounding pass — the premise that the repo still needed publishing, and
> T-3101-followup-01's foreign-SDN-pending confirmation, found on 2026-08-28 only because an
> unrelated read of `internal/apidoc` happened to show its routes. That last one is the
> uncomfortable case: nothing in the workflow would have caught it, because nobody was going to
> re-derive an item until an agent was dispatched to "fix" it. Each of the others
> was caught by the "re-derive before you fix" rule; without it, four agents would have spent
> their run "fixing" working code, and the diffs would have looked like progress. The generalised
> lesson, now on its third instance after the SDN-fabric fixtures and the five-day-stale
> `CLAUDE.md` cluster line: **this project's documents outlive their accuracy, and a backlog is a
> document.** Re-derivation is not diligence theatre here; it is the highest-yield step in the
> loop. Treat every remaining item in this file as a hypothesis with a date on it.

---

## Phase 38 — Open the source (12 items, T-3801–T-3812)

*The repo is already public; this phase closes the exposure that implies and makes the project
adoptable rather than merely visible.*

| ID | Item | Size | Blocked on |
|---|---|---|---|
| T-3801 | **License hygiene.** ~~License selection~~ — Apache-2.0 has been in force since 2026-08-06 (LICENSE/NOTICE exist); the owner ratifies it as final rather than picks it. Remaining: SPDX headers across both trees, a `go-licenses` + npm license-compatibility CI gate, and a written policy for future deps. | M | owner: ratify (light) |
| T-3802 | **Exposure audit of the already-public history.** The full history is live, `192.168.1.x` and all. Scan it (gitleaks/trufflehog, dev-time deps) for secrets, credentials, personal data; report what is exposed; the owner then decides remediation (rotate / rewrite / accept) from findings, not from guesses. Verified by scanning, never asserted. | L | owner: remediation call |
| T-3803 | **Contribution infrastructure.** CONTRIBUTING.md and issue templates exist — extend them (they carry stale cross-references), add code of conduct, DCO sign-off gate, and a good-first-issue curation pass over `planning/tasks/`. | M | — |
| T-3804 | **Governance & ADR publication.** D1–D11 already exist as decisions; publish them as numbered ADRs with their context, plus maintainer model, release cadence, and an LTS statement. | M | — |
| T-3805 | **Security policy & coordinated disclosure.** SECURITY.md exists — extend it with a private vulnerability reporting channel, embargo/advisory workflow, and a documented supported-versions window. | S | — |
| T-3806 | **Reproducible builds.** Pinned toolchains, `SOURCE_DATE_EPOCH`, stripped paths, and a `verify-reproducible` script proving byte-identical rebuild — closing the residual `internal/hubreg` vetting note that says this check is NOT performed. | M | — |
| T-3807 | **SBOM + provenance on releases.** Cosign signing and installer verification already exist — the real gap is CycloneDX SBOMs for both trees on every release, plus SLSA provenance once Actions run. | M | Actions (partial) |
| T-3808 | **Fork-safe public CI.** The seven `ci-local` jobs as Actions workflows that run on fork PRs without secrets exposure. `main` already has partial protection (enforce_admins, no force-push); the real gap is required status checks — narrower than "no branch protection". **See the CI-strategy conflict below — this card's premise is contested, not merely blocked.** | M | owner: resolve T-3301 vs. restore |
| T-3809 | **Published OpenAPI spec.** The generator already exists (T-2405, gated by `TestOpenAPI_EveryRouteIsDescribed`) — the gap is publishing it on the docs site and a generated-TS-client type-check as the drift tripwire. | M | — |
| T-3810 | **Contributor quickstart.** One-command dev environment against `pvemock` (no PVE needed), an architecture walkthrough written for a stranger, and a "first change" tutorial that ends in a passing `make check`. | M | — |
| T-3811 | **Plugin developer portal.** The frozen SDK's docs published properly, an example-plugin template repo, and `vnproxctl plugin scaffold` to stamp one out. | M | — |
| T-3812 | **Telemetry transparency page.** Publish the aggregate stats the collector holds, document the exact bytes sent (the `preview` output, rendered), and review the consent UX against OSS norms — opt-in stays opt-in. | S | — |

## Phase 39 — Deepen the map (12 items, T-3901–T-3912)

*The core promise is "see your network". These add the layers operators keep shelling out for.*

| ID | Item | Size | Blocked on |
|---|---|---|---|
| T-3901 | **STP/RSTP on the map.** Root bridge, port roles and states, blocked ports painted on the topology — the first question in any L2 loop hunt. Read-only. | M | — |
| T-3902 | **Multicast visibility.** Bridge MDB browser and IGMP/MLD snooping state, sibling to the existing MAC/FDB browser. | M | — |
| T-3903 | **Route explorer.** Kernel FIB + FRR RIB per node with a visual next-hop graph and a "which path would this address take" lookup, complementing the firewall-centric path simulator. | L | — |
| T-3904 | **Compiled-ruleset inspector.** Read-only view of the nftables ruleset PVE actually installed, cross-linked to the visual firewall rules that produced each chain. Explicitly not an editor — the PVE engine boundary stands. | M | — |
| T-3905 | **Neighbor binding timeline.** IP↔MAC binding history with flap detection, building on `internal/neighbor` — turns "the ARP table now" into "what changed and when". | M | T-3712 first |
| T-3906 | **Guest ego view.** One guest's whole network story on one screen: NICs, bridges, paths, firewall verdicts, flows, findings. The inspector shows an entity; this shows a neighborhood. | M | — |
| T-3907 | **Physical cabling plan.** Rack/cable map derived from LLDP with printable output — the diagram every homelab wiki draws by hand. | M | — |
| T-3908 | **"What changed" heat layer.** Paint entities by config-change recency from snapshot history; incident triage starts with "what moved last". | S | — |
| T-3909 | **Federated map stitching.** The multi-cluster capsule view already exists (T-1202) — the gap is WireGuard interconnect links drawn as edges between capsules instead of today's bare grid. Views only — the federation ownership boundary stands. | M | — |
| T-3910 | **Flow replay.** Animate the bounded rings across the map — 24h for metrics, 60min for flows (each ring's real window, not a claimed uniform 24h). Distinct from config history playback, which already exists; a view, not a warehouse. | M | — |
| T-3911 | **Composable dashboard.** Per-user tile grid over built-in tiles plus the plugin SDK's `dashboardTile` extension point, which currently has no first-party surface that composes it. | M | — |
| T-3912 | **Blast-radius lens.** From a finding or a failsim result, collapse the map to the affected subgraph. `internal/failsim` computes impact; nothing renders it as a focused view yet. | M | — |

## Phase 40 — Operate at scale (15 items, T-4001–T-4015)

*Integrations and automation: the phase that decides whether OSS adopters can fit vnprox into an
existing shop. Everything stages through the change engine; nothing here adds an apply path.*

| ID | Item | Size | Blocked on |
|---|---|---|---|
| T-4001 | **Terraform/OpenTofu provider.** Read data sources + resources that stage changesets; apply remains a vnprox review action. The single biggest adoption lever for IaC shops. | L | — |
| T-4002 | **Ansible collection.** Modules mirroring the provider's stage-only contract, plus a dynamic inventory source built from the topology. | L | T-4001 patterns |
| T-4003 | **Runbooks.** Parameterized sequences of read-checks and changeset templates attached to finding types; "prepare remediation" stages and stops. The MCP/plugin write boundary (stage, never apply) already defines the contract. | L | — |
| T-4004 | **PVE upgrade advisor.** Preflight for known network-affecting changes between PVE versions — the conntrack-procfs class of break (T-3711) as a checkable catalog, not tribal memory. | M | — |
| T-4005 | **Spec capture CLI.** The export itself already ships (`internal/spec.Export`, `GET /api/v1/spec`) — the gap is that no `vnproxctl spec` subcommand exposes it, so the config-as-code on-ramp exists but has no door. | S | — |
| T-4006 | **Freeze windows.** Time-based deny/warn rules in policy-as-code plus a change-calendar view; pairs with scheduled apply, which exists. | M | — |
| T-4007 | **Node maintenance mode.** Suppress findings/alerts for a node during a declared window, tied to the same calendar; suppressions are visible and expire. | S | T-4006 |
| T-4008 | **Policy packs on the hub.** Shareable policy-as-code bundles as a new signed registry artifact type — additive to the hub format, same signing/revocation story as blueprints. | M | DNS for hosted reach |
| T-4009 | **Air-gapped bundle.** Offline install bundle plus `vnproxctl hub mirror` for clusters with no outbound network — the demo daemon already proves the product runs without one. | M | — |
| T-4010 | **`vnproxctl watch`.** Live TUI over the WS events stream (additive event use only, per the D10 freeze) — findings, applies, drift as they happen, from a terminal. | M | — |
| T-4011 | **CLI machine surface.** `-o json` already covers ~12 subcommands — the gap is the remainder (policy, gitsync, certs, apply, changesets-review, peertrust), documented stable schemas, and shell completions, which are entirely absent. | S | — |
| T-4012 | **Audit/finding SIEM export.** Structured streaming export (syslog/JSONL) of the audit log and findings stream; vnprox stays not-a-warehouse by shipping events out, not storing more. | M | — |
| T-4013 | **Read-only SNMP switch counters.** Port errors/discards/utilization from LLDP-discovered switches painted on map edges. Read-only — the guarded-push boundary of `internal/switchdrv` is untouched. | L | — |
| T-4014 | **SPAN/mirror session manager.** `tc mirred` mirror sessions staged through the change engine, bounded and audited exactly like packet capture. | M | — |
| T-4015 | **First-class WireGuard tunnels (UI).** The changeset ops (`OpWgTunnelCreate` etc.) and a general tunnels API already exist — only the UI is federation-scoped (`ConnectClustersWizard`). The gap is a general creation surface, not backend machinery. | S | — |

## Phase 41 — Intelligence & envelope (11 items, T-4101–T-4111)

*Compositions over the primitives the product already has, plus the scale/verification envelope.*

| ID | Item | Size | Blocked on |
|---|---|---|---|
| T-4101 | **Anomaly-triggered capture.** A baseline anomaly arms a bounded, audited packet capture — opt-in, hard caps, and off by default; `internal/baseline` and `internal/capture` exist, the trigger wiring does not. | M | — |
| T-4102 | **Postmortem export.** Incident-mode timeline rendered to a Markdown/HTML postmortem via `internal/docexport` — the incident view becomes a document you can file. | S | — |
| T-4103 | **What-if capacity planner.** "Add N guests of profile X" evaluated against bandwidth headroom, IPAM exhaustion, and failure-impact simultaneously — a composition of `capacity` + `ipam` + `failsim`. | L | — |
| T-4104 | **Deterministic explainers.** Plain-language "explain this finding / this diff" generated from templates over typed data — no LLM backend required, so it works for every install. | M | — |
| T-4105 | **Permission explainer.** "Why can't I?" — resolve the current user's PVE ACL through vnprox's capability mapping and show exactly which privilege is missing for a denied action (D3 makes this fully determinable). | M | — |
| T-4106 | **Overlay readiness preflight.** Composed VXLAN/EVPN check before a zone apply: BGP sessions up, VTEP reachability, MTU headroom via `mtuprobe` — three existing signals, one verdict. | M | — |
| T-4107 | **Scale envelope.** A documented, perf-gated target (50 nodes / 5 000 guests): pvemock scale profiles, server-side topology tiling where measurement demands it, `perfbudget` gates so the envelope cannot silently regress. | L | — |
| T-4108 | **Nightly lab burndown.** `scripts/pve-lab.sh` promoted to a scheduled pipeline that works `needs-hardware-validation.md` automatically and files transcripts — ~150 open items stop depending on a human remembering. | M | Actions; lab teardown fix |
| T-4109 | **PTR completeness audit.** Reverse-DNS coverage check in the IPAM/DNS view — forward records exist via PowerDNS; nothing audits the reverse zones. | S | — |
| T-4110 | **LACP hash visualizer.** Which flows hash to which bond member. **Hardware-flagged:** real NICs and a physical switch; ships behind the standard "needs hardware validation" note with lab-simulated fixtures. | M | hardware |
| T-4111 | **eBPF datapath probes (exploratory).** Per-flow latency at the bridge without payload inspection. A time-boxed spike with an explicit go/no-go: it would be a new major dependency class, and the no-new-major-deps rule means the spike's deliverable is a recommendation, not a merge. | spike | owner: dependency call |

---

## Sequencing

```
Wave 0 (debt gate) ──────────► must clear before Phase 39 dispatch;
   T-2505-fu-01 (exp 09-15)     runs concurrently with Phase 38's
   T-3713      (exp 09-22)      owner-decision items (license, cut)
   tenant scoping, T-3712, T-3101-fu-01, T-3406-fu-02, AlertRules, plan index

Phase 38 ── T-3802 starts immediately (the history is already public;
   every day unscanned is a day exposed). T-3801, T-3803–T-3812
   parallel after. T-3808 blocked on Actions billing.

Phase 39 ── after Wave 0. Three parallel lanes:
   L2 truth:   T-3901 → T-3902 → T-3905 (after T-3712)
   Paths:      T-3903 → T-3904 → T-3906
   Map layers: T-3908 → T-3912 → T-3910 → T-3907; T-3909, T-3911 independent

Phase 40 ── T-4001 first (its stage-only contract shapes T-4002/T-4003);
   T-4004/T-4005/T-4011 parallel anytime; T-4006 → T-4007;
   T-4008 after DNS; T-4013–T-4015 parallel late.

Phase 41 ── compositions after their inputs stabilize; T-4107/T-4108
   run long and start early; T-4110 hardware-flagged; T-4111 is a spike.
```

**The critical path is decisions, not code.** Four owner calls gate more than any engineering
dependency: the exposure-remediation call once T-3802's scan reports (rotate / rewrite / accept),
Actions billing (T-3808, T-4108, Sigstore end-to-end), public DNS (T-4008 hosted reach, and the
already-built-but-unreachable docs site), and the eBPF dependency call (T-4111). The license
call, counted here in the first draft, was already made on 2026-08-06. Everything else is
dispatchable.

## Execution model (how this actually runs)

Same model that shipped Phases 37 and the gap work, stated once so every phase file can reference
it instead of repeating it:

- **Sonnet sub-agents implement; the coordinating agent (Opus) manages.** One card per agent,
  cards written to be executable without conversation context. The coordinator reviews diffs,
  runs the gates, sequences waves, and owns the final report.
- **Card authoring is itself Wave A of each phase.** The phase files
  (`planning/tasks/phase-{38..41}.md`) carry card *stubs* at Phase-37 fidelity; a card is
  expanded to full fidelity (deliverables, acceptance criteria, evidence obligations) by a sonnet
  agent immediately before dispatch, grounded in the code as it exists *then*, not as it existed
  when this roadmap was written.
- **Gates are unchanged:** `make ci` green before merge; every PVE-facing model change carries a
  `pvesh usage` transcript in `planning/reports/evidence/`; fixtures match pvecube, and when they
  disagree the fixture is wrong; pvecube stays read-only outside explicitly authorized deploys;
  nothing mutates network config outside `internal/change/`.
- **Batch size:** 3–5 concurrent agents per wave, matching what Phase 37 and the gap work
  sustained without merge conflicts. Independent lanes (see sequencing) can run as parallel waves.
- **Every wave ends with a deploy check** against `vnprox-dev` for anything peer- or PVE-facing,
  because the project's recurring failure mode — a claim nobody re-derived — is caught on the
  node, not in the mock.

## What is deliberately absent from the 50

Recorded so the next planner does not "rediscover" them: hosted public demo (owner decided NO,
2026-08-23, permanent); anything replacing the PVE firewall engine; long-horizon flow/metric
warehousing; payload inspection; general switch management beyond the guarded push; TLS cert
issuance/renewal; non-root operation; a third PVE node (nothing in the 50 claims quorum
behaviour); and the already-carded backlog listed in Wave 0.
