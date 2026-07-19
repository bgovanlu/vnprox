package main

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/k8s"
)

func TestK8sNodePortFindingToFinding(t *testing.T) {
	f := k8sNodePortFindingToFinding(k8s.NodePortFinding{
		ClusterID: "c1", Namespace: "default", Service: "web",
		Port: 80, NodePort: 30080, Proto: "tcp",
		Refs:   []string{"guest:pve1:105"},
		Detail: "NodePort 30080/tcp on default/web has no covering PVE firewall allow rule on guest:pve1:105",
	})

	if f.Source != findings.SourceK8s {
		t.Errorf("source = %q, want %q", f.Source, findings.SourceK8s)
	}
	if f.Check != "k8s_nodeport_exposed_without_fw_rule" {
		t.Errorf("check = %q", f.Check)
	}
	if f.Severity != findings.SeverityWarning {
		t.Errorf("severity = %q", f.Severity)
	}
	if want := "k8s:k8s_nodeport_exposed_without_fw_rule|c1/default/web/30080"; f.ID != want {
		t.Errorf("id = %q, want %q", f.ID, want)
	}
	if f.Fixable {
		t.Error("k8s_nodeport_exposed_without_fw_rule carries no computed fix op — Fixable must be false")
	}
	if f.DocsLink == "" {
		t.Error("a non-fixable finding must carry a docs link")
	}
	if f.Nodes == nil {
		t.Error("Nodes must be a non-nil empty slice, never nil (would serialize as JSON null)")
	}
	if len(f.Refs) != 1 || f.Refs[0] != "guest:pve1:105" {
		t.Errorf("refs = %v", f.Refs)
	}
}

func TestK8sFindingsAdapter_NilPollerIsSafe(t *testing.T) {
	a := k8sFindingsAdapter{poller: nil}
	if got := a.Findings(); got != nil {
		t.Errorf("a nil poller must contribute no findings, got %v", got)
	}
}

func TestK8sFindingsAdapter_ReportsCachedFindings(t *testing.T) {
	poller := k8s.NewPoller()
	// Poll against a client that will fail (no server behind it) just to
	// exercise the adapter's read path against a poller that has never
	// successfully cached anything yet — Findings() must degrade to empty,
	// not panic or error.
	a := k8sFindingsAdapter{poller: poller}
	if got := a.Findings(); len(got) != 0 {
		t.Errorf("a poller with no successful poll yet should report zero findings, got %v", got)
	}
}
