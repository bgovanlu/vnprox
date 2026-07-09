# Feature spec — Firewall & path simulation

vnprox configures the existing pve-firewall (cluster/node/guest `.fw` files via PVE API) — it never installs its own ruleset engine.

## 1. Scope model

Three levels — Datacenter, Node, Guest — shown as one navigable hierarchy with effective-policy inheritance made explicit: for any guest, a "resolved view" shows the full evaluation order (cluster rules → security groups → guest rules → default policies) as PVE actually applies it, with each rule's origin labeled.

## 2. Rule editor

- Table editor with drag-to-reorder (position matters; reorders are `fw.rule.move` ops), inline enable/disable, and a builder row: direction, action, source/dest (with autocomplete from aliases, ipsets, guest IPs, subnets), proto/ports (service-name presets), interface, macro picker (HTTP, SMTP, ... with expansion preview), log level, comment.
- **Objects**: aliases, IPSets, and security groups get first-class editors with usage tracking ("this alias is referenced by 9 rules — view"). Deleting a referenced object is blocked with the reference list.
- **Rule effects preview** (P1): before apply, for a guest-scope or group rule, vnprox lists which guests/IPs the rule will match, computed from inventory.
- Enable/disable firewall at each scope with an explicit "what will happen" summary (e.g. "Datacenter firewall is OFF: none of these rules are active" banner — the classic PVE footgun made visible).

## 3. Writes

All edits are changeset ops (`fw.*`) applied via PVE API with the user's ticket. pve-firewall picks up file changes itself (its 10s compile loop); vnprox verifies post-apply that the compiled status reports no errors and surfaces `pve-firewall` status per node.

## 4. Log viewer (P1)

Tail per-node firewall logs (peer API), parsed and filterable (guest, direction, action); each log line links back to the matching rule where determinable (PVE logs include rule references in most drop/reject cases).

## 5. Path simulator (P0)

**Question answered:** "Can A reach B on proto/port X — and if not, what stops it?"

- **Endpoints:** guest NIC, arbitrary IP, or "external/WAN".
- **Evaluation:** static analysis over inventory — L2 adjacency (same bridge/VNet + VLAN tag compatibility along the path), L3 (subnet membership, gateways from SDN subnets and guest agent-reported IPs where available, SNAT flags), then firewall evaluation in PVE's real order at every enforcement point the path crosses (source guest out-rules, dest guest in-rules, node/cluster scopes), including macro expansion, ipset/alias resolution, and default policies.
- **Output:** verdict (allow / deny / unreachable), the hop-by-hop path rendered on the topology map, and for deny: the exact blocking rule (deep link to its editor); for unreachable: the missing link (e.g. "VLAN 30 is not trunked on bond0 of node pve2" or "no route between subnets — different zones without exit node").
- **Honesty contract:** the simulator evaluates *configured* state, not live packets. Results are labeled "simulated"; conntrack-dependent nuances (established flows, NAT reflection) are listed as caveats in the result panel when relevant. A "verify live" button (P2) may later run an actual probe via guest agent.

## 6. Simulator engine notes

Lives in `internal/sim`, pure functions over an inventory snapshot: `Simulate(graph, src, dst, proto, port) Result`. Must be exhaustively table-test-covered — this feature's value collapses if it lies. Every supported PVE firewall feature is either correctly evaluated or explicitly reported as "not evaluated: <feature>" in the result caveats. No silent approximations.
