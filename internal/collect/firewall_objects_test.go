// SPDX-License-Identifier: Apache-2.0

package collect_test

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const fixtureSingleNode = "../../testdata/clusters/single-node.yaml"

// TestFirewallObjects_ClusterAliasIngested is T-501's collector deliverable:
// the cluster-scope alias declared in testdata/clusters/single-node.yaml
// (firewall.cluster.aliases: "management_net") must be ingested onto the
// cluster FwRuleset entity, not just its rules/options — the gap
// FromPVEFirewall's aliases/ipsets/groups parameters (added for T-501)
// close.
func TestFirewallObjects_ClusterAliasIngested(t *testing.T) {
	srv := loadFixtureServer(t, fixtureSingleNode)
	c, graph, _ := newTestCollector(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.RunPVELoop(ctx) }()

	ref := inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}
	waitFor(t, 3*time.Second, "cluster firewall ruleset to converge", func() bool {
		_, ok := graph.Snapshot().Get(ref)
		return ok
	})

	ent, ok := graph.Snapshot().Get(ref)
	if !ok {
		t.Fatal("cluster fw-ruleset entity not found")
	}
	rs, ok := ent.(*inventory.FwRuleset)
	if !ok {
		t.Fatalf("entity is a %T, want *inventory.FwRuleset", ent)
	}
	if len(rs.Aliases) != 1 || rs.Aliases[0].Name != "management_net" || rs.Aliases[0].CIDR != "192.168.1.0/24" {
		t.Fatalf("Aliases = %+v, want the fixture's one management_net/192.168.1.0/24 alias", rs.Aliases)
	}
}
