# Feature spec — Change management

The change engine is vnprox's safety core. Architecture §4 defines the lifecycle; this spec defines behavior and UX.

## 1. Changeset drawer (UX)

A persistent drawer (bottom-right) accumulates draft ops as the user edits anywhere in the UI — map drag-drops, form edits, firewall rule changes all land here. The drawer shows: op list (human-readable summaries, reorderable, individually removable), live validation status, and buttons **Review & apply** / **Discard**. Multiple named drafts can be parked and resumed. Editing never mutates anything until apply.

## 2. Validation

Runs on every draft change and again immediately before apply (state may have moved). Validator classes, executed in order:

1. **Schema** — types, ranges (VID 1–4094, MTU 576–9216, bond mode enums, CIDR syntax).
2. **Referential** — targets exist; no duplicate enslavement; name collisions; VID overlaps on a trunk; address overlaps with existing subnets.
3. **Safety (interlocks)** — see `docs/security.md`: management-IP interface, corosync links, bridges with running guests. Hard errors.
4. **Advisory** — style/health warnings (bond without `xmit_hash_policy layer3+4` on 802.3ad, bridge without description, single-slave bond).

**Cross-node consistency (T-607 docs audit correction): not a pre-apply validator class.** An earlier version of this doc listed "same-named bridge divergence, MTU path mismatches introduced by the change, SDN zone node coverage" as a fifth pre-apply validator class between Safety and Advisory; no such class exists in `internal/change` (`validate.go` explicitly marks that insertion point unassigned). SDN zone node-membership/bridge-existence checks for the zone *being changed* are covered by `sdnValidate` (SDN-specific, not general cross-node bridge/MTU checks). General cross-node consistency (same-named bridge divergence, MTU path mismatches across nodes) is instead caught **after the fact** by the async drift checker (30s interval, `docs/features/topology.md` §6) rather than blocking or warning at validate/apply time. This is a real, currently-accepted gap, not a documentation nit: it means a change that introduces cross-node drift is not flagged until the next drift cycle, not at review time. Flagged here per T-607 AC1's "justified exception" allowance; follow-up: add a genuine pre-apply cross-node validator class (P1 candidate for the next release, not implemented in this pass since it is new validator logic, out of scope for a verification-only release-gate task).

Findings carry optional machine-applicable `fix` patches (API doc). Errors block apply; warnings require an explicit "apply with warnings" checkbox.

## 3. Diff & plan review

The review screen shows three tabs: **Summary** (op cards), **File diff** (unified diffs of every file the change touches, per node — `/etc/network/interfaces`, SDN configs, firewall files), and **Plan** (the exact ordered steps: which PVE API calls, which nodes reload, in what order). Nothing applies until the user has seen this screen.

## 4. Apply, confirm, rollback

- Apply executes the plan; each step's outcome streams to the UI (`changeset.status` WS events).
- Default confirm window **120s** (configurable 30–600). The countdown renders as a full-width banner. Confirmation requires an authenticated API round-trip — if connectivity broke, rollback happens server-side at the deadline.
- Rollback restores pre-snapshot files on affected nodes and re-runs ifreload + SDN apply as needed; result is audited and the changeset marked `rolled_back` with the failure step preserved for diagnosis.
- Manual rollback of a `committed` changeset is offered for 7 days (creates a new restoring changeset via the normal flow).
- **Management-path changes (T-703):** a changeset whose ops intersect a node's resolved management path (the server-computed `touchesMgmtPath` flag — docs/api.md's changesets section) gets an *additive* apply-time ceremony on top of the normal flow, never an interlock override: the review screen requires a typed node-name acknowledgement (audited as `changeset.mgmt_ack`) before Apply enables, and the commit-confirm window defaults to and cannot be set below **180s** (rejected server-side with `confirm_window_too_short`). The T-203 safety interlock (`safety.protected_interface`) remains the enforcement backstop behind the guided management-redundancy wizard, which is interlock-clean by construction (it preserves the management IP value and its physical connectivity in every changeset's net effect). Re-addressing the management IP stays out of scope (docs/security.md's "no override in UI" is unamended).
- If a node becomes unreachable mid-apply: remaining steps abort, completed nodes roll back, the unreachable node's daemon rolls back locally at its deadline (each node arms its own local timer at step start — no cross-node dependency for safety).

## 5. Editors: bridges, bonds, VLANs, interfaces

Form-based editors open from the map or list views; every field has inline help written for non-networking-experts (e.g. bond modes explained with "use this when..."). Specific requirements:

- **Bridge**: kind (Linux/OVS), ports multi-select with conflict hints, VLAN-aware toggle with VID range editor, addresses, MTU, STP, comment. Deleting a bridge with attached guests requires choosing a reattachment target (generates the guest ops in the same changeset).
- **Bond**: mode selector with live guidance, slave picker showing current link state/speed of candidates, LACP options (rate, hash policy), MII monitor interval. Post-apply, the inspector shows live bond state (mode, MII status, active slave, per-slave link/failure-count) parsed from `/proc/net/bonding/*` (`internal/host/bonding.go`). **Known gap (flagged, T-607):** LACP actor/partner system ID and 802.3ad port-state flags specifically are not parsed/exposed — only the bond-level fields above. `web/src/changesets/editors/BondEditor.tsx` already self-documents this as an acknowledged host-collector dependency. Follow-up: extend `internal/host/bonding.go`'s `/proc/net/bonding/<name>` parser with the actor/partner block if/when a task picks this up; not release-blocking for v1.0 (the bond-level fields already shipped cover the safety-relevant signals — slave down, mode, active slave).
- **VLAN**: parent picker, VID, addresses, MTU (warn when exceeding parent).
- **Interface**: MTU, addresses/gateway, autostart, comment. Renaming NICs is out of scope v1 (link files are a hardware-specific minefield) — document the manual procedure instead.

## 6. Guest NIC operations

List + map views support: reattach to another bridge/VNet, change VLAN tag, rate limit, toggle firewall flag, disconnect/connect (`link_down`). **Bulk mode**: select N guests (filter by current bridge/VLAN/node) → one changeset moving all, with per-guest hotplug attempted and per-guest results reported. Ops use the user's ticket via PVE API (`PUT /nodes/{n}/qemu/{vmid}/config` etc.).

## 7. Raw editor escape hatch

A Monaco-based editor for `/etc/network/interfaces` per node with interfaces(5) syntax linting and the same validators run against the parsed result. Saving creates a changeset whose single op is `iface.raw.replace` — still diffed, still commit-confirmed. This keeps power users inside the safety envelope instead of SSHing around it.

## 8. Audit UI

Filterable table (user, date range, target, result) over the merged cluster audit log; each row expands to op summaries and links to the changeset and its snapshots.
