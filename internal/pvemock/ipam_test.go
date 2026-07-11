package pvemock

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestIPAM_CreateDeleteIP exercises T-405's write path: reserve then
// release an address via POST/DELETE /cluster/sdn/vnets/{vnet}/ips, and
// confirm GET .../status reflects each mutation immediately.
func TestIPAM_CreateDeleteIP(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	statusOf := func() []map[string]any {
		req := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/pve/status", ticket, "", nil)
		body := mustStatus(t, srv, req, http.StatusOK)
		data, _ := body["data"].([]any)
		out := make([]map[string]any, 0, len(data))
		for _, d := range data {
			m, _ := d.(map[string]any)
			out = append(out, m)
		}
		return out
	}
	hasIP := func(rows []map[string]any, ip string) bool {
		for _, r := range rows {
			if r["ip"] == ip {
				return true
			}
		}
		return false
	}

	before := statusOf()
	if hasIP(before, "10.100.0.77") {
		t.Fatal("precondition: 10.100.0.77 already allocated")
	}

	createBody, _ := json.Marshal(map[string]string{"ip": "10.100.0.77", "mac": "aa:bb:cc:dd:ee:01", "hostname": "test1", "subnet": "10.100.0.0/24"})
	create := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets/vnet100/ips", ticket, csrf, createBody)
	mustStatus(t, srv, create, http.StatusOK)

	after := statusOf()
	if !hasIP(after, "10.100.0.77") {
		t.Fatalf("10.100.0.77 not present after create: %+v", after)
	}

	// Duplicate create (same subnet+ip) is rejected.
	dupBody, _ := json.Marshal(map[string]string{"ip": "10.100.0.77", "subnet": "10.100.0.0/24"})
	dup := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets/vnet100/ips", ticket, csrf, dupBody)
	mustStatus(t, srv, dup, http.StatusBadRequest)

	deleteBody, _ := json.Marshal(map[string]string{"ip": "10.100.0.77"})
	del := authedRequest(t, http.MethodDelete, "/api2/json/cluster/sdn/vnets/vnet100/ips", ticket, csrf, deleteBody)
	mustStatus(t, srv, del, http.StatusOK)

	final := statusOf()
	if hasIP(final, "10.100.0.77") {
		t.Fatalf("10.100.0.77 still present after delete: %+v", final)
	}

	// Deleting again 404s (not found).
	del2 := authedRequest(t, http.MethodDelete, "/api2/json/cluster/sdn/vnets/vnet100/ips", ticket, csrf, deleteBody)
	mustStatus(t, srv, del2, http.StatusNotFound)
}

// TestIPAM_CreateIP_UnknownVnet confirms a vnet with no resolvable IPAM
// plugin (and more than one configured plugin, so the single-plugin
// fallback doesn't apply) is rejected.
func TestIPAM_CreateIP_UnknownVnet(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	body, _ := json.Marshal(map[string]string{"ip": "10.5.5.5"})
	req := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets/nope/ips", ticket, csrf, body)
	mustStatus(t, srv, req, http.StatusBadRequest)
}

// TestGuestAgentNetworkInterfaces exercises T-405's guest-agent-reported-IP
// read path against the ipam-lab.yaml fixture's scripted agent responses.
func TestGuestAgentNetworkInterfaces(t *testing.T) {
	srv := newTestServer(t, "ipam-lab.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")

	req := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/qemu/300/agent/network-get-interfaces", ticket, "", nil)
	body := mustStatus(t, srv, req, http.StatusOK)
	data, _ := body["data"].(map[string]any)
	result, _ := data["result"].([]any)
	if len(result) != 1 {
		t.Fatalf("result = %+v, want 1 interface", result)
	}
	iface, _ := result[0].(map[string]any)
	if iface["hardware-address"] != "AA:BB:CC:DD:EE:01" {
		t.Errorf("hardware-address = %v, want AA:BB:CC:DD:EE:01", iface["hardware-address"])
	}
	addrs, _ := iface["ip-addresses"].([]any)
	if len(addrs) != 1 {
		t.Fatalf("ip-addresses = %+v, want 1", addrs)
	}
	addr, _ := addrs[0].(map[string]any)
	if addr["ip-address"] != "10.50.0.10" {
		t.Errorf("ip-address = %v, want 10.50.0.10", addr["ip-address"])
	}

	// A guest with no agent_interfaces declared (not exercised by this
	// fixture directly, but lxc has no route at all) 404s cleanly.
	lxcReq := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/qemu/999/agent/network-get-interfaces", ticket, "", nil)
	mustStatus(t, srv, lxcReq, http.StatusNotFound)
}
