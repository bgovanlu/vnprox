// SPDX-License-Identifier: Apache-2.0

// k8s.go wires T-1501's Kubernetes overlay mapping engine into the daemon's
// unified findings stream: k8sFindingsAdapter converts internal/k8s.Poller's
// cached NodePort-exposure findings into the findings.Finding shape. The
// app-store cluster registry (store.NewK8sClusterRepo) and the poll cache
// (k8s.NewPoller) are constructed directly in server.go alongside every
// other repository/service — this file only holds the small findings-stream
// conversion, the same "composition root does the conversion so the domain
// package need not import internal/findings" pattern findings.go's
// ipamFindingsAdapter already establishes for internal/ipam.

package main

import (
	"strconv"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/k8s"
)

// k8sNodePortDocsLink is the remediation pointer for
// k8s_nodeport_exposed_without_fw_rule — not fixable (a firewall rule
// addition is a judgment call about intended exposure, not a computable
// patch), so it links to the docs instead, the same "remediation ... docs
// link otherwise" contract every other non-fixable producer follows.
const k8sNodePortDocsLink = "docs/api.md#kubernetes-t-1501"

// k8sFindingsAdapter adapts internal/k8s.Poller's cached NodePort-exposure
// findings into the unified findings stream (findings.K8sProvider).
// Nil-safe: a nil poller (e.g. a build with no k8s clusters ever
// registered/polled) contributes zero findings, matching every other
// optional producer's degraded mode.
type k8sFindingsAdapter struct {
	poller *k8s.Poller
}

func (a k8sFindingsAdapter) Findings() []findings.Finding {
	if a.poller == nil {
		return nil
	}
	cached := a.poller.CachedFindings()
	out := make([]findings.Finding, 0, len(cached))
	for _, f := range cached {
		out = append(out, k8sNodePortFindingToFinding(f))
	}
	return out
}

// k8sNodePortFindingToFinding maps one k8s.NodePortFinding to a unified
// Finding: source k8s, check k8s_nodeport_exposed_without_fw_rule, a
// content-derived stable id (cluster/namespace/service/nodePort, never
// random) so re-polling unchanged state reproduces byte-identical ids —
// the same property Engine's change/notify tracking depends on for every
// other producer. Nodes is left empty (a k8s cluster is not itself a PVE
// cluster node, the same "this producer has no node to name" treatment
// wan_degraded's own finding already documents); Refs names every
// uncovered guest's ref. Never fixable.
func k8sNodePortFindingToFinding(f k8s.NodePortFinding) findings.Finding {
	id := "k8s:k8s_nodeport_exposed_without_fw_rule|" + f.ClusterID + "/" + f.Namespace + "/" + f.Service + "/" + strconv.Itoa(int(f.NodePort))
	return findings.Finding{
		ID:       id,
		Source:   findings.SourceK8s,
		Check:    "k8s_nodeport_exposed_without_fw_rule",
		Severity: findings.SeverityWarning,
		Detail:   f.Detail,
		DocsLink: k8sNodePortDocsLink,
		// Nodes has no `omitempty` (findings.Finding), so a nil slice here
		// would serialize as JSON `null` and crash the frontend's
		// `for (const n of f.nodes)` — the exact bug ipamConflictToFinding/
		// probeDivergenceToFinding above already document and guard
		// against; a k8s cluster is not itself a PVE cluster node (the
		// same "nothing to name" case wan_degraded's own finding
		// documents in docs/api.md), so this is legitimately always empty,
		// but it must be `[]`, never `null`.
		Nodes:   []string{},
		Refs:    append([]string(nil), f.Refs...),
		Fixable: false,
	}
}
