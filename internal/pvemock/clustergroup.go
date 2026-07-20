package pvemock

import (
	"fmt"
	"net/http/httptest"
)

// clustergroup.go (T-1201) lets a single test process run N independently-
// addressed mock PVE clusters at once — distinct Servers, distinct fixtures,
// distinct httptest listeners — so internal/federation's Aggregator can be
// exercised against several simulated clusters, with one made unreachable
// mid-test (MockCluster.Close). It is the multi-cluster analogue of the
// single-server httptest.NewServer(pvemock.NewServer(f)) pattern every
// existing pve/federation test already uses, factored out so the fan-out
// tests don't each hand-roll a slice of servers.
//
// This lives in the (non-_test) package on purpose: internal/federation's
// own tests import it, exactly as they import LoadFixture/NewServer.

// MockClusterSpec names one cluster in a group: a logical Name (the id the
// test tags aggregated results by) and the testdata/clusters/*.yaml fixture
// backing it.
type MockClusterSpec struct {
	Name        string
	FixturePath string
}

// MockCluster is one running mock cluster within a ClusterGroup.
type MockCluster struct {
	server  *httptest.Server
	Fixture *Fixture
	Name    string
	// URL is the base URL of this cluster's mock PVE API listener, suitable
	// as a pve.Config.APIURL.
	URL string
}

// Close stops just this one cluster's listener, simulating an
// unreachable/partitioned cluster mid-aggregation (federation failure
// isolation, T-1201 AC3). Idempotent.
func (c *MockCluster) Close() {
	if c.server != nil {
		c.server.Close()
		c.server = nil
	}
}

// ClusterGroup is a set of running MockClusters sharing one test process.
type ClusterGroup struct {
	byName   map[string]*MockCluster
	Clusters []*MockCluster
}

// StartClusterGroup loads each spec's fixture and starts an httptest server
// for it, returning the running group. On any load/start failure it closes
// whatever it already started and returns the error, so a caller never leaks
// a half-started group. Every spec's Name must be unique.
func StartClusterGroup(specs []MockClusterSpec, opts ...Option) (*ClusterGroup, error) {
	g := &ClusterGroup{byName: make(map[string]*MockCluster, len(specs))}
	for _, spec := range specs {
		if spec.Name == "" {
			g.Close()
			return nil, fmt.Errorf("pvemock: cluster spec has empty Name")
		}
		if _, dup := g.byName[spec.Name]; dup {
			g.Close()
			return nil, fmt.Errorf("pvemock: duplicate cluster name %q in group", spec.Name)
		}
		f, err := LoadFixture(spec.FixturePath)
		if err != nil {
			g.Close()
			return nil, fmt.Errorf("pvemock: loading fixture for cluster %q: %w", spec.Name, err)
		}
		srv := httptest.NewServer(NewServer(f, opts...))
		mc := &MockCluster{server: srv, Fixture: f, Name: spec.Name, URL: srv.URL}
		g.byName[spec.Name] = mc
		g.Clusters = append(g.Clusters, mc)
	}
	return g, nil
}

// ByName returns the running cluster with the given name, or nil.
func (g *ClusterGroup) ByName(name string) *MockCluster { return g.byName[name] }

// Close stops every cluster's listener. Idempotent; safe to defer.
func (g *ClusterGroup) Close() {
	for _, c := range g.Clusters {
		c.Close()
	}
}
