// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// statusPeerTrust builds the peer-API trust anchor `vnproxctl status` probes
// through (T-1906) — the identical [peer] configuration vnproxd itself uses,
// so a peer this command calls "reachable" is one the daemon would actually
// talk to. The escape-hatch banner is suppressed here (discardLogger): the
// operator-visible warning belongs to the daemon's own startup log, and
// printing it on every `vnproxctl status` invocation would train people to
// ignore it. The posture is instead *displayed* as part of the status output
// (describePeerTrust).
func statusPeerTrust(cfg *config.Config) (*peer.Trust, error) {
	return peer.NewTrust(peer.TrustOptions{
		Mode:   cfg.Peer.TLSTrust,
		CAFile: cfg.Peer.CAFile,
		Ack:    cfg.Peer.TLSTrustAck,
		Logger: discardLogger(),
	})
}

// describePeerTrust renders a one-line human summary of a peer-TLS posture for
// `vnproxctl status`, so an operator reading peer reachability can see at a
// glance whether those verdicts were reached with pinning on.
func describePeerTrust(st peer.TrustStatus) string {
	switch {
	case st.Error != "":
		return fmt.Sprintf("TLS trust: %s (%s) — UNUSABLE, every peer fails closed: %s", st.Mode, st.CAFile, st.Error)
	case st.Mode == peer.TrustInsecure:
		return "TLS trust: insecure — peer certificates are NOT verified at all (escape hatch; never for production)"
	case st.Mode == peer.TrustSystem:
		return "TLS trust: system pool — peer certificates are NOT pinned to this cluster's CA (escape hatch)"
	default:
		return fmt.Sprintf("TLS trust: pinned to the cluster CA (%s)", st.CAFile)
	}
}
