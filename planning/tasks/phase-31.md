# Phase 31 — All of Proxmox networking

**Roadmap:** [`docs/roadmap-earned.md`](../../docs/roadmap-earned.md) ·
**Plan:** [`../implementation-plan-earned.md`](../implementation-plan-earned.md)

Context for every card in this phase: `docs/architecture.md`, `docs/development.md`,
`docs/api.md`, `docs/data-model.md`, `docs/compatibility.md`, and the hardware capture at
[`../reports/evidence/pve-9.2.4-sdn-schema.txt`](../reports/evidence/pve-9.2.4-sdn-schema.txt).

The organising rule, from the arc roadmap: vnprox claims to be a visual interface for **all**
Proxmox networking. Phase 30 tested that claim against vnprox's own API and found 17 route
families no human could reach. **This phase tests it against Proxmox's API instead** — and the
gap is wider, because the surfaces missing here were never built at all.

---

## The evidence these cards are scoped from

Phase 30's cards were scoped from a mechanical census of `docs/openapi.json` versus `web/src`.
That method cannot see this phase's gap: a PVE feature vnprox never modelled has no vnprox route
to be absent from. So this phase was scoped a different way, and the method matters more than
the result because the result will go stale.

**`pvecube` runs PVE 9.2.4.** It has since before Phase 29, and no card in three phases had
asked it what its API looks like. Twenty minutes of read-only `pvesh ls` / `pvesh usage` against
it produced every finding below. The full transcript is checked in, not paraphrased.

```bash
# the method, so it can be re-run and disagreed with (read-only; `usage` mutates nothing)
ssh root@<node> 'pvesh ls /cluster/sdn'
ssh root@<node> 'pvesh usage /cluster/sdn/zones -v'          # the type enum is the payload
ssh root@<node> 'pvesh usage /cluster/sdn/fabrics/fabric -v'
ssh root@<node> 'pvesh usage /cluster/sdn/vnets/<vnet>/firewall/rules -v'
```

**A `pvesh usage` type enum is the single highest-yield read in this repository.** Every
false-model finding below is one line of one enum. None of them needed a cluster, a capture
harness, or a write.

### What hardware said, versus what the repo believed

| # | The repo believed | PVE 9.2.4 says | Card |
|---|---|---|---|
| 1 | `openfabric`/`ospf` are PVE 9 SDN **zone types** | Zone enum is `<evpn \| faucet \| qinq \| simple \| vlan \| vxlan>`. Fabrics are their own family, `/cluster/sdn/fabrics`; `openfabric`/`ospf` are two of four **protocols** (`bgp \| openfabric \| ospf \| wireguard`) | `T-3101` |
| 2 | Valid zone types are `simple/vlan/qinq/vxlan/evpn` | `faucet` is also real. vnprox **refuses to stage a zone PVE would accept** | `T-3101` |
| 3 | `/cluster/sdn` holds zones, vnets, subnets, dns, ipams, controllers | Also `prefix-lists` and `route-maps` — two families vnprox does not model at all | `T-3101` |
| 4 | Controller types are evpn/bgp/isis | `<bgp \| evpn \| faucet \| isis>` — `faucet` again | `T-3102` |
| 5 | Firewall rule directions are `in`/`out`/`group` | `<forward \| group \| in \| out>` at **every** scope — cluster, node, **and** vnet — plus `policy_forward` and `log_level_forward` options | `T-3103` |
| 6 | VNet-scope firewall is "missing from the scope model" | Confirmed, with its shape: `/cluster/sdn/vnets/{vnet}/firewall/{options,rules}` | `T-3103` |
| 7 | IPAM plugin types are a vnprox concern | `<netbox \| phpipam \| pve>` — matches what vnprox already reads; no gap here | `T-3104` |

Findings 1 and 5 are the same defect: **vnprox rejects input real Proxmox accepts**, silently,
at validate time. Finding 1 additionally sat inside the compatibility matrix as a *passing
check* for four phases — corrected before this phase was written
(`compat: repoint the SDN Fabrics gate at the divergence that exists`), because a card cannot be
scoped against a fixture that is lying.

### What this method cost, and the rule that follows

Every one of these was reachable from a node this project has had SSH access to since v1.2.1.
The reason none surfaced earlier is that `CLAUDE.md` says *"you do NOT have a live Proxmox
cluster… develop against `internal/pvemock/` fixtures"* — true when written, false since pvecube
existed, and nobody re-read it against reality. **The standing instruction was correct about
clusters and wrong about nodes.**

> **Rule for this phase and every phase after it:** before modelling a PVE object, run
> `pvesh usage` against it on pvecube and check the capture in. A fixture written from release
> notes and a check written from the same release notes will agree with each other forever. This
> is the second time that exact failure has been found in this repository
> (`sdn_fabric_zone_gate`, and T-2905's `backup` security assertion before it).

**pvecube is a single node.** It answers "what shape is this API" definitively and "how does
this behave across a cluster" not at all. Cross-node fabric realization, controller
convergence, and per-node fabric membership stay in
[`../reports/needs-hardware-validation.md`](../reports/needs-hardware-validation.md) for
`T-3201`'s second node. No card here may claim cluster behaviour it observed on one node.

---

## T-3101 · SDN Fabrics, modelled from the real API ★

**Priority:** P0 · **Owns:** `internal/sdn/`, `internal/change/params_sdn*.go`,
`internal/change/validate_schema.go`, `internal/pvemock/sdn*.go`, `web/src/sdn/`

**Read the capture before writing a line of this card.** The roadmap's original scoping for
T-3101 — "model the zone types, add wizard + editors + map overlay" — was wrong, and is
corrected in place in `docs/roadmap-earned.md`. Building it as written would have produced the
wrong object graph, the wrong wizard, and a map overlay hanging off a field that does not exist.

### The real model

```
POST /cluster/sdn/fabrics/fabric
  --id        [a-zA-Z0-9][a-zA-Z0-9-]{0,6}[a-zA-Z0-9]   # max 8 chars — shorter than any
                                                        # other SDN id vnprox validates
  --protocol  <bgp | openfabric | ospf | wireguard>
  --ip_prefix / --ip6_prefix                            # node IP allocation
  --lock-token                                          # global SDN config lock (unmodelled)
  # conditional on protocol:
  #   openfabric: --csnp_interval, --hello_interval, --route_filter
  #   ospf:       --area, --redistribute
  #   bgp:        --redistribute
GET  /cluster/sdn/fabrics/all   ->  {"fabrics":[],"nodes":[]}
     /cluster/sdn/fabrics/node  ->  per-node fabric membership, its own collection
```

Note three things the shape tells you that prose would not:

1. **`--protocol` is conditional-schema.** The valid parameter set depends on the protocol
   chosen. This is the first PVE object vnprox models with that property; decide deliberately
   whether `validate_schema.go` grows a conditional arm or the params types split by protocol,
   and write the reason down.
2. **`wireguard` is a fabric protocol — and it is genuinely WireGuard.** *(Answered
   2026-08-16 from hardware, so the card does not have to guess: `protocol=wireguard` takes
   `--persistent_keepalive`, a real WireGuard parameter.)* vnprox already has a WireGuard
   subsystem (`internal/change/params_wg.go`, T-1401's tunnels). **These are two different
   management planes over the same protocol** — T-1401's tunnels are vnprox-managed peer links;
   a WireGuard fabric is PVE-managed underlay transport. They must not share a model, and a
   fabric must not appear as a T-1401 tunnel or vice versa. Say in the report how you kept them
   apart, and make sure the topology map distinguishes them.
3. **`--lock-token` is a global SDN config lock — and vnprox does not take it.** *(Answered
   2026-08-16, and the answer turned into a filed safety gap:
   [`../reports/T-3101-followup-01.md`](../reports/T-3101-followup-01.md).)* `PUT /cluster/sdn`
   applies **all** pending SDN changes cluster-wide, vnprox issues it with empty params, and no
   validator anywhere reads SDN pending state. An operator can therefore approve a one-line
   vnprox changeset and commit an unrelated change staged in the PVE GUI — never validated,
   never diffed, not in the audit trail as applied, and outside the rollback they believe they
   have.

   **This card does not fix that** — three defensible fixes exist and choosing between them is a
   product decision. What this card must do is **not widen it**: every fabric op inherits the
   same apply path. If T-3101 lands before the decision, its report says so explicitly rather
   than letting new surface inherit the problem silently.

### Also in scope — the two defects the same capture exposed

- **`faucet` is a real SDN zone type** and `internal/change.validSdnZoneTypes` rejects it, so
  vnprox refuses to stage a zone real PVE accepts. Fix the validator. Whether the zone *wizard*
  offers faucet is a separate, smaller question — a faucet zone needs a faucet controller, and
  offering a wizard for a combination vnprox cannot complete is worse than offering none. Decide,
  implement one, and say which in the report.
- **`prefix-lists` and `route-maps`** are live `/cluster/sdn` families vnprox does not model.
  Scope them **read-only** in this card (inventory + display); CRUD is out of scope and gets its
  own card if it is wanted. They are BGP route-policy objects and almost certainly couple to
  `T-3102`'s controllers — establish the relationship, don't guess it.

### Acceptance criteria

1. `internal/pvemock` serves the fabrics family with the real shapes, including the conditional
   protocol schema, and rejects a fabric id longer than 8 characters exactly as PVE's pattern
   does. The compat profile's `SDNFabrics` gate keeps working and its 501-below-9.0 behaviour is
   unchanged.
2. Fabrics appear in the SDN tree and on the map, with per-node membership from
   `/cluster/sdn/fabrics/node` — not inferred from zones.
3. Fabric create/update/delete flow through the change engine as ops. No path mutates fabric
   config outside `internal/change/`.
4. `validSdnZoneTypes` accepts `faucet`, with a test that fails if the set drifts from the enum
   captured in the evidence file, naming that file in its failure message.
5. `prefix-lists` and `route-maps` are readable and displayed; a test pins that they are
   read-only.
6. Every new type-enum mirror (Go → TypeScript) is guarded the way
   `internal/change/canary_ts_contract_test.go` guards Phase 30's — a Go test that reads the
   `.ts` as text, so the two cannot be made to agree by construction.
7. The report states, explicitly: the `wireguard` protocol answer, the `--lock-token` answer, and
   what remains unproven for want of a second node.

**Needs hardware validation:** everything cross-node. pvecube has no fabrics configured and one
node; this card models the API's *shape*, and cannot observe fabric convergence, per-node
realization, or what a fabric does to the underlay. File those in
`../reports/needs-hardware-validation.md` under `T-3201`.

---

## T-3102 · SDN controllers as first-class objects

**Priority:** P1 · **Owns:** `internal/change/op.go` (this wave's op-const block),
`internal/change/params_sdn_controller.go` (new), `internal/change/apply_sdn.go`,
`internal/sdn/service.go`, `web/src/sdn/controllers/`

Controllers are a **string field on a zone** today (`internal/sdn/service.go:77`,
`Controller string`). `internal/blueprint/starters.go:246` documents the consequence in a
starter's own text: *"no `sdn.controller.create` op; see the T-603 report"* — a blueprint that
cannot complete itself and says so.

Real types: `<bgp | evpn | faucet | isis>`. The roadmap named evpn/bgp/isis; `faucet` is the
same omission `T-3101` fixes on the zone side, and the two should land consistently.

### Acceptance criteria

1. `sdn.controller.{create,update,delete}` ops exist, validate, diff, and apply through the
   change engine. This card owns the op-const block for its wave — no other card in the wave adds
   an `OpType`.
2. Controllers are objects in the SDN tree with their own inspector, not a string on a zone. The
   zone's `controller` field becomes a reference.
3. EVPN/BGP status attaches to the controller rather than being inferred (`/api/v1/sdn/evpn/status`
   already exists — this is a re-attachment, not a new route).
4. `internal/blueprint/starters.go`'s apology text is deleted and the starter completes, or the
   report says why it still cannot.
5. Deleting a controller a zone references is blocked with the reference list, matching how
   `internal/fw` already blocks deleting a referenced alias.

---

## T-3103 · Firewall fidelity: `forward`, VNet scope, real resolution order

**Priority:** P1 · **Owns:** `internal/fw/`, `internal/inventory/entity.go`,
`internal/change/params_fw.go`, `web/src/firewall/`

Three items, and hardware reordered them — the one the roadmap ranked last is the most urgent.

**1. `forward` is a rule direction vnprox rejects.** `validFwDirections` is
`{in, out, group}`; real PVE 9.2 accepts `<forward | group | in | out>` at **cluster, node, and
vnet scope**, with `policy_forward` and `log_level_forward` options alongside. An operator with a
single `forward` rule anywhere in their firewall hits a validation failure from vnprox on an
unrelated edit. This was not in the roadmap's audit at all.

**It looks like a one-line fix to `validFwDirections` and it is not — do not treat it as one.**
`internal/fw/resolve.go:58` declares `Direction` as `"in" | "out"` and line 207 branches on
`direction == "out"`, so a `forward` rule admitted by the validator alone would resolve down the
inbound path and the simulator would report a **confidently wrong** answer about whether traffic
is permitted. That is worse than today's honest rejection. The direction set and the resolver
must move together, and the result must be compared against real pve-firewall output (item 3
below) before either is called done.

The equivalent gap on the SDN side, `faucet`, *was* safe to fix alone and was fixed on
2026-08-16 — one gate, one call site, and every other branch already keyed on specific types
with a generic fallthrough. The difference between the two is the whole reason this note exists:
**check the consumers before widening a validator.** A validator that accepts more than its
consumers understand converts a visible refusal into a silent wrong answer.

**2. VNet-scope firewall**, exactly as the roadmap said, now with its shape:
`/cluster/sdn/vnets/{vnet}/firewall/{options,rules}`. `FwScope` gains a fourth value
(`internal/inventory/entity.go:686`). Every place that switches on `FwScope` must be found and
handled — a `default:` arm that silently drops vnet rules is the failure mode to test for, not to
hope against.

**3. Rule-effects preview and real resolution order.** `docs/features/firewall.md:7` documents
`internal/fw/resolve.go`'s ordering as *"a documented simplification, not a byte-for-byte match of
real pve-firewall's rule-chain traversal"*, specifically that cluster-scope rules reach a guest
only via an explicit security-group reference. **pvecube can settle this**, and it is the one
part of this card that must be checked against hardware rather than reasoned about: build a
ruleset, read back what pve-firewall compiles, and compare. If the simplification is wrong, the
simulator gives wrong answers about whether traffic is permitted, which is the worst class of bug
this product can ship.

`docs/features/firewall.md:13` also records that IP-level effects preview is unimplemented. That
stays out of scope unless items 1–3 land early; if it does, say so rather than half-landing it.

### Acceptance criteria

1. `forward` is accepted at every scope, with `policy_forward`/`log_level_forward` modelled and a
   test pinning the direction set against the captured enum.
2. `FwScopeVNet` exists; every `FwScope` switch handles it; a test proves no arm silently drops
   vnet rules.
3. Resolution order is compared against real pve-firewall output on pvecube. `docs/features/
   firewall.md`'s caveat is rewritten to state what was observed — **whichever way it came out.**
   A confirmation is as valuable as a correction here and gets written down just as plainly.
4. The rule editor, resolved view, and simulator all handle vnet scope and `forward`.

---

## T-3104 · IPAM completion

**Priority:** P1 · **Owns:** `internal/ipam/`, `internal/change/op.go` (its wave's op block),
`web/src/ipam/`, the IP-entry fields listed below

Real IPAM plugin types are `<netbox | phpipam | pve>` — which is what vnprox already reads. **No
model gap here**, unlike every other card in this phase. Scope is therefore narrower than the
roadmap implies, and the card should not go looking for a discrepancy that isn't there.

1. **Next-free everywhere.** `NextFreePicker` is wired into exactly one field: `BridgeEditor.tsx`.
   `docs/features/ipam.md:21` names the gap. Wire it into `VlanEditor.tsx`,
   `InterfaceEditor.tsx`, and the SDN subnet-gateway field. **Note `web/src/sdn/wizards/
   SubnetStep.tsx` already uses the same underlying allocation query** without the component —
   reuse, don't duplicate, and don't regress its behaviour.
2. **PVE IPAM plugin entry CRUD** as change-engine ops.
3. **External-IPAM write client.** The read path ships; writes return
   `ErrSyncNotConfigured` (`internal/ipam/sync.go:30`). `docs/status-matrix.md:49` counts this as
   one of six deliberately-absent backends. Productionize the write path **behind the same
   preview/apply split the read path already has** (`/ipam/external-sync/preview`,
   `/ipam/external-sync/apply` both exist) — an IPAM write that cannot be previewed is a write
   outside the change engine's guarantee in spirit if not in letter.

### Acceptance criteria

1. Every IP-entry field in the UI offers next-free; a test enumerates the fields so a new one
   added later without the picker is caught.
2. IPAM plugin CRUD flows through the change engine.
3. External-IPAM writes are previewable, and a failed external write cannot leave vnprox's view
   and the external system disagreeing without a finding being raised.
4. `docs/features/ipam.md:21` and `docs/status-matrix.md:49` are updated in the same change.

---

## T-3105 · Restore fidelity — **rescoped, and mostly closed as already-decided**

**Priority:** P2 · **Owns:** `internal/change/restore_ops.go`, `internal/inventory/` (Bond model)

The roadmap gave this card three items. **Two of them are not debts — they are decisions,
recorded in code, that the roadmap misread as unfinished work.** Verified 2026-08-16:

- **"Reverse-proxy ingress write support, read-only since Arc 3 deferred it."** Not deferred.
  `internal/ingress/doc.go` establishes read-only as an **architectural invariant**: every
  discoverer issues GET only, `zerowrite_test.go` proves it both by source inspection and by
  driving each discoverer against an instrumented server, and T-1702's plugin SDK seam is built
  on the single-method `IngressDiscoverer` interface that invariant permits. The cited source,
  `docs/roadmap-universal.md:146`, does not defer writes — it scopes the feature as *"read-only
  discovery of the reverse-proxy layer"*. Making ingress writable would break a published
  guarantee, a test written to enforce it, and a plugin contract. **That is a product decision,
  not an implementation task.** Per `CLAUDE.md`, it is flagged here rather than actioned. If it
  is genuinely wanted, it needs its own card, its own security review, and a rewrite of that
  package's doc comment — not a line item in a restore-fidelity card.
- **"NIC renaming's partial implementation has three unvalidated hardware behaviours."** Physical
  NIC rename is not partially implemented; it is **explicitly out of scope**, stated twice —
  `internal/change/op.go:43` and `internal/change/params_iface.go:41`, both reading *"Physical NIC
  (udev) renames are out of scope"*, with the reason (reboot-realized, hardware-specific) and the
  guard (blocked when guests are attached). Logical iface rename is complete. There is no partial
  implementation to finish.

**What actually remains is one item, and it is real:** time-machine restore cannot re-create OVS
bonds. `internal/change/restore_ops.go:175` returns `ErrRestoreUnsupported` with an honest reason
— *"OVS bond re-creation needs its `ovs_bridge` attachment, which the inventory model does not
carry."* Carry it.

That the code says exactly why it fails, at the point of failure, is why this one survived
scrutiny while the other two did not. **A refusal that names its own cause is a card; a refusal
that names a design decision is a decision.**

### Acceptance criteria

1. `inventory.Bond` carries its `ovs_bridge` attachment; the collector populates it.
2. Restoring a snapshot containing an OVS bond re-creates it, proved by a round-trip test that
   fails against today's code.
3. `ErrRestoreUnsupported` still fires for anything genuinely unsupported — the fix narrows the
   refusal, it does not delete it.
4. The report states the two descoped items and their reasons, so the next reader does not
   re-derive them a third time.

---

## T-3106 · Localization (i18n) — the rescheduled T-2006

**Priority:** P2 · **Owns:** `web/src/**` (framework + string extraction) — **runs alone in its
own wave**, because it touches every component every other card writes

T-2006 verbatim: the single Arc-4 roadmap item with **zero code in the tree**. Confirmed
2026-08-16 — no `i18n`, `useTranslation`, or `LocaleProvider` anywhere under `web/src`, and no
i18n dependency in `web/package.json`.

**Scope this honestly or do not start it.** vnprox's frontend is 273 test files and ~2000 tests
deep. "Localize the UI" is not a card; it is an arc. What fits in one card:

1. The framework, chosen and wired (a new frontend dependency — **note it in the report** per
   `CLAUDE.md`, and prefer one with no runtime CDN fetch, since the service worker must not cache
   authenticated responses and the CSP forbids external hosts).
2. String extraction across a **proven, bounded subset**, chosen and named up front.
3. A lint or test gate that fails on a new hardcoded user-facing string in localized areas — so
   the boundary holds instead of eroding.
4. English as the only shipped locale, with a second locale stubbed far enough to prove the
   pipeline round-trips.

**If the decision is instead to retire localization permanently, that is a legitimate outcome**
and this card closes as retired — the roadmap already says so. Either way the answer gets written
down; what must not happen is a fourth arc in which it silently rolls forward.

### Acceptance criteria

1. `make check` passes with the framework in place and no `any` / unchecked casts (strict TS).
2. The gate in item 3 fails on a deliberately-added hardcoded string, demonstrated.
3. The subset in item 2 is named in the card's report as a boundary, with what is outside it.
4. `docs/status-matrix.md` and `docs/roadmap-earned.md` record the real state — not "in progress".

---

## Waves

Contention in this phase is **two files**: `internal/change/op.go` (op consts + `paramFactories`)
and `internal/change/validate_schema.go` (the params switch). Everything else is separable. The
rule is therefore **one op-adding card per wave**, and `T-3106` alone at the end.

| Wave | Cards | Why here |
|---|---|---|
| 1 | `T-3101`, `T-3105` | T-3101 owns `validate_schema.go` and the SDN model; T-3105 owns `restore_ops.go` + the inventory Bond model. Disjoint. T-3101 is P0 and everything SDN-shaped waits on its object graph |
| 2 | `T-3102`, `T-3103` | T-3102 owns the wave's op-const block; T-3103 owns `internal/fw/` + `entity.go`. Disjoint, and both build on wave 1's model |
| 3 | `T-3104` | Owns the wave's op-const block; touches `web/src/ipam/` and the editors |
| 4 | `T-3106` | Touches every component the three waves above wrote. Cannot overlap with any of them |

**Migrations:** none expected — every object in this phase is PVE-owned, and per `CLAUDE.md`
vnprox never persists a shadow copy of PVE config as authoritative state. If a card finds it
needs one, it claims its wave's single number (0049 → wave 1, 0050 → wave 2, 0051 → wave 3,
0052 → wave 4) and adds its `versionSeeds` fixture. The `migrate_fromeach_test.go` loop is never
relaxed, narrowed, or skipped.

**`docs/openapi.json`:** unlike Phase 30, cards here **may** add routes — but far fewer than the
scope suggests. The document is route-level, not body-schema-level, so extending a response shape
(fabrics in the SDN tree, controllers as objects, vnet-scope rulesets) leaves it byte-identical.
Only a genuinely new route family changes it. A card that thinks it needs a new route family says
so in its report **before** adding one, and the orchestrator runs `make openapi` once per wave on
the merged state — never the agents.

**Standing constraints for every agent in this phase**, unchanged from Phases 29–30: no
`make check` / `make e2e` / Playwright / `git commit` / `git push` / `git stash` / `git checkout`
— the orchestrator runs the gate per wave. Agents run focused package tests for files they
touched. Every fix ships with the test that proves the old behaviour was wrong. A card that makes
a documented claim true or false updates that doc sentence in the same change.

**One rule specific to this phase:** before modelling any PVE object, run `pvesh usage` against
it on pvecube and check the capture in. Do not model from `pvemock`, from `docs/`, or from
Proxmox release notes — all three have now been observed to be wrong about the same feature at
the same time, because they were written from each other.
