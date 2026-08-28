// SPDX-License-Identifier: Apache-2.0

// Package route implements T-3903's route explorer: kernel FIB (every
// routing table, v4 and v6) + policy rules + FRR's RIB (when FRR is
// installed) per node, a visual next-hop graph, and a "which path would
// this address take" lookup — the operator question this package exists
// to answer.
//
// This is deliberately NOT internal/sim. internal/sim/l3.go evaluates
// reachability across vnprox's own inventory model (PVE SDN zones/VNets/
// subnets, firewall rules) and explicitly *declines* to evaluate routing
// once a flow leaves that model — see l3Path's comment: "Routing decisions
// need both endpoints anchored to a subnet. Plain (non-SDN) bridges route
// via host routing tables the inventory does not carry — honestly not
// evaluated rather than guessed," which surfaces to the caller as a
// FeatureExternalRouting caveat (internal/sim/caveats.go). This package is
// exactly the capability internal/sim disclaims there: it reads a node's
// *actual* kernel/FRR routing state directly (not vnprox's inventory model
// of what SDN configured), and answers "what would this node's kernel
// actually do with this destination right now" for any destination,
// on-fabric or not. The two surfaces answer different questions and are
// meant to be cross-linked, not merged: internal/sim explains *why* PVE's
// SDN config would or wouldn't forward a flow between two modeled
// endpoints (including firewall verdicts); internal/route shows the raw
// host-level truth once a destination falls outside what internal/sim
// models symbolically. Neither package imports the other.
//
// Package layout, mirroring internal/evpn/internal/neighbor's shape (a
// pure, fuzzable byte-level parser package + a small cluster-fan-out
// Service on top):
//
//   - fib.go: ParseFIBRoutes, the kernel routing-table parser over
//     `ip -j route show table all` (and its `-6` counterpart) JSON output.
//   - rules.go: ParsePolicyRules, the `ip -j rule show` policy-routing
//     rule parser.
//   - frrrib.go: ParseFRRRIB, the FRR RIB parser over
//     `vtysh -c "show ip route json"` (and "show ipv6 route json") output —
//     tolerant of both the plain and `vrf all`-wrapped shapes FRR produces
//     (see planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt).
//   - lookup.go: Lookup, the pure longest-prefix-match + policy-rule
//     evaluation engine implementing "which path would this address take."
//   - graph.go: next-hop graph assembly for the topology-style view.
//   - service.go: Service, the cluster-aware fan-out (local node via a
//     Fetcher, every peer via the peer API) assembling one Snapshot per
//     node — the same Config{Host, Peers, LocalNode, Logger} shape
//     internal/neighbor.Service and internal/evpn.Service already use.
//
// Raw-byte fetching itself lives in internal/host (route.go, this task's
// addition there) — the same package every other exec/netlink-backed host
// read lives in (frr.go's vtysh calls, lldp.go's lldpctl call) — rather
// than being duplicated in this package. This package depends only on the
// small Fetcher/PeerSource seams it declares in service.go, which
// *host.Real and *pvemock.FixtureHostReader both satisfy structurally
// (Go's structural typing, the same "small interface, real type satisfies
// it" pattern documented in docs/architecture.md §2) without either of
// those packages importing this one.
//
// Read-only throughout: no method in this package stages, validates, or
// applies any change — internal/change is never imported here.
package route
