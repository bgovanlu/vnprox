package pvemock

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestSDN_ZoneVnetSubnetCRUD exercises full create/read/update/delete for
// zones, vnets, and subnets, plus the cluster apply endpoint clearing
// pending markers.
func TestSDN_ZoneVnetSubnetCRUD(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	// Create a new zone.
	zoneBody, _ := json.Marshal(SDNZoneSpec{ID: "ztest", Type: "simple", Nodes: []string{"pve1"}})
	createZone := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/zones", ticket, csrf, zoneBody)
	mustStatus(t, srv, createZone, http.StatusOK)

	getZone := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones/ztest", ticket, "", nil)
	body := mustStatus(t, srv, getZone, http.StatusOK)
	data, _ := body["data"].(map[string]any)
	if data["pending"] != string(PendingNew) {
		t.Fatalf("new zone pending = %v, want %q", data["pending"], PendingNew)
	}

	// Create a vnet under it.
	vnetBody, _ := json.Marshal(SDNVnetSpec{ID: "vtest", Zone: "ztest", Tag: 999})
	createVnet := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets", ticket, csrf, vnetBody)
	mustStatus(t, srv, createVnet, http.StatusOK)

	// Vnet under nonexistent zone should fail.
	badVnetBody, _ := json.Marshal(SDNVnetSpec{ID: "vbad", Zone: "nope"})
	badVnet := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets", ticket, csrf, badVnetBody)
	mustStatus(t, srv, badVnet, http.StatusBadRequest)

	// Create a subnet under the vnet.
	subnetBody, _ := json.Marshal(SDNSubnetSpec{ID: "10.99.0.0-24", CIDR: "10.99.0.0/24"})
	createSubnet := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets/vtest/subnets", ticket, csrf, subnetBody)
	mustStatus(t, srv, createSubnet, http.StatusOK)

	listSubnets := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/vnets/vtest/subnets", ticket, "", nil)
	body = mustStatus(t, srv, listSubnets, http.StatusOK)
	subnets, _ := body["data"].([]any)
	if len(subnets) != 1 {
		t.Fatalf("expected 1 subnet, got %d", len(subnets))
	}

	// Update the vnet (should mark it as pending=changed).
	updateBody, _ := json.Marshal(SDNVnetSpec{Zone: "ztest", Tag: 1000})
	updateVnet := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn/vnets/vtest", ticket, csrf, updateBody)
	mustStatus(t, srv, updateVnet, http.StatusOK)

	// Apply cluster-wide: pending markers should clear.
	applyReq := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn", ticket, csrf, nil)
	body = mustStatus(t, srv, applyReq, http.StatusOK)
	upid, _ := body["data"].(string)
	if upid == "" {
		t.Fatalf("expected UPID from sdn apply")
	}

	getZoneAfter := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones/ztest", ticket, "", nil)
	body = mustStatus(t, srv, getZoneAfter, http.StatusOK)
	data, _ = body["data"].(map[string]any)
	if data["pending"] != "" && data["pending"] != nil {
		t.Fatalf("zone pending after apply = %v, want empty", data["pending"])
	}

	// Delete zone -> marked pending=deleted, then apply removes it.
	deleteZone := authedRequest(t, http.MethodDelete, "/api2/json/cluster/sdn/zones/ztest", ticket, csrf, nil)
	mustStatus(t, srv, deleteZone, http.StatusOK)
	applyReq2 := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn", ticket, csrf, nil)
	mustStatus(t, srv, applyReq2, http.StatusOK)

	getZoneGone := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones/ztest", ticket, "", nil)
	mustStatus(t, srv, getZoneGone, http.StatusNotFound)
}

// TestSDN_RunningView exercises T-401's "?running=1" view against
// evpn-lab.yaml's "vlanz" zone, which the fixture stages as pending=changed
// (mtu 1600) with an explicit running override (mtu 1500): the default
// view must show the staged edit, "?running=1" must show the pre-edit
// value, and a create+apply cycle for a fresh object must appear in the
// running view only after the apply (never before).
func TestSDN_RunningView(t *testing.T) {
	srv := newTestServer(t, "evpn-lab.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	staged := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones/vlanz", ticket, "", nil)
	body := mustStatus(t, srv, staged, http.StatusOK)
	data, _ := body["data"].(map[string]any)
	if data["pending"] != string(PendingChanged) {
		t.Fatalf("staged vlanz pending = %v, want changed", data["pending"])
	}
	if mtu, _ := data["mtu"].(float64); mtu != 1600 {
		t.Fatalf("staged vlanz mtu = %v, want 1600", data["mtu"])
	}

	running := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones/vlanz?running=1", ticket, "", nil)
	body = mustStatus(t, srv, running, http.StatusOK)
	data, _ = body["data"].(map[string]any)
	if data["pending"] != "" && data["pending"] != nil {
		t.Fatalf("running vlanz pending = %v, want empty", data["pending"])
	}
	if mtu, _ := data["mtu"].(float64); mtu != 1500 {
		t.Fatalf("running vlanz mtu = %v, want 1500 (the pre-edit value)", data["mtu"])
	}

	// A brand-new object (pending=new) has no running counterpart at all
	// until it's applied.
	zoneBody, _ := json.Marshal(SDNZoneSpec{ID: "ztest2", Type: "simple", Nodes: []string{"pve1"}})
	createZone := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/zones", ticket, csrf, zoneBody)
	mustStatus(t, srv, createZone, http.StatusOK)

	beforeApply := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones/ztest2?running=1", ticket, "", nil)
	mustStatus(t, srv, beforeApply, http.StatusNotFound)

	applyReq := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn", ticket, csrf, nil)
	mustStatus(t, srv, applyReq, http.StatusOK)

	afterApply := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones/ztest2?running=1", ticket, "", nil)
	body = mustStatus(t, srv, afterApply, http.StatusOK)
	data, _ = body["data"].(map[string]any)
	if data["pending"] != "" && data["pending"] != nil {
		t.Fatalf("post-apply running ztest2 pending = %v, want empty", data["pending"])
	}

	// vlanz's own running view should now match its (now-applied) staged
	// view too — the apply synced them.
	runningAfter := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones/vlanz?running=1", ticket, "", nil)
	body = mustStatus(t, srv, runningAfter, http.StatusOK)
	data, _ = body["data"].(map[string]any)
	if mtu, _ := data["mtu"].(float64); mtu != 1600 {
		t.Fatalf("post-apply running vlanz mtu = %v, want 1600 (now in sync with staged)", data["mtu"])
	}
}

// TestSDN_ReadOnlyUserCannotAllocate proves SDN.Audit alone does not confer
// SDN.Allocate.
func TestSDN_ReadOnlyUserCannotAllocate(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "auditor@pve", "readonly")

	body, _ := json.Marshal(SDNZoneSpec{ID: "nope", Type: "simple"})
	req := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/zones", ticket, csrf, body)
	mustStatus(t, srv, req, http.StatusForbidden)
}

// --- T-701 acceptance criterion 4: pvemock stops being more permissive
// than real PVE for the two subnet shapes real PVE rejects at stage time.

func TestSDN_SubnetCreateRejectsSNATWithoutGateway(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	body, _ := json.Marshal(SDNSubnetSpec{ID: "10.99.0.0-24", CIDR: "10.99.0.0/24", SNAT: true})
	req := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets/vnet100/subnets", ticket, csrf, body)
	mustStatus(t, srv, req, http.StatusBadRequest)
}

func TestSDN_SubnetCreateRejectsGatewayOutsideCIDR(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	body, _ := json.Marshal(SDNSubnetSpec{ID: "10.99.0.0-24", CIDR: "10.99.0.0/24", Gateway: "10.1.2.3"})
	req := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets/vnet100/subnets", ticket, csrf, body)
	mustStatus(t, srv, req, http.StatusBadRequest)
}

func TestSDN_ZoneCreateRejectsInvalidName(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	// A hyphenated zone id is outside PVE's SDN id charset — real PVE
	// rejects it at create with "Parameter verification failed" (issue #3).
	body, _ := json.Marshal(SDNZoneSpec{ID: "bad-zone", Type: "simple"})
	req := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/zones", ticket, csrf, body)
	mustStatus(t, srv, req, http.StatusBadRequest)
}

func TestSDN_VnetCreateRejectsInvalidName(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	// vlanz is an existing zone in three-node-vlan.yaml; the underscore in
	// the vnet id is what PVE rejects.
	body, _ := json.Marshal(SDNVnetSpec{ID: "bad_vnet", Zone: "vlanz"})
	req := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets", ticket, csrf, body)
	mustStatus(t, srv, req, http.StatusBadRequest)
}

func TestSDN_SubnetUpdateRejectsSNATWithoutGateway(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	// vnet100's existing subnet (10.100.0.0/24, gateway 10.100.0.1 — see
	// three-node-vlan.yaml) updated to drop its gateway while keeping snat
	// on should be rejected exactly like a create would be.
	body, _ := json.Marshal(SDNSubnetSpec{CIDR: "10.100.0.0/24", SNAT: true})
	req := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn/vnets/vnet100/subnets/10.100.0.0-24", ticket, csrf, body)
	mustStatus(t, srv, req, http.StatusBadRequest)
}

func TestSDN_SubnetCreateRegistersGatewayIPAMRecord(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	body, _ := json.Marshal(SDNSubnetSpec{ID: "10.99.0.0-24", CIDR: "10.99.0.0/24", Gateway: "10.99.0.1"})
	createSubnet := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets/vnet100/subnets", ticket, csrf, body)
	mustStatus(t, srv, createSubnet, http.StatusOK)

	statusReq := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/pve/status", ticket, "", nil)
	statusBody := mustStatus(t, srv, statusReq, http.StatusOK)
	entries, _ := statusBody["data"].([]any)
	found := false
	for _, raw := range entries {
		e, _ := raw.(map[string]any)
		if e["subnet"] == "10.99.0.0/24" && e["ip"] == "10.99.0.1" && e["gateway"] == float64(1) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no gateway:true ipam entry for 10.99.0.1 in %+v", entries)
	}

	// Updating the subnet to a different gateway refreshes (not
	// duplicates) the record.
	updateBody, _ := json.Marshal(SDNSubnetSpec{CIDR: "10.99.0.0/24", Gateway: "10.99.0.2"})
	updateSubnet := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn/vnets/vnet100/subnets/10.99.0.0-24", ticket, csrf, updateBody)
	mustStatus(t, srv, updateSubnet, http.StatusOK)

	statusReq2 := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/pve/status", ticket, "", nil)
	statusBody2 := mustStatus(t, srv, statusReq2, http.StatusOK)
	entries2, _ := statusBody2["data"].([]any)
	gatewayCount := 0
	for _, raw := range entries2 {
		e, _ := raw.(map[string]any)
		if e["subnet"] == "10.99.0.0/24" && e["gateway"] == float64(1) {
			gatewayCount++
			if e["ip"] != "10.99.0.2" {
				t.Errorf("gateway entry ip = %v, want refreshed 10.99.0.2", e["ip"])
			}
		}
	}
	if gatewayCount != 1 {
		t.Fatalf("got %d gateway entries for 10.99.0.0/24 after update, want exactly 1: %+v", gatewayCount, entries2)
	}
}

// TestSDN_SubnetCreateRegistersGatewayIPAMRecord_NoConfiguredIPAM proves the
// synthesized default-"pve"-IPAM fallback (ipam.go's defaultIpamID doc
// comment): single-node.yaml declares no sdn.ipams at all, yet a full
// zone/vnet/subnet chain still ends up with a readable gateway record.
func TestSDN_SubnetCreateRegistersGatewayIPAMRecord_NoConfiguredIPAM(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	zoneBody, _ := json.Marshal(SDNZoneSpec{ID: "homelab", Type: "simple", Nodes: []string{"pve1"}})
	mustStatus(t, srv, authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/zones", ticket, csrf, zoneBody), http.StatusOK)

	vnetBody, _ := json.Marshal(SDNVnetSpec{ID: "vnet1", Zone: "homelab"})
	mustStatus(t, srv, authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets", ticket, csrf, vnetBody), http.StatusOK)

	subnetBody, _ := json.Marshal(SDNSubnetSpec{ID: "10.50.0.0-24", CIDR: "10.50.0.0/24", Gateway: "10.50.0.1"})
	mustStatus(t, srv, authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets/vnet1/subnets", ticket, csrf, subnetBody), http.StatusOK)

	statusReq := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/pve/status", ticket, "", nil)
	statusBody := mustStatus(t, srv, statusReq, http.StatusOK)
	entries, _ := statusBody["data"].([]any)
	found := false
	for _, raw := range entries {
		e, _ := raw.(map[string]any)
		if e["subnet"] == "10.50.0.0/24" && e["ip"] == "10.50.0.1" && e["gateway"] == float64(1) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no gateway:true ipam entry for 10.50.0.1 in %+v", entries)
	}
}

// TestSDN_StatusListsAllObjects exercises GET /cluster/sdn (the flattened
// pending-vs-applied tree).
func TestSDN_StatusListsAllObjects(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")
	req := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn", ticket, "", nil)
	body := mustStatus(t, srv, req, http.StatusOK)
	entries, _ := body["data"].([]any)
	if len(entries) == 0 {
		t.Fatalf("expected sdn status entries for three-node-vlan fixture")
	}
}
