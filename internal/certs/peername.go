package certs

import "strings"

// ResolveVerifyName picks the identity pinned peer TLS should verify a node's
// certificate as, given that certificate, the node's authoritative PVE name,
// and the address the peer client will actually dial.
//
// This is the fix for T-1906-bug-01. Peers are dialled by IP, and real PVE
// certificates do not reliably carry the node's current IP as a SAN — the
// certificate on the hardware this was found on carries a *stale* address
// (192.168.100.99) for a node that now lives at 192.168.1.9. Pinned
// verification against the dial IP therefore fails closed on a correctly
// configured cluster, which is the worst kind of failure: correct components,
// no operator error, everything down.
//
// The connection still goes to the IP. Only the identity checked against the
// certificate changes — exactly what `curl --resolve` does. Because pmxcfs
// already holds every node's certificate locally, this needs no network round
// trip and can be decided before the first peer call.
//
// # Why this does not weaken the pin
//
// Three properties, each of which has an adversarial test in peername_test.go:
//
//  1. The CA pin is untouched. A certificate that does not chain to the
//     cluster CA is still rejected, whatever name it claims.
//  2. Candidate names are derived from the *authoritative* node name (PVE's
//     own cluster membership), never from whatever the presented certificate
//     happens to claim. Reading a name out of the peer's certificate and then
//     verifying the certificate against that name would verify nothing at all.
//  3. The FQDN candidate must be rooted at the node name — a DNS SAN is only
//     eligible when its first label equals the node name. So node A's
//     certificate can never authenticate node B, even though both were issued
//     by the same cluster CA.
//
// The `expected` certificate is the one pmxcfs says that node should be
// serving, used only to choose among candidate names. It is not pinned: a node
// that has legitimately just renewed its certificate must keep working, and
// the CA pin is what carries the security here.
func ResolveVerifyName(expected Certificate, node, dialAddr string) (name string, covered bool) {
	// 1. The node's own name, if the certificate covers it. This is the
	//    common case on a real cluster: pve-ssl.pem carries DNS:<nodename>.
	if node != "" && expected.Covers(node) {
		return node, true
	}

	// 2. An FQDN rooted at the node name — real PVE also issues
	//    DNS:<nodename>.<domain> (and, with a root dot,
	//    "pvecube.localdomain."). Eligibility is decided by the node name we
	//    were given, not by the certificate, so this cannot drift onto an
	//    unrelated name that happens to be in the SAN list.
	if node != "" {
		if fqdn, ok := fqdnRootedAt(expected, node); ok {
			return fqdn, true
		}
	}

	// 3. The dial address, when the certificate genuinely covers it. Some
	//    deployments do keep IP SANs current, and there is no reason to
	//    prefer a name in that case.
	if dialAddr != "" && expected.Covers(dialAddr) {
		return dialAddr, true
	}

	// Nothing is covered. Return the dial address so the handshake proceeds
	// and fails closed with crypto/tls's own hostname error — the caller gets
	// a real, specific TLS failure rather than this function inventing one —
	// and report covered=false so the daemon can raise cert_san_mismatch
	// *before* the first peer call rather than after it.
	return dialAddr, false
}

// fqdnRootedAt returns a DNS SAN whose first label is exactly node.
//
// Wildcard SANs are deliberately not eligible. A certificate carrying
// `*.example.com` would match any node in that domain, so accepting it here
// would let one node's certificate authenticate another's — precisely the
// property this function exists to preserve. A wildcard is still honoured when
// crypto/tls checks a name chosen by rule 1 or 3; it just cannot *originate* a
// candidate name.
func fqdnRootedAt(cert Certificate, node string) (string, bool) {
	want := strings.ToLower(node)
	for _, dns := range cert.DNSNames() {
		san := normalizeHost(dns)
		if strings.HasPrefix(san, "*.") {
			continue
		}
		label, rest, found := strings.Cut(san, ".")
		if found && rest != "" && label == want {
			return san, true
		}
	}
	return "", false
}

// VerifyNames maps the address the peer client dials each node at to the name
// its certificate should be verified as. This is the shape internal/peer's
// trust anchor consumes: it sees an address, not a node.
//
// Nodes whose certificate covers nothing usable are still included, mapped to
// their dial address, so the handshake produces crypto/tls's own hostname
// error rather than silently falling back to some other node's name.
func VerifyNames(inv Inventory, facts ClusterFacts) map[string]string {
	out := make(map[string]string, len(facts.DialAddrs))
	for node, addr := range facts.DialAddrs {
		if addr == "" {
			continue
		}
		leaf, ok := inv.LeafFor(node)
		if !ok {
			// No certificate on record for this node: dial-address identity
			// is the only thing we could ask for, and cert_missing already
			// reports the underlying problem.
			out[addr] = addr
			continue
		}
		name, _ := ResolveVerifyName(leaf, node, addr)
		out[addr] = name
	}
	return out
}
