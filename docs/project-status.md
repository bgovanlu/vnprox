# vnprox — project status

**As of:** 2026-08-15 · **Latest release:** `v4.0.0` (tag `v4.0.0`, commit `5c19dcc`, 2026-08-14) · **Active arc:** Arc 6 — Earned (`docs/roadmap-earned.md`), Phases 29 and 30 shipped, Phase 31 next

Companion documents: [`status-matrix.md`](status-matrix.md) (the per-feature audit grid and its method) and [`datasheet.md`](datasheet.md) (shipped capability, for external readers).

---

## 1. Headline

vnprox is feature-complete against five shipped arcs — v1.0 through v3.5.0, plus the
"proven, not just built" arc, which closed as **v4.0.0** on 2026-08-14 (phases 20 and 21's
seven outstanding cards landed, taking the version number `docs/roadmap-proven.md` reserved
for the end of that arc; `v3.1`/`v3.2`/`v3.3` were never tagged individually — see CHANGELOG
`[4.0.0]`).

The current work is **Arc 6 — "Earned"** (`docs/roadmap-earned.md`, phases 29–33), scoped from
a 2026-08-15 audit that found several things v4.0.0's release note and `docs/security.md`
claim as true are not, in production. Phase 29 is in flight now — see `docs/roadmap-earned.md`
for the finding-by-finding detail and `planning/tasks/phase-29.md` for its six cards.

| Dimension | State | Basis |
|---|---|---|
| Feature delivery (Arcs 1–5) | **All scoped cards shipped, one AC left explicitly open** | 157 cards across Arcs 1–4 (`status-matrix.md` §2, frozen at `6c0957e`) plus 25 cards across Arc 5, phases 25–28 (§6.4 below); `T-2505` shipped with two acceptance criteria recorded as not met rather than faked closed |
| Backend implementation | **97% as of the pre-Arc-5 sweep** | `status-matrix.md` §2's 77-area matrix predates Arc 5 and was deliberately not re-run against the current tree — see that document's own note at its top |
| Automated test gate | `make check` green locally | Go + web tests, lint, vet, govulncheck, npm audit |
| CI | **Disabled, not running** | All three GitHub Actions workflows (`CI`, `Packaging matrix`, `Release`) are `disabled_manually`: GitHub Actions billing was exhausted 2026-08-11, and every trigger since then has failed on a payment error rather than a test result (CHANGELOG `[4.0.0]`, "Changed"). `scripts/ci-local.sh` reproduces every job in `ci.yml`/`packaging-matrix.yml` and is the actual gate on the dev host today. **This corrects a claim this document used to make in the other direction**: §6.2 below records `status-matrix.md` §5.7 correcting an earlier "CI is unfunded" claim as wrong, on 2026-08-08 — and that correction was itself accurate only until 2026-08-11 |
| Hardware validation | **~11%** | 15 validated against 127 open items, per `docs/roadmap-earned.md`'s 2026-08-15 audit (`planning/reports/needs-hardware-validation.md`) |
| Docs currency | **in progress, this pass** | This document, `status-matrix.md`, `datasheet.md`, `README.md`, and the doc indexes were rewritten 2026-08-15 for v4.0.0 (`T-2906`, phase 29); the truth/visibility gaps the audit found in the *product* (not the docs) are Phase 29–30 code cards, not this one |

The headline risk is unchanged in kind from the last two arcs: **the gap between "our tests
pass" and "this works, safely, on your cluster and in a real production browser" is still the
project's dominant risk.** Phase 29 is a direct response to that gap as of v4.0.0 specifically —
a shipped-but-CSP-blocked PWA and embed views, a peer host-write path with no safety validation
on the receiving side, bearer tokens that keep write capability after `read_only` is turned on,
and a hub-install path reachable to arbitrary root execution — closed before Phase 30 adds
anything new.

---

## 2. Arc and phase status

| Arc / phase | Phases | Target | Cards | Done | State |
|---|---|---|---|---|---|
| 1 — Visual network manager | 0–7 | v1.0 | 49 | 49 | ● Shipped |
| 2 — Beyond the cluster | 8–12 | v2.0 | 37 | 37 | ● Shipped |
| 3 — Universal networking tool | 13–17 | v3.0 | 35 | 35 | ● Shipped |
| 4 — Proven, not just built | 18–21 | v3.1 → v4.0 (**shipped as v4.0.0**; `v3.1`/`v3.2`/`v3.3` never tagged) | 26 | 24 | ● **Shipped, two cards rescheduled** — `T-2006` (i18n, no code) → `T-3106` and `T-2102`'s hosting half → `T-3301`; see the paragraph below |
| 22 — Online help | 22 | folded into v3.5.0 | 5 | 5 | ● Shipped |
| 23 — Certificate management | 23 | folded into v3.5.0 | 5 | 5 | ● Shipped |
| 24 — Operator leverage | 24 | folded into v3.5.0 | 10 | 10 | ● Shipped (two cards landed in a second pass — `planning/tasks/phase-24.md`) |
| 5 — Adoptable, not just proven | 25–28 | v3.5.0 | 25 | 25 | ◐ **Shipped, one AC explicitly open** (`T-2505`'s `--repeat-each=2` criterion — `planning/tasks/phase-25.md`) |
| 6 — Earned | 29–33 | v4.1 → v5.0 | 25 | 0 | ○ **Proposed 2026-08-15, Phase 29 in flight** — `docs/roadmap-earned.md` |
| **Total shipped** | | | **157 + 25 = 182** | **182** (Arc 4's 26 folds into the 157) | — |

**Arc 4's phase 21 line ("distribute it") is worth naming precisely**, because the v4.0.0
release note oversold it: `T-2101` (Terraform/Ansible cross-repo wiring), `T-2103` (compat
matrix), `T-2104` (registry capability-agreement gate), and `T-2105` (docs site) all shipped
code. `T-2102` (signed apt repository) shipped the **machinery**
(`packaging/build-apt-repo.sh`, `packaging/apt-repo.md`) but not a hosted repository — the
thing an operator actually needs in order to `apt install` anything from the internet. That
gap, and `T-2006` (i18n, phase 20, zero code in the tree), were **not descoped on the record**
when v4.0.0 shipped claiming "phases 20 and 21 complete." Both are now rescheduled —
`T-2006` as `T-3106`, `T-2102` as `T-3301` — in `docs/roadmap-earned.md`, and this paragraph is
that correction going on the record.

---

## 3. Open items

The open-items backlog that used to live in this section is **superseded by
[`docs/roadmap-earned.md`](roadmap-earned.md)**, which states plainly: "this document
supersedes the open-items sections of every earlier roadmap." Every item that was open here as
of the previous revision of this section is now either shipped (§2 above, §6 below) or absorbed
into a Phase 29–33 card — `roadmap-earned.md`'s "Where every leftover went" table is the
authoritative cross-reference; do not re-derive it here.

The six cards in flight right now, most urgent first (`planning/tasks/phase-29.md`):

| # | Card | Item | Pri |
|---|---|---|---|
| 1 | `T-2901` | Un-break the PWA and the embeds: CSP/headers vs. shipped features | P0 |
| 2 | `T-2902` | Peer host-write safety parity + audit attribution (incl. source IP) | P0 |
| 3 | `T-2903` | Bearer tokens honor `read_only`; token expiry; `token.use` flood control | P0 |
| 4 | `T-2904` | Hub plugin install hardening (endpoint constraints, config-gated trust) | P0 |
| 5 | `T-2905` | Auth/daemon hardening punch list (sessions, limiter, SSRF, timeouts, perms) | P1 |
| 6 | `T-2906` | Documentation truth pass + single doc index — this card | P1 |

One hard external deadline holds regardless of phase order: the `scale.spec.ts` quarantine
entry (`web/e2e/quarantine.json`, `T-2505-followup-01`) **expires 2026-09-15**; an expired
quarantine fails the build.

---

## 4. What is genuinely strong

Worth stating plainly, because an audit that only lists gaps misrepresents the artifact. This
section predates Arc 5 and Arc 6 and none of it has been contradicted since — if anything, Arc 5
added more of the same shape (a policy engine that refuses, a canary apply that pauses, a
two-person rule enforced server-side).

- **Every safety invariant the product claims has a test whose failure is loud.** An AI operator
  cannot apply a change — not by policy but because adding a mutating tool name makes a guard
  test *panic*. A plugin's capability scope is a ceiling the installer enforces. Peer TLS cannot
  silently fall back to the system trust pool. Certificate scanning cannot read a private key,
  because the type has nowhere to put one. See `status-matrix.md` §4 — eleven invariants, none
  failing, as of that document's own commit.
- **The v3.0 API contract has held**, and grew a machine-readable form (`docs/openapi.json`,
  `T-2405`) rather than breaking. Frozen, additive-only, and enforced by a gate.
- **Documentation is unusually honest about its own gaps**, including this one: Arc 6 exists
  specifically because a docs audit found the honesty had lapsed for a couple of releases, and
  this document is one of the corrections.
- **Recent process discipline held under pressure.** A speculative CI fix was written, failed to
  reproduce at three sizes, and was **reverted rather than shipped** (Arc 4). `T-2505` shipped
  with two acceptance criteria explicitly unmet rather than narrated as done (Arc 5). `T-2505-followup-02`
  was root-caused a second time, correctly, after a first diagnosis turned out to name a fix that
  was already present (v4.0.0's own "Fixed" section, CHANGELOG).

---

## 5. Trajectory and recommended sequence

The recommended order is now Arc 6's own phase order, stated in full in
[`docs/roadmap-earned.md`](roadmap-earned.md) — this section does not restate it, to avoid a
second place for it to go stale. In short:

1. **Phase 29 first** ("Make v4.0 true") — several of its six cards are live defects or open
   security gaps in the release currently deployed: the PWA/embed CSP break, the unvalidated
   peer host-write path, bearer tokens ignoring `read_only`, and the hub-install exec path.
   Everything after Phase 29 builds on a release whose claims are true.
2. **Phase 30** ("The visible product") — ~19 backend feature areas with zero frontend client
   get their screens.
3. **Phase 31** ("All of Proxmox networking") — PVE 9's SDN Fabrics and VNet-scope firewall,
   currently unmodeled.
4. **Phase 32** ("Proven on iron") — before Phase 33, because nothing should go public carrying
   a safety claim (commit-confirm) that has never been observed working on real hardware.
5. **Phase 33** ("In the world") — CI decision, a real apt host, public repo, docs site,
   security-disclosure contact, hosted demo.

**One hard external deadline, phase order notwithstanding:** the `scale.spec.ts` quarantine
entry expires 2026-09-15 (§3 above); `T-3204` must resolve or consciously renew it before then.

---

## 6. Delivery history

Dated snapshots, kept in order rather than rewritten in place. §1–§5 above are the current
state; everything below is historical record — read a date range's own paragraph as true as of
that date, not as of today.

### 6.1 2026-08-05 to 2026-08-06 — deploy-time validation, phases 22–23

| Date | Change |
|---|---|
| 2026-08-05 | Deploy-time validation: **5 items moved from unvalidated to validated** (CA path, migration chain, `backup`, `support-bundle` redaction, pmxcfs permissions) |
| 2026-08-05 | `T-1906-bug-01` filed — pinned peer TLS vs. a stale IP SAN, found on hardware |
| 2026-08-05 | Phase 22 shipped: online help on all 26 screens, with an enforced coverage gate |
| 2026-08-06 | Phase 23 shipped: certificate management, and `T-1906-bug-01` **fixed** |
| 2026-08-06 | This audit: `LICENSE` gap and `docs/features.md` staleness identified — neither previously tracked |
| 2026-08-06 | `T-2106` (Apache-2.0 + attribution) and `T-2107` (`features.md`) closed; e2e gate landed observe-only, `T-2108` filed |
| 2026-08-07 | `T-2108` and `T-1806-bug-01` closed: e2e suite green (89 passed / 0 failed) and the job is required. `T-2003-bug-01` root-caused and fixed. Ten product defects found by the gate, four of which had green unit tests over invented fixtures |
| 2026-08-06 | `T-1807-bug-02`: enforced port registry closes the collision class that had recurred five times in one phase. Also eliminated two candidate explanations for `T-1806-bug-02` (recorded on that card so they are not re-derived) |
| 2026-08-06 | `T-1904` (`vnproxctl doctor`) shipped — **phase 19 complete**. Ten checks, remediation structurally enforced; two follow-ups filed rather than left implicit |

### 6.2 Phase 24 — operator leverage (added 2026-08-08)

A full-stack audit at `7a8ef6d` found the binding constraint had moved from *"can vnprox do this"*
to *"can one operator stay on top of what vnprox is telling them"*. Ten cards followed —
[`docs/roadmap-leverage.md`](roadmap-leverage.md), [`planning/tasks/phase-24.md`](../planning/tasks/phase-24.md).

**Three candidates were dropped on contact with the code, and that is worth recording** because
each had a plausible-sounding case for existing:

| Dropped | Why |
|---|---|
| Post-confirm revert of a committed changeset | **Already ships** — `Service.Rollback` builds a restoring draft from the changeset's own pre-apply snapshot (`apply_restore.go`) |
| Standalone map SVG/PNG export | **Already ships** — `web/src/topology/ExportMapMenu.tsx`. `features.md` still calls it a known gap at T-607; that line is stale, not the product |
| Four-eyes approval | **Already ships** — `ApprovalPolicy.AllowSelfApproval`, enforced server-side |

**Delivery:** six cards shipped in the first pass (`T-2401`, `T-2402`, `T-2403`, `T-2404`,
`T-2408`, `T-2410`), one partial (`T-2406`), three deferred; a second pass on 2026-08-09 shipped
two more (`T-2405`, `T-2407`) and closed `T-2411` (untracking the built binaries from git).
`T-2409` (per-spec e2e store isolation) remains **built but not merged** — it works and is
proven, but misses its own wall-clock and green-suite acceptance criteria; the branch is
`t-2409-e2e-store-isolation`. Full per-card notes, the packaging-job root cause
(`T-1806-bug-02`, a `SIGPIPE`/`pipefail` race in a `grep -q` pipeline that failed *when the
pattern matched*), and the two GitHub-Actions-billing corrections are in
`planning/tasks/phase-24.md`'s own delivery-record sections — not restated here to avoid a
second place for them to go stale.

### 6.3 Arc 5 — adoptable, not just proven (planned 2026-08-10)

A second full-stack audit at `42ba175` scoped a fifth arc. Its organising finding is that the
headline figures in the then-current §1 disagreed with each other in a specific way: **feature
delivery 91%, backend implementation 97%, hardware validation 9%, external consumers 0.** Adding
a sixth networking domain does not move any of those; the remaining value is in *assembly and
proof*.

Twenty-five cards across four phases — [`docs/roadmap-adopted.md`](roadmap-adopted.md),
[`planning/implementation-plan-adopted.md`](../planning/implementation-plan-adopted.md):

| Phase | Theme | Cards | Question it answers |
|---|---|---|---|
| [25](../planning/tasks/phase-25.md) | Proof that runs itself | 6 | Can the 9% figure move without a human on a ladder? |
| [26](../planning/tasks/phase-26.md) | Guardrails | 5 | Can the change engine refuse a bad change, not just narrate it? |
| [27](../planning/tasks/phase-27.md) | Config as code | 6 | Can the cluster's network live in git and stay there? |
| [28](../planning/tasks/phase-28.md) | Adoption | 8 | Can someone who has never met us run this? |

**Three candidates were dropped on contact with the code**, as three were in phase 24 —
failure-impact analysis (`internal/failsim`, T-1604), explainable posture scoring
(`internal/posture`, T-1607), and traffic-baseline anomaly detection (`internal/baseline`,
T-1601) all already ship.

`T-2505` subsumes the open `T-2409` and inherits its unfinished investigation, including the two
hypotheses already refuted and recorded, so they are not re-derived a third time.

### 6.4 Arc 5 — delivery (2026-08-13)

All 25 cards across phases 25–28 shipped, merged to `main`, and cut as **v3.5.0**. "Shipped" here
means each card delivered what its own acceptance criteria named — for six of the ten Phase 26/27
backend cards below, and for `T-2604`'s break-glass control, what was named did not include a
`web/src` client, so a card can legitimately be `● Shipped` and still be unreachable except by
`curl`/CLI. That gap, not a shipping defect, is what `docs/status-matrix.md` §7 and Arc 6's Phase
30 exist to close.

**Checked directly against `web/src` 2026-08-19 (debt sweep), not assumed from an earlier claim
about which of these had no client.** Five of the six originally-headless cards, plus `T-2604`'s
break-glass, gained a real `web/src` caller in Phase 30 (2026-08-16) — `T-2601` policy-as-code
(`web/src/governance/PoliciesPanel.tsx`), `T-2602` canary apply and `T-2603` auto-rollback
(`web/src/changesets/ApplyStrategyPanel.tsx`, `RolloutPanel.tsx`), `T-2701` git spec sync
(`web/src/drift/GitSyncPanel.tsx`), `T-2706` compliance mapping
(`web/src/governance/CompliancePanel.tsx`), and `T-2604`'s break-glass
(`web/src/changesets/BreakGlassPanel.tsx`). **One remains genuinely headless**: `T-2702`
(changeset → pull request, `POST /changesets/{id}/propose`) — confirmed by grep against `web/src`
that no caller exists; Phase 30's `T-3001` covered `[gitsync]`/spec/drift, not this route, and
never claimed to. See `docs/status-matrix.md` §7 for the row-by-row correction.

| Card | Item | State |
|---|---|---|
| `T-2501` | Self-executing hardware validation suite (`vnproxctl verify`, 26 checks) | ● Shipped |
| `T-2502` | Record/replay real PVE traffic into fixtures | ● Shipped — no cassette here is from real PVE hardware yet, stated in the cassette directory's own name (`mock-three-node-vlan`) |
| `T-2503` | Opt-in compatibility telemetry (`vnproxctl telemetry`) | ● Shipped |
| `T-2504` | Nightly soak and resource-leak gate (`make soak`) | ● Shipped |
| `T-2505` | E2E sharding, isolation, and flake quarantine | ◐ **Shipped with two ACs explicitly not met, recorded rather than faked closed**: one of the four original failures is bisected but its mechanism is unexplained and the spec is quarantined (`web/e2e/quarantine.json`, expires 2026-09-15, `T-2505-followup-01`); `--repeat-each=2` still fails because most specs assume a fresh store, which shard-level isolation doesn't provide. See `planning/tasks/phase-25.md`'s delivery record and `status-matrix.md` §5.11 |
| `T-2506` | Performance regression budget gate (`make perf`) | ● Shipped |
| `T-2601` | Policy-as-code guardrails at the validate stage | ● Shipped |
| `T-2602` | Canary / staged multi-node apply | ● Shipped |
| `T-2603` | Finding-triggered auto-rollback inside the confirm window | ● Shipped — closes T-2602's `gate: auto` gap by wiring the canary health checker into `cmd/vnproxd` |
| `T-2604` | Enforced two-person rule on protected op classes | ● Shipped |
| `T-2605` | Post-apply topology preview | ● Shipped |
| `T-2701` | Git-backed spec sync | ● Shipped |
| `T-2702` | Changeset → pull request | ● Shipped |
| `T-2703` | Drift-to-git reconciliation | ● Shipped |
| `T-2704` | Point-in-time topology diff | ● Shipped |
| `T-2705` | Mutating MCP tools that stage, never apply | ● Shipped |
| `T-2706` | Compliance profiles and evidence export | ● Shipped — one general profile, explicitly not a certification claim |
| `T-2801` | One-command install and built-in demo mode | ● Shipped |
| `T-2802` | Hosted read-only demo and guided tour | ◐ **Corrected 2026-08-19 (debt sweep): still ◐, now for a different, smaller reason.** `--public-demo` and its edge, session isolation, rate caps, and guided tour were always built and tested; **a real hosted instance now exists** at `demo.vnprox.com` (`pve001`, Arc 6's `T-3303`, 2026-08-18). Still not `●` only because `demo.vnprox.com` doesn't resolve publicly yet (VPS DNS, owner-deferred) — see `planning/tasks/phase-33.md` |
| `T-2803` | Hosted signed registry for blueprints and plugins | ◐ **Corrected 2026-08-19 (debt sweep): this row was wrong, and inconsistent with itself.** It previously read `● Shipped` while its own note described the identical unhosted-instance gap this table marks `T-2802` `◐` for — `planning/tasks/phase-28.md:253` already flagged that self-contradiction. It is now scored `◐` for consistency. **The underlying gap is since closed anyway**: a real hosted registry now exists at `registry.vnprox.com` with all four `T-2104` seed blueprints published through the real `vnproxctl hub publish`/`hub index` pipeline (`T-3303`, 2026-08-18); it carries the same `◐`-for-DNS reason as `T-2802` now, not the original "no instance" reason |
| `T-2804` | Incident mode | ● Shipped |
| `T-2805` | Multi-user presence and changeset locking | ● Shipped — locks and presence are node-local; a peer-API fan-out for cross-node presence is a stated, unfilled gap |
| `T-2806` | Map annotation layer | ● Shipped |
| `T-2807` | Scheduled digest reports | ● Shipped — the API route (`GET`/`PUT /digest/schedule`) landed in a follow-up commit after the card's first pass, since no acceptance criterion named one and three other cards were touching `docs/openapi.json` at the time |
| `T-2808` | In-app assistant over the MCP read tools | ● Shipped |

**Two real product defects surfaced during this arc.** Both disclosed on their originating
cards rather than fixed silently, and one has since closed:

- `T-2505-followup-02`, the guest-interior panel's stuck error state, is **fixed as of v4.0.0**
  (2026-08-14) — see §6.5 below. The original diagnosis (missing cache invalidation) was wrong;
  the invalidation was already present. The real cause was a `TanStack Query v5` `undefined`
  sentinel being treated as an error state.
- `scale.spec.ts › v2 canvas renderer` still fails only after two specific preceding specs in
  the same browser process — reproducible, but its mechanism remains unexplained
  (`T-2505-followup-01`, quarantined, expires 2026-09-15).

**What §6.3–6.4 do not claim.** §1's headline figures are a mechanical sweep against a specific
commit, predate Arc 5 and Arc 6, and are not recomputed here — recomputing them needs the same
commands `status-matrix.md` §6 describes, re-run against the current tree, which no card in
either arc has done. What *is* true without re-running anything: `T-2501` makes the
hardware-validation figure *movable* by someone with a cluster — it does not move it itself,
because no card can validate hardware without hardware — and `T-2801`/`T-2802` answer "can
someone who has never met us run this" for the *installed* case (yes) while leaving the
*hosted-demo* case exactly where `T-2803`'s registry hosting also sits: designed, tested, and
not actually deployed anywhere.

### 6.5 v4.0.0 release and Arc 6 proposed (2026-08-14 to 2026-08-15)

**v4.0.0 tagged 2026-08-14** (commit `5c19dcc`): phases 20 and 21's seven outstanding cards
(three in phase 20, four in phase 21) landed, closing Arc 4 under the version number
`docs/roadmap-proven.md` reserved for the end of that arc. No schema break, no API break — an
upgrade from any 3.x is an ordinary package upgrade (migrations run through 0046). The same
release recorded that GitHub Actions billing had been exhausted since 2026-08-11 and all three
workflows were switched to `disabled_manually`, and fixed the `T-2505-followup-02` guest-interior
defect (see §6.4).

**A 2026-08-15 four-dimension audit** (features, docs, security, planning leftovers) found that
several things v4.0.0's own release note and `docs/security.md` claim as true are not, in
production — a CSP that blocks the PWA/service-worker and embed views it also shipped, a peer
host-write path with no receiving-side validation, bearer tokens that outlive `read_only`, a hub
install path reachable to arbitrary root execution, and a documentation set that had stopped
being updated two releases back (this document included). That audit is
[`docs/roadmap-earned.md`](roadmap-earned.md), Arc 6. Phase 29 ("Make v4.0 true") is in flight;
`T-2906`, this document's own rewrite, is one of its six cards.
