# Phase 41 — Intelligence & envelope

**Arc:** *the primitives already exist; this phase composes them into verdicts an operator can act
on, and puts a measured, gated envelope around the whole product.*

## Premise

Every card in this phase reads at least two existing packages and produces one new, narrower
thing: a trigger, a rendered document, a verdict, an explanation, a measured limit. None of the
eleven introduce a new subsystem the way Phases 39–40 do — the risk here is not "nothing to build
on", it is duplicating a composition that's subtler than it looks, or building a second version of
something that turns out to already exist under a different name. Card authoring for this phase
therefore leaned harder than usual on reading the actual code before writing each card; several
cards below correct an assumption the roadmap's one-line description would otherwise have carried
into dispatch (noted per card as **Repo fact**).

Cards here are **stubs at Phase-37 fidelity**: a 2–4 sentence summary, 3–6 deliverable bullets
naming real files/packages, 2–4 checkable acceptance criteria. Per the roadmap's "Execution model",
each is expanded to full fidelity by a sonnet agent immediately before dispatch, grounded in the
code as it exists *then* — this file is not that expansion.

---

### T-4101 · Anomaly-triggered capture
**model:** sonnet-5 · **size:** M · **depends:** —

A `baseline.Anomaly` (internal/baseline, T-1601's deviation detector) arms a bounded packet
capture via `internal/capture` (T-1301) instead of an operator manually starting one after the
fact. `internal/capture`'s doc.go already states the contract this trigger must inherit exactly:
gated on the `capture` capability, server-enforced hard caps (time/size/packet-count) that a
request can only lower, audited start/stop, opt-in. There is currently no wiring between the two
packages at all — `grep -rl baseline internal/capture` returns nothing.

**Repo fact:** capture's caps and audit are already non-negotiable at the `Coordinator` level, so
this card is pure wiring (an anomaly becomes a capture *request* through the existing `Coordinator.
Start` path), not a new enforcement layer — a dispatch agent reimplementing caps/audit here would
be duplicating `internal/capture/coordinator.go`.

**Deliverables**
- A new small adapter (likely `internal/baseline` exposing an `Anomaly`-to-capture-request mapping,
  wired at the `cmd/vnproxd` composition root, matching how `internal/findings/adapt_baseline.go`
  already adapts `Anomaly` into a `Finding`) that calls `capture.Coordinator.Start` when armed.
- A config gate: anomaly-armed capture is **off by default**, a distinct opt-in from capture's
  general availability, per this phase's own framing.
- Audit entries distinguishing an anomaly-armed capture from an operator-started one (actor =
  "system:baseline" or equivalent, referencing the triggering `Anomaly`/`Finding` id).
- Tests: arming fires within the anomaly's Ref scope only, caps are unchanged from a manual
  session's caps, and the feature stays inert with the gate off.

**Acceptance criteria**
1. Off by default; enabling it requires an explicit config key, verified by a test that asserts no
   capture starts on an anomaly when the key is unset.
2. Every anomaly-armed session is capped and audited identically to a manual one — no new cap path.
3. The triggering anomaly/finding id is recoverable from the audit row.

---

### T-4102 · Postmortem export
**model:** sonnet-5 · **size:** S · **depends:** —

Render an incident's timeline (`internal/incident`, read-only view of what happened) to a
Markdown/HTML postmortem document via `internal/docexport`.

**Repo fact — this is not `internal/incident/export.go`.** That file already exports an incident,
but into a support-bundle archive (`backup.Bundle`, the redaction path shared with every other
bundle producer) — an operational artifact for filing a support case, not a readable document. It
is unrelated to this card and must not be mistaken for prior art on the same deliverable.
`internal/docexport`, separately, already renders three dual-format (Markdown+HTML) artifacts this
way — config doc (T-605), posture report (T-1607), compliance report (T-2706, see
`internal/docexport/compliance.go`'s own doc comment: "extends this package's existing dual-format
machinery ... with a third artifact rather than introducing a parallel renderer") — so the
postmortem is docexport's **fourth** artifact in that same machinery, not a new renderer shape.

**Deliverables**
- `internal/docexport/postmortem.go` (or similarly named): `Data` gathering from
  `internal/incident`'s `Timeline`/`service.go`, `Render`-style Markdown+HTML functions matching
  the compliance.go pattern (one `Data`, two pure rendering functions).
- Wired into whatever incident already exposes as its "close" action, alongside (not replacing) the
  existing support-bundle export.
- A golden test in the same shape `docexport`'s other three artifacts use.

**Acceptance criteria**
1. Postmortem Markdown and HTML render from one `Data` value with no structural drift between them
   (the same property the other three artifacts hold, checked the same way).
2. The existing incident support-bundle export (`internal/incident/export.go`) is untouched.
3. The rendered document includes the incident timeline, not a re-derivation of it.

---

### T-4103 · What-if capacity planner
**model:** sonnet-5 · **size:** L · **depends:** —

"Add N guests of profile X" evaluated simultaneously against bandwidth headroom
(`internal/capacity`), IPAM exhaustion (`internal/ipam`), and failure-impact
(`internal/failsim`) — one verdict from three signals that today only answer their own question.

**Repo fact:** none of the three currently does what-if evaluation. `internal/capacity` only
trends *observed* history forward (`Forecast`/`Analyze` fit a linear trend over retained
rollups — see `forecast.go`'s doc comment); there is no "what if N more guests" input surface
anywhere in the package. `internal/ipam` and `internal/failsim` are likewise both read/observe
today. This card is genuinely new composition, not a wrapper around an existing what-if primitive.

**Deliverables**
- A new package or a clearly-scoped addition (likely `internal/capacity` gains a
  `WhatIf(profile, n, ...)` entry point, or a new small package composing the three, per
  CLAUDE.md's "no new major dependencies" — this is composition of existing internal packages, not
  a new dependency) that takes a guest-profile + count and projects: capacity headroom consumed
  (reusing `capacity.Forecast`'s math against a synthetic added load), IPAM pool exhaustion
  (reusing `internal/ipam`'s pool/allocation types), and worst-case failure impact of the resulting
  footprint (reusing `internal/failsim/impact.go`).
- API surface exposing one combined verdict, not three separate calls a client must reconcile.
- Tests: a scenario that is fine on all three axes, and one that fails on exactly one axis, proving
  the axes are independently reported rather than collapsed into a single pass/fail.

**Acceptance criteria**
1. One request returns a combined verdict citing all three signals by name.
2. No persisted "what if" state — this is evaluate-and-discard, consistent with capacity's existing
   "not a warehouse" stance (`internal/capacity/doc.go`'s bounded-retention framing extends to this,
   not more retained state).
3. `internal/capacity`'s existing `Forecast`/`Analyze` behavior is unchanged for real (non-what-if)
   callers.

---

### T-4104 · Deterministic explainers
**model:** sonnet-5 · **size:** M · **depends:** —

Plain-language "explain this finding / this diff" generated from templates over typed data — no
LLM backend, so it works on every install.

**Repo fact — findings' `Detail` field is already free text, not typed data.** `internal/findings.
Finding.Detail` (`internal/findings/types.go`) is a `string` the producer renders once; there is no
structured detail struct underneath it to template over. The stable, typed handle a template can
actually key off is `Finding.Check` (the check id, e.g. `lacp_partner_mismatch`) cross-referenced
against `internal/findings/catalog.go`'s `allCheckNames` vocabulary — which today is a flat name
list with no descriptive metadata attached, not a template table. So "explain this finding" needs a
new per-check template registry keyed on `Check`, not a reformatting of `Detail`. By contrast,
change-engine diffs (`internal/change`'s `Op`/diff types) are genuinely typed already, so "explain
this diff" can template directly over those structs without this problem.

**Deliverables**
- A new template registry (likely `internal/findings` gains an `Explain(check string) Explainer` or
  similar, or a new small package) mapping each `catalog.go` check name to a plain-language
  template — parameterized by the finding's `Refs`/`Nodes`/`Severity`, not by parsing `Detail`.
- A parallel explainer for changeset diffs over `internal/change`'s existing typed `Op` structs.
- `catalog_test.go`'s existing guard pattern (parse-and-cross-check) extended or mirrored so a check
  added later cannot silently ship without an explainer — or an explicit, tracked exemption list if
  full coverage isn't feasible in one card.
- No network call, no model client, no API key config — verified by a test asserting the package
  imports nothing network-capable.

**Acceptance criteria**
1. Every catalog check name resolves to a non-empty explanation, or is on an explicit, tested
   exemption list.
2. Works with zero configuration on a fresh install (no LLM backend to reach).
3. A diff explanation reads from `Op` fields directly, never from a re-parsed string.

---

### T-4105 · Permission explainer
**model:** sonnet-5 · **size:** M · **depends:** —

"Why can't I?" — resolve the current user's PVE ACL through vnprox's own capability mapping and
name exactly which privilege is missing for a denied action.

**Repo fact — the capability mapping is `internal/auth/caps.go`.** `BuildCapabilities(perms
pve.Permissions, nodes []string) map[string]Capabilities` is the D3 derivation named in the
roadmap: PVE ACL privileges → `{netRead, netWrite, sdnRead, sdnWrite, fwRead, fwWrite, guestNet,
audit}` per node, re-derived hourly (`internal/auth/doc.go`). Enforcement is
`middleware.go`'s `RequireCap(cap)`, downstream of `SessionMiddleware`/`CSRFMiddleware`. This is
the exact code to compose, not rebuild: the explainer is a *read* over the same derivation
`RequireCap` already consults, run on-demand for a hypothetical action instead of at request time.

**Deliverables**
- A new read endpoint/service (likely `internal/auth` gains an `Explain(action, node string)`
  method, or a thin `internal/api` handler over `BuildCapabilities`'s existing output) that, given a
  denied action, names the missing capability and — where derivable — the PVE privilege that would
  grant it.
- Wired to whatever surfaces a 403 today so the UI can offer "why?" without a second round trip
  reimplementing the derivation.
- Tests: a user missing exactly one capability gets a precise answer; a user missing several gets
  all of them, not just the first found.

**Acceptance criteria**
1. The explanation is derived from `caps.BuildCapabilities`'s live output, not a duplicated copy of
   the ACL→capability table.
2. Read-only: calling the explainer never re-derives or caches capabilities outside the existing
   hourly cycle.
3. Matches D3: the answer is fully determinable from data vnprox already holds, no PVE round trip
   beyond what `BuildCapabilities` already requires.

---

### T-4106 · Overlay readiness preflight
**model:** sonnet-5 · **size:** M · **depends:** —

Composed VXLAN/EVPN check before a zone apply: BGP sessions up (`internal/evpn`), VTEP reachability,
measured MTU headroom (`internal/mtuprobe`) — three existing signals, one verdict, surfaced at
**validate time** in the change engine.

**Repo fact — the insertion point is `internal/change/validate_sdn.go`, and it already does a
degraded version of one-third of this.** `sdnValidate`'s MTU check (`underlayMTU`/`vxlanOverhead`
constants) is explicitly flagged in its own doc comment as an *assumed* default (1500), not
measured: "resolving the actual path MTU to every configured peer address ... is host-reader/
collector work no task before T-402 does" — which is precisely what `internal/mtuprobe` now is.
This card upgrades that assumption to `mtuprobe`'s measured `Result`, and adds BGP session state
(`internal/evpn.Service`, already tracks flap history and session state via FRR) and VTEP
reachability as two new checks alongside it. **Do not confuse this with `internal/change/
preflight.go`** — that file is a different, already-shipped composition (T-1604's failsim veto)
hooked into the *scheduler* for unattended applies, not into validate; it establishes the
injected-interface pattern to follow (`ImpactPreflighter`-style seam, concrete impl at the
`cmd/vnproxd` composition root) but is not itself extended by this card.

**Deliverables**
- Extend `sdnValidate` (or add a sibling validator called from the same place) with a new seam
  interface — matching `preflight.go`'s `ImpactPreflighter` shape — for BGP-up, VTEP-reachable, and
  measured-MTU-headroom, backed by `internal/evpn` and `internal/mtuprobe` at the composition root.
- Fall back to the existing assumed-MTU math when `mtuprobe` has no measurement yet for a given
  link, rather than blocking validation on missing data.
- A single combined "overlay readiness" verdict attached to the changeset's validate response, not
  three separate warnings a client must correlate.
- Tests: BGP down blocks with a named reason; MTU headroom present uses the measured value over the
  assumed default; no regression to non-VXLAN/EVPN zone validation.

**Acceptance criteria**
1. Surfaces at validate time only — no new apply path, per this phase's explicit scope line.
2. When `mtuprobe` has a measurement for the relevant link, it is used in place of the assumed
   underlay-MTU constant; the assumed default remains the fallback.
3. A BGP session down or VTEP unreachable produces a validate-time finding naming which signal
   failed, not a generic "overlay not ready".

---

### T-4107 · Scale envelope
**model:** sonnet-5 · **size:** L · **depends:** —

A documented, perf-gated target (50 nodes / 5 000 guests): pvemock scale profiles, server-side
topology tiling where measurement demands it, `internal/perfbudget` gates so the envelope cannot
silently regress.

**Repo fact — `internal/pvemock` has no scale/size profiles today.** `grep -rn "scale|Scale"
internal/pvemock/*.go` turns up nothing but incidental matches (a ticket-lifetime comment). What
the package *does* have is `compat_versions.go`'s `PVEVersionProfile` — a fixed table of PVE
*version* behavior (which endpoints a release line supports), unrelated to cluster *size*. So the
scale profiles this card needs (a mock fixture set with N nodes / M guests, generated rather than
hand-authored) are genuinely new, not an existing knob to flip on. `internal/perfbudget` (budget.go,
calibrate.go, gate.go) already exists and is the right place to register the new envelope's gates —
read it before adding a parallel gating mechanism.

**Deliverables**
- A scale-profile generator for `internal/pvemock` (e.g. `internal/pvemock/scaleprofile.go`) that
  synthesizes a fixture at a given node/guest count, distinct from `compat_versions.go`'s version
  profiles.
- Perf gates registered in `internal/perfbudget` for the 50-node/5000-guest target on the operations
  that matter most under load (topology render, findings sweep, changeset validate) — using
  `perfbudget`'s existing `budget.go`/`gate.go` machinery, not a new one.
- Server-side topology tiling (or a documented decision that it isn't needed yet, backed by the
  measurement) if the perf gate shows the flat render doesn't hold at target scale.
- `docs/` gains the documented envelope number itself — this card's other deliverables exist to
  make that number defensible, not just asserted.

**Acceptance criteria**
1. A generated pvemock fixture at 50 nodes/5000 guests exists and is exercised by at least one perf
   gate test.
2. `perfbudget` gates fail the build if the envelope regresses — demonstrated by intentionally
   breaking one and showing the gate catches it.
3. If tiling is added, it's proven necessary by a measurement in the gate output, not added
   speculatively.

---

### T-4108 · Nightly lab burndown
**model:** sonnet-5 · **size:** M · **depends:** Actions billing restored; lab teardown exercised
against a populated lab first

`scripts/pve-lab.sh` promoted to a scheduled pipeline that works `needs-hardware-validation.md`
automatically and files transcripts, so ~150 open items stop depending on a human remembering to
run them.

**Repo fact — `pve-lab.sh down` is explicitly untested against a populated lab today**, per the
script's own header comment: "`down` has NOT been exercised on a populated lab yet — that path is
still [untested]." A nightly pipeline that builds a lab, runs validation, and tears down
unattended will hit that untested path on every single run, unsupervised — this is a real
precondition, not a formality, and it must be exercised and fixed (or the pipeline must run `down`
under supervision first) before this card can be called done. The script is otherwise complete
(`up`/`join`/`status`/`down` subcommands, ISO staging checks) at `scripts/pve-lab.sh` (24 KB).

**Deliverables**
- A GitHub Actions workflow (blocked on the owner's Actions billing restoration, per Wave 0) that
  runs `pve-lab.sh up` → the destructive subset of `needs-hardware-validation.md` → `pve-lab.sh
  down` on a schedule, against the disposable nested lab only (never `vnprox-dev`).
- `pve-lab.sh down` exercised at least once against a fully populated lab before this ships, with
  fixes for whatever that exposes, and the script's own header comment updated once it's no longer
  true.
- Transcript filing: each run's results land in `planning/reports/evidence/` and update
  `needs-hardware-validation.md`'s open items automatically, not by hand.

**Acceptance criteria**
1. `pve-lab.sh down` is proven safe against a populated lab (evidence, not the current untested
   claim) before the nightly schedule is turned on.
2. A scheduled run only touches the disposable lab; `vnprox-dev` is never a target.
3. `needs-hardware-validation.md`'s open count reflects the automated runs without manual editing.

---

### T-4109 · PTR completeness audit
**model:** sonnet-5 · **size:** S · **depends:** —

Reverse-DNS coverage check in the IPAM/DNS view: forward records exist via PowerDNS
(`internal/sdn/dns.go`, `internal/pve/sdn_dns.go`, `internal/inventory/dns.go`); nothing today
audits whether the reverse zones match.

**Repo fact:** PowerDNS integration is real and already models A/AAAA/PTR/CNAME/TXT record types
(`internal/pve/sdn_dns.go`: "SDNDnsRecord is one A/AAAA/PTR/CNAME/TXT record"), so PTR is already a
known record kind — but a grep for PTR-specific logic beyond that type comment turns up nothing:
no existing check compares forward (A/AAAA) coverage against reverse (PTR) coverage. This is a new,
fairly small check, not a rename of something that already runs.

**Deliverables**
- A new check (likely `internal/findings`, alongside the other `health_*.go` checks, e.g.
  `health_ptrcoverage.go`) that, for each forward record vnprox knows about via
  `internal/inventory/dns.go`, verifies a matching PTR exists in the corresponding reverse zone
  read through the same `internal/sdn/dns.go` / PowerDNS path.
- A finding raised for a forward record with no PTR, or a PTR pointing somewhere the forward record
  doesn't match (dangling/mismatched).
- Surfaced in the IPAM/DNS view alongside existing DNS-adjacent data, not a separate page.

**Acceptance criteria**
1. A forward record with a correct matching PTR raises nothing.
2. A forward record missing its PTR, and a stale/mismatched PTR, both raise distinct findings.
3. Uses the existing PowerDNS read path (`internal/sdn/dns.go`) — no second DNS client.

---

### T-4110 · LACP hash visualizer
**model:** sonnet-5 · **size:** M · **depends:** — · **hardware-flagged**

Which flows hash to which bond member, painted on the map.

**Repo fact:** bond/LACP handling already exists in both the topology and findings layers —
`internal/findings/health_lacpmismatch.go` (T-804, partner/actor state mismatch detection) and
`internal/metrics/bond_test.go` (a documented expectation that "a bad LACP hash" shows as an
imbalance between sibling links) — but nothing renders *which flow hashes to which member*, only
mismatch/imbalance detection. This card is a new visualization composing `internal/topology`'s bond
model with `internal/flow` records, not a duplicate of the existing LACP checks.

**This is HARDWARE-FLAGGED, per CLAUDE.md's three allowed categories: it needs real NICs and a
physical switch** (per-member hash outcome is a property of the switch's own hashing algorithm
interacting with real link members — a nested lab cannot produce it). It ships behind lab-simulated
fixtures for development/tests, with a `planning/reports/needs-hardware-validation.md` entry filed
under "needs real NICs" (the category this exact case is named under in CLAUDE.md), not a bare
"needs hardware" line.

**Deliverables**
- A hash-outcome view composing `internal/topology`'s bond/member model with `internal/flow`
  records (which member a given flow's hash landed on, where determinable from observed traffic
  distribution).
- Lab-simulated fixtures standing in for real per-member hash behavior in tests, clearly labeled as
  simulated.
- A `needs-hardware-validation.md` entry under "needs real NICs" for the real-switch-hash
  verification this cannot prove without hardware.

**Acceptance criteria**
1. Renders against lab-simulated fixtures with a passing test suite.
2. The `needs-hardware-validation.md` entry names "needs real NICs" (or "needs a physical switch"),
   never a bare "needs hardware" line.
3. Existing `health_lacpmismatch` behavior is unchanged — this is additive visualization, not a
   replacement for the mismatch check.

---

### T-4111 · eBPF datapath probes (exploratory spike)
**model:** sonnet-5 · **size:** spike · **depends:** owner: dependency call

Per-flow latency at the bridge without payload inspection — a time-boxed spike whose deliverable is
a **written recommendation, not a merge**.

An eBPF loader library and a kernel-version support matrix would be vnprox's first dependency of
this class, which CLAUDE.md's ground rules require flagging rather than deciding unilaterally
("No new major dependencies without a note in your report") and which the roadmap makes explicit:
"it would be a new major dependency class ... the no-new-major-deps rule means the spike's
deliverable is a recommendation, not a merge." **No payload inspection** — flow metadata/latency
only, consistent with the permanent scope boundary in `docs/features.md`.

**Deliverables**
- A prototype (throwaway, not merged to a long-lived branch/package) demonstrating per-flow latency
  measurement at the bridge via eBPF, run against PVE 9's actual shipped kernel version — not a
  generic recent-kernel assumption.
- A kernel-support matrix: which PVE 9.x kernel lines the approach works on, and what breaks on
  older ones still in the support window.
- A dependency-cost writeup: which loader library (if any), binary size/build complexity impact,
  CO-RE portability, and what it would mean for the single-binary `vnproxd` distribution model.
- A go/no-go recommendation document (in `planning/reports/`) the owner can act on, explicitly not
  landed code.

**Acceptance criteria**
1. The deliverable is a recommendation document with prototype evidence attached, not a merged
   feature.
2. The kernel-support matrix is checked against PVE 9's actual kernel, not assumed from generic
   eBPF documentation.
3. No payload inspection anywhere in the prototype — verified by describing exactly what the probe
   reads (metadata/timing) and confirming it excludes packet contents.
4. The document states a clear go/no-go, not just "here are some options".

---

## Sequencing

```
Long-running, start early:
  T-4107 (scale envelope)        ─── perf work, runs the whole phase
  T-4108 (nightly burndown)      ─── blocked on Actions billing + a lab-teardown
                                      fix that should land before the pipeline,
                                      not discovered by the pipeline

Compositions, dispatchable once their inputs are confirmed stable (all are today):
  T-4101  baseline → capture            (independent)
  T-4102  incident → docexport          (independent; NOT the same seam as
                                          incident's existing support-bundle export)
  T-4103  capacity + ipam + failsim     (independent, largest of the compositions)
  T-4104  findings + change             (independent; needs a new template
                                          registry — Detail is not typed data)
  T-4105  auth/caps.go                  (independent; smallest — reads one
                                          existing function's output)
  T-4106  evpn + mtuprobe → validate_sdn.go
                                         (independent; do not touch preflight.go,
                                          a different existing composition)
  T-4109  PowerDNS/dns.go               (independent, smallest DNS card)

Hardware-flagged, ships with simulated fixtures + a needs-hardware-validation.md entry:
  T-4110  (needs real NICs + a physical switch — see CLAUDE.md's three categories)

Spike, gated on the owner:
  T-4111  (recommendation only; no code merges without the owner's dependency call)
```

Nothing in this phase blocks anything else in this phase — every composition reads inputs that
already exist and are stable, so all nine non-hardware, non-spike cards (T-4101–T-4106, T-4109, and
the long-running T-4107/T-4108) are dispatchable in parallel waves of 3–5, per the roadmap's
execution model. T-4110 and T-4111 have their own gates stated above and should not be batched with
the others.

## Explicitly not in this phase

- **A new apply path.** T-4106's overlay preflight is a validate-time verdict; nothing in this
  phase adds a way to mutate network config outside `internal/change`.
- **An LLM backend.** T-4104's explainers are deterministic templates so the product works
  identically on every install; adding a model dependency would contradict the card's own premise.
- **Landed eBPF code.** T-4111 is scoped to a recommendation; the earliest any eBPF code merges is
  a future phase, contingent on the owner's go call.
- **Real hardware validation of T-4110.** The card ships with simulated fixtures; the real-switch
  verification is filed to `needs-hardware-validation.md`, not attempted in this phase.
- **Retained flow/metric warehousing beyond what T-4103's what-if evaluation needs in-flight.** The
  planner evaluates and discards; it does not grow `internal/capacity`'s retention footprint.
