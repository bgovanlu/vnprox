// Package peer implements vnprox's intra-cluster API: the shared cluster
// secret, HMAC-SHA256 request signing/verification, the peer HTTP server
// (mounted at /api/peer/* on the same listener as the rest of vnproxd), and
// the peer HTTP client (discovery, signing, per-peer circuit breaking).
//
// This is the authenticated channel every cluster-aware feature rides on
// (docs/architecture.md §5, §1): any node's daemon can read another node's
// host-local state (LLDP, live stats, the literal interfaces(5) file) or
// drive its host-local writes (stage/reload/restore) through a peer's
// /api/peer/* routes instead of local syscalls/exec. It is never exposed to
// the browser SPA — docs/api.md's Peer API section is explicit that it is
// "internal only".
//
// Security model (docs/security.md "Transport"): every request under
// /api/peer/* is authenticated by an HMAC-SHA256 signature over the
// request's method, path (+query), a hash of the body, and a timestamp,
// keyed by a 256-bit secret shared cluster-wide via pmxcfs
// (/etc/pve/vnprox/, root:root 0600, already cluster-replicated). The
// signature is verified — including a ±30s replay window and a
// short-lived exact-replay cache — before any route handler runs; there is
// no other authentication layer on these routes (SPA session cookies are
// never consulted and grant nothing here). TLS is provided by the same
// listener/certificate as the rest of vnproxd (docs/architecture.md §9).
//
// Single-node deployments (no PVE cluster) have zero peers: Client.Peers
// returns an empty list and every caller-visible code path in dependent
// packages (T-302+) is expected to short-circuit locally rather than
// special-case an empty peer set.
package peer
