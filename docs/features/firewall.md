# Feature spec — Firewall & path simulation

vnprox configures the existing pve-firewall (cluster/node/guest `.fw` files via PVE API) — it never installs its own ruleset engine.

## 1. Scope model

Three levels — Datacenter, Node, Guest — shown as one navigable hierarchy with effective-policy inheritance made explicit: for any guest, a "resolved view" shows the full evaluation order (cluster rules → security groups → guest rules → default policies) with each rule's origin labeled. **Caveat (flagged, T-607):** `internal/fw/resolve.go` implements this ordering as a documented simplification, not a byte-for-byte match of real pve-firewall's rule-chain traversal — in real PVE, cluster-scope rules only reach a guest's evaluation via an explicit security-group reference, not directly; the code comment flags this as needing hardware validation (this repo has no live PVE cluster to verify against — CLAUDE.md's "needs hardware validation" note applies here).

## 2. Rule editor

- Table editor with drag-to-reorder (position matters; reorders are `fw.rule.move` ops), inline enable/disable, and a builder row: direction, action, source/dest (with autocomplete from aliases, ipsets, guest IPs, subnets), proto/ports (service-name presets), interface, macro picker (HTTP, SMTP, ... with expansion preview), log level, comment.
- **Objects**: aliases, IPSets, and security groups get first-class editors with usage tracking ("this alias is referenced by 9 rules — view"). Deleting a referenced object is blocked with the reference list.
- **Rule effects preview** (P1): before apply, for a group rule, vnprox lists which guests the rule will match (security-group references), computed from inventory (`internal/fw/effects.go`'s `MatchingGuests`). IP-level effects preview is not implemented — a smaller scope than "guests/IPs" as originally worded here, reasonable for a P1 feature.
- Enable/disable firewall at each scope with an explicit "what will happen" summary (e.g. "Datacenter firewall is OFF: none of these rules are active" banner — the classic PVE footgun made visible).

## 3. Writes

All edits are changeset ops (`fw.*`) applied via PVE API with the user's ticket. pve-firewall picks up file changes itself (its 10s compile loop); vnprox verifies post-apply that the compiled status reports no errors and surfaces `pve-firewall` status per node.

## 4. Log viewer (P1)

Tail per-node firewall logs (peer API), parsed and filterable (guest, direction, action); each log line links back to the matching rule where determinable. Correction (this section had drifted from `docs/api.md`'s already-accurate wording): real pve-firewall log lines do **not** embed rule positions/references — rule correlation is heuristic/best-effort (matched against the resolved ruleset by direction/action/guest, not a literal reference PVE logged), so "where determinable" is doing real work here, not a rare edge case.

## 5. Path simulator (P0)

**Question answered:** "Can A reach B on proto/port X — and if not, what stops it?"

- **Endpoints:** guest NIC, arbitrary IP, or "external/WAN".
- **Evaluation:** static analysis over inventory — L2 adjacency (same bridge/VNet + VLAN tag compatibility along the path), L3 (subnet membership, gateways from SDN subnets and guest agent-reported IPs where available, SNAT flags), then firewall evaluation in PVE's real order at the guest-scope enforcement points the path crosses (source guest out-rules, dest guest in-rules), including macro expansion, ipset/alias resolution, and default policies — node/cluster (host chain) scopes are disclosed as off-path rather than evaluated, see below.
- **Output:** verdict (allow / deny / unreachable / **indeterminate** — the engine's honest fourth verdict for a path it cannot fully evaluate, per `docs/api.md`'s Simulator section and the honesty contract below), the hop-by-hop path rendered on the topology map, and for deny: the exact blocking rule (deep link to its editor); for unreachable: the missing link (e.g. "VLAN 30 is not trunked on bond0 of node pve2" or "no route between subnets — different zones without exit node").
- Node/cluster-scope (host chain) rules are disclosed via a `node-firewall-not-on-path` caveat rather than evaluated for guest-to-guest forwarded traffic — a deliberate, disclosed simplification (`internal/sim/firewall.go`), not a silent gap.
- **Honesty contract:** the simulator evaluates *configured* state, not live packets. Results are labeled "simulated"; conntrack-dependent nuances (established flows, NAT reflection) are listed as caveats in the result panel when relevant. A "verify live" button (P2) may later run an actual probe via guest agent.

## 6. Simulator engine notes

Lives in `internal/sim`, pure functions over an inventory snapshot: `Simulate(graph, src, dst, proto, port) Result`. Must be exhaustively table-test-covered — this feature's value collapses if it lies. Every supported PVE firewall feature is either correctly evaluated or explicitly reported as "not evaluated: <feature>" in the result caveats. No silent approximations.
