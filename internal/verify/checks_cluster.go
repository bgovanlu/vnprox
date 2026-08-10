package verify

// checks_cluster.go holds the checks that read the cluster through the vnprox
// daemon and through PVE, and compare the two.
//
// A recurring shape here is worth naming once: where a check can, it asserts
// one source against a *different* source rather than against a constant.
// "The LLDP collector named a local interface PVE has never heard of" is a
// defect no single-source assertion can see, and cross-source disagreement is
// the only class of defect a fixture genuinely cannot fake — which is the
// whole reason this suite has to run on iron.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// documented enums, spelled from docs/api.md rather than from any Go type, so
// a check fails if the daemon starts emitting a value the contract does not
// name.
var (
	driftCheckFamilies = map[string]bool{
		"bridge_divergence":       true,
		"mtu_consistency":         true,
		"sdn_realization":         true,
		"pending_interfaces":      true,
		"file_runtime_divergence": true,
		"spec_drift":              true,
		"vf_spoofcheck_mismatch":  true,
	}
	flowSources = map[string]bool{
		"sflow":     true,
		"netflow5":  true,
		"netflow9":  true,
		"ipfix":     true,
		"conntrack": true,
	}
	externalSubnetSources = map[string]bool{
		"manual":   true,
		"netbox":   true,
		"phpipam":  true,
		"external": true,
	}
)

// checkChangeCommitted asks the one question about the change engine that a
// mock cannot answer: has this engine ever driven a real PVE node all the way
// from staged to committed?
func checkChangeCommitted(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			ID     string   `json:"id"`
			Status string   `json:"status"`
			Nodes  []string `json:"nodes"`
		} `json:"items"`
	}
	ev, err := daemonJSON(ctx, d, "/changesets", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("the change engine's applied history")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read the changeset history: %v", err), ev)
	}

	var committed []string
	for _, cs := range body.Items {
		if cs.Status == "committed" {
			committed = append(committed, cs.ID)
		}
	}
	if len(committed) == 0 {
		return Skip(fmt.Sprintf("no changeset on this cluster has reached `committed`: %d changesets exist, none applied and confirmed. Stage and apply one through vnprox, confirm it, and re-run", len(body.Items)), ev)
	}

	// A committed changeset with an unreadable diff is worse than none: it
	// means the engine recorded an apply whose effect it can no longer
	// describe, which is the audit story falling over.
	diffEv, diffErr := daemonJSON(ctx, d, "/changesets/"+committed[0]+"/diff", nil)
	if diffErr != nil {
		return Fail(fmt.Sprintf("changeset %s is committed but its diff is unreadable: %v", committed[0], diffErr), ev, diffEv)
	}
	return Pass(fmt.Sprintf("%d changeset(s) reached committed on this cluster; %s's diff is still readable", len(committed), committed[0]), ev, diffEv)
}

// checkCommitConfirmWindow is the read-only half of row 4.
//
// The claim it tests is not "a timer exists" — that is unit-tested — but that
// this installation's window is one an unattended revert can actually honour.
// The revert runs on a sealed PVE ticket, and a PVE ticket lives two hours;
// a confirm window longer than that arms a rollback that will find its own
// credential expired at the moment it needs it. That is only checkable
// against a real installation's configured value.
func checkCommitConfirmWindow(ctx context.Context, d Deps) Outcome {
	var body struct {
		Version                  string `json:"version"`
		ConfirmTimeoutDefaultSec int    `json:"confirmTimeoutDefaultSec"`
	}
	ev, err := daemonJSON(ctx, d, "/config", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("the configured commit-confirm window")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read the daemon's configuration: %v", err), ev)
	}

	const (
		mgmtFloorSec      = 180  // change.MgmtConfirmTimeoutFloor
		ticketLifetimeSec = 7200 // pve.TicketLifetime
	)
	switch {
	case body.ConfirmTimeoutDefaultSec <= 0:
		return Fail(fmt.Sprintf("this node reports a commit-confirm window of %ds: an apply would never arm a rollback", body.ConfirmTimeoutDefaultSec), ev)
	case body.ConfirmTimeoutDefaultSec >= ticketLifetimeSec:
		return Fail(fmt.Sprintf("the commit-confirm window is %ds, at or beyond a real PVE ticket's %ds lifetime: an unattended revert would find its sealed ticket expired", body.ConfirmTimeoutDefaultSec, ticketLifetimeSec), ev)
	case body.ConfirmTimeoutDefaultSec < mgmtFloorSec:
		// Not a failure: the floor is enforced per-changeset for
		// management-path changes, so a shorter default is legal. Worth
		// reporting, and worth reporting as evidence rather than silence.
		return Pass(fmt.Sprintf("commit-confirm window is %ds (below the %ds management-path floor, which is enforced per-changeset) and well inside a ticket's %ds lifetime", body.ConfirmTimeoutDefaultSec, mgmtFloorSec, ticketLifetimeSec), ev)
	default:
		return Pass(fmt.Sprintf("commit-confirm window is %ds: at or above the %ds management-path floor and inside a ticket's %ds lifetime", body.ConfirmTimeoutDefaultSec, mgmtFloorSec, ticketLifetimeSec), ev)
	}
}

// checkDriftConfigVsLive validates the shape and internal consistency of what
// a real cluster's drift detector emits.
func checkDriftConfigVsLive(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			ID       string   `json:"id"`
			Check    string   `json:"check"`
			Severity string   `json:"severity"`
			Detail   string   `json:"detail"`
			Nodes    []string `json:"nodes"`
		} `json:"items"`
	}
	ev, err := daemonJSON(ctx, d, "/drift", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("live drift findings")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read drift findings: %v", err), ev)
	}
	if len(body.Items) == 0 {
		// Honest: an empty list exercised the route and nothing else. It is
		// not evidence that the detector works, so it is not a pass.
		return Skip("this cluster reports no drift, so nothing exercised a finding's shape. To exercise it, introduce a deliberate divergence (e.g. edit /etc/network/interfaces without reloading) and re-run", ev)
	}

	known := make([]string, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		known = append(known, n.Name)
	}
	var problems []string
	for _, f := range body.Items {
		if !driftCheckFamilies[f.Check] {
			problems = append(problems, fmt.Sprintf("finding %q names check family %q, which docs/api.md does not document", f.ID, f.Check))
		}
		if strings.TrimSpace(f.ID) == "" {
			problems = append(problems, fmt.Sprintf("a %s finding has no stable id", f.Check))
		}
		for _, n := range f.Nodes {
			if len(known) > 0 && !containsString(known, n) {
				problems = append(problems, fmt.Sprintf("finding %q names node %q, which is not in this cluster (%s)", f.ID, n, strings.Join(known, ", ")))
			}
		}
	}
	if len(problems) > 0 {
		return Fail(fmt.Sprintf("%d drift finding(s) do not match the documented contract: %s", len(problems), strings.Join(problems, "; ")), ev)
	}
	return Pass(fmt.Sprintf("%d drift finding(s), every one in a documented check family with a stable id and real node names", len(body.Items)), ev)
}

// checkDriftNodeVsNode is the half the matrix marks `B`: comparing nodes
// against each other, which needs nodes to compare.
func checkDriftNodeVsNode(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			ID    string   `json:"id"`
			Check string   `json:"check"`
			Nodes []string `json:"nodes"`
		} `json:"items"`
	}
	ev, err := daemonJSON(ctx, d, "/drift", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("cross-node drift findings")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read drift findings: %v", err), ev)
	}

	var crossNode int
	for _, f := range body.Items {
		if len(uniqueStrings(f.Nodes)) >= 2 {
			crossNode++
		}
	}
	if crossNode == 0 {
		online := onlineNodes(d.Nodes)
		return Skip(fmt.Sprintf("no drift finding spans two nodes on this %d-node cluster, so the node-vs-node comparison reported nothing to check. To exercise it, create the same-named bridge on two nodes with different VLAN-awareness and re-run", len(online)), ev)
	}
	return Pass(fmt.Sprintf("%d drift finding(s) span two or more nodes: the cross-node comparison ran against real, differing nodes", crossNode), ev)
}

// checkFlowsIngested tests the one thing the eBPF/sampler scaffolding cannot
// self-report: that real datagrams from a real exporter arrived and decoded.
func checkFlowsIngested(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			Node   string `json:"node"`
			SrcIP  string `json:"srcIp"`
			DstIP  string `json:"dstIp"`
			Source string `json:"source"`
			At     int64  `json:"at"`
			Bytes  int64  `json:"bytes"`
			Proto  int    `json:"proto"`
		} `json:"items"`
	}
	ev, err := daemonJSON(ctx, d, "/flows", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("ingested flow records")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read flow records: %v", err), ev)
	}
	if len(body.Items) == 0 {
		return Skip("no flow record has been ingested on this node. Point a real sFlow/NetFlow/IPFIX exporter at this node's configured listener (or enable a host-local sampler in [flows]) and re-run", ev)
	}

	sources := map[string]int{}
	var problems []string
	for i, r := range body.Items {
		if !flowSources[r.Source] {
			problems = append(problems, fmt.Sprintf("record %d has source %q, which docs/api.md does not document", i, r.Source))
		}
		sources[r.Source]++
		if r.SrcIP == "" || r.DstIP == "" {
			problems = append(problems, fmt.Sprintf("record %d from %s has no source or destination address", i, r.Source))
		}
		if r.At <= 0 {
			problems = append(problems, fmt.Sprintf("record %d from %s carries no timestamp", i, r.Source))
		}
	}
	if len(problems) > 0 {
		return Fail(fmt.Sprintf("%d ingested flow record(s) are malformed: %s", len(problems), strings.Join(problems, "; ")), ev)
	}
	// A live exporter is the interesting case; a host-local conntrack sampler
	// proves much less, so the detail says which one was actually observed.
	return Pass(fmt.Sprintf("%d flow record(s) ingested and well-formed, from %s", len(body.Items), describeCounts(sources)), ev)
}

// checkCaptureAFPacket asks whether a capture on this node ever put real
// bytes on disk. The matrix's note for row 31 is "Real AF_PACKET backend
// unvalidated", and a capture group with zero packets across every session is
// exactly what an unprivileged or stubbed backend produces.
func checkCaptureAFPacket(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			ID       string `json:"id"`
			Sessions []struct {
				Node    string `json:"node"`
				Iface   string `json:"iface"`
				State   string `json:"state"`
				Packets int64  `json:"packets"`
				Bytes   int64  `json:"bytes"`
			} `json:"sessions"`
		} `json:"items"`
	}
	ev, err := daemonJSON(ctx, d, "/captures", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("packet-capture sessions")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read capture groups: %v", err), ev)
	}

	var finished, withPackets int
	for _, g := range body.Items {
		for _, s := range g.Sessions {
			if s.State == "running" {
				continue
			}
			finished++
			if s.Packets > 0 && s.Bytes > 0 {
				withPackets++
			}
		}
	}
	switch {
	case finished == 0:
		return Skip("no packet capture has finished on this node. Start a capture on an interface carrying live traffic, let it stop, and re-run", ev)
	case withPackets == 0:
		return Fail(fmt.Sprintf("%d finished capture session(s), none of which recorded a single packet: the AF_PACKET backend captured nothing on real interfaces", finished), ev)
	default:
		return Pass(fmt.Sprintf("%d of %d finished capture session(s) recorded real packets and bytes off a real interface", withPackets, finished), ev)
	}
}

// checkLLDPNeighbors cross-checks two independent readers of the same node:
// the LLDP collector's idea of which local interface a neighbour is on, and
// PVE's own interface list. A neighbour on an interface PVE has never heard
// of means one of the two is wrong, and neither can notice that alone.
func checkLLDPNeighbors(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			Node       string `json:"node"`
			LocalIface string `json:"localIface"`
			ChassisID  string `json:"chassisId"`
			PortID     string `json:"portId"`
		} `json:"items"`
	}
	ev, err := daemonJSON(ctx, d, "/lldp", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("LLDP neighbours")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read LLDP neighbours: %v", err), ev)
	}
	if len(body.Items) == 0 {
		return Skip("no LLDP neighbour is visible from this cluster. Install lldpd on a node cabled to an LLDP-advertising switch (`vnproxctl`/the UI can do this) and re-run", ev)
	}
	if d.Cluster == nil {
		return skipNoCluster("cross-checking LLDP's local interface names against PVE's own")
	}

	node := localNode(d.Nodes)
	if node == "" {
		return skipNoCluster("cross-checking LLDP against PVE needs a named cluster node")
	}
	ifaces, err := d.Cluster.Interfaces(ctx, node)
	if err != nil {
		return Fail(fmt.Sprintf("LLDP reported %d neighbour(s) but PVE's interface list for %s could not be read: %v", len(body.Items), node, err), ev)
	}
	pveEv := NewEvidence(SourcePVEAPI, fmt.Sprintf("GET /nodes/%s/network", node), describeIfaces(ifaces))

	names := make([]string, 0, len(ifaces))
	for _, i := range ifaces {
		names = append(names, i.Name)
	}
	var problems []string
	var matched int
	for _, n := range body.Items {
		if n.Node != node {
			continue // a peer's neighbours are checked when the suite runs there
		}
		if n.ChassisID == "" || n.PortID == "" {
			problems = append(problems, fmt.Sprintf("neighbour on %s carries no chassis or port id", n.LocalIface))
		}
		if !containsString(names, n.LocalIface) {
			problems = append(problems, fmt.Sprintf("neighbour on local interface %q, which PVE does not list for %s (PVE knows: %s)", n.LocalIface, node, strings.Join(names, ", ")))
			continue
		}
		matched++
	}
	if len(problems) > 0 {
		return Fail(fmt.Sprintf("LLDP and PVE disagree about %s: %s", node, strings.Join(problems, "; ")), ev, pveEv)
	}
	if matched == 0 {
		return Skip(fmt.Sprintf("LLDP reported %d neighbour(s), none of them on %s (the node this run reads PVE from), so there was nothing to cross-check", len(body.Items), node), ev, pveEv)
	}
	return Pass(fmt.Sprintf("%d LLDP neighbour(s) on %s, every one on an interface PVE also reports", matched, node), ev, pveEv)
}

// checkExternalSubnetProvenance tests row 14's honesty property: an external
// subnet's provenance is recorded rather than flattened into "we have it".
func checkExternalSubnetProvenance(ctx context.Context, d Deps) Outcome {
	var body struct {
		Items []struct {
			ID     string `json:"id"`
			CIDR   string `json:"cidr"`
			Source string `json:"source"`
		} `json:"items"`
	}
	ev, err := daemonJSON(ctx, d, "/ipam/external-subnets", &body)
	if errors.Is(err, errNoDaemon) {
		return skipNoDaemon("registered external subnets")
	}
	if err != nil {
		return Fail(fmt.Sprintf("could not read external subnets: %v", err), ev)
	}
	if len(body.Items) == 0 {
		return Skip("no external subnet is registered on this install, so nothing exercised the provenance path. Register one (manually, or by configuring a NetBox/phpIPAM source) and re-run", ev)
	}

	counts := map[string]int{}
	var problems []string
	for _, s := range body.Items {
		if !externalSubnetSources[s.Source] {
			problems = append(problems, fmt.Sprintf("subnet %s reports source %q, which docs/api.md does not document", s.CIDR, s.Source))
		}
		counts[s.Source]++
		if strings.TrimSpace(s.CIDR) == "" {
			problems = append(problems, fmt.Sprintf("subnet %s has no CIDR", s.ID))
		}
	}
	if len(problems) > 0 {
		return Fail(fmt.Sprintf("%d external subnet(s) do not match the documented contract: %s", len(problems), strings.Join(problems, "; ")), ev)
	}
	if counts["manual"] == len(body.Items) {
		return Skip(fmt.Sprintf("all %d external subnet(s) are manually registered, so no NetBox/phpIPAM sync path was exercised — which matches the matrix note that the production write client is unwritten. Configure a real external IPAM source to exercise it", len(body.Items)), ev)
	}
	return Pass(fmt.Sprintf("%d external subnet(s), provenance recorded per row: %s", len(body.Items), describeCounts(counts)), ev)
}

// --- small shared helpers ---------------------------------------------------

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// describeCounts renders a map as "3 sflow, 1 netflow9", sorted so a report
// diff between two runs is not noise.
func describeCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	return strings.Join(parts, ", ")
}

func describeIfaces(ifaces []Iface) string {
	parts := make([]string, 0, len(ifaces))
	for _, i := range ifaces {
		parts = append(parts, fmt.Sprintf("%s (%s, mtu %d)", i.Name, i.Type, i.MTU))
	}
	return strings.Join(parts, "\n")
}

// mustJSON renders a value for evidence. A marshal failure is reported
// in-band rather than swallowed, since evidence nobody can read is the thing
// this package exists to stop shipping.
func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("(could not render evidence: %v)", err)
	}
	return string(b)
}

// recentEnough is the shared spelling of "this timestamp is live, not a
// leftover", used by the WireGuard handshake and HA lease checks.
func recentEnough(now time.Time, unixSeconds int64, window time.Duration) bool {
	if unixSeconds <= 0 {
		return false
	}
	return now.Sub(time.Unix(unixSeconds, 0)) <= window
}
