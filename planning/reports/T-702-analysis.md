# T-702/T-703 analysis — management-path visibility and guided redundancy

Triage date: 2026-07-12 (pre-implementation investigation for the T-702/T-703 cards in
`planning/tasks/phase-7.md`).

## How mgmt detection works today

Detection exists — but only inside the change engine, exclusively to power the T-203 interlocks.
Nothing display-facing consumes it.

- **Detection:** `change.DetectProtected` — `internal/change/protected.go:139-187`. For each
  `inventory.Node` it builds a wanted-IP set from `Node.IP` (sourced from PVE
  `GET /cluster/status`, see `internal/inventory/entity.go:91-99`) plus corosync ring addresses
  from `host.CorosyncConfig` (`internal/host/corosync.go`, parsed from `/etc/pve/corosync.conf`),
  then matches it against `Bridge.Addresses` and `VlanIface.Addresses` — the only two inventory
  kinds carrying addresses. IP matching is mask-insensitive (`addrMatchesAny`,
  protected.go:195-205).
- **Persistence:** onboarding-confirmed set at `/etc/pve/vnprox/protected.json`
  (`protected.go:22`, pmxcfs-replicated so inherently cluster-wide), exposed via
  `GET/PUT /protected-interfaces` + `GET /protected-interfaces/suggest`
  (`internal/api/protected.go:87-97`), confirmed in the onboarding "protected" step
  (`web/src/onboarding/protectedDraft.ts`).
- **Enforcement:** `safetyValidate` → `protectedInterfaceFindings`
  (`internal/change/validate_safety.go:22-110`): net-effect projection checks that every
  protected address survives (`proj.hasHostIP`), isn't parked on a port-less bridge, and that a
  protected bridge keeps ≥1 port (`protectedBridgePathFindings`). `SafetyOptions` is rebuilt from
  protected.json on every validation (`internal/change/service.go:246-252`). The only override is
  the **global config flag** `safety.allow_dangerous_ops` (`internal/config/config.go:235`);
  docs/security.md locks in "no override in UI".

## Gaps

1. **Zero display surface.** `internal/topology` is a pure function of the inventory snapshot;
   `badgesOf` (`internal/topology/project.go:371`) knows nothing of mgmt/protected. Neither graph
   view, switch faceplate, inspector, nor `EntityDetail` marks anything as management.
2. **No role distinction.** `ProtectedConfig.Nodes` is a flat ref list — "management IP" vs
   "corosync link" is not recorded, but display wants distinct badges.
3. **No path/redundancy computation.** Nothing walks carrier → bridge → ports → bond → slaves to
   answer "which physical NICs carry mgmt" or "is that path a single point of failure".
4. **Known enforcement scope gap** (self-documented at `validate_safety.go:112-121`): a protected
   ref of kind physnic/bond contributes no protected IPs — harmless for display but worth
   carrying into the status contract.
5. **Corosync addrs aren't in inventory** — only readable from the local corosync.conf. A
   pure-snapshot projection can compute the *mgmt* badge (Node.IP is in the snapshot) but
   corosync classification must be injected.
6. **Skipped onboarding ⇒ empty protected.json ⇒ silently inert interlocks and no display
   source.** Visibility falls back to live `DetectProtected` output, labeled
   "detected (unconfirmed)".
7. **No guided flow at all** for creating a dedicated mgmt interface or making the mgmt path
   redundant.

## Reconciling "wizard edits mgmt" with "interlock blocks mgmt"

The crucial existing fact: **T-203 is net-effect based and already permits
connectivity-preserving mgmt restructuring** — T-203 AC 2 (`planning/tasks/phase-2.md`):
*"moving the mgmt IP to a new bridge and deleting the old one in one changeset validates
clean."* So the wizard does **not** need an interlock override at all if every flow **preserves
the mgmt IP value and its physical path by construction**:

- *Migrate uplink to bond:* `bridge.port.remove(eno1)` + `bond.create(bond0, slaves=[eno1,eno2])`
  + `bridge.port.add(bond0)` — address stays on vmbr0, final port count ≥1 → validates clean.
- *Dedicated mgmt VLAN interface:* `vlan.create` (carries `Addresses`, `params_vlan.go:19-21`)
  with the **existing** mgmt IP + `iface.update`/`bridge.update` removing it from the old
  carrier — net effect preserves the IP → validates clean.
- *Re-addressing (changing the IP value) is explicitly out of scope* — it implies
  corosync/hosts/pveproxy changes beyond interface config, and keeping it out preserves
  docs/security.md's locked "no override in UI" decision. No security.md amendment needed.

The interlock stays armed as a **backstop**, and the ceremony is *additive*, not an override:
any changeset whose ops intersect the protected/mgmt path gets an explicit acknowledgement gate
at apply (extending T-207's warnings-checkbox pattern), a typed node-name confirmation, and the
commit-confirm window front-and-center — if the apply severs connectivity, the daemon-side
rollback timer (restart-safe per T-205) restores the pre-state. Post-commit, the carrier ref may
have moved, so the flow ends with an audited `PUT /protected-interfaces` refresh.

## Contract changes required

- **docs/api.md** (additive): new node badge values on `GET /topology`: `mgmt`, `corosync`,
  `mgmt-path`; new endpoint `GET /protected-interfaces/status` (per-node roles + resolved
  physical path + `redundant` bool + `source: confirmed|detected`); new `/findings` health check
  `mgmt_single_path`; T-703 adds a server-computed `touchesMgmtPath` flag on changesets.
- **docs/data-model.md:** no inventory entity or op-vocabulary changes — existing `bond.*`,
  `bridge.port.*`, `vlan.create`, `iface.update` ops suffice (verified against `params_*.go`).
- **docs/features/topology.md §2/§3** and **monitoring.md §5**: document the badges and the new
  health check.

## pvemock fixture needs

- `single-node.yaml`: vmbr0(192.168.1.10)←eno1 is already the perfect SPOF case, but has no
  spare NIC — add `eno2` (unconfigured, link up) as the redundancy candidate.
- `three-node-vlan.yaml`: already bonded/redundant (mgmt 10.10.0.11-13) — the "already
  redundant, wizard says so" case.
- Add a mgmt-on-VLAN-subinterface node (in `messy-brownfield.yaml` or a new small fixture) to
  cover the VlanIface carrier path, plus corosync ring data in the mock host reader if
  `internal/pvemock/hostreader.go` doesn't serve it yet.

## Needs real-PVE hardware validation

1. `ifreload -a` while restructuring the *active* mgmt bridge: length/character of the transient
   outage; whether the browser's confirm round-trip survives; LACP bond formation against a real
   switch (and that `active-backup` — the wizard's default when LLDP can't verify LACP — fails
   over cleanly).
2. Auto-rollback with mgmt actually down, especially when the changeset targets a **peer**
   node's mgmt path (T-304 local-timer machinery under real partition).
3. `GET /cluster/status` node-IP semantics on multi-homed hosts, and corosync ring addrs given
   as hostnames rather than IPs.
