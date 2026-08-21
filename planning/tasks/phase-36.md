# Phase 36 — Actionable findings: every flagged item offers its fix

Goal: when vnprox tells an operator something is wrong, it should also offer to put it right.
Today the topology banners are pure text — "dnsmasq is not running on node pvecube" with nothing to
click, "No LLDP data yet" with a link to documentation, "no successful poll yet — context canceled"
with no next step at all. The information is good. The screen stops one step short of being useful.

Scoped from a screenshot of the running instance, 2026-08-21, showing three flagged items with three
different *kinds* of remedy — which is the whole design problem.

---

## The spine: a Fix button is not permission to invent a mutation path

CLAUDE.md's hard rule is that network changes flow through `internal/change/` — stage, validate,
diff, apply, confirm/rollback — and nothing else. A button labelled "Fix" is exactly the kind of
thing that erodes that rule by accident, because the shortest path from a banner to a fixed cluster
is always a direct call to something. So the tier a remedy belongs to is decided by **what the fix
is**, never by what is convenient to wire up:

**Tier 1 — change-engine fix.** The remedy is a change to Proxmox network configuration. Already
built and untouched by this phase: `Finding.Fixable` → `POST /findings/{id}/fix` → a changeset
draft that still validates, diffs and requires approval. Only drift-sourced findings compute one
today. *Nothing in this phase may add a Tier 1 remedy that bypasses that route.*

**Tier 2 — operational action.** The remedy is a host operation that is *not* network configuration:
installing a package, starting a systemd unit, re-running a poll. These have no changeset to stage —
there is no PVE config to diff — but they are still mutations, so they carry the same ceremony:
explicit `{confirm: true}`, capability gate, fixed argv with no operator-supplied strings, one audit
row per node including failures and refusals. `POST /lldp/install` (T-605) is the existing exemplar
and the template every new Tier 2 action copies. The IPAM external-sync routes set the same
precedent for writes that sit outside `internal/change` by nature: *mirror the contract even where
the mechanism doesn't apply.*

**Tier 3 — guided navigation.** The remedy is a human decision, or configuration vnprox cannot infer.
The button carries the operator to the right screen with the context pre-filled rather than
pretending to know the answer. `mgmt_single_path`'s redundancy wizard and `sim_divergence`'s
simulator deep link are the existing exemplars.

A finding gets exactly one tier. If none fits, it stays detection-only and says why — an honest
"here is what to run" beats a button that does something adjacent to what the operator wanted.

---

## What the reference instance actually shows

| Banner | Remedy | Tier | Backend today |
|---|---|---|---|
| `No LLDP data yet — Set up lldpd` | install + enable `lldpd`, all nodes | 2 | **exists** — `POST /lldp/install`, netWrite + CSRF + confirm, fans out to peers, audits `lldp.install` |
| `dnsmasq is not running on node pvecube`<br>`frr is not running on node pvecube` | start the unit on that node | 2 | **missing** — `GET /api/peer/host/services` reads status (T-602); nothing starts one |
| `host (node pve001): no successful poll yet — context canceled` | re-run the poll; if it keeps failing, fix that node's reachability | 2 + 3 | **partly** — `collect.Collector.RefreshNow` exists as a Go primitive with no route |

Two of the three already have most of their machinery. The work is mostly making findings *say* what
their remedy is, and giving the banners somewhere to put the button.

---

## T-3601 · A finding says what its remedy is, and every surface can render it
**model:** strong · **size:** M · **depends:** — · **context:** `internal/findings/types.go`, `internal/api/findings.go`, `web/src/findings/{FindingsList,FindingsStreamPanel}.tsx`, `web/src/topology/{StalenessBanner,UnrefFindingsBanner}.tsx`, `TopologyPage.tsx`

**Objective:** One vocabulary for "here is the remedy", declared by the producer and rendered
identically wherever a finding appears. Without this, Phase 36 becomes three special cases wired
into three components, and the fourth remedy anyone adds becomes a fourth.

**Deliverables:**
- `Finding` gains a `Remediation` describing the offered action: its tier, a stable action id, a
  human label, and whatever the action needs (node, service, source). Absent — not a placeholder —
  when the finding is detection-only. `Fixable` stays exactly as it is; Tier 1 is not re-modelled.
- `docs/api.md`'s `GET /findings` shape updated in the same change; the badge/field contract is
  depended on by other tasks.
- One shared frontend affordance. `FindingsList` already takes an optional `action: {label,
  onClick}` — generalise *that*, do not add a second vocabulary beside it.
- The topology banners (`StalenessBanner`, `UnrefFindingsBanner`, the LLDP notice) render remedies
  the same way the findings stream does. An operator must not learn two idioms for one concept.
- A Tier 2 action always confirms before it fires, naming every node it will touch.

**Acceptance criteria:**
1. A finding with no remediation renders exactly as it does today, in both surfaces.
2. The same finding shown in the findings stream and in a topology banner offers the same action
   with the same label — pinned by a test over one fixture, not by inspection.
3. No component special-cases a `check` value to decide what button to draw.

## T-3602 · The LLDP banner offers to install lldpd
**model:** sonnet-5 · **size:** S · **depends:** T-3601 · **context:** `internal/api/lldpinstall.go`, `docs/api.md` `POST /lldp/install`, the topology LLDP notice in `TopologyPage.tsx`

**Objective:** The cheapest win in the phase: the backend has been there since T-605 and the banner
still only links to documentation.

**Deliverables:**
- The notice offers "Install lldpd on all nodes", gated on `netWrite`, behind the same confirm
  dialog the first-login walkthrough already uses — reuse that flow, do not fork it.
- The per-node `{results: [{node, ok, error?}]}` response is shown honestly: a partial success names
  which nodes failed and why. "Done" when two of five nodes failed is a lie.
- Read-only users see the docs link exactly as today, not a disabled button.

**Acceptance criteria:**
1. The button stages nothing and calls `POST /lldp/install` with `confirm: true`.
2. A mixed success/failure response renders per-node outcomes.
3. Absent `netWrite`, no button is rendered at all.

## T-3603 · Collector staleness offers a retry, and a way to the real problem
**model:** sonnet-5 · **size:** M · **depends:** T-3601 · **context:** `internal/collect` (`RefreshNow`), `internal/api/health.go`, `StalenessBanner.tsx`

**Objective:** "no successful poll yet — context canceled" currently tells an operator that
something is broken and nothing about what to do next.

**Deliverables:**
- A route to re-run one collector source now. This is Tier 2 but touches no host state — it is
  vnprox re-reading, not vnprox writing — so it is the *safest* new action in the phase and should
  be built first as the tier's proving ground. Rate-limited; a button that can be leaned on must not
  become a way to hammer PVE.
- The banner distinguishes "transient, try again" from "this node has never polled successfully",
  and for the latter offers Tier 3 navigation to that node's connection settings rather than a retry
  that will fail identically.
- Retrying reports what happened. A retry that silently fails again is worse than no button.

**Acceptance criteria:**
1. Refreshing a healthy source is a no-op that reports success; refreshing a failing one reports the
   error text, not a spinner that stops.
2. The rate limit is enforced server-side and tested.
3. A source that has never succeeded offers navigation, not a retry.

## T-3604 · `service_down` offers to start the unit
**model:** strong · **size:** M · **depends:** T-3601, T-3603 · **context:** `internal/findings/health_service.go` (`watchedServices`), `internal/peer/server.go`, `internal/host/lldp_install_linux.go` as the fixed-argv precedent

**Objective:** dnsmasq and frr are SDN's DHCP and routing daemons; one being down is a real outage
with a one-line remedy the operator currently has to leave vnprox to apply.

**This is the card that deserves the most scrutiny in the phase.** It adds the ability to start a
systemd unit on a node, which is a genuinely new class of power for this product.

**Deliverables:**
- A peer route that starts a unit, with the unit name checked against
  `health_service.go`'s existing `watchedServices` set **server-side, on the receiving node** — not
  validated by the coordinator and trusted, and never an operator-supplied string reaching argv.
  `InstallLLDPD`'s fixed-argv construction is the shape to copy.
- The same T-2902 receiving-side treatment every other peer write route gets: attribution fields,
  local audit row for every start, refusal and failure.
- Starting a unit is not restarting it and is not enabling it at boot. Do exactly the one thing;
  say so in the label. If the unit is masked or its start fails, surface the systemd error verbatim.
- Decide, and record, whether this needs `netWrite` or a capability of its own. dnsmasq and frr are
  network daemons, so `netWrite` is defensible — but "can edit bridges" and "can start daemons" are
  not obviously the same permission, and the split T-3403 made for `Automation`/`AutomationWrite` is
  the precedent for taking that question seriously.

**Acceptance criteria:**
1. A unit outside the watched set is refused by the *receiving* node, with a test that calls the
   peer route directly rather than going through the coordinator.
2. Every outcome — started, refused, failed — produces an audit row naming actor and origin.
3. The finding clears on the next poll after a successful start, with no special-casing to make it
   disappear early.

## T-3605 · Prove it end to end, and write down the power that was added
**model:** sonnet-5 · **size:** S · **depends:** T-3602, T-3603, T-3604 · **context:** `docs/security.md` (Safety interlocks), `docs/api.md`, `web/e2e/`

**Deliverables:**
- `docs/security.md` gains the Tier 2 contract as a named concept: what an operational action may
  do, what it may never do, and why it is not a change-engine bypass.
- The capability matrix in the docs reflects whatever T-3604 decided.
- One e2e per button proving the whole path, including the refusal path for a read-only session.

**Acceptance criteria:**
1. A read-only session sees no Tier 2 button anywhere in the topology view.
2. Each action's audit row is asserted, not assumed.

---

## Deliberately not in this phase

- **Restart/stop/enable for arbitrary units.** T-3604 adds *start*, for two named units. A general
  service manager is a different product decision and should be asked for explicitly.
- **Auto-remediation.** Nothing here fires without a human pressing a button. A finding that fixes
  itself is a finding nobody reviews.
- **Tier 1 additions.** No new computed changeset fixes; that is `internal/change`'s own roadmap.

---

## Delivery record — 2026-08-21

All five cards delivered. The three banners in the screenshot that started this now offer their
remedies, and each got the tier its remedy actually deserves rather than a uniform one.

**T-3601** turned out to be the load-bearing card, and for a reason the plan only half anticipated.
The findings stream chose its two existing actions by testing `check` against string literals inside
a React component. That put the vocabulary in the renderer: every new remedy had to be added to
every surface that displays findings, and a surface that was missed rendered the finding with no
button and **no error**. Producers now declare; one resolver renders; an unrecognised `action`
resolves to nothing rather than guessing.

**T-3603 introduced a third tier the plan did not have.** The plan called a collector refresh Tier 2
and would have given it a confirmation dialog. It writes nothing to any node, and asking "re-read the
cluster?" trains operators to click through the dialogs that guard real mutations. So
`RemedyOperationalRead` exists — as a distinct kind rather than a `confirm: false` flag, because a
boolean's zero value would make "I forgot to set it" and "this needs no confirmation" the same
state, which is the wrong default for the one field between an operator and an unannounced mutation.

**T-3604's capability question got an answer, not a default.** `netWrite`, because the two
allow-listed units are network daemons the cluster already runs — starting one restores intended
state rather than changing it, strictly less invasive than the bridge edits `netWrite` already
permits. Recorded in `docs/security.md` **with its expiry condition**: if the allow-list widens
beyond that description, the reasoning no longer holds and the question must be reopened.

### Deviations from the plan as written

1. **T-3603's navigation half was dropped, because the card was wrong twice over.** It said a source
   that has never polled successfully should navigate to that node's connection settings rather than
   offer a retry. "No successful poll yet" means "not since this daemon started", which includes
   "the peer came back a moment ago" — exactly when an operator reaches for retry. And there is no
   per-node connection-settings screen to navigate to. Retry is offered in both cases; the result
   text carries the distinction.
2. **T-3605's audit assertions live in the Go handler tests, not the e2e.** The row is produced in
   the handler, and the most interesting case — a refused unit — can be driven directly there.
   Asserting it through the SPA would have tested the audit *page* rather than the audit *write*.
3. **The e2e is a pair, not a single check.** AC1 as written ("a read-only session sees no Tier 2
   button") passes just as well if the banners never render for anybody. It is paired with a
   write-capable test that fails loudly if no affordance renders at all, so the security assertion
   cannot go quietly meaningless.

### Two bugs found while building it

- **Per-action state keyed on `(check, node)` collided.** dnsmasq and frr down on one node produce
  two findings identical in source, check and nodes — so starting one showed its error next to the
  other. The list key and the action key are now separate functions with separate jobs.
- **`internal/topology` cannot import `internal/findings`** (the dependency runs the other way), so
  the remedy on the wire is a structural copy. A silently-diverged copy is precisely the failure
  this phase exists to prevent, so a test round-trips one through the other's JSON — which is also
  why `FindingRemediation`'s field order carries a `//nolint:govet` rather than being packed: Go
  emits JSON keys in declaration order, and "fixing" the alignment would break the check that
  guards the copy.

### Still true from "deliberately not in this phase"

No restart/stop/enable for arbitrary units, no auto-remediation, no new Tier 1 fixes. Nothing here
fires without a human pressing a button.
