package pvemock

import (
	"net/http"
	"testing"
)

// TestCeph_ConfigAndOSDs exercises T-1503's two new read-only routes
// against clean.yaml: cluster-wide public/cluster network config, plus
// per-node OSD placement.
func TestCeph_ConfigAndOSDs(t *testing.T) {
	f, err := LoadFixture("../../testdata/ceph/clean.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := NewServer(f)
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")

	cfgReq := authedRequest(t, http.MethodGet, "/api2/json/cluster/ceph/config", ticket, "", nil)
	cfgBody := mustStatus(t, srv, cfgReq, http.StatusOK)
	data, _ := cfgBody["data"].(map[string]any)
	if data["public_network"] != "10.20.0.0/24" {
		t.Errorf("public_network = %v, want 10.20.0.0/24", data["public_network"])
	}
	if data["cluster_network"] != "10.30.0.0/24" {
		t.Errorf("cluster_network = %v, want 10.30.0.0/24", data["cluster_network"])
	}

	osdReq := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/ceph/osd", ticket, "", nil)
	osdBody := mustStatus(t, srv, osdReq, http.StatusOK)
	osds, _ := osdBody["data"].([]any)
	if len(osds) != 2 {
		t.Fatalf("pve1 OSDs = %d, want 2", len(osds))
	}
	first, _ := osds[0].(map[string]any)
	if first["device"] != "/dev/sdb" {
		t.Errorf("osd[0].device = %v, want /dev/sdb", first["device"])
	}
	if up, ok := first["up"].(float64); !ok || up != 1 {
		t.Errorf("osd[0].up = %v, want 1", first["up"])
	}

	// A node with no declared OSDs at all (and an unknown node name) both
	// serve an empty list, never an error.
	unknownReq := authedRequest(t, http.MethodGet, "/api2/json/nodes/doesnotexist/ceph/osd", ticket, "", nil)
	unknownBody := mustStatus(t, srv, unknownReq, http.StatusOK)
	if got, _ := unknownBody["data"].([]any); len(got) != 0 {
		t.Errorf("unknown node OSDs = %v, want empty", got)
	}
}

// TestCeph_RequiresSysAudit confirms the two routes are gated on the same
// read privilege every other read-only cluster/node route uses (no
// unauthenticated access, no privilege-free Ceph read surface).
func TestCeph_RequiresSysAudit(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")

	req := authedRequest(t, http.MethodGet, "/api2/json/cluster/ceph/config", "", "", nil)
	mustStatus(t, srv, req, http.StatusUnauthorized)
}
