# Phase 7 — Post-1.0: functional-by-default SDN and the management path

Goal: close the two field-reported gaps — wizard-created SDN networks that don't route (no
gateway), and a management path that is invisible and un-editable. T-701 is independent; T-703
depends on T-702.

Origin: user-reported issues triaged 2026-07-12; root-cause analyses live in
`planning/reports/T-701-analysis.md` and `planning/reports/T-702-analysis.md`.

---

## T-701 · Subnet gateways in the guided flow: defaults, requirement validation, PVE fidelity
**model:** sonnet-5 · **size:** M · **depends:** T-402, T-403, T-405 · **context:** `docs/features/sdn.md` §2 §4, `docs/features/ipam.md` §2, `docs/api.md` (/changesets finding shape, /sdn), `docs/data-model.md` §3 (sdn ops)

**Objective:** A wizard-created SDN network is functional by default: the guided flow proposes a
gateway where one is needed (zone-type-aware), the change engine blocks the configs real PVE
rejects (SNAT without gateway, gateway outside CIDR) and warns on the ones PVE accepts but that
ship broken traffic (EVPN subnet without gateway / SNAT without exit nodes) — covering raw and
non-wizard paths too — and pvemock stops being more permissive than real PVE so the gap can never
re-hide in CI. All mutations remain staged `sdn.*` changeset ops; nothing writes outside the
change engine.

**Deliverables:**
- Shared wizard subnet step: extract the five wizards' copy-pasted subnet block
  (`web/src/sdn/wizards/*ZoneWizard.tsx`) into one `SubnetStep` component with: gateway
  pre-filled to the CIDR's first usable IP as the user types the CIDR (reuse
  `web/src/ipam/nextFree.ts` / `NextFreePicker` per T-405's shared-component contract, skipping
  known allocations when the subnet overlaps an existing one); an explicit "no gateway — keep
  this network isolated" opt-out instead of a silently-empty field; SNAT checkbox disabled until
  a gateway is set (with a plain-English reason string in `strings.ts`); zone-type-specific copy
  (simple: optional unless SNAT; vlan/qinq/vxlan: gateway lives on your external router — vnprox
  records it for DHCP/IPAM but does not create it; evpn: gateway becomes the anycast address on
  every node — strongly recommended, and SNAT additionally requires at least one exit node,
  cross-checked against the wizard's own exitNodes selection live).
- Validators (`internal/change`), all pure/projection-folded so subnets, zones, and exit nodes
  created in the same changeset are honored, and independent of op origin (wizard, editor forms,
  raw API): (1) schema class: `schema.gateway_not_in_subnet` error on
  `sdn.subnet.create/update` when gateway ∉ CIDR, with a machine-applicable fix patch (set
  first usable IP, `validate_fix.go` pattern — substituted changeset must revalidate clean per
  the existing property test); (2) sdn class (`validate_sdn.go`): `sdn.snat_requires_gateway`
  error when a subnet's effective snat=true with no effective gateway (real PVE rejects this at
  stage time), also with the first-usable-IP fix; (3) advisory class:
  `sdn.evpn_gateway_missing` warning for an evpn-zone subnet with no gateway, and
  `sdn.snat_requires_exit_node` warning for a snat subnet whose evpn zone has no effective
  exit nodes (PVE accepts both; traffic is broken, so advisory not error).
- pvemock fidelity (`internal/pvemock/sdn.go`): subnet create/update rejects snat-without-gateway
  and gateway-outside-CIDR with 400 + PVE-style message; on create/update with a gateway,
  register/refresh the gateway record in the pve IPAM state (`gateway: true`), matching the
  hand-modeled records in `three-node-vlan.yaml`/`evpn-lab.yaml`/`ipam-lab.yaml`; existing
  fixtures must load unchanged.
- Fail fast on missing PVE session: `internal/api/changesets.go`'s apply/rollback handlers
  currently discard `GatewayFor`'s failure, so an sdn/fw/ipam-carrying changeset dies mid-apply
  with "no PVE gateway available ... (no user session)" (`apply_exec.go`). When the plan
  contains steps requiring a PVEGateway and none resolves, reject before snapshot/apply with the
  stable code `pve_session_required` (document in `docs/api.md` retroactively per
  docs/development.md definition-of-done #4, alongside the three new finding codes).
- Docs: zone-type gateway semantics note in `docs/features/sdn.md` §2; needs-hardware-validation
  entries (exact real-PVE rejection strings/versions for the two rejected shapes; when PVE writes
  the gateway IPAM record; EVPN behavior with absent gateway) appended to
  `planning/reports/needs-hardware-validation.md`.

**Acceptance criteria:**
1. Simple wizard against the `single-node` fixture: typing CIDR 10.50.0.0/24 pre-fills gateway
   10.50.0.1; SNAT is disabled while the gateway is cleared and enabled once set; the finished
   wizard's drafted ops carry the gateway (golden ops, extending T-403's scripted-input set).
   Choosing "keep isolated" drafts a subnet with no gateway and no snat, and validates clean.
2. A changeset with `sdn.subnet.create {cidr: 10.50.0.0/24, snat: true, gateway: ""}` posted
   directly (no wizard) → validation returns error `sdn.snat_requires_gateway` with a fix patch
   setting gateway 10.50.0.1; applying the fix revalidates clean. Gateway `10.9.9.1` on the same
   CIDR → `schema.gateway_not_in_subnet` error with the same fix; both are table tests plus the
   existing fix property test extended.
3. `evpn-lab` fixture: drafting a subnet without a gateway in zone `evpnz` yields warning
   `sdn.evpn_gateway_missing`; the same subnet with snat=true after removing the zone's exit
   nodes in the same changeset yields `sdn.snat_requires_exit_node`; a subnet mirroring the
   existing 192.168.50.0/24 (gateway + snat + exit nodes) yields neither.
4. pvemock: POST subnet with snat and no gateway → 400; with out-of-CIDR gateway → 400; a
   validator-bypassing changeset (ops injected in-test) applying the snat-without-gateway shape
   fails at the `sdn_stage` step and T-402's rollback restores staged SDN config — asserted
   against the fixture's pre-state. Creating the full simple-zone wizard output against
   `single-node` leaves a `gateway: true` IPAM record for 10.50.0.1 readable via `GET
   /ipam/subnets` allocations.
5. Applying an sdn-carrying changeset with no resolvable PVEGateway is rejected up front with
   `pve_session_required` and the changeset remains in its pre-apply status (no `failed` row
   containing "no PVE gateway available"); regression test at the API layer.
6. `make check` passes; `three-node-vlan`/`evpn-lab`/`ipam-lab`/`single-node` fixtures load
   byte-compatible except where AC4's gateway-IPAM registration required additions.

---

## T-702 · Management-path visibility: detect, badge, inspect
**model:** sonnet-5 · **size:** M · **depends:** T-602, T-607 (ships on the released v1 surface; uses T-203's detection) · **context:** `docs/features/topology.md` §2 §3, `docs/security.md` (Safety interlocks), `docs/features/blueprints.md` §3, `docs/features/monitoring.md` §5, `docs/api.md` (topology, protected-interfaces, findings)

**Objective:** Make each node's management path a first-class, visible thing: which interface carries the node's management IP (and corosync links), which physical NICs ultimately carry it, and whether that path has any redundancy — in both topology views, the inspector, and the findings stream. Today this knowledge exists only inside the T-203 interlocks (`internal/change/protected.go`); no user-facing surface shows it.

**Deliverables:** A shared classification/path resolver, factored so both the change engine and topology can use it: given an inventory snapshot + the confirmed `ProtectedConfig` (falling back to live `DetectProtected` when protected.json is empty — labeled `source: "detected"`), produce per node: each protected ref with roles (`mgmt` from `Node.IP` match, `corosync` from ring-addr match — both roles possible on one ref), the resolved physical path (carrier → parent bridge for VLAN sub-interfaces → bridge ports → bond slaves → PhysNics), and a `redundant` bool (≥2 link-up physical NICs in the path). New `GET /protected-interfaces/status` returning exactly that shape (documented in `docs/api.md`; additive). Topology projection: `mgmt`/`corosync` badges on carrier nodes and `mgmt-path` on path members — corosync/confirmed-set input injected into `internal/topology` via a seam (Project stays a pure function; `internal/api` supplies it the way the finding-badge overlay already is, `router.go:68`), badge vocabulary documented in `docs/features/topology.md` §3. UI: distinct mgmt badge treatment in the graph view (`EntityNode`), the switch faceplate (`switchModel.ts`/`SwitchFaceplate.tsx` — badge the faceplate header and the uplink-bay ports on the path), and an inspector "Management path" section (carrier, path chain, redundancy statement in plain English, `source: detected` caveat with a link to the onboarding protected step when unconfirmed). New health check `mgmt_single_path` in `internal/findings` (source `health`, severity warning, one finding per non-redundant node, stable id, `docsLink`, hysteresis-exempt since it's structural not counter-based) — this is also T-703's entry point. pvemock: add spare `eno2` to `single-node.yaml`; add one mgmt-on-VLAN-sub-interface case; corosync ring data served by the mock host reader if absent.

**Acceptance criteria:**
1. Golden projection tests: `three-node-vlan` → every vmbr0 badges `mgmt` (+`corosync` where rings match), its bond and both slave NICs badge `mgmt-path`, all three nodes `redundant: true`; `single-node` → vmbr0 badges `mgmt`, eno1 badges `mgmt-path`, `redundant: false`; the VLAN-carrier fixture badges the sub-interface `mgmt` and walks through its parent bridge.
2. `GET /protected-interfaces/status` matches the badge set exactly (property test: every ref with a role badge appears in status and vice versa), reports `source: "confirmed"` with protected.json present and `"detected"` with it absent — and with it absent, badges still render.
3. `single-node` raises exactly one `mgmt_single_path` finding (stable id across polls); `three-node-vlan` raises none; the finding names the node and the carrier ref, and the existing `/topology` finding-badge overlay dashes the carrier.
4. Both views render the badge: `SwitchView.render.test.tsx` and a graph-view render test assert the mgmt marker against the three-node-vlan fixture; inspector test asserts the "Management path" section incl. the not-redundant wording.
5. Existing T-203 behavior byte-identical: `internal/change` tests untouched and green (the resolver refactor must not change validator semantics).
6. `docs/api.md` + `docs/features/topology.md` updated; `make check` green.

---

## T-703 · Guided management-redundancy & dedicated-mgmt-interface wizard
**model:** **strong (Opus/Fable-class)** · **size:** L · **depends:** T-702 · **context:** `docs/features/change-management.md` §1 §2 §4 §5, `docs/architecture.md` §4, `docs/security.md` (Safety interlocks — binding constraint), T-403's wizard framework (`web/src/sdn/wizards/`), `docs/api.md` (changesets, protected-interfaces)

**Objective:** A guided flow to fix what T-702 exposes: make a node's management path redundant, or move management onto a proper dedicated VLAN interface — the single most dangerous edit the product can perform. The wizard must produce changesets that are **interlock-clean by construction**: every flow preserves the management IP *value* and its physical connectivity in the changeset's net effect, which T-203's net-effect analysis already validates clean (phase-2 T-203 AC 2). There is **no interlock override**: re-addressing the management IP stays out of scope (docs/security.md's "no override in UI" stands unamended), and the interlock remains armed as the backstop behind the wizard.

**Deliverables:** Wizard flows (T-403's `WizardShell` step engine + live preview pane, launched from T-702's `mgmt_single_path` finding, the inspector's "Management path" section, and the topology NewEntityMenu): **(A) Bond the management uplink** — pick a second candidate NIC (link state + LLDP neighbor shown; warn on no-carrier or LLDP showing a different switch pair without MLAG evidence), choose mode with plain-English guidance (default `active-backup` when LLDP can't confirm LACP peer config; explicit "your switch must be configured for LACP first" copy for `802.3ad`), emit `bridge.port.remove` + `bond.create` + `bridge.port.add` targeting the mgmt bridge; **(B) Add/replace a slave in an existing mgmt-path bond** (`bond.update`); **(C) Dedicated mgmt VLAN interface** — create a VLAN sub-interface (`vlan.create` with the node's *existing* mgmt address + gateway) and remove the address from the old carrier (`bridge.update`/`iface.update`) in the same changeset. Output is always a changeset draft into the standard drawer — never a direct apply; per-node flows with an optional "repeat for other nodes" fan-out that stages one changeset per node (each node's own candidate NICs re-selected; cluster-aware per CLAUDE.md). Apply-side ceremony (additive, not an override): a server-computed `touchesMgmtPath` flag on the changeset (ops ∩ T-702's path refs, documented in `docs/api.md`), which the review screen turns into a mandatory acknowledgement block — typed node name, plain-English explanation of the commit-confirm safety net, confirm window defaulted to 180s and not reducible below the default for such changesets — and an audit entry recording the acknowledgement. Post-commit: if the carrier ref moved (flow C), prompt an audited `PUT /protected-interfaces` refresh pre-filled from `GET /protected-interfaces/suggest`. All explanatory copy in one strings file (T-403's non-expert bar). Hardware-validation notes in the report are mandatory (see AC 7).

**Acceptance criteria:**
1. Golden ops per flow against pvemock: flow A on `single-node` (eno1+eno2, both modes), flow B and "already redundant → wizard says so and offers nothing destructive" on `three-node-vlan`, flow C on the T-702 VLAN fixture — each produced changeset validates with **zero** safety-class findings.
2. Backstop proof: tamper tests mutate each wizard's golden ops (drop the `bridge.port.add`; change flow C's address to a new IP) → validation fails with `safety.protected_interface`. The wizard cannot construct these states through its UI (unit test on the op builder).
3. End-to-end against pvemock `single-node`: finding → wizard → drawer → review (ack block renders, typed node name required, apply disabled until complete) → apply → countdown → confirm → `committed`; fixture interfaces file shows the bond; the `mgmt_single_path` finding clears on the next poll; audit trail contains the acknowledgement entry.
4. No-confirm path: same changeset, deadline expires → `rolled_back`, pre-state byte-identical, finding still present.
5. `touchesMgmtPath` is computed server-side for *any* changeset touching the path (hand-built drafts included, wizard or not) and false otherwise (table test); confirm window floor enforced server-side (`400` on a lower `confirmTimeoutSec`).
6. Flow C commit → protected-set refresh prompt appears, accepting it PUTs the new carrier ref (audited); declining leaves protected.json untouched and a warning visible in T-702's status (`staleProtected` or equivalent).
7. Report enumerates what could not be proven against mocks — at minimum: real ifreload outage window on an active mgmt bridge, LACP against a real switch, active-backup failover, auto-rollback with mgmt down on a peer node — as the hardware-validation list. `make check` green; abandoning the wizard leaves no draft residue.
