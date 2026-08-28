// SPDX-License-Identifier: Apache-2.0

// health_peertrust.go implements T-1906's peer-API TLS trust findings
// (source "peer"). The peer API carries cluster-wide network mutations —
// cross-node changeset application, distributed rollback timers, host-writer
// calls — so *why* a peer cannot be talked to is not a detail: an operator has
// to be able to tell a dead node from a machine that answered on the peer port
// with a certificate this daemon refuses.
//
// Three checks, deliberately separate rather than one "peer problem" finding:
//
//   - peer_unreachable — nothing answered. Warning, debounced like every other
//     continuously-recomputed signal, because a single missed poll is noise.
//   - peer_untrusted — something answered and failed certificate verification
//     against the pinned cluster CA. Error, and hysteresis-exempt: this is a
//     security event, not a noisy counter, the same treatment source "rogue"
//     gets (types.go's SourceRogue).
//   - peer_trust_degraded — this daemon's own posture is weaker than the
//     pinned default: an escape hatch is configured, the pinned CA is
//     unreadable (in which case no peer is reachable at all, by design), or
//     the client is dialling plaintext http. Fires immediately; it describes
//     configuration, which does not flap.
//
// Nothing here is fixable (Finding.Fixable stays false). A certificate trust
// problem is never something a generated changeset should paper over.

package findings

import (
	"fmt"
	"sort"
)

// T-1906's finding check names.
const (
	CheckPeerUntrusted     = "peer_untrusted"
	CheckPeerUnreachable   = "peer_unreachable"
	CheckPeerTrustDegraded = "peer_trust_degraded"
)

const peerTrustDocsLink = "docs/security.md#transport"

// peerUnreachableRise/Fall debounce the reachability check on the findings
// cycle, matching the 2-cycle rise/fall every other continuously-recomputed
// producer uses (wgRiseCycles/wgFallCycles).
const (
	peerUnreachableRise = 2
	peerUnreachableFall = 2
)

// Peer trust states, mirroring internal/peer.PeerTrustState's string values.
// Duplicated as plain strings rather than imported so internal/findings keeps
// no dependency on internal/peer (the same decoupling FederatedCluster uses
// for internal/federation).
const (
	PeerStateOK          = "ok"
	PeerStateUnreachable = "unreachable"
	PeerStateUntrusted   = "untrusted"
)

// PeerTrustStatus is one peer's last observed verdict.
type PeerTrustStatus struct {
	Node  string
	Addr  string
	State string
	Error string
}

// PeerTrustReport is this daemon's whole peer-TLS posture: the configured
// trust mode, whether its anchor is usable, the scheme it dials, and the last
// verdict for every peer it has actually talked to. cmd/vnproxd's
// peerTrustAdapter builds it from *peer.Client.TrustReport.
type PeerTrustReport struct {
	// LocalNode names this node, so a posture finding (which is about this
	// daemon, not a peer) has somewhere to point.
	LocalNode string
	// Mode is internal/peer.TrustMode's string value.
	Mode string
	// CAFile is the pinned trust anchor path.
	CAFile string
	// AnchorError is non-empty when the pinned CA is currently unusable, in
	// which case peer TLS fails closed — no peer is reachable.
	AnchorError string
	// Scheme is the URL scheme the peer client dials ("https" in production).
	Scheme string
	Peers  []PeerTrustStatus
	// Pinned reports whether peers are verified against the cluster CA.
	Pinned bool
}

// PeerTrustProvider is the findings engine's seam onto the peer client's TLS
// trust posture (T-1906). A nil provider skips these three checks entirely,
// the same degradation every other optional Config field uses.
type PeerTrustProvider interface {
	PeerTrust() PeerTrustReport
}

// peerTrustFindings raises the peer_untrusted / peer_unreachable /
// peer_trust_degraded checks from prov's report.
func peerTrustFindings(prov PeerTrustProvider, unreachDB *debouncer) []Finding {
	if prov == nil {
		return nil
	}
	rep := prov.PeerTrust()
	if rep.Mode == "" && len(rep.Peers) == 0 && rep.AnchorError == "" {
		// A lazily-set adapter that has not been pointed at a peer client yet
		// reports the zero value. "No data" must contribute nothing — never a
		// posture complaint about a client that does not exist.
		return nil
	}

	var out []Finding
	out = append(out, peerPostureFindings(rep)...)

	live := map[string]bool{}
	for _, p := range rep.Peers {
		live[p.Node] = true
		switch p.State {
		case PeerStateUntrusted:
			// Never debounced. One certificate that does not chain to the
			// cluster CA is already the whole signal; waiting two cycles to
			// mention it would be waiting two cycles to mention a possible
			// impersonation of a node that can rewrite the cluster's network.
			out = append(out, Finding{
				ID:       peerFindingID(CheckPeerUntrusted, p.Node),
				Source:   SourcePeer,
				Check:    CheckPeerUntrusted,
				Severity: SeverityError,
				Detail: fmt.Sprintf("Peer %s (%s) answered on the peer API port but its TLS certificate did not verify against the pinned cluster CA — vnproxd refused the connection and applied nothing. This is not a connectivity problem. Likely causes, in order: that node serves a certificate this cluster's CA did not issue (a rebuilt node, a custom pveproxy certificate, a stale /etc/pve); the certificate is not valid for the address the peer is reached at; or something other than that node is answering on its address. Peer error: %s",
					p.Node, p.Addr, p.Error),
				Nodes:    []string{p.Node},
				Refs:     []string{"peer:" + p.Node},
				DocsLink: peerTrustDocsLink,
			})
		case PeerStateUnreachable:
			if !unreachDB.Evaluate(p.Node, true, peerUnreachableRise, peerUnreachableFall) {
				continue
			}
			out = append(out, Finding{
				ID:       peerFindingID(CheckPeerUnreachable, p.Node),
				Source:   SourcePeer,
				Check:    CheckPeerUnreachable,
				Severity: SeverityWarning,
				Detail: fmt.Sprintf("Peer %s (%s) is not answering on the peer API port — nothing responded, so cross-node reads and coordination for that node degrade to this node's own view. Its certificate was never in question. Peer error: %s",
					p.Node, p.Addr, p.Error),
				Nodes:    []string{p.Node},
				Refs:     []string{"peer:" + p.Node},
				DocsLink: peerTrustDocsLink,
			})
		default:
			unreachDB.Evaluate(p.Node, false, peerUnreachableRise, peerUnreachableFall)
		}
	}
	unreachDB.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// peerPostureFindings reports this daemon's own trust posture whenever it is
// weaker than the pinned default. Each cause gets its own stable finding id,
// so two simultaneous causes are two findings rather than one that silently
// drops the second.
func peerPostureFindings(rep PeerTrustReport) []Finding {
	nodes := sortedUnique([]string{rep.LocalNode})
	newPosture := func(cause, severity, detail string) Finding {
		return Finding{
			ID:       "peer:" + CheckPeerTrustDegraded + "|" + cause,
			Source:   SourcePeer,
			Check:    CheckPeerTrustDegraded,
			Severity: severity,
			Detail:   detail,
			Nodes:    nodes,
			DocsLink: peerTrustDocsLink,
		}
	}

	var out []Finding
	switch {
	case rep.Mode == "insecure":
		out = append(out, newPosture("unverified", SeverityError,
			"Peer-API TLS verification is disabled on this node ([peer] tls_trust = \"insecure\"). Peer certificates are not checked at all, so any host able to answer on the peer port can impersonate a peer daemon and drive cluster-wide network changes. This is a development escape hatch and must never be set on a production node."))
	case rep.Mode == "system":
		out = append(out, newPosture("unpinned", SeverityWarning,
			"Peer-API TLS is not pinned to this cluster's CA on this node ([peer] tls_trust = \"system\"): peer daemons are authenticated against the host system trust store, so a certificate from any publicly-trusted CA is accepted as a peer. Remove the [peer] tls_trust / tls_trust_ack keys to restore pinning to "+defaultOrCAFile(rep)+"."))
	case !rep.Pinned:
		out = append(out, newPosture("unpinned", SeverityWarning,
			"Peer-API TLS trust on this node is decided by a caller-supplied HTTP client rather than the pinned cluster CA, so vnproxd cannot vouch for how peer certificates are verified."))
	}

	if rep.AnchorError != "" {
		out = append(out, newPosture("anchor_unavailable", SeverityError,
			fmt.Sprintf("The pinned peer-API trust anchor %s cannot be read, so no peer certificate can be verified and every peer is treated as unreachable — vnproxd fails closed here and never falls back to the system trust store. On a PVE node this file is provided by pmxcfs; check that /etc/pve is mounted and pve-cluster is running. Error: %s",
				defaultOrCAFile(rep), rep.AnchorError)))
	}

	if rep.Scheme != "" && rep.Scheme != "https" {
		out = append(out, newPosture("plaintext", SeverityError,
			fmt.Sprintf("The peer API client on this node dials %q, not https — peer traffic carrying cluster-wide network mutations is neither encrypted nor authenticated by TLS at all, and CA pinning cannot apply.", rep.Scheme)))
	}
	return out
}

func defaultOrCAFile(rep PeerTrustReport) string {
	if rep.CAFile == "" {
		return "the cluster CA"
	}
	return rep.CAFile
}

func peerFindingID(check, node string) string {
	return "peer:" + check + "|" + node
}
