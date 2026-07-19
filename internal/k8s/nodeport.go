// nodeport.go implements the k8s_nodeport_exposed_without_fw_rule finding
// (T-1501's card): a NodePort/LoadBalancer service's port has no covering
// PVE firewall allow rule on the backing guest/node. "Covering" is
// deliberately narrow and literal — an explicit, enabled, inbound ACCEPT
// rule (on the correlated guest's own ruleset, or the cluster-scope
// ruleset every guest also sees, mirroring real pve-firewall's visibility
// rule) whose proto/port match. This check does not reason about default
// policy (ACCEPT vs DROP) or macro/alias/ipset expansion — see this file's
// own doc comment on scope — it answers exactly the question the finding
// name asks: does an explicit allow rule exist, yes or no.
//
// Uses flow/rule metadata only — no live probe, no payload inspection,
// consistent with every other health-style check in this codebase.

package k8s

import (
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// FwLookup resolves the firewall rulesets visible to a k8s node's
// correlated PVE guest: its own guest-scope ruleset (nil if none/never
// configured) and the cluster-scope ruleset every guest also sees (nil if
// none). internal/api's k8s routes build this from the same live
// inventory graph snapshot every other firewall-aware read in this
// codebase already uses — no second read path.
type FwLookup func(guestRef string) (guest, cluster *inventory.FwRuleset)

// NodePortFinding is one exposed-without-coverage NodePort/LoadBalancer
// port. Refs names every correlated, matched k8s node's guest ref found
// without coverage (a NodePort listens on every cluster node, so any one
// uncovered node is enough for the service to be reachable through it).
type NodePortFinding struct {
	ClusterID string   `json:"clusterId"`
	Namespace string   `json:"namespace"`
	Service   string   `json:"service"`
	Proto     string   `json:"proto"`
	Detail    string   `json:"detail"`
	Refs      []string `json:"refs"`
	Port      int32    `json:"port"`
	NodePort  int32    `json:"nodePort"`
}

// CheckNodePortExposure evaluates every NodePort/LoadBalancer service port
// in services against every matched node correlation in nodes (unmatched
// nodes are skipped — this check never asserts anything about a node it
// cannot resolve to a guest, per this package's "never guessed" contract).
// A service/port with zero matched nodes produces no finding either way:
// there is nothing this check can honestly conclude without at least one
// resolvable backing node.
func CheckNodePortExposure(clusterID string, services []Service, nodes []NodeCorrelation, lookup FwLookup) []NodePortFinding {
	var out []NodePortFinding
	for _, s := range services {
		t := s.Spec.EffectiveType()
		if t != "NodePort" && t != "LoadBalancer" {
			continue
		}
		for _, p := range s.Spec.Ports {
			if p.NodePort == 0 {
				continue
			}
			proto := strings.ToLower(p.EffectiveProtocol())

			var uncovered []string
			matchedAny := false
			for _, n := range nodes {
				if !n.Matched {
					continue
				}
				matchedAny = true
				var guestRS, clusterRS *inventory.FwRuleset
				if lookup != nil {
					guestRS, clusterRS = lookup(n.GuestRef)
				}
				if !rulesetCovers(guestRS, proto, int(p.NodePort)) && !rulesetCovers(clusterRS, proto, int(p.NodePort)) {
					uncovered = append(uncovered, n.GuestRef)
				}
			}
			if !matchedAny || len(uncovered) == 0 {
				continue
			}
			out = append(out, NodePortFinding{
				ClusterID: clusterID, Namespace: s.Metadata.Namespace, Service: s.Metadata.Name,
				Port: p.Port, NodePort: p.NodePort, Proto: proto, Refs: uncovered,
				Detail: "NodePort " + strconv.Itoa(int(p.NodePort)) + "/" + proto + " on " +
					s.Metadata.Namespace + "/" + s.Metadata.Name + " has no covering PVE firewall allow rule on " +
					strings.Join(uncovered, ", "),
			})
		}
	}
	return out
}

// rulesetCovers reports whether rs contains at least one enabled, inbound,
// ACCEPT rule matching proto+port. A nil or ruleset-disabled rs never
// covers (see this file's doc comment on why disabled counts as
// no-coverage).
func rulesetCovers(rs *inventory.FwRuleset, proto string, port int) bool {
	if rs == nil || !rs.Enabled {
		return false
	}
	for _, r := range rs.Rules {
		if !r.Enabled {
			continue
		}
		if !strings.EqualFold(r.Direction, "in") {
			continue
		}
		if !strings.EqualFold(r.Action, "ACCEPT") {
			continue
		}
		if r.Proto != "" && !strings.EqualFold(r.Proto, proto) {
			continue
		}
		if !dportMatches(r.Dport, port) {
			continue
		}
		return true
	}
	return false
}

// dportMatches parses a PVE firewall dport spec ("", "80", "80,443",
// "5900:5999", or a mix, comma-separated) and reports whether port falls
// inside it. An empty spec is a wildcard (matches every port) — real
// pve-firewall semantics for an unset dport field.
func dportMatches(spec string, port int) bool {
	if spec == "" {
		return true
	}
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		lo, hi, ok := parseDportToken(tok)
		if !ok {
			continue
		}
		if port >= lo && port <= hi {
			return true
		}
	}
	return false
}

func parseDportToken(tok string) (lo, hi int, ok bool) {
	if idx := strings.Index(tok, ":"); idx >= 0 {
		loS, hiS := tok[:idx], tok[idx+1:]
		l, err1 := strconv.Atoi(strings.TrimSpace(loS))
		h, err2 := strconv.Atoi(strings.TrimSpace(hiS))
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return l, h, true
	}
	v, err := strconv.Atoi(tok)
	if err != nil {
		return 0, 0, false
	}
	return v, v, true
}
