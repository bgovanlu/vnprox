package pvemock

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
)

// TestServer_OptionsAndAccessors covers the small constructor-time knobs
// (WithLogger, State()) that aren't exercised by any HTTP-level test.
func TestServer_OptionsAndAccessors(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.Default()
	srv := NewServer(f, WithLogger(logger))
	if srv.State() == nil {
		t.Fatalf("State() returned nil")
	}
	if srv.log != logger {
		t.Fatalf("WithLogger did not take effect")
	}
}

// TestSDN_ListAndGetEndpoints sweeps the read-only zone/vnet/subnet list
// and single-item GET endpoints against the three-node-vlan fixture, which
// ships one zone with two vnets/subnets.
func TestSDN_ListAndGetEndpoints(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")

	for _, path := range []string{
		"/api2/json/cluster/sdn/zones",
		"/api2/json/cluster/sdn/vnets",
		"/api2/json/cluster/sdn/vnets/vnet100/subnets",
		"/api2/json/cluster/sdn/vnets/vnet100",
		"/api2/json/cluster/sdn/vnets/vnet100/subnets/10.100.0.0-24",
	} {
		req := authedRequest(t, http.MethodGet, path, ticket, "", nil)
		rec, body := doJSON(t, srv, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, body=%v", path, rec.Code, body)
		}
	}

	// Not-found paths for each single-item getter.
	for _, path := range []string{
		"/api2/json/cluster/sdn/vnets/nope",
		"/api2/json/cluster/sdn/vnets/vnet100/subnets/nope",
	} {
		req := authedRequest(t, http.MethodGet, path, ticket, "", nil)
		rec, _ := doJSON(t, srv, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, rec.Code)
		}
	}
}

// TestSDN_VnetAndSubnetDelete exercises the delete paths for vnets and
// subnets (marks pending=deleted; object remains visible until apply).
func TestSDN_VnetAndSubnetDelete(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	delSubnet := authedRequest(t, http.MethodDelete, "/api2/json/cluster/sdn/vnets/vnet100/subnets/10.100.0.0-24", ticket, csrf, nil)
	rec, body := doJSON(t, srv, delSubnet)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete subnet status = %d", rec.Code)
	}
	_ = body

	getSubnet := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/vnets/vnet100/subnets/10.100.0.0-24", ticket, "", nil)
	rec, body = doJSON(t, srv, getSubnet)
	if rec.Code != http.StatusOK {
		t.Fatalf("get subnet after delete status = %d", rec.Code)
	}
	data, _ := body["data"].(map[string]any)
	if data["pending"] != string(PendingDeleted) {
		t.Fatalf("subnet pending = %v, want %q", data["pending"], PendingDeleted)
	}

	delVnet := authedRequest(t, http.MethodDelete, "/api2/json/cluster/sdn/vnets/vnet200", ticket, csrf, nil)
	if rec, _ := doJSON(t, srv, delVnet); rec.Code != http.StatusOK {
		t.Fatalf("delete vnet status = %d", rec.Code)
	}
	missingVnet := authedRequest(t, http.MethodDelete, "/api2/json/cluster/sdn/vnets/nope", ticket, csrf, nil)
	if rec, _ := doJSON(t, srv, missingVnet); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing vnet status = %d, want 404", rec.Code)
	}

	missingZoneUpdate := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn/zones/nope", ticket, csrf, []byte(`{}`))
	if rec, _ := doJSON(t, srv, missingZoneUpdate); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing zone status = %d, want 404", rec.Code)
	}
	missingZoneDelete := authedRequest(t, http.MethodDelete, "/api2/json/cluster/sdn/zones/nope", ticket, csrf, nil)
	if rec, _ := doJSON(t, srv, missingZoneDelete); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing zone status = %d, want 404", rec.Code)
	}
}

// TestNetworkGet_SingleIface exercises GET /nodes/{node}/network/{iface}.
func TestNetworkGet_SingleIface(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")

	ok := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/network/vmbr0", ticket, "", nil)
	rec, body := doJSON(t, srv, ok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET single iface status = %d", rec.Code)
	}
	data, _ := body["data"].(map[string]any)
	if data["iface"] != "vmbr0" {
		t.Fatalf("iface = %v, want vmbr0", data["iface"])
	}

	missing := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/network/doesnotexist", ticket, "", nil)
	if rec, _ := doJSON(t, srv, missing); rec.Code != http.StatusNotFound {
		t.Fatalf("GET missing iface status = %d, want 404", rec.Code)
	}
}

// TestMockControl_SetNetworkReloadFail exercises the /mock control endpoint
// directly (as opposed to fixture-level or query-param overrides).
func TestMockControl_SetNetworkReloadFail(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	flagBody, _ := json.Marshal(map[string]bool{"fail": true})
	req, err := http.NewRequest(http.MethodPost, "/mock/nodes/pve1/network-reload-fail", bytes.NewReader(flagBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	rec, _ := doJSON(t, srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mock control status = %d", rec.Code)
	}

	reloadReq := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/network", ticket, csrf, nil)
	_, body := doJSON(t, srv, reloadReq)
	upid, _ := body["data"].(string)
	statusReq := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/tasks/"+upid+"/status", ticket, "", nil)
	_, statusBody := doJSON(t, srv, statusReq)
	data, _ := statusBody["data"].(map[string]any)
	exitStatus, _ := data["exitstatus"].(string)
	if exitStatus == "OK" {
		t.Fatalf("expected reload to fail after /mock control flag, got OK")
	}

	missingNode, err := http.NewRequest(http.MethodPost, "/mock/nodes/nope/network-reload-fail", bytes.NewReader(flagBody))
	if err != nil {
		t.Fatal(err)
	}
	missingNode.Header.Set("Content-Type", "application/json")
	rec2, _ := doJSON(t, srv, missingNode)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("mock control for missing node status = %d, want 404", rec2.Code)
	}
}
