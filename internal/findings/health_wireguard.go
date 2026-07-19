// health_wireguard.go implements T-1401's WireGuard monitoring findings
// (source "wireguard"): wg_handshake_stale (a peer whose last handshake is
// past the stale threshold) and wg_endpoint_drift (a peer whose live observed
// endpoint disagrees with its configured one — the NAT-rebind case). Both are
// computed fresh from a live wg-show-dump-equivalent poll (WGProvider), never
// persisted as truth (docs/architecture.md §7's storage rule), and both are
// hysteresis-debounced so a single noisy poll doesn't flap the stream
// (T-1401 AC4).

package findings

import (
	"fmt"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/wireguard"
)

const (
	CheckWgHandshakeStale = "wg_handshake_stale"
	CheckWgEndpointDrift  = "wg_endpoint_drift"
)

const wireguardDocsLink = "docs/api.md#wireguard"

// WgHandshakeStaleThreshold is how old a peer's last handshake may be before
// it is considered stale. WireGuard re-handshakes roughly every two minutes
// while a tunnel carries traffic, so a handshake older than a few minutes on
// an otherwise-configured peer means the link is not currently passing
// traffic. Deliberately generous to avoid flapping a healthy-but-idle link.
const WgHandshakeStaleThreshold = 5 * time.Minute

const (
	wgRiseCycles = 2 // AC4: a single missed check must not fire
	wgFallCycles = 2
)

// WGProvider is the findings engine's seam onto the live WireGuard state
// (cmd/vnproxd adapts its wg-show-dump poll into this). A nil provider skips
// both checks entirely, the same degradation every other optional producer
// uses.
type WGProvider interface {
	// WireGuardState returns the current live observed state of every
	// WireGuard tunnel this daemon can see, each peer's ConfiguredEnd already
	// merged in from the app-store intent so wg_endpoint_drift can compare.
	WireGuardState() []wireguard.ObservedTunnel
}

// wgFindings runs both WireGuard checks against the current live state.
func wgFindings(p WGProvider, staleDB, driftDB *debouncer, now time.Time) []Finding {
	if p == nil {
		return nil
	}
	state := p.WireGuardState()
	var out []Finding
	out = append(out, checkWgHandshakeStale(state, staleDB, now)...)
	out = append(out, checkWgEndpointDrift(state, driftDB)...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// checkWgHandshakeStale flags every peer whose last handshake is older than
// WgHandshakeStaleThreshold, hysteresis-debounced. A peer that has never
// handshaked at all (age unknown) is not flagged — a freshly-created tunnel
// must not immediately fire, and this producer has no tunnel-age signal to
// tell "new" from "long-broken" apart.
func checkWgHandshakeStale(state []wireguard.ObservedTunnel, db *debouncer, now time.Time) []Finding {
	var out []Finding
	live := map[string]bool{}
	for _, tun := range state {
		for _, peer := range tun.Peers {
			key := wgPeerKey(tun, peer)
			live[key] = true
			age, ok := peer.HandshakeAge(now)
			breach := ok && age > WgHandshakeStaleThreshold
			if !db.Evaluate(key, breach, wgRiseCycles, wgFallCycles) {
				continue
			}
			detail := fmt.Sprintf("WireGuard peer %s on %s (%s) last handshook %s ago — past the %s stale threshold; the tunnel is not currently passing traffic",
				shortKey(peer.PublicKey), tun.Node, tun.IfName, age.Round(time.Second), WgHandshakeStaleThreshold)
			out = append(out, newWireguardFinding(CheckWgHandshakeStale, SeverityWarning, detail, tun, peer))
		}
	}
	db.Prune(live)
	return out
}

// checkWgEndpointDrift flags every peer whose live endpoint disagrees with its
// configured one (a NAT rebind), hysteresis-debounced.
func checkWgEndpointDrift(state []wireguard.ObservedTunnel, db *debouncer) []Finding {
	var out []Finding
	live := map[string]bool{}
	for _, tun := range state {
		for _, peer := range tun.Peers {
			key := wgPeerKey(tun, peer)
			live[key] = true
			if !db.Evaluate(key, peer.EndpointDrifted(), wgRiseCycles, wgFallCycles) {
				continue
			}
			detail := fmt.Sprintf("WireGuard peer %s on %s (%s) is reaching us from %s, but its configured endpoint is %s — a NAT rebind or address change",
				shortKey(peer.PublicKey), tun.Node, tun.IfName, peer.Endpoint, peer.ConfiguredEnd)
			out = append(out, newWireguardFinding(CheckWgEndpointDrift, SeverityWarning, detail, tun, peer))
		}
	}
	db.Prune(live)
	return out
}

// wgPeerKey is the stable, content-derived debounce/finding key for one peer:
// node, interface, and the peer's public key.
func wgPeerKey(tun wireguard.ObservedTunnel, peer wireguard.ObservedPeer) string {
	return tun.Node + "|" + tun.IfName + "|" + peer.PublicKey
}

// newWireguardFinding builds a SourceWireguard finding with a stable id (like
// newHealthFinding's scheme, "wireguard:" prefixed). Refs name the tunnel
// (a wg-tunnel Ref string); Nodes names the owning node.
func newWireguardFinding(check, severity, detail string, tun wireguard.ObservedTunnel, peer wireguard.ObservedPeer) Finding {
	tunnelRef := "wg-tunnel:" + tun.Node + ":" + tun.IfName
	return Finding{
		ID:       "wireguard:" + check + "|" + wgPeerKey(tun, peer),
		Source:   SourceWireguard,
		Check:    check,
		Severity: severity,
		Detail:   detail,
		Nodes:    []string{tun.Node},
		Refs:     []string{tunnelRef},
		DocsLink: wireguardDocsLink,
	}
}

// shortKey renders the first 8 characters of a base64 WireGuard key for a
// human-readable detail string (the full key is long and identity-only).
func shortKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "…"
}
