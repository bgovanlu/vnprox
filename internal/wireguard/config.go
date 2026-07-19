package wireguard

import (
	"fmt"
	"strings"
)

// RenderConfig renders the on-node wg-quick configuration file for a tunnel
// and its peers. privateKey is the plaintext base64 private key, decrypted
// just-in-time on the owning node immediately before the file is written and
// never held longer — this function is called only by the node-local apply
// step (cmd/vnproxd's WGGateway), never by any read path, so the plaintext
// key never crosses a package boundary toward an API response or a log line.
//
// External peers (Peer.External) are rendered exactly like managed peers in
// the [Peer] stanzas here — the on-node config must list every peer allowed
// to connect, external or not; "external" only governs that vnprox never runs
// an apply step against that peer's *own* side (T-1401 AC5), not whether it
// appears in our own interface's peer list.
func RenderConfig(t Tunnel, privateKey string, peers []Peer) string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", privateKey)
	for _, addr := range t.Addresses {
		fmt.Fprintf(&b, "Address = %s\n", addr)
	}
	if t.ListenPort > 0 {
		fmt.Fprintf(&b, "ListenPort = %d\n", t.ListenPort)
	}
	if t.MTU > 0 {
		fmt.Fprintf(&b, "MTU = %d\n", t.MTU)
	}
	for _, p := range peers {
		b.WriteString("\n[Peer]\n")
		fmt.Fprintf(&b, "PublicKey = %s\n", p.PublicKey)
		if p.Endpoint != "" {
			fmt.Fprintf(&b, "Endpoint = %s\n", p.Endpoint)
		}
		if len(p.AllowedIPs) > 0 {
			fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(p.AllowedIPs, ", "))
		}
		if p.KeepaliveSec > 0 {
			fmt.Fprintf(&b, "PersistentKeepalive = %d\n", p.KeepaliveSec)
		}
	}
	return b.String()
}

// RenderPeerConfig renders the wg-quick config block an *external* peer would
// install on ITS own side to connect to this tunnel (GET
// /wireguard/tunnels/{id}/peer-config). vnprox never holds an external peer's
// private key — its own key hygiene is explicitly out of vnprox's control
// (T-1401's residual-risk note) — so the [Interface] PrivateKey is emitted as
// a clearly-marked placeholder the operator fills in on the peer itself.
//
// ourEndpoint is the host:port the external peer should dial to reach this
// tunnel (this node's reachable address + the tunnel's ListenPort). peerAddrs
// is the address(es) the peer takes inside the tunnel (its AllowedIPs on our
// side, which become its own [Interface] Address). ourTunnelAddrs is what the
// peer routes back to us ([Peer] AllowedIPs).
func RenderPeerConfig(t Tunnel, peer Peer, ourEndpoint string) string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString("PrivateKey = <REPLACE_WITH_THIS_PEER'S_OWN_PRIVATE_KEY>\n")
	for _, addr := range peer.AllowedIPs {
		fmt.Fprintf(&b, "Address = %s\n", addr)
	}
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", t.PublicKey)
	if ourEndpoint != "" {
		fmt.Fprintf(&b, "Endpoint = %s\n", ourEndpoint)
	}
	if len(t.Addresses) > 0 {
		fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(t.Addresses, ", "))
	}
	if peer.KeepaliveSec > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", peer.KeepaliveSec)
	}
	return b.String()
}
