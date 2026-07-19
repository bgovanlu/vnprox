// health_serviceclass.go implements T-1504's service_traffic_on_wrong_network
// finding (source "flow", not "health" — see SourceFlow's doc comment):
// storage/cluster traffic vnprox's classifier attributed to a service
// (migration/backup/ceph-public/ceph-cluster/corosync) whose own VLAN falls
// outside that service's declared network — the "storage traffic is eating
// the guest VLAN" scenario this task's card names. Fed by
// internal/flow.Classifier.Verdict (metadata only — see that package's doc
// comment for the honesty contract), never a second detector.

package findings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/flow"
)

const CheckServiceTrafficOnWrongNetwork = "service_traffic_on_wrong_network"

const serviceTrafficDocsLink = "docs/features/monitoring.md#5-health-checks"

// serviceTrafficRiseCycles/serviceTrafficFallCycles: the same 2-cycle
// each-way hysteresis window corosync_link_degraded uses for a comparable
// live-runtime-derived signal — a single noisy/transient sample never flips
// the finding on its own.
const (
	serviceTrafficRiseCycles = 2
	serviceTrafficFallCycles = 2
)

// FlowProvider is the findings engine's seam onto internal/flow's
// classifier + recent-samples store: the classification verdict for every
// flow record this cycle should evaluate. The caller (cmd/vnproxd) decides
// the recency window/row cap it queries flow_samples with before
// classifying — the same "caller decides the fan-out/window" shape
// CorosyncProvider/WanProvider already establish. Nil skips the check
// entirely, same degradation as every other optional producer input in
// this package.
type FlowProvider interface {
	RecentClassified() ([]flow.Classified, error)
}

// serviceTrafficKey is the (serviceClass, vlan) pair this check tracks —
// the finding fires per pair, not per flow record, so a burst of matching
// records doesn't multiply findings.
type serviceTrafficKey struct {
	class flow.ServiceClass
	vlan  int
}

func (k serviceTrafficKey) String() string {
	return fmt.Sprintf("%s|vlan%d", k.class, k.vlan)
}

// checkServiceTrafficOnWrongNetwork evaluates prov's current classified-flow
// batch: for every (serviceClass, vlan) pair observed this cycle, breach is
// true iff at least one record in that pair was flagged WrongNetwork by the
// classifier (internal/flow.Classified.WrongNetwork — VLAN known, matching
// source declares a VLAN set, and this VLAN isn't in it). Debounced per
// pair exactly like checkCorosyncLinkDegraded debounces per (node, ring).
func checkServiceTrafficOnWrongNetwork(prov FlowProvider, db *debouncer) []Finding {
	if prov == nil {
		return nil
	}
	classified, err := prov.RecentClassified()
	if err != nil {
		return nil
	}

	breachByKey := map[serviceTrafficKey]bool{}
	nodesByKey := map[serviceTrafficKey]map[string]bool{}

	for _, c := range classified {
		if c.ServiceClass == flow.ServiceClassUnclassified {
			continue
		}
		key := serviceTrafficKey{class: c.ServiceClass, vlan: c.Record.VLAN}
		if _, ok := breachByKey[key]; !ok {
			breachByKey[key] = false
		}
		if c.WrongNetwork {
			breachByKey[key] = true
			if nodesByKey[key] == nil {
				nodesByKey[key] = map[string]bool{}
			}
			if c.Record.Node != "" {
				nodesByKey[key][c.Record.Node] = true
			}
		}
	}

	keys := make([]serviceTrafficKey, 0, len(breachByKey))
	for k := range breachByKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].class != keys[j].class {
			return keys[i].class < keys[j].class
		}
		return keys[i].vlan < keys[j].vlan
	})

	var out []Finding
	live := map[string]bool{}
	for _, k := range keys {
		idKey := k.String()
		live[idKey] = true
		active := db.Evaluate(idKey, breachByKey[k], serviceTrafficRiseCycles, serviceTrafficFallCycles)
		if !active {
			continue
		}

		nodeSet := nodesByKey[k]
		nodes := make([]string, 0, len(nodeSet))
		for n := range nodeSet {
			nodes = append(nodes, n)
		}

		detail := fmt.Sprintf(
			"%s traffic observed on VLAN %d, outside its declared network — storage/cluster traffic is sharing a network it shouldn't (e.g. eating the guest VLAN)",
			k.class, k.vlan)
		out = append(out, Finding{
			ID:       serviceTrafficFindingIDPrefix + idKey,
			Source:   SourceFlow,
			Check:    CheckServiceTrafficOnWrongNetwork,
			Severity: SeverityWarning,
			Detail:   detail,
			Nodes:    sortedUnique(nodes),
			DocsLink: serviceTrafficDocsLink,
		})
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// serviceTrafficFindingIDPrefix is checkServiceTrafficOnWrongNetwork's own
// id scheme's fixed prefix — exported (via ServiceClassFromFindingID) so
// GET /history/events (internal/api/history.go, T-1007/T-1504) can carry
// serviceClass on this finding's "finding"-kind timeline entries without
// finding_events needing a new persisted column: the id itself already
// content-derives the serviceClass (see checkServiceTrafficOnWrongNetwork's
// ID construction above), so parsing it back out at render time is the
// same "recompute from what's already there" stance this codebase takes
// elsewhere rather than duplicating storage.
const serviceTrafficFindingIDPrefix = "flow:" + CheckServiceTrafficOnWrongNetwork + "|"

// ServiceClassFromFindingID extracts the serviceClass segment from a
// service_traffic_on_wrong_network finding's id (e.g.
// "flow:service_traffic_on_wrong_network|ceph-public|ceph-public|vlan20"),
// ok=false for any other id shape (a different check's id, or a malformed
// one) — never a guess.
func ServiceClassFromFindingID(id string) (serviceClass string, ok bool) {
	rest, found := strings.CutPrefix(id, serviceTrafficFindingIDPrefix)
	if !found {
		return "", false
	}
	class, _, found := strings.Cut(rest, "|")
	if !found || class == "" {
		return "", false
	}
	return class, true
}
