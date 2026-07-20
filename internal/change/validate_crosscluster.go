package change

// Cross-cluster changeset scoping (T-1201, docs/architecture §1/§5). A
// federated vnprox primary attaches multiple PVE clusters; every changeset
// is scoped to exactly one of them (Changeset.ClusterID). Config ownership
// stays strictly per-cluster — there is no cross-cluster mutation primitive
// — so an op whose target Ref belongs to a *different* attached cluster than
// the changeset's own ClusterID is rejected here, at validation time, with
// the stable codeCrossClusterRef error. This is deliberately a pure,
// table-testable function (like every other validator class in this
// package): the node->cluster membership map is threaded in by the caller
// (Service, via its optional ClusterMembership seam), never read live from
// inside this package.

import "github.com/bgovanlu/vnprox/internal/inventory"

// ValidateClusterScope returns a blocking finding for every op in ops whose
// target Ref resolves — through nodeCluster — to a cluster other than
// clusterID.
//
// Scoping rules, chosen to keep a single-cluster deployment completely
// unaffected (the "implicit default cluster" the card requires):
//
//   - clusterID == "" means "the implicit default/local cluster": scoping is
//     not enforced (a pre-federation deployment never populates ClusterID and
//     must keep validating exactly as before). Returns nil.
//   - nodeCluster == nil / empty means "no federation membership is known"
//     (no clusters attached yet, or the resolver was unavailable): nothing to
//     compare against. Returns nil.
//   - A cluster-scoped op (empty Target.Node — e.g. an SDN or cluster-firewall
//     op) belongs to the changeset's cluster by definition; there is no node
//     to map, so it is never a cross-cluster violation.
//   - A node-scoped op whose Target.Node is present in nodeCluster and maps to
//     a cluster different from clusterID is a violation. A node absent from
//     nodeCluster is left alone (the resolver simply doesn't know it yet —
//     "never guessed", the same conservative stance the referential class
//     takes for entities it can't resolve).
func ValidateClusterScope(clusterID string, ops []Op, nodeCluster map[string]string) []Finding {
	if clusterID == "" || len(nodeCluster) == 0 {
		return nil
	}
	var findings []Finding
	for _, op := range ops {
		node := op.Target.Node
		if node == "" {
			continue // cluster-scoped op: belongs to the changeset's cluster by definition
		}
		owner, known := nodeCluster[node]
		if !known || owner == clusterID {
			continue
		}
		findings = append(findings, crossClusterFinding(clusterID, owner, op.Target))
	}
	return findings
}

// crossClusterFinding builds the codeCrossClusterRef error for one offending
// op target.
func crossClusterFinding(changesetCluster, targetCluster string, ref inventory.Ref) Finding {
	return errorf(codeCrossClusterRef, ref.String(),
		"op target %s belongs to cluster %q but this changeset is scoped to cluster %q; a changeset may not span clusters",
		ref.String(), targetCluster, changesetCluster)
}
