// SPDX-License-Identifier: Apache-2.0

package wireguard

import "time"

// Tunnel is one WireGuard interface's app-owned intent (docs/data-model.md
// §2's wireguard_tunnels table). It is NOT the live interface — WireGuard's
// on-node state stays authoritative (ObservedTunnel below is the live read).
// PrivateKey is the plaintext raw key only in the narrow, in-memory window
// between GenerateKeypair and sealing; it is never serialized here and the
// store keeps only the sealed form.
type Tunnel struct {
	ID         string
	Node       string
	IfName     string
	PublicKey  string
	Carrier    string
	CreatedBy  string
	Addresses  []string
	ListenPort int
	MTU        int
	CreatedAt  int64
}

// Peer is one remote endpoint of a Tunnel (docs/data-model.md §2's
// wireguard_peers table). External peers (a road-warrior, or a cluster
// vnprox does not manage) are External: true — modeled read-only, config-
// export-only, never targeted by an apply step of vnprox's own (T-1401 AC5).
type Peer struct {
	TunnelID     string
	PublicKey    string
	Endpoint     string
	ClusterID    string
	AllowedIPs   []string
	KeepaliveSec int
	External     bool
}

// ObservedPeer is one peer's live, authoritative WireGuard state as read from
// `wg show <if> dump` — never persisted. Endpoint is where the peer is
// actually reaching us from right now (which can drift from its configured
// endpoint after a NAT rebind — the wg_endpoint_drift case).
type ObservedPeer struct {
	LastHandshake    time.Time
	PublicKey        string
	Endpoint         string
	ConfiguredEnd    string
	AllowedIPs       []string
	RxBytes          int64
	TxBytes          int64
	PersistentKeep   int
	ConfiguredExtern bool
}

// ObservedTunnel is one WireGuard interface's live state — the shape the
// monitoring findings (wg_handshake_stale / wg_endpoint_drift) evaluate, and
// the read-view GET /wireguard/tunnels renders live status from.
type ObservedTunnel struct {
	Node       string
	IfName     string
	PublicKey  string
	Peers      []ObservedPeer
	ListenPort int
}

// HandshakeAge returns how long ago p last completed a handshake relative to
// now, and whether a handshake has ever been observed. A peer that has never
// handshaked (zero LastHandshake) returns ok=false — the caller decides
// whether "never" is stale (it is, once the tunnel has existed long enough),
// distinct from "handshaked, but a while ago".
func (p ObservedPeer) HandshakeAge(now time.Time) (age time.Duration, ok bool) {
	if p.LastHandshake.IsZero() {
		return 0, false
	}
	return now.Sub(p.LastHandshake), true
}

// EndpointDrifted reports whether p's live endpoint disagrees with its
// configured one — the NAT-rebind case wg_endpoint_drift fires on. A peer
// with no configured endpoint (a roaming peer we never pinned) can never
// drift, and neither can one with no live endpoint yet (not connected).
func (p ObservedPeer) EndpointDrifted() bool {
	if p.ConfiguredEnd == "" || p.Endpoint == "" {
		return false
	}
	return p.ConfiguredEnd != p.Endpoint
}
