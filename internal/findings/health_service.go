// health_service.go implements docs/features/monitoring.md §5's
// "dnsmasq/frr service down on a node" check. Unlike every other check in
// this package, this one needs raw data no existing collector gathers
// (systemd unit status) — see internal/host.Reader.Services (new in this
// task) and collect.Config.OnServices, which feeds Engine.IngestServices
// once per node per host-loop tick, the same "hook into the existing
// cadence" pattern T-601 used for OnStats/metrics.Sampler.Ingest.
//
// watchedServices is the fixed, small set of network-relevant systemd units
// vnprox cares about: dnsmasq (SDN DHCP) and frr (SDN EVPN/routing). A node
// that has never reported one of these (not installed — most nodes have no
// reason to run frr, say, unless they use SDN EVPN) never gets flagged for
// it: Engine.serviceStatus only records what OnServices actually reported,
// and checkServiceDown only evaluates keys present in that map.

package findings

import (
	"fmt"
	"sort"
	"sync"
)

const CheckServiceDown = "service_down"

const serviceDownDocsLink = "docs/features/monitoring.md#5-health-checks"

const (
	serviceDownRiseCycles = 2
	serviceDownFallCycles = 2
)

// serviceStatusStore holds the most recently observed systemd unit status
// per (node, service), fed by Engine.IngestServices. Safe for concurrent use.
type serviceStatusStore struct {
	status map[string]map[string]bool
	mu     sync.Mutex
}

func newServiceStatusStore() *serviceStatusStore {
	return &serviceStatusStore{status: map[string]map[string]bool{}}
}

// ingest records node's current service-status map (replacing, not
// merging, its previous entry — a service absent from status this time
// means "not observed this poll", not "still whatever it was last time";
// callers always pass a complete map for the units they polled).
func (s *serviceStatusStore) ingest(node string, status map[string]bool) {
	if node == "" || len(status) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]bool, len(status))
	for k, v := range status {
		cp[k] = v
	}
	s.status[node] = cp
}

// snapshot returns a defensive copy of the current node -> service -> active
// map.
func (s *serviceStatusStore) snapshot() map[string]map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]map[string]bool, len(s.status))
	for node, svcs := range s.status {
		cp := make(map[string]bool, len(svcs))
		for k, v := range svcs {
			cp[k] = v
		}
		out[node] = cp
	}
	return out
}

// checkServiceDown is the CheckServiceDown family: one finding per (node,
// service) currently — and, per db's hysteresis, consistently — reported
// inactive.
func checkServiceDown(store *serviceStatusStore, db *debouncer) []Finding {
	var out []Finding
	live := map[string]bool{}

	byNode := store.snapshot()
	nodes := make([]string, 0, len(byNode))
	for n := range byNode {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	for _, node := range nodes {
		services := byNode[node]
		names := make([]string, 0, len(services))
		for svc := range services {
			names = append(names, svc)
		}
		sort.Strings(names)

		for _, svc := range names {
			active := services[svc]
			key := node + "|" + svc
			live[key] = true
			firing := db.Evaluate(key, !active, serviceDownRiseCycles, serviceDownFallCycles)
			if !firing {
				continue
			}
			detail := fmt.Sprintf("%s is not running on node %s", svc, node)
			// Built directly rather than via newHealthFinding: a systemd
			// service has no inventory.Ref (it isn't a map entity), so
			// there's nothing to put in Refs, and the (node, service) pair
			// — not just node — must be part of the ID or two different
			// down services on the same node would collide onto one ID.
			f := Finding{
				ID:       "health:" + CheckServiceDown + "|" + key,
				Source:   SourceHealth,
				Check:    CheckServiceDown,
				Severity: SeverityError,
				Detail:   detail,
				Nodes:    []string{node},
				DocsLink: serviceDownDocsLink,
				// T-3604: the remedy is to start the unit. Mutating, so it
				// is the confirmed operational tier — not Fixable, because
				// starting a daemon is not a change to Proxmox network
				// configuration and has no changeset to stage.
				//
				// Declared for every watched unit without checking which
				// one it is: the allow-list that decides what may actually
				// be started lives in internal/host, enforced on the node
				// that runs the command. A producer that tried to
				// second-guess it here would be a fourth place for the two
				// lists to drift apart.
				Remedy: &Remediation{
					Action: RemedyActionServiceStart,
					Kind:   RemedyOperational,
					Label:  "Start " + svc,
					Params: map[string]string{"node": node, "service": svc},
				},
			}
			out = append(out, f)
		}
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
