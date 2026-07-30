# Phase 14 — Edge & reach: the network beyond the bridge (v2.2)

Goal: the cluster's network doesn't end at the cluster. Phase 14 gives vnprox authority and
visibility over three things that sit *outside* PVE's own config surface — site-to-site WireGuard
tunnels, the NAT/edge boundary between the cluster and the WAN, and IPv6 as a managed capability
rather than "addresses happen to render" — while holding every new mutation to the exact
stage→validate→diff→apply→confirm/rollback guarantee CLAUDE.md makes non-negotiable. Every new
domain this phase touches (WireGuard's on-node state, upstream ISP/WAN devices, third-party
reverse proxies) is something vnprox does not own: per the carried-forward new-domain invariant
(`docs/architecture.md` §7's storage rule, restated for this arc in the design doc's §6), vnprox
persists **intent + audit only** and treats the owning system's live state as authoritative. Two
surfaces stay read-only forever this arc: WAN/upstream probing and reverse-proxy discovery only
ever probe targets the operator explicitly configured — no unsolicited scanning, no write path.

Dependency shape: T-1401 (WireGuard engine) and T-1403 (Edge & NAT cockpit) are the two independent
roots — neither needs the other to start. T-1404 (IPv6 suite) depends only on already-shipped
surfaces (path simulator, blueprints, IPAM) and can also start on day one. T-1402 (WireGuard map
edges + wizard) fans out from T-1401; T-1405 (WAN health) and T-1406 (ingress visibility) fan out
from T-1403's edge/uplink model; T-1407 (tunnel-aware federation transport) fans out from T-1401.
**T-1401 is this phase's one security-sensitive review checkpoint** (design doc §3, marked 🔒): key
custody and mgmt-path interlock coverage are audited by an Opus-class reviewer before T-1402/T-1407
(or anything downstream) dispatches, on top of the standing per-task adversarial review every card
this arc receives.

Origin: `docs/roadmap-universal.md`'s "Phase 14 — Edge & reach" section (v2.2) and its carried-forward
invariants — Proxmox/the owning system stays authoritative, every mutation flows through the change
engine, read-only surfaces stay read-only. This phase's exit demo: two federated clusters joined by
a wizard-built WireGuard tunnel; a dual-stack VLAN rolled out by the IPv6 wizard; the Edge layer
showing exactly which ports the lab exposes to the internet — with one of them flagged as forwarding
to a powered-off guest.

---

## T-1401 · WireGuard tunnel engine & changeset integration core ★
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-205 (change engine), T-703 (mgmt-path interlock), T-1201 (federation core, for cluster↔cluster edges) · **context:** `docs/architecture.md` §4 §6 §7, `docs/security.md` (Authentication's session-key/AES-256-GCM pattern, Host footprint's fixed-argv subprocess convention), `docs/api.md` (changesets `touchesMgmtPath` section), `docs/data-model.md` §2 §3, `docs/features/change-management.md` §2 §4, `planning/tasks/phase-7.md` T-703, `planning/tasks/phase-12.md` T-1201 (federation registry) and T-1205 (new-op-group + credential-storage precedent)

**Objective:** First-class WireGuard as a new `wg.*` changeset op group — key/config generation
included — flowing through the ordinary stage→validate→diff→apply→confirm/rollback lifecycle, with
private keys generated on and never leaving the owning node. This is the arc's key-custody and
crypto-integration boundary: T-1402, T-1407, T-1306 (Phase 13's MTU prober), and T-1604 (Phase 16's
failure-impact sim) all build on the tunnel model this card fixes. **Standing review gate:** per the
design doc's adversarial-review policy this card receives a dedicated Opus-class review of the key
handling and mgmt-path coverage below before any dependent task dispatches — not just the per-task
review every card gets.

**Safety analysis (required, key-custody review):**
- **Key custody.** A tunnel's private key is generated *on the owning node* via stdlib
  `crypto/ecdh`'s X25519 curve (Go 1.20+ — no new third-party crypto dependency needed for keygen
  itself) as part of applying a `wg.tunnel.create` op; it is written once to the `wireguard_tunnels`
  table AES-256-GCM-encrypted at rest using the identical cipher/key construction
  `sessions.pve_ticket_enc` and the peer cluster secret already use (`internal/store/cipher.go`'s
  `SessionCipher` — not a second key pair), and is never returned by any API response, log line, or
  audit-log detail field. Only the derived public key is exportable (`GET
  /wireguard/tunnels/{id}/pubkey`). A tunnel's key is never regenerated in place by an `update` op —
  regenerating means delete-and-recreate, so key rotation is always visible as two ordinary audited
  changeset ops, never a silent in-place overwrite.
- **mgmt-path interlock coverage.** `internal/topology.ResolveMgmtPaths` (T-702/T-703) is extended so
  a tunnel whose carrier interface is itself part of a node's resolved management or corosync path
  is `touchesMgmtPath: true` on any `wg.*` op — T-703's ceremony (typed node-name acknowledgement,
  180s confirm-window floor) applies with **no override**, exactly like every other mgmt-path-
  touching op family (`docs/security.md`'s "no override in UI" is unamended by this card).
- **Residual risk, stated plainly:** an *external* peer's key material is never vnprox's to protect —
  peers outside vnprox's control are modeled read-only with config export only, and their own key
  hygiene is explicitly out of this card's control (documented in the operator-facing copy, not
  papered over).

**Deliverables:**
- New package `internal/wireguard`: tunnel/peer model, X25519 keygen, and an apply-step type applied
  by `cmd/vnproxd`'s `NodeAgent` alongside the existing iface/SDN steps — writes the on-node
  WireGuard config and execs `wg`/`wg-quick` with a fixed argv array (no dynamic shell
  interpolation), mirroring the existing `ifreload`/`lldpctl` subprocess convention
  (`docs/security.md`'s Host footprint section). WireGuard's own on-node state (interface, live
  handshake/transfer counters) stays authoritative; vnprox never shadow-copies it as truth.
- New op group `wg.*` (`docs/data-model.md` §3 addition): `wg.tunnel.create/update/delete`,
  `wg.peer.add/remove`. New Ref kinds `wg-tunnel`/`wg-peer`. External (non-vnprox-owned) peers are
  `wg.peer.add {external: true}` — modeled read-only, config-export-only, never targeted by an apply
  step of vnprox's own.
- New app-store table `wireguard_tunnels` (id, node, ifName, privateKeyEnc, publicKey, listenPort,
  addresses, mtu, createdBy, createdAt) and `wireguard_peers` (tunnel_id, publicKey, endpoint?,
  allowedIps, presharedKeyEnc?, keepaliveSec?, external bool, clusterId?) — app-owned intent+audit
  per the new-domain invariant.
- Findings (`source: "wireguard"`, computed fresh from a live `wg show <if> dump`-equivalent poll,
  never persisted as truth): `wg_handshake_stale` (handshake age past threshold, hysteresis-
  debounced) and `wg_endpoint_drift` (a peer's live observed endpoint disagrees with its configured
  one — the NAT-rebind case).
- `GET /wireguard/tunnels` (read view, live status + config, mirrors `GET /sdn`'s
  running-vs-config-truth pattern), `GET /wireguard/tunnels/{id}/pubkey` (public key only), `GET
  /wireguard/tunnels/{id}/peer-config` (exportable config for an external peer's own side).
- Fixture family (new, deliverable of this card): **WireGuard state fixtures** — handshake-age,
  transfer-counter, and endpoint-drift scenarios, plus an external-peer export case.
- `docs/api.md` new WireGuard section; `docs/data-model.md` new op group/Ref kinds/tables;
  `docs/security.md` new credential-storage note (same AES-256-GCM primitive, explicitly cross-
  referenced, not a new one).

**Acceptance criteria:**
1. `wg.tunnel.create` applied against a fixture never surfaces the private key in any API response,
   audit-log detail, or log output — assert on the raw stored bytes and every response body
   (mirrors T-1201 AC1's credential-ciphertext test).
2. `GET /wireguard/tunnels/{id}/pubkey` round-trips the public key derived from the same keypair;
   no route or op can retrieve the private key.
3. A `wg.tunnel.create` targeting a carrier interface on a node's resolved mgmt/corosync path is
   `touchesMgmtPath: true` and gets T-703's ceremony (typed ack required, 180s floor); a lower
   `confirmTimeoutSec` is rejected with `confirm_window_too_short` — golden test against
   `three-node-vlan` extended with a WireGuard-carrying uplink.
4. WireGuard state fixtures drive `wg_handshake_stale`/`wg_endpoint_drift`: a stale-handshake
   scenario raises exactly one finding with hysteresis (a single missed check doesn't fire); an
   endpoint-drift scenario raises the other; both clear when the fixture returns to healthy.
5. An external peer (`{external: true}`) is never targeted by an apply step (assert zero writes
   reach it) and its config-export route returns a complete peer-config block.
6. Full lifecycle test: stage → validate → diff → apply → confirm against a fixture, then a second
   test forcing rollback (commit-confirm timeout) — the tunnel and its generated keypair are fully
   reverted, no orphaned key material left in the store.
7. `docs/api.md`, `docs/data-model.md`, `docs/security.md` updated; `make check` green.

---

## T-1402 · WireGuard map edges & "connect two clusters" wizard
**model:** sonnet-5 · **size:** M · **depends:** T-1401, T-1202 (global topology), T-901/T-902 (renderer) · **context:** `docs/features/topology.md` §1 §2 §3, `docs/api.md` (topology rendering contract, WS `topology.delta`), T-1401's tunnel/finding model, `planning/tasks/phase-12.md` T-1202 (cluster-capsule pattern)

**Objective:** Tunnels rendered as map edges — between federated clusters or to standalone
endpoints — with live handshake/transfer/endpoint-drift status, plus a guided "connect two clusters"
flow that stages both the tunnel **and** the firewall rules it needs in one reviewable changeset.
No half-open state where a tunnel exists but its firewall doesn't.

**Deliverables:**
- Frontend: a new edge kind (`wg-tunnel`) rendered between the two endpoints it connects — a cluster
  capsule (T-1202) on each end for a federated link, or a standalone-endpoint node for an
  external/road-warriorless site-to-site peer. Edge status paints from T-1401's live findings:
  healthy (recent handshake), `wg_handshake_stale` (amber), `wg_endpoint_drift` (dashed/flagged).
- "Connect two clusters" wizard (`web/src/wireguard/ConnectClustersWizard.tsx`): picks a source and
  target cluster (or a manually-entered external endpoint), previews the resulting routed link, and
  submits **one** `POST /changesets` whose ops are `wg.tunnel.create` + `wg.peer.add` on both ends
  (where both ends are vnprox-managed) plus the `fw.rule.create` ops opening exactly the traffic the
  link needs — a single reviewable diff, never two changesets or a partial apply.
- Vitest + Testing Library for the wizard's step logic and the edge-status paint mapping; `web/e2e`
  Playwright: attach two mock clusters, run the wizard end to end, confirm the resulting changeset
  contains both the tunnel and firewall ops, apply it, and see the edge render healthy afterward.

**Acceptance criteria:**
1. A tunnel between two attached mock clusters renders as one map edge with the correct live status
   badge for each of T-1401's three states (healthy/stale-handshake/endpoint-drift) — Vitest test.
2. The wizard's preview pane matches the changeset it actually submits (no drift between preview and
   submitted ops) — Testing Library test.
3. Playwright: wizard run against two pvemock-backed clusters produces one changeset with both
   `wg.*` and `fw.*` ops; applying it leaves the edge rendering healthy on the next poll.
4. Regression: no code path in the wizard or the edge renderer calls any apply/mutate route other
   than the single `POST /changesets` the wizard stages — the wizard cannot construct a half-open
   state (tunnel without its firewall rules) even by cancelling mid-flow.
5. `docs/features/topology.md` updated for the new edge kind; `make check` green.

---

## T-1403 · Edge & NAT cockpit
**model:** sonnet-5 · **size:** L · **depends:** T-402 (SDN views), T-205 (change engine), T-901 (renderer) · **context:** `docs/features/sdn.md` §2 (simple-zone SNAT), `docs/api.md` (`GET /sdn` `Subnet.snat`, `Firewall/SDN/IPAM` read-view section), `docs/features/change-management.md` §7 (raw interfaces editor pattern), `docs/architecture.md` §4 (`NodeAgent` interfaces-file write path)

**Objective:** A dedicated "Edge" map layer answering "how does traffic actually leave, and what's
exposed inbound?" — default routes, upstream gateway(s), PVE-host NAT/masquerade and port-forward
rules (including PVE SDN simple-zone NAT, already surfaced read-only via `GET /sdn`'s
`Subnet.snat`), and guest-level DNAT paths, editable via ordinary changeset ops. Surfacing
inbound exposure must not itself become a write path — every edit here is a normal staged op, no
new mutation mechanism.

**Deliverables:**
- New Ref kinds `nat-rule:<node>:<id>` and `static-route:<node>:<id>`. New op group `nat.*`
  (`nat.masquerade.create/delete`, `nat.portforward.create/update/delete`) and `route.static.create/
  update/delete` (`docs/data-model.md` §3 addition) — applied via `NodeAgent`'s existing
  interfaces-file write path as `post-up`/`post-down` stanzas, the same "vnprox writes the file it
  will re-read" pattern `iface.raw.replace` already established (`docs/features/change-management.md`
  §7); a node's *default* gateway stays owned by the existing `iface.update`'s `gateway` field — this
  card adds only additional/policy static routes, not a second way to set the primary gateway.
- `GET /edge/routes` (per-node default route + any static routes, live `ip route` cross-checked
  against config) and `GET /edge/nat` (masquerade/port-forward rules + SDN simple-zone NAT read from
  `GET /sdn`'s existing `Subnet.snat` + guest-level DNAT paths), both netRead-gated, read-only.
- Frontend: a new "Edge" map layer (nav rail entry, reusing T-901's renderer) drawing default
  route(s), NAT/masquerade badges, and port-forward rules with an inbound-exposure summary per node;
  a port-forward whose target guest is currently powered off is flagged distinctly.
- Fixture family (new, deliverable of this card): **Edge/NAT fixtures** — default routes,
  masquerade, port-forward rules (including one targeting a powered-off guest), PVE SDN simple-zone
  NAT, and guest-level DNAT scenarios.

**Acceptance criteria:**
1. `GET /edge/routes` and `GET /edge/nat` against the new Edge/NAT fixture return the expected
   default route, masquerade, port-forward, and SDN simple-zone NAT rows — golden test.
2. `nat.portforward.create` applies as a `post-up`/`post-down` stanza pair in `/etc/network/
   interfaces` (golden file diff against the dev-interfaces sandbox), reversible on rollback.
3. `route.static.create` validates (schema: valid CIDR/gateway; referential: gateway reachable via a
   known interface) — table test including a rejection case.
4. The Edge layer flags a port-forward rule pointing at a currently powered-off guest — golden
   projection test (this is the phase's own exit-demo scenario).
5. Regression: `/edge/*` routes accept no request body and no `netWrite` capability check exists on
   them — every mutation to NAT/route state goes through the `nat.*`/`route.*` changeset ops, never
   a dedicated write route.
6. `docs/features/sdn.md` and `docs/api.md` updated with the new Edge section, op group, and Ref
   kinds; `make check` green.

---

## T-1404 · IPv6 enablement suite
**model:** sonnet-5 · **size:** L · **depends:** T-503 (path simulator engine), T-603 (blueprints), T-405 (IPAM), T-1303 (latency mesh, per-family extension) · **context:** `docs/api.md` (Path simulator section, `EndpointSpec`/`SimulateResult`), `docs/features/ipam.md` §1 §2, `docs/features/blueprints.md` §1 §2, `docs/features/sdn.md` §6 ("IPv6 SLAAC management — display yes, config P1")

**Objective:** Promote IPv6 from "addresses render correctly" to a managed capability: RA/SLAAC/
DHCPv6 visibility per segment, dual-stack drift findings that catch the classic silent failure (v4
works, v6 is broken), a prefix-delegation-aware IPv6 planning grid in IPAM, and a guided dual-stack
rollout wizard built on blueprints. The latency mesh and path simulator learn to run per-family.

**Deliverables:**
- `GET /ipv6/segments` (netRead-gated): per-VLAN/VNet RA/SLAAC/DHCPv6 visibility — RA presence,
  M/O flags, advertised prefix(es), DHCPv6-server presence — read-only, sourced the same way LLDP is
  (a bounded host-local RA/DHCPv6 observation, fanned out via the peer API like every other
  `host/*` read in `docs/api.md`'s Peer API section).
- `POST /simulate/path` and `POST /simulate/verify` gain an optional `family: "v4"|"v6"` request
  field (default `v4`, backward compatible); `SimulateResult` is unchanged in shape — a dual-stack
  check is two calls, one per family, compared by the caller.
- New finding `dualstack_drift` (`source: "findings"`, per-guest/per-segment): the v4-family
  simulated verdict is `allow`/reachable while the v6-family verdict for the identical
  src/dst/proto/port is `deny`/`unreachable`/`indeterminate` — explainable (names both verdicts),
  never silent.
- Latency mesh per-family extension (T-1303's probe rings gain a `family` dimension): v4 and v6
  probes run independently on any dual-stack-capable segment.
- IPv6 planning grid: `GET /ipam/subnets/{prefix}/v6-plan` — given a delegated prefix (e.g. a /56),
  proposes aligned /64 subnets against existing VLANs/VNets (prefix-delegation aware); DHCPv6-PD
  from an upstream vnprox doesn't manage is surfaced read-only, never configured by vnprox.
- Guided dual-stack rollout wizard (`web/src/ipv6/DualStackWizard.tsx`): built on T-603's
  `blueprint.Instantiate` pattern — adds IPv6 addressing/RA/DHCPv6 config to an existing VLAN/VNet as
  one changeset, idempotent re-run like every other blueprint instantiation.
- Fixture family (new, deliverable of this card): **IPv6 RA/SLAAC/DHCPv6 fixtures** — healthy
  dual-stack, v6-broken (silent failure), and DHCPv6-PD-from-upstream scenarios.

**Acceptance criteria:**
1. `GET /ipv6/segments` against the new fixture returns RA/SLAAC/DHCPv6 visibility matching the
   fixture's configured scenario — golden test.
2. A fixture where v4 path-simulation allows and v6 denies/is unreachable for the identical tuple
   raises exactly one `dualstack_drift` finding naming both verdicts; a healthy dual-stack fixture
   raises none.
3. `POST /simulate/path {family: "v6"}` returns a distinct, correct verdict from the same request
   with `family: "v4"` against a fixture with an IPv6-only firewall rule.
4. Latency mesh test: a dual-stack segment produces independent v4 and v6 latency/loss series
   (synthetic fixture), not one merged series.
5. `GET /ipam/subnets/{prefix}/v6-plan` against a /56-delegated fixture proposes /64 subnets aligned
   to existing VLANs — golden test.
6. Dual-stack wizard produces one changeset (idempotent re-instantiation test: running it twice
   against the now-converged state yields zero ops the second time) — Vitest + Testing Library.
7. Regression: no op or route lets vnprox write a DHCPv6-PD request to an upstream device — visibility
   only, asserted by a zero-write-surface test.
8. `docs/features/ipam.md`, `docs/features/blueprints.md`, `docs/api.md` updated; `make check` green.

---

## T-1405 · WAN & upstream health
**model:** sonnet-5 · **size:** M · **depends:** T-1303 (probe infra), T-1005 (alert routing), T-1403 (uplink/edge model) · **context:** `docs/api.md` (Alert Rules section, WS `firewall.log.batch`/`flow.batch` bounded-ring convention), T-1403's `GET /edge/routes` uplink model, `planning/tasks/phase-11.md` T-1104 (webhook/token precedent reused by alert routing)

**Objective:** Per-uplink availability/latency/loss to configurable reference targets, multi-WAN
visibility where it exists, and `wan_degraded` findings routed through T-1005's alert rules — the
dashboard tile that says "it's the ISP, not the cluster." Visibility and findings only; no
failover automation.

**Deliverables:**
- `GET/PUT /wan/targets` (netWrite+CSRF for the write, netRead for the read; audited `wan.targets_
  update`): per-node, per-uplink (from T-1403's `GET /edge/routes` uplink list) configurable
  reference targets (IP/hostname list) to probe.
- A probe loop reusing T-1303's mesh-probe infra, retargeted at the configured external reference
  targets instead of internal peers; bounded jitter/loss/latency history ring per uplink with
  explicit age/size caps and an export path, mirroring T-1303's own retention contract.
- New finding `wan_degraded` (`source: "wan"`, hysteresis-debounced) routed through the existing
  `alert_rules`/webhook delivery pipeline (T-1005) — no new delivery mechanism.
- `GET /wan/status`: per-uplink current availability/latency/loss plus a dashboard tile verdict
  ("likely your ISP" when WAN probes are degraded but internal cluster health is otherwise clean).

**Acceptance criteria:**
1. Configuring reference targets and probing against a fixture with one degraded/unreachable target
   raises `wan_degraded` only after the hysteresis window (a single missed probe doesn't fire).
2. Multi-WAN: two configured uplinks on one node, one degraded — `GET /wan/status` reports each
   uplink's status independently.
3. A `wan_degraded` finding routed through an `alert_rules` entry (T-1005) delivers one signed
   webhook to a mock target — reuses T-1104's webhook-delivery test pattern.
4. The history ring enforces its stated size/age cap and exposes an export path — test asserts the
   cap is honored and export returns the expected bounded set.
5. Regression: no changeset op type or write route exists for WAN failover/uplink switching —
   `GET /wan/status` and the probe loop never call any mutating route.
6. `docs/api.md` updated for the new routes and `wan_degraded` finding code; `make check` green.

---

## T-1406 · Ingress visibility
**model:** sonnet-5 · **size:** M · **depends:** T-1403 (edge layer, for the inbound path) · **context:** `docs/api.md` (Firewall/SDN/IPAM read-view conventions), T-1403's `GET /edge/nat` port-forward model, `docs/architecture.md` §7 (app-owned store rule), design doc §5 (fixture reused by T-1702's future plugin conformance set)

**Objective:** Read-only discovery of the reverse-proxy layer (HAProxy, nginx, Caddy, Traefik, via
their own status endpoints/config, only where the operator explicitly points vnprox at them) behind
a small `IngressDiscoverer` interface, deliberately shaped so Phase 17's plugin SDK (T-1702) can
make it pluggable later. Draws the full inbound path: WAN → port-forward → proxy guest → backend
guest.

**Deliverables:**
- New package `internal/ingress`: an `IngressDiscoverer` interface (`Discover(ctx, Target) (ProxyState,
  error)`) with four read-only implementations — HAProxy (stats socket/CSV), nginx (`stub_status`/
  Plus API), Caddy (admin API), Traefik (API) — each parsing only status/config data, issuing no
  mutating call to the target.
- New app-store table `ingress_targets` (id, kind, address, credentialEnc?, addedBy, addedAt) —
  operator-configured only; discovery iterates exactly this table, never scans a network range.
- `GET/POST/DELETE /ingress/targets` (netWrite+CSRF for mutations, audited `ingress.target_add`/
  `ingress.target_remove`), `GET /ingress/status` (aggregated discovered backends/routes per target,
  correlated to known guest refs by backend address).
- Frontend: extends T-1403's Edge layer to draw WAN → port-forward → proxy guest → backend guest as
  one connected chain when an `ingress_targets` entry correlates to a port-forward rule.
- Fixture family (new, deliverable of this card): **reverse-proxy status-endpoint doubles** for
  HAProxy, nginx, Caddy, and Traefik.

**Acceptance criteria:**
1. Each of the four `IngressDiscoverer` implementations parses its status-endpoint double correctly
   — table test per vendor.
2. `GET /ingress/status` correlates a discovered backend address to a known guest ref — golden test.
3. The full WAN → port-forward → proxy guest → backend guest chain renders as one connected path on
   the Edge layer when a port-forward and an `ingress_targets` entry line up — golden projection test.
4. Regression: no `IngressDiscoverer` implementation or route issues any mutating call to a
   configured target — grep-verifiable (read-only HTTP verbs only) plus a fixture-double test
   asserting zero write calls received.
5. Discovery only ever probes rows in `ingress_targets` — a target the operator never added is never
   contacted (no network-range scan exists in the package).
6. `docs/api.md` updated for the new routes; `docs/architecture.md` §7 gains the `ingress_targets`
   table entry; `make check` green.

---

## T-1407 · Tunnel-aware federation transport

> **Status: implemented post-v3.0.2** (not part of the original v2.0.0/v3.0.0 cuts — see
> `planning/reports/T-1407.md`). This card sat unimplemented through v3.0.2 (no report, no code —
> see that report's own note on how the gap was found and closed) despite six sibling phase-14
> cards shipping around it; downstream reports/docs that flagged the gap
> (`planning/reports/T-1401.md`, `planning/reports/T-1402.md`, `docs/features/topology.md`) should
> be read as historical context for *why* the gap existed, not current state.
>
> **Follow-ups closed in v3.0.4** (`planning/reports/T-1407-followups.md`): the two items that
> report left for the next agent — `wireguard_peers.cluster_id` reconciled with
> `clusters.wg_tunnel_id` (one effective linkage resolved on read, explicit override wins,
> otherwise derived from the peer annotation) and a UI for the linkage (the connect-clusters
> wizard's optional "Federated cluster" field, tagging the peer inside the same changeset).

**model:** sonnet-5 · **size:** S · **depends:** T-1401, T-1201 (federation core) · **context:** `planning/tasks/phase-12.md` T-1201 (cluster registry, `partial`/`failedClusters` fan-out convention), T-1401's tunnel findings

**Objective:** A federation peer reachable only over a vnprox-managed WireGuard tunnel gets
health-checked as a unit — tunnel down ⇒ peer unreachable ⇒ **one** finding, not the cascade of
per-surface unreachable-cluster errors T-1201's aggregator would otherwise raise across topology,
audit, and IPAM-conflict reads simultaneously.

**Deliverables:**
- Extend T-1201's `clusters` table with an optional `wgTunnelId` linkage field, marking a cluster as
  reachable via a specific T-1401-managed tunnel.
- Extend T-1201's `Aggregator`/failure-isolation path: when a linked tunnel's own health (T-1401's
  `wg_handshake_stale` finding or a hard-down status) indicates the tunnel is down, the aggregator
  suppresses the individual per-surface `partial`/`failedClusters` entries that cluster would
  otherwise generate and instead raises exactly one `tunnel_down_peer_unreachable` finding naming
  the cluster and tunnel.
- No auto-healing: the finding's only action links to the tunnel's changeset editor (T-1401/T-1402);
  a human fixes it through an ordinary `wg.*` changeset.

**Acceptance criteria:**
1. A federated cluster linked to a healthy WireGuard tunnel aggregates normally — no synthetic
   finding, ordinary `GET /federation/topology`/`GET /audit` behavior.
2. The linked tunnel's fixture-driven handshake goes stale past threshold → the aggregator reports
   the cluster unreachable via exactly one `tunnel_down_peer_unreachable` finding, and the per-surface
   `partial`/`failedClusters` noise that plain unreachability would otherwise produce across
   topology/audit/IPAM-conflict reads is collapsed into that one finding.
3. A federation peer *not* linked to a vnprox-managed tunnel gets T-1201's ordinary unreachable-peer
   handling unchanged — regression test.
4. No auto-remediation exists: the finding carries no `fix` and no code path re-applies or restarts
   the tunnel automatically.
5. `docs/data-model.md`'s `clusters` table entry gains the `wgTunnelId` field; `docs/api.md` updated
   for the new finding code; `make check` green.

---

## Card-author notes

No conflicts found between the design document and `docs/roadmap-universal.md` for this phase —
task IDs, titles, sizes, model assignments, and scope-in/out all matched cleanly (one dependency-ID
correction: the design document cites `T-501 (path simulator)` for T-1404, but arc-1's T-501 is
"Firewall read" — the path simulator engine is **T-503**; corrected in T-1404's `depends:` line
above, matching the same correction phase-16.md applies for T-1604), and
the roadmap's exit-demo language ("one of them flagged as forwarding to a powered-off guest") is
reproduced verbatim in this file's intro and used as an explicit T-1403 acceptance criterion.

Two grounding notes for the next agent picking up these cards:

- **Federation (T-1201), the latency mesh (T-1303), and global topology (T-1202)** are cited here as
  dependencies per the design doc's convention that `T-801…T-1208`-range IDs refer to shipped/planned
  code, not open work — but as of this writing `planning/tasks/` has no `phase-12.md`-successor
  `phase-13.md` file yet, and the actual `internal/federation`/`internal/probe`-mesh-extension/
  `internal/wireguard` packages do not exist in the repo. Whichever phase lands first should not
  assume the other's package layout beyond what this file and `planning/tasks/phase-12.md` already
  commit to (table/route shapes named above).
- **WireGuard on-node apply mechanism** (T-1401) is specified here as "write config, exec `wg`/
  `wg-quick` with fixed argv" by analogy to the existing `ifreload`/`lldpctl` subprocess pattern
  (`docs/security.md`'s Host footprint section) and `github.com/vishvananda/netlink`'s partial
  WireGuard link-type support (already an approved dependency) as an alternative; the executing
  agent should pick one concretely and document the choice, since neither this design nor
  `docs/development.md` pins it today.
