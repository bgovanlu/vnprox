package wireguard

import "time"

// This file is T-1401's WireGuard state fixtures deliverable: named,
// self-contained observed-state scenarios the monitoring findings
// (wg_handshake_stale / wg_endpoint_drift) and the read-view/export routes
// are developed and tested against, since there is no live WireGuard kernel
// module in the dev/test environment (real kernel behaviour is a
// needs-hardware-validation item). Each scenario expresses handshake ages
// relative to a caller-supplied `now`, so a test can drive a peer across the
// stale threshold and back deterministically.

// FixtureNode / FixtureIfName are the node and interface every fixture below
// is anchored to.
const (
	FixtureNode   = "pve1"
	FixtureIfName = "wg0"
)

// FixtureHealthy is a tunnel whose single peer handshook recently and whose
// live endpoint matches its configured one — no finding should fire.
func FixtureHealthy(now time.Time) ObservedTunnel {
	return ObservedTunnel{
		Node: FixtureNode, IfName: FixtureIfName, PublicKey: "SRVpubKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=", ListenPort: 51820,
		Peers: []ObservedPeer{{
			PublicKey:     "PEERoneKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=",
			Endpoint:      "203.0.113.10:51820",
			ConfiguredEnd: "203.0.113.10:51820",
			AllowedIPs:    []string{"10.10.0.2/32"},
			LastHandshake: now.Add(-30 * time.Second),
			RxBytes:       120000, TxBytes: 98000,
		}},
	}
}

// FixtureStaleHandshake is the healthy tunnel except its peer's last
// handshake is well past the stale threshold — wg_handshake_stale fires
// (after hysteresis).
func FixtureStaleHandshake(now time.Time) ObservedTunnel {
	t := FixtureHealthy(now)
	t.Peers[0].LastHandshake = now.Add(-15 * time.Minute)
	return t
}

// FixtureEndpointDrift is the healthy tunnel except its peer is now reaching
// us from a different endpoint than the one on file (a NAT rebind) —
// wg_endpoint_drift fires (after hysteresis). Handshake stays recent, so
// wg_handshake_stale must NOT fire on this scenario.
func FixtureEndpointDrift(now time.Time) ObservedTunnel {
	t := FixtureHealthy(now)
	t.Peers[0].Endpoint = "203.0.113.99:51820" // observed, drifted
	t.Peers[0].ConfiguredEnd = "203.0.113.10:51820"
	return t
}

// FixtureExternalPeerTunnel / FixtureExternalPeer are the intent-level export
// case (T-1401 AC5): a tunnel with one external, read-only peer whose own
// side config is exportable via RenderPeerConfig but which vnprox never
// targets with an apply step of its own.
func FixtureExternalPeerTunnel() Tunnel {
	return Tunnel{
		ID: "01HWGTUNNEL0000000000000001", Node: FixtureNode, IfName: FixtureIfName,
		PublicKey: "SRVpubKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=", ListenPort: 51820,
		Addresses: []string{"10.10.0.1/24"}, MTU: 1420, Carrier: "vmbr0",
		CreatedBy: "root@pam", CreatedAt: 1721300000,
	}
}

func FixtureExternalPeer() Peer {
	return Peer{
		TunnelID:   "01HWGTUNNEL0000000000000001",
		PublicKey:  "PEERextKEYaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=",
		AllowedIPs: []string{"10.10.0.4/32"},
		External:   true,
	}
}
