package k8s_test

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/k8s"
	"github.com/bgovanlu/vnprox/internal/k8smock"
)

func loadClusterFixture(t *testing.T, name string) k8smock.Fixture {
	t.Helper()
	f, err := k8smock.LoadFixtureFile("../../testdata/k8s/" + name)
	if err != nil {
		t.Fatalf("LoadFixtureFile(%s): %v", name, err)
	}
	return f
}

func TestClient_NodesPodsServicesDaemonSets(t *testing.T) {
	f := loadClusterFixture(t, "cluster-calico.yaml")
	srv, _ := k8smock.NewServer(f)
	defer srv.Close()

	c := &k8s.Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
	ctx := context.Background()

	nodes, err := c.Nodes(ctx)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("len(nodes) = %d, want 2", len(nodes))
	}

	pods, err := c.Pods(ctx)
	if err != nil {
		t.Fatalf("Pods: %v", err)
	}
	if len(pods) != 1 {
		t.Errorf("len(pods) = %d, want 1", len(pods))
	}

	services, err := c.Services(ctx)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	if len(services) != 1 {
		t.Errorf("len(services) = %d, want 1", len(services))
	}

	daemonsets, err := c.KubeSystemDaemonSets(ctx)
	if err != nil {
		t.Fatalf("KubeSystemDaemonSets: %v", err)
	}
	if len(daemonsets) != 2 {
		t.Errorf("len(daemonsets) = %d, want 2", len(daemonsets))
	}
}

// TestClient_NewClient_EndToEndAgainstMock exercises the production
// kubeconfig -> ResolveContext -> NewClient -> live GET path together
// (not just Client{HTTPClient: ...} constructed directly), pointing a
// resolved kubeconfig at k8smock's plain-HTTP test server (NewClient's TLS
// wiring is inert for a non-https URL, so this validates the wiring code
// path itself without needing a TLS-fronted mock).
func TestClient_NewClient_EndToEndAgainstMock(t *testing.T) {
	f := loadClusterFixture(t, "cluster-flannel.yaml")
	srv, _ := k8smock.NewServer(f)
	defer srv.Close()

	kc, err := k8s.LoadKubeconfigFile("../../testdata/k8s/kubeconfig-token.yaml")
	if err != nil {
		t.Fatalf("LoadKubeconfigFile: %v", err)
	}
	rc, err := k8s.ResolveContext(kc)
	if err != nil {
		t.Fatalf("ResolveContext: %v", err)
	}
	rc.Server = srv.URL // point at the mock instead of the fixture's placeholder

	c, err := k8s.NewClient(rc)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	nodes, err := c.Nodes(context.Background())
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("len(nodes) = %d, want 2", len(nodes))
	}
}

func TestNewClient_NoServer(t *testing.T) {
	if _, err := k8s.NewClient(k8s.ResolvedConfig{}); err != k8s.ErrNoServer {
		t.Errorf("NewClient error = %v, want ErrNoServer", err)
	}
}
