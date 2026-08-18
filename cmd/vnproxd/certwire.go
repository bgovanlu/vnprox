// certwire.go wires T-2301..T-2303's certificate service into the daemon:
// the cluster facts it needs, the findings adapter, and the peer-TLS
// verification-name resolver that fixes T-1906-bug-01.

package main

import (
	"context"
	"net"

	"github.com/bgovanlu/vnprox/internal/certs"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// clusterStatusSource is the subset of the PVE client the certificate service
// needs: who the cluster's members are, and what address each is reachable at.
// Deliberately narrow so the service is not handed the whole PVE client.
type clusterStatusSource = peer.ClusterStatusSource

// certClusterFactsFor adapts sdnPVEClient (a concrete *pve.Client, possibly
// nil) into certs.ClusterFactsFunc, and is what server.go's wiring must call
// instead of certClusterFacts directly.
//
// The nil check here is deliberately on the concrete *pve.Client, not on the
// clusterStatusSource interface certClusterFacts takes: a nil *pve.Client
// passed as an interface argument produces a non-nil interface value (a
// typed nil — Go's classic gotcha, "an interface holding a nil concrete
// pointer is not itself nil"), so certClusterFacts's own `src == nil` guard
// never fires for it. That is exactly what happened on pve001's first cold
// start, before vnprox-setup had ever run and cfg.PVE.TokenFile did not yet
// exist: setupCollect returned a nil *pve.Client, server.go passed it
// straight to certClusterFacts, the nil check inside compiled but never
// matched, and certs.NewService's first Refresh() called ClusterStatus on a
// nil *pve.Client — a nil-pointer panic three frames down in
// internal/pve.(*Client).do, crashing the whole daemon instead of degrading
// the way the collector already does for the identical "no PVE client yet"
// condition (see setupCollect's doc comment).
func certClusterFactsFor(client *pve.Client) certs.ClusterFactsFunc {
	if client == nil {
		return nil
	}
	return certClusterFacts(client)
}

// certClusterFacts adapts PVE's cluster status into certs.ClusterFacts.
//
// The dial addresses come from the *same* cluster status peer.Client.Peers
// derives its addresses from, on purpose: a second, independently-computed
// answer to "what address is this peer dialled at" is exactly how a check ends
// up validating a certificate against something other than what the transport
// actually does.
//
// Callers: use certClusterFactsFor(sdnPVEClient), not this function
// directly, unless src is already known non-nil by construction (e.g. a
// test double) — see certClusterFactsFor's doc comment for why the nil
// check below does not catch every nil case.
func certClusterFacts(src clusterStatusSource) certs.ClusterFactsFunc {
	if src == nil {
		return nil
	}
	return func(ctx context.Context) (certs.ClusterFacts, error) {
		entries, err := src.ClusterStatus(ctx)
		if err != nil {
			return certs.ClusterFacts{}, err
		}
		facts := certs.ClusterFacts{DialAddrs: map[string]string{}}
		for _, e := range entries {
			if e.Type != "node" || e.Name == "" {
				continue
			}
			facts.Members = append(facts.Members, e.Name)
			// The local node has no peer address to dial and legitimately
			// reports none; including it with an empty address would make the
			// SAN check silently skip it, which is right — a node never
			// verifies its own certificate over the peer API.
			if e.IP != "" && !e.Local {
				facts.DialAddrs[e.Name] = e.IP
			}
		}
		return facts, nil
	}
}

// certFindingsAdapter converts certs.Issue into the findings engine's own
// CertIssue, keeping internal/findings free of any dependency on
// internal/certs.
type certFindingsAdapter struct {
	svc *certs.Service
}

func (a certFindingsAdapter) Issues() []findings.CertIssue {
	if a.svc == nil {
		return nil
	}
	in := a.svc.Issues()
	out := make([]findings.CertIssue, 0, len(in))
	for _, i := range in {
		out = append(out, findings.CertIssue{
			Check:       i.Check,
			Severity:    i.Severity,
			Node:        i.Node,
			Path:        i.Path,
			Detail:      i.Detail,
			Remediation: i.Remediation,
		})
	}
	return out
}

// attachCertVerifyNames points the peer trust anchor at the certificate
// service's verification-name mapping.
//
// This is the line that fixes T-1906-bug-01: without it, peers are verified
// against the IP they are dialled at, which real PVE certificates do not
// reliably cover. The resolver is consulted per request and re-derived
// whenever the inventory refreshes, so a node that renews its certificate is
// picked up without a restart.
func attachCertVerifyNames(trust *peer.Trust, svc *certs.Service) {
	if trust == nil || svc == nil {
		return
	}
	trust.SetVerifyNameResolver(func(dialHost string) string {
		// Peer addresses are host:port, but Trust hands us the bare host; be
		// tolerant of either so a future caller passing the full address does
		// not silently get no mapping.
		if h, _, err := net.SplitHostPort(dialHost); err == nil {
			dialHost = h
		}
		return svc.VerifyNameFor(dialHost)
	})
}
