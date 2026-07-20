package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pbs"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// fakePBSService returns a canned overlay (or an error) for the /topology
// decoration and GET /pbs route tests.
type fakePBSService struct {
	err     error
	overlay pbs.Overlay
}

func (f fakePBSService) PBSOverlay(context.Context) (pbs.Overlay, error) {
	return f.overlay, f.err
}

func sampleOverlay() pbs.Overlay {
	host := pbs.Host{Ref: pbs.HostRef("10.50.0.9"), Address: "10.50.0.9", Datastores: []string{"main"}, StorageIDs: []string{"pbs-main"}}
	carrier := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	riding := inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}
	return pbs.Overlay{
		Hosts: []pbs.Host{host},
		Paths: []pbs.BackupPath{
			{
				Host: host.Ref, Node: "pve1", Carrier: carrier, RidingOn: riding,
				LinkMbps: 10000, LinkKnown: true,
				StorageIDs: []string{"pbs-main"},
				Jobs:       []pbs.JobSummary{{ID: "backup-daily", Storage: "pbs-main", Schedule: "daily", Guests: 2}},
				SizingHint: "pve1 backs up ... (heuristic estimate).",
			},
			// A path whose egress vnprox could not resolve: no edge should be
			// emitted for it, but the host node still stands.
			{Host: host.Ref, Node: "pve2", SizingHint: "unresolved"},
		},
	}
}

// TestPaintPBS_AddsHostNodeAndBackupEdge is T-1206 AC1/AC2 at the /topology
// decoration seam: a pbs-host node is injected and a backup-path edge is
// drawn from the backing-up node's egress carrier to the host — and no edge
// is drawn for the node whose egress didn't resolve.
func TestPaintPBS_AddsHostNodeAndBackupEdge(t *testing.T) {
	top := &topology.Topology{
		Nodes: []topology.Node{{ID: "bridge:pve1:vmbr0", Kind: "bridge"}},
	}
	paintPBS(context.Background(), top, fakePBSService{overlay: sampleOverlay()})

	var hostNode *topology.Node
	for i := range top.Nodes {
		if top.Nodes[i].ID == "pbs-host::10.50.0.9" {
			hostNode = &top.Nodes[i]
		}
	}
	if hostNode == nil {
		t.Fatalf("no pbs-host node injected into topology; nodes = %+v", top.Nodes)
	}
	if hostNode.Kind != "pbs-host" || hostNode.Label != "10.50.0.9" {
		t.Errorf("pbs-host node = %+v, want kind pbs-host label 10.50.0.9", *hostNode)
	}
	if hostNode.NodeGroup != "" {
		t.Errorf("pbs-host must be cluster-scoped (empty NodeGroup), got %q", hostNode.NodeGroup)
	}

	backupEdges := 0
	for _, e := range top.Edges {
		if e.Kind != "backup-path" {
			continue
		}
		backupEdges++
		if e.From != "bridge:pve1:vmbr0" || e.To != "pbs-host::10.50.0.9" {
			t.Errorf("backup-path edge = %+v, want vmbr0 -> pbs-host", e)
		}
	}
	if backupEdges != 1 {
		t.Fatalf("backup-path edges = %d, want exactly 1 (pve2's unresolved egress draws none)", backupEdges)
	}
}

// TestPaintPBS_ErrorDegradesQuietly proves an overlay-read error leaves the
// topology untouched rather than failing the request.
func TestPaintPBS_ErrorDegradesQuietly(t *testing.T) {
	top := &topology.Topology{Nodes: []topology.Node{{ID: "bridge:pve1:vmbr0"}}}
	before := len(top.Nodes)
	paintPBS(context.Background(), top, fakePBSService{err: context.DeadlineExceeded})
	if len(top.Nodes) != before || len(top.Edges) != 0 {
		t.Errorf("a PBS read error must not mutate the topology; got %d nodes / %d edges", len(top.Nodes), len(top.Edges))
	}
}

// TestHandlePBSStatus_Response is T-1206 AC3's route: GET /pbs serves the
// hosts and per-node sizing hints.
func TestHandlePBSStatus_Response(t *testing.T) {
	rr := httptest.NewRecorder()
	handlePBSStatus(fakePBSService{overlay: sampleOverlay()}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/pbs", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got pbsOverlayResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Hosts) != 1 || got.Hosts[0].Ref != "pbs-host::10.50.0.9" {
		t.Errorf("hosts = %+v, want one pbs-host::10.50.0.9", got.Hosts)
	}
	if len(got.Paths) != 2 {
		t.Fatalf("paths = %d, want 2", len(got.Paths))
	}
	// pve1's resolved path carries its carrier/riding refs; pve2's omits them.
	byNode := map[string]pbsPathResponse{}
	for _, p := range got.Paths {
		byNode[p.Node] = p
	}
	if byNode["pve1"].Carrier != "bridge:pve1:vmbr0" || byNode["pve1"].RidingOn != "bond:pve1:bond0" {
		t.Errorf("pve1 path refs = %+v, want carrier vmbr0 / riding bond0", byNode["pve1"])
	}
	if byNode["pve2"].Carrier != "" {
		t.Errorf("pve2 unresolved egress must omit carrier, got %q", byNode["pve2"].Carrier)
	}
}

// TestHandlePBSStatus_Error surfaces a 500 on an overlay-read error.
func TestHandlePBSStatus_Error(t *testing.T) {
	rr := httptest.NewRecorder()
	handlePBSStatus(fakePBSService{err: context.DeadlineExceeded}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/pbs", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}
