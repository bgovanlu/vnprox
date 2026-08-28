// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log/slog"
	"sync"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// newPeerTrust builds this daemon's single peer-API TLS trust anchor from
// [peer] (T-1906) and immediately forces its first evaluation.
//
// The forced evaluation is the point of doing it here rather than lazily on
// the first peer request: an operator whose `/etc/pve/pve-root-ca.pem` is
// missing (pmxcfs not mounted, pve-cluster down, a non-PVE host with no
// escape hatch configured) learns about it from a startup ERROR naming the
// path, not from cross-node reads mysteriously degrading an hour later. The
// daemon still starts — peer TLS fails closed, every peer is treated as
// unverifiable, and the node serves its own view, which is the documented
// single-node degradation — because refusing to boot would turn a transient
// pmxcfs problem into a total outage of the very UI an operator needs to
// diagnose it.
func newPeerTrust(cfg *config.Config, logger *slog.Logger) (*peer.Trust, error) {
	trust, err := peer.NewTrust(peer.TrustOptions{
		Mode:   cfg.Peer.TLSTrust,
		CAFile: cfg.Peer.CAFile,
		Ack:    cfg.Peer.TLSTrustAck,
		Logger: logger,
	})
	if err != nil {
		return nil, err
	}
	trust.Status()
	return trust, nil
}

// peerTrustAdapter is the composition-root bridge from *peer.Client's TLS
// trust posture to internal/findings' peer_untrusted / peer_unreachable /
// peer_trust_degraded producer (T-1906 AC5).
//
// Lazily set, mirroring federationTunnelAdapter/fwAnalyticsAdapter:
// setupFindings runs before the coordinator's peer client exists (server.go
// builds the findings engine first), so the adapter is wired in with its
// target unset and filled via set() once that client is built — always before
// the daemon serves a request or the findings loop ticks.
type peerTrustAdapter struct {
	client *peer.Client
	// localNode is the same closure the rest of runDaemon shares (it resolves
	// through the collector, which may not have polled yet at wiring time) —
	// held as a func, not a snapshotted string, so a posture finding names the
	// node once that is known instead of forever reporting "".
	localNode func() string
	mu        sync.Mutex
}

func (a *peerTrustAdapter) set(client *peer.Client, localNode func() string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = client
	a.localNode = localNode
}

// PeerTrust implements findings.PeerTrustProvider. An unset adapter reports an
// empty report, which raises no findings — the same "not wired yet contributes
// nothing" degradation every other findings adapter in this package uses.
func (a *peerTrustAdapter) PeerTrust() findings.PeerTrustReport {
	a.mu.Lock()
	client, localNodeFn := a.client, a.localNode
	a.mu.Unlock()
	if client == nil {
		return findings.PeerTrustReport{}
	}
	localNode := ""
	if localNodeFn != nil {
		localNode = localNodeFn()
	}

	rep := client.TrustReport()
	out := findings.PeerTrustReport{
		LocalNode:   localNode,
		Mode:        string(rep.Mode),
		CAFile:      rep.CAFile,
		AnchorError: rep.AnchorError,
		Scheme:      rep.Scheme,
		Pinned:      rep.Pinned,
		Peers:       make([]findings.PeerTrustStatus, 0, len(rep.Peers)),
	}
	for _, p := range rep.Peers {
		out.Peers = append(out.Peers, findings.PeerTrustStatus{
			Node:  p.Node,
			Addr:  p.Addr,
			State: string(p.State),
			Error: p.Error,
		})
	}
	return out
}
