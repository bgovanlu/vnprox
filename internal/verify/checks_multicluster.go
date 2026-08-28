// SPDX-License-Identifier: Apache-2.0

package verify

// checks_multicluster.go covers the four matrix rows whose `B` mark means
// "there has never been a second cluster to try this against": federation,
// cross-cluster IPAM, the WireGuard interconnect, and HA active/standby.
//
// Each of these has a credential- or safety-shaped invariant that *is*
// checkable on a single install (a sealed credential must never be echoed; a
// private key must never leave the node that generated it), and a behavioural
// half that genuinely needs the second cluster. They are separate checks on
// purpose: folding them together would make the checkable half skip along with
// the uncheckable one, and the checkable half is the one that would catch a
// credential leak.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// credentialFieldNames are the JSON keys a federation or tunnel response must
// never carry. Matched against the raw body, not a decoded struct: decoding
// into a struct that has no such field is precisely how a leak stays
// invisible, since encoding/json silently drops what the struct does not name.
var credentialFieldNames = []string{
	`"credential"`,
	`"token"`,
	`"secret"`,
	`"password"`,
	`"apiToken"`,
	`"privateKey"`,
	`"presharedKey"`,
}

func checkFederationCredentialNeverEchoed(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			APIURL string `json:"apiUrl"`
		} `json:"items"`
	}
	ev, err := daemonJSON(ctx, d, "/federation/clusters", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("attached federation clusters")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read attached clusters: %v", err), ev)
	}
	if len(body.Items) == 0 {
		return Skip("no remote cluster is attached, so no sealed credential existed to be echoed. Attach a second real cluster and re-run", ev)
	}

	// The raw body is what we scan, and it is also what we attach as
	// evidence — so a reader can check the scan rather than trust it.
	raw := ev.Output
	var found []string
	for _, field := range credentialFieldNames {
		if strings.Contains(raw, field) {
			found = append(found, field)
		}
	}
	if len(found) > 0 {
		return Fail(fmt.Sprintf("GET /federation/clusters echoed %s for %d attached cluster(s): the credential sealed on attach is being handed back", strings.Join(found, ", "), len(body.Items)), ev)
	}
	return Pass(fmt.Sprintf("%d attached cluster(s), none of whose responses carry any of the %d credential-shaped fields", len(body.Items), len(credentialFieldNames)), ev)
}

// checkFederationRoundTrip is the half that needs the second cluster: does a
// federated read actually cross the wire and come back whole?
func checkFederationRoundTrip(ctx context.Context, d Deps) Outcome {
	var clusters struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	clusterEv, err := daemonJSON(ctx, d, "/federation/clusters", &clusters)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("a federated read across clusters")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read attached clusters: %v", err), clusterEv)
	}
	if len(clusters.Items) == 0 {
		return Skip("no remote cluster is attached to this one, so there was no federated round trip to make. This is the row the matrix marks `B` — attach a second real cluster and re-run", clusterEv)
	}

	var topo struct {
		Clusters []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Nodes []struct {
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"clusters"`
		FailedClusters []string `json:"failedClusters"`
		Partial        bool     `json:"partial"`
	}
	topoEv, err := daemonJSON(ctx, d, "/federation/topology", &topo)
	if err != nil {
		return Fail(fmt.Sprintf("%d cluster(s) attached but the federated topology could not be read: %v", len(clusters.Items), err), clusterEv, topoEv)
	}
	if len(topo.FailedClusters) > 0 || topo.Partial {
		return Fail(fmt.Sprintf("the federated read came back partial: %d of %d attached cluster(s) failed (%s). A federation that answers with a hole is the failure this row has never been able to observe",
			len(topo.FailedClusters), len(clusters.Items), strings.Join(topo.FailedClusters, ", ")), clusterEv, topoEv)
	}
	if len(topo.Clusters) < 2 {
		return Fail(fmt.Sprintf("%d cluster(s) attached but the federated topology returned %d: the remote cluster's own view never arrived", len(clusters.Items), len(topo.Clusters)), clusterEv, topoEv)
	}

	var remoteNodes int
	for _, c := range topo.Clusters {
		remoteNodes += len(c.Nodes)
	}
	return Pass(fmt.Sprintf("federated topology returned %d cluster(s) and %d node(s) with no failed cluster: a real cross-cluster read completed", len(topo.Clusters), remoteNodes), clusterEv, topoEv)
}

func checkFederationIPAMConflicts(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			Type     string   `json:"type"`
			Severity string   `json:"severity"`
			Message  string   `json:"message"`
			Clusters []string `json:"clusters"`
		} `json:"items"`
		FailedClusters []string `json:"failedClusters"`
		Partial        bool     `json:"partial"`
	}
	ev, err := daemonJSON(ctx, d, "/federation/ipam/conflicts", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("cross-cluster IPAM conflicts")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read cross-cluster IPAM conflicts: %v", err), ev)
	}
	if len(body.FailedClusters) > 0 {
		return Fail(fmt.Sprintf("the conflict scan could not reach %s, so its answer covers only part of the federation and cannot be read as 'no conflicts'", strings.Join(body.FailedClusters, ", ")), ev)
	}

	var crossCluster int
	var problems []string
	for _, c := range body.Items {
		if c.Type != "cross_cluster_duplicate_subnet" {
			continue
		}
		crossCluster++
		// The documented contract is a pair. A cross-cluster conflict naming
		// one cluster is a conflict with itself, which is not a thing.
		if len(uniqueStrings(c.Clusters)) != 2 {
			problems = append(problems, fmt.Sprintf("a cross_cluster_duplicate_subnet conflict names %d cluster(s) (%s) instead of the documented pair", len(c.Clusters), strings.Join(c.Clusters, ", ")))
		}
	}
	if len(problems) > 0 {
		return Fail(strings.Join(problems, "; "), ev)
	}
	if crossCluster == 0 {
		return Skip(fmt.Sprintf("the scan completed against every attached cluster and found no cross-cluster subnet overlap (%d intra-cluster conflict(s)), so the cross-cluster path itself reported nothing. Configure an overlapping CIDR in two attached clusters to exercise it", len(body.Items)), ev)
	}
	return Pass(fmt.Sprintf("%d cross-cluster subnet overlap(s) detected across a real federation, each naming the two clusters that share it", crossCluster), ev)
}

func checkWireGuardNoPrivateKey(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			ID     string `json:"id"`
			Node   string `json:"node"`
			IfName string `json:"ifName"`
			Status struct {
				InterfaceUp bool `json:"interfaceUp"`
				PeerCount   int  `json:"peerCount"`
			} `json:"status"`
		} `json:"items"`
	}
	ev, err := daemonJSON(ctx, d, "/wireguard/tunnels", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("WireGuard tunnels")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read WireGuard tunnels: %v", err), ev)
	}
	if len(body.Items) == 0 {
		return Skip("no WireGuard tunnel is configured, so no private key existed to be leaked. Create a real tunnel and re-run", ev)
	}

	var found []string
	for _, field := range []string{`"privateKey"`, `"presharedKey"`, `"private_key"`} {
		if strings.Contains(ev.Output, field) {
			found = append(found, field)
		}
	}
	if len(found) > 0 {
		return Fail(fmt.Sprintf("GET /wireguard/tunnels carries %s across %d tunnel(s): a key generated on the owning node has left it", strings.Join(found, ", "), len(body.Items)), ev)
	}
	return Pass(fmt.Sprintf("%d real tunnel(s), none of whose responses carry private or pre-shared key material", len(body.Items)), ev)
}

// wgHandshakeWindow is how recently a peer must have handshaken for the
// tunnel to count as live. WireGuard rekeys every 120s under traffic and
// sends a keepalive-driven handshake at least every 180s where one is
// configured; 10 minutes is comfortably past both, so a peer outside it is
// not "between handshakes", it is down.
const wgHandshakeWindow = 10 * time.Minute

func checkWireGuardHandshake(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			ID     string `json:"id"`
			Node   string `json:"node"`
			IfName string `json:"ifName"`
			Peers  []struct {
				PublicKey         string `json:"publicKey"`
				Endpoint          string `json:"endpoint"`
				LastHandshakeUnix int64  `json:"lastHandshakeUnix"`
				RxBytes           int64  `json:"rxBytes"`
				External          bool   `json:"external"`
			} `json:"peers"`
			Status struct {
				InterfaceUp bool `json:"interfaceUp"`
				PeerCount   int  `json:"peerCount"`
			} `json:"status"`
		} `json:"items"`
	}
	ev, err := daemonJSON(ctx, d, "/wireguard/tunnels", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("WireGuard peer handshakes")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read WireGuard tunnels: %v", err), ev)
	}
	if len(body.Items) == 0 {
		return Skip("no WireGuard tunnel is configured. Bring up a real tunnel to a second cluster and re-run", ev)
	}

	now := d.Now()
	var up, handshaken int
	var stale []string
	for _, t := range body.Items {
		if !t.Status.InterfaceUp {
			continue
		}
		up++
		var live bool
		for _, p := range t.Peers {
			if recentEnough(now, p.LastHandshakeUnix, wgHandshakeWindow) {
				live = true
				break
			}
		}
		if live {
			handshaken++
			continue
		}
		stale = append(stale, fmt.Sprintf("%s (%s on %s, %d peer(s))", t.ID, t.IfName, t.Node, len(t.Peers)))
	}

	switch {
	case up == 0:
		return Skip(fmt.Sprintf("%d tunnel(s) configured, none with its interface up, so no handshake could have happened. Bring the interface up on a node with a routable path to the peer and re-run", len(body.Items)), ev)
	case len(stale) > 0:
		return Fail(fmt.Sprintf("%d of %d up tunnel(s) have no peer that handshook within %s: %s. An interface that is up with no handshake is the exact failure a single-cluster test cannot produce",
			len(stale), up, wgHandshakeWindow, strings.Join(stale, "; ")), ev)
	default:
		return Pass(fmt.Sprintf("%d up tunnel(s), every one with a peer that handshook within %s over a real path", handshaken, wgHandshakeWindow), ev)
	}
}

func checkHALeaseAndReplication(ctx context.Context, d Deps) Outcome {
	var body struct {
		Role                string `json:"role"`
		LastError           string `json:"lastError"`
		Term                int64  `json:"term"`
		LeaseExpiresAt      int64  `json:"leaseExpiresAt"`
		ReplicationLag      int64  `json:"replicationLag"`
		ReplicationDegraded bool   `json:"replicationDegraded"`
	}
	ev, err := daemonJSON(ctx, d, "/ha/status", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("this node's HA role and lease")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read HA status: %v", err), ev)
	}
	if body.Role == "" || body.Role == "disabled" {
		return Skip(fmt.Sprintf("HA is not enabled on this node (role %q), so there is no lease to hold and no replication to lag. Enable HA on two or more real nodes and re-run", body.Role), ev)
	}

	now := d.Now()
	switch {
	case body.Role == "active" && !recentEnough(now, body.LeaseExpiresAt, 0) && body.LeaseExpiresAt > 0 && time.Unix(body.LeaseExpiresAt, 0).Before(now):
		// An active node whose own lease has already expired is a split-brain
		// waiting to be observed: it believes it is authoritative and the
		// fencing mechanism says it is not.
		return Fail(fmt.Sprintf("this node reports role=active at term %d with a lease that expired at %s (now %s): it is acting authoritative past its own fence",
			body.Term, time.Unix(body.LeaseExpiresAt, 0).UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339)), ev)
	case body.ReplicationDegraded:
		return Fail(fmt.Sprintf("HA reports replication degraded (role %s, lag %ds, lastError %q): a failover now would promote a standby that is behind",
			body.Role, body.ReplicationLag, body.LastError), ev)
	case body.Role == "standby" && body.ReplicationLag <= 0 && body.Term == 0:
		return Skip("this node reports role=standby at term 0 with no replication observed yet; it has not seen an active peer. Start vnproxd on the other node(s) and re-run", ev)
	default:
		return Pass(fmt.Sprintf("HA role %s at term %d, replication lag %ds, not degraded, on a %d-node cluster",
			body.Role, body.Term, body.ReplicationLag, len(onlineNodes(d.Nodes))), ev)
	}
}
