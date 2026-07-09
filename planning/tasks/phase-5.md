# Phase 5 — Firewall & path simulator

Goal: the visual firewall and the truth-telling path simulator. Milestone: **v0.9**. T-503 is a correctness core — its value collapses if it lies (spec's honesty contract).

---

## T-501 · Firewall read: rulesets, objects, resolved view
**model:** sonnet-5 · **size:** M · **depends:** T-101 · **context:** `docs/features/firewall.md` §1 (the spec), `docs/api.md` (/firewall), `docs/data-model.md` (FwRuleset/FwRule)

**Objective:** Complete, correct visibility of pve-firewall state across all three scopes before any writes.

**Deliverables:** Firewall collector: cluster/node/guest rulesets, options (incl. enablement per scope), aliases, ipsets, security groups → inventory (data-model shapes); **resolution engine**: for any guest, the effective evaluation order exactly as pve-firewall applies it (cluster → groups → guest → defaults) with origin labels — this resolver is also T-503's substrate, build it as a pure, well-tested library (`internal/fw`); `GET /firewall/rulesets?scope=` + `/firewall/objects` per API doc (group expansion included); UI: hierarchy navigation, per-scope rule tables (read-only this task), the resolved view per spec, the "firewall is OFF at scope X" banner logic per spec §2, object usage tracking display ("referenced by N rules").

**Acceptance criteria:**
1. Golden resolved views for ≥5 fixture guests covering: cluster-only rules, group inclusion, guest overrides, disabled-scope, default-policy fallthrough.
2. Resolution order verified against PVE documentation semantics in table tests (each documented evaluation step has a test proving its position).
3. Enablement banners: datacenter-off fixture shows the documented warning at every affected scope.
4. Alias/ipset/group usage counts correct on fixtures; macro definitions render expansion previews.
5. `internal/fw` resolver is I/O-free (pure over inventory snapshot) — enforced by package review + no forbidden imports (lint rule).

---

## T-502 · Firewall ops & editors
**model:** sonnet-5 · **size:** L · **depends:** T-501, T-205 · **context:** `docs/features/firewall.md` §2 §3 (the spec)

**Objective:** Full firewall editing through the change engine.

**Deliverables:** `fw.*` ops per data model (rule create/update/delete/move, options, aliases, ipsets, groups) with position semantics handled explicitly (moves are position ops, concurrent-edit-safe via revalidation against live position state at apply); validators (referenced-object deletion blocked with reference list per spec; syntax per pve-firewall grammar; ipset/alias content validation; macro existence); post-apply verification per spec §3 (compiled status per node clean, surfaced otherwise); editor UI per spec §2: drag-to-reorder tables, inline enable/disable, builder row with autocomplete (aliases/ipsets/guest IPs/subnets from inventory, service presets, macro picker with expansion preview), object editors with usage guards, scope enable/disable with the "what will happen" summary; rule effects preview (P1, per spec: list matched guests/IPs computed via the T-501 resolver).

**Acceptance criteria:**
1. Lifecycle: build a guest ruleset (3 rules via builder incl. a macro), reorder by drag, apply → pvemock `.fw` state matches golden; post-apply verification passes.
2. Deleting an alias referenced by 9 fixture rules → blocked, reference list rendered, deep-links work.
3. Move-op race: fixture position shifted between draft and apply → revalidation catches, user prompted (no silent misplacement).
4. Effects preview for a group rule lists exactly the fixture's matching guests.
5. Scope-disable summary correctly states consequences for each scope (golden strings).

---

## T-503 · Path simulator engine ★
**model:** **strong (Opus/Fable-class)** · **size:** L · **depends:** T-501, T-103 (review checkpoint: truth core) · **context:** `docs/features/firewall.md` §5 §6 (the spec and its honesty contract — binding)

**Objective:** `Simulate(graph, src, dst, proto, port) → Result`: static reachability over configured state with the blocking cause identified. **Every supported feature evaluated correctly or explicitly declared "not evaluated" in caveats — no silent approximation.**

**Deliverables:** `internal/sim`, pure over an inventory snapshot: endpoint resolution (guest NIC / IP literal / external per spec); L2 path computation (shared bridge/VNet membership, VLAN tag compatibility along NIC→bond→bridge→VNet chains incl. trunk pruning, QinQ, VLAN-aware bridge VID sets); L3 (subnet membership, SDN gateways, guest-agent IPs with source-confidence caveat, SNAT flags, zone boundaries + exit-node routing per SDN semantics); firewall evaluation via T-501's resolver at every enforcement point the path crosses, in PVE's real order, with macro/alias/ipset resolution and default policies; verdict + hop list + blocking rule ref / missing-link description per API doc shape; explicit caveat generation (conntrack nuances, guest-internal firewalls, "not evaluated: <feature>" for anything out of engine scope); `POST /simulate/path` endpoint.

**Acceptance criteria:**
1. Verdict test matrix ≥80 cases spanning: same-bridge allow, VLAN mismatch unreachable (with the exact missing-trunk message per spec), cross-zone unreachable vs. exit-node reachable, deny with blocking rule at each enforcement point (cluster/group/guest × in/out), macro expansion correctness, ipset membership, default-policy falls, disabled-scope passthrough, SNAT-flagged asymmetry caveat, external endpoints both directions.
2. Differential property: for firewall-only questions, engine verdicts agree with the T-501 resolved view on every fixture guest pair (script-generated exhaustive sweep on the three-node fixture).
3. Every `Result` includes the caveat list; a grep-able inventory in the report maps each pve-firewall/SDN feature → evaluated | caveated (the honesty audit — reviewer checks it for completeness).
4. ≥90% coverage; pure (no I/O imports, lint-enforced); 10k random simulations on the largest fixture < 5s total (benchmark).
5. Unknown/unsupported entity kinds in the path → explicit `not evaluated` caveat, never a confident verdict.

---

## T-504 · Simulator UI & map path rendering
**model:** sonnet-5 · **size:** M · **depends:** T-503, T-107 · **context:** `docs/features/firewall.md` §5, `docs/user-guide.md` §3 (task table)

**Objective:** Make the simulator a first-class tool: endpoint pickers, verdict presentation, the path drawn on the map.

**Deliverables:** Tools → Path simulator page: endpoint pickers (guest NIC search, IP entry with subnet context, "external"), proto/port with service presets; result panel: verdict banner, hop-by-hop list, blocking-rule card deep-linking to its editor, missing-link explanation, caveats rendered honestly (per the spec's labeling requirement — "simulated" badge always visible); map integration: "Trace path" from any two entities (inspector quick action + right-click), path highlighted hop-by-hop on the topology canvas with the verdict color, blocking point marked; shareable state (URL-encoded simulation params).

**Acceptance criteria:**
1. The user-guide scenario ("why can't VM A reach VM B") is demonstrable end-to-end on the fixture: deny verdict → blocking rule card → one click lands in the rule editor with the rule focused.
2. Unreachable-VLAN case renders the missing link on the map at the correct edge.
3. Caveats always visible when present (not collapsed by default); "simulated" badge on every result.
4. Simulation URL round-trips (paste → same result rendered).
5. Trace-path from map pre-fills and runs; works for guest→guest, guest→external, IP→guest.

---

## T-505 · Firewall log viewer (P1)
**model:** sonnet-5 · **size:** S · **depends:** T-301, T-501 · **context:** `docs/features/firewall.md` §4

**Objective:** Tail and correlate pve-firewall logs cluster-wide.

**Deliverables:** Per-node log reader (pve-firewall log format parser, fixture corpus incl. format variants and garbage lines) via peer API with tail/follow (bounded buffer); UI: filterable stream (guest, direction, action, node), pause/resume, rule correlation per spec (parse rule references where PVE logs include them → deep-link to the rule; label uncorrelatable lines honestly); rate cap + drop indicator for log storms.

**Acceptance criteria:**
1. Fixture log corpus parses (garbage lines skipped with counter, never crash); filters compose correctly.
2. Correlated lines deep-link to the right rule; uncorrelatable lines labeled as such.
3. Follow mode over WS sustains a storm fixture (10k lines/min) with UI cap + drop indicator engaged, no browser lockup.
