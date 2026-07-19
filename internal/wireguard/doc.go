// Package wireguard is T-1401's WireGuard tunnel engine core: the app-owned
// tunnel/peer intent model, X25519 key generation, on-node wg-quick config
// rendering, and the live `wg show <if> dump`-equivalent observed-state
// parser the monitoring findings (wg_handshake_stale / wg_endpoint_drift)
// are computed from.
//
// Two invariants this package exists to hold (docs/security.md's WireGuard
// credential-storage note, docs/data-model.md §2's new-domain rule):
//
//   - Key custody. A tunnel's private key is generated on the owning node via
//     stdlib crypto/ecdh's X25519 curve (GenerateKeypair) — no third-party
//     crypto dependency — and this package returns it exactly once, to the
//     caller that seals it with internal/store's SessionCipher (the identical
//     AES-256-GCM primitive sessions.pve_ticket_enc uses) before it ever
//     touches the wireguard_tunnels table. Only the derived public key
//     (PublicKeyFor) is ever exportable. This package never logs, prints, or
//     round-trips a private key through any exported string.
//
//   - Proxmox/WireGuard stays authoritative. WireGuard's own on-node state
//     (the live interface, handshake ages, transfer counters, the endpoint a
//     peer is actually reaching us from) is read fresh via ParseDump and never
//     persisted as truth — vnprox persists intent + audit only.
//
// This package is deliberately free of any store, HTTP, or change-engine
// dependency: it is pure model + crypto + text rendering/parsing, wired into
// the change engine's apply step and the findings engine by the composition
// root (cmd/vnproxd). The one on-node side effect — writing the config file
// and exec'ing wg/wg-quick with a fixed argv array — lives in cmd/vnproxd's
// WGGateway implementation, not here, mirroring how the ifreload/lldpctl
// subprocess convention (docs/security.md's Host footprint section) keeps its
// exec surface at the daemon edge.
package wireguard
