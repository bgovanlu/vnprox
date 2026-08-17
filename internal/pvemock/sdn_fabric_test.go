package pvemock

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestSDNFabric_CRUD exercises full create/read/update/delete for a fabric,
// plus the cluster apply endpoint clearing its pending marker — the
// fabric-shaped counterpart of TestSDN_ZoneVnetSubnetCRUD (sdn_test.go).
func TestSDNFabric_CRUD(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	body, _ := json.Marshal(SDNFabricSpec{ID: "fab1", Protocol: "ospf", Area: "0.0.0.0", Redistribute: []string{"connected"}})
	create := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/fabrics/fabric", ticket, csrf, body)
	mustStatus(t, srv, create, http.StatusOK)

	get := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/fabrics/fabric/fab1", ticket, "", nil)
	resp := mustStatus(t, srv, get, http.StatusOK)
	data, _ := resp["data"].(map[string]any)
	if data["pending"] != string(PendingNew) {
		t.Fatalf("new fabric pending = %v, want %q", data["pending"], PendingNew)
	}
	if data["protocol"] != "ospf" {
		t.Fatalf("protocol = %v, want ospf", data["protocol"])
	}

	updateBody, _ := json.Marshal(SDNFabricSpec{Area: "0.0.0.1", Redistribute: []string{"connected"}})
	update := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn/fabrics/fabric/fab1", ticket, csrf, updateBody)
	mustStatus(t, srv, update, http.StatusOK)

	// Protocol is not editable on update — sending a different protocol on
	// the wire must not change the fabric's stored protocol (mirrors
	// handleSDNFabricUpdate's own "preserve existing protocol" behavior).
	sneakyProtocol, _ := json.Marshal(SDNFabricSpec{Protocol: "bgp", Redistribute: []string{"connected"}})
	updateSneaky := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn/fabrics/fabric/fab1", ticket, csrf, sneakyProtocol)
	mustStatus(t, srv, updateSneaky, http.StatusOK)
	getAfterSneaky := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/fabrics/fabric/fab1", ticket, "", nil)
	afterSneaky := mustStatus(t, srv, getAfterSneaky, http.StatusOK)
	sneakyData, _ := afterSneaky["data"].(map[string]any)
	if sneakyData["protocol"] != "ospf" {
		t.Fatalf("protocol after update = %v, want ospf (protocol must not be editable)", sneakyData["protocol"])
	}

	applyReq := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn", ticket, csrf, nil)
	mustStatus(t, srv, applyReq, http.StatusOK)

	getAfterApply := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/fabrics/fabric/fab1", ticket, "", nil)
	afterApply := mustStatus(t, srv, getAfterApply, http.StatusOK)
	afterData, _ := afterApply["data"].(map[string]any)
	if afterData["pending"] != "" && afterData["pending"] != nil {
		t.Fatalf("fabric pending after apply = %v, want empty", afterData["pending"])
	}

	del := authedRequest(t, http.MethodDelete, "/api2/json/cluster/sdn/fabrics/fabric/fab1", ticket, csrf, nil)
	mustStatus(t, srv, del, http.StatusOK)
	applyReq2 := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn", ticket, csrf, nil)
	mustStatus(t, srv, applyReq2, http.StatusOK)

	getGone := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/fabrics/fabric/fab1", ticket, "", nil)
	mustStatus(t, srv, getGone, http.StatusNotFound)
}

// TestSDNFabric_IDPattern pins the captured `--id` pattern (2-8 chars,
// alphanumeric with interior hyphens) — both a too-short and a too-long id
// must be rejected, and a valid hyphenated id must be accepted.
func TestSDNFabric_IDPattern(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	for _, tc := range []struct {
		name string
		id   string
		want int
	}{
		{"single char too short", "a", http.StatusBadRequest},
		{"nine chars too long", "abcdefghi", http.StatusBadRequest},
		{"valid with interior hyphen", "fab-01", http.StatusOK},
		{"valid two chars", "f1", http.StatusOK},
		{"valid eight chars", "fabricid", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(SDNFabricSpec{ID: tc.id, Protocol: "bgp", Redistribute: []string{"connected"}})
			req := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/fabrics/fabric", ticket, csrf, body)
			mustStatus(t, srv, req, tc.want)
		})
	}
}

// TestSDNFabric_ProtocolConditionalFields pins the conditional-per-protocol
// schema the capture's "Conditional options:" blocks describe: a field that
// belongs to one protocol is rejected on a fabric of a different protocol.
func TestSDNFabric_ProtocolConditionalFields(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	// csnp_interval is openfabric-only; rejected on a bgp fabric.
	badBody, _ := json.Marshal(SDNFabricSpec{ID: "bgpfab", Protocol: "bgp", CSNPInterval: 30})
	bad := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/fabrics/fabric", ticket, csrf, badBody)
	mustStatus(t, srv, bad, http.StatusBadRequest)

	// persistent_keepalive is wireguard-only; rejected on an ospf fabric.
	badBody2, _ := json.Marshal(SDNFabricSpec{ID: "ospffab", Protocol: "ospf", PersistentKeepalive: 25})
	bad2 := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/fabrics/fabric", ticket, csrf, badBody2)
	mustStatus(t, srv, bad2, http.StatusBadRequest)

	// The matching combination for each protocol is accepted.
	for _, tc := range []SDNFabricSpec{
		{ID: "bgp1", Protocol: "bgp", Redistribute: []string{"connected"}},
		{ID: "ofab1", Protocol: "openfabric", CSNPInterval: 30, HelloInterval: 5, RouteFilter: "pl1"},
		{ID: "ospf1", Protocol: "ospf", Area: "0.0.0.0", Redistribute: []string{"static"}, RouteFilter: "pl1"},
		{ID: "wg1", Protocol: "wireguard", PersistentKeepalive: 25},
	} {
		t.Run(tc.Protocol, func(t *testing.T) {
			body, _ := json.Marshal(tc)
			req := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/fabrics/fabric", ticket, csrf, body)
			mustStatus(t, srv, req, http.StatusOK)
		})
	}

	// An unrecognized protocol is rejected.
	unknownBody, _ := json.Marshal(SDNFabricSpec{ID: "badproto", Protocol: "rip"})
	unknown := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/fabrics/fabric", ticket, csrf, unknownBody)
	mustStatus(t, srv, unknown, http.StatusBadRequest)

	// Out-of-range csnp_interval (>600) is rejected even for the right protocol.
	outOfRangeBody, _ := json.Marshal(SDNFabricSpec{ID: "ofab2", Protocol: "openfabric", CSNPInterval: 900})
	outOfRange := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/fabrics/fabric", ticket, csrf, outOfRangeBody)
	mustStatus(t, srv, outOfRange, http.StatusBadRequest)
}

// TestSDNFabric_AllAndNode exercises GET /cluster/sdn/fabrics/all (the
// combined fabrics+nodes read) and GET /cluster/sdn/fabrics/node
// (fixture-seeded per-node membership, its own collection — not inferred
// from the fabric list).
func TestSDNFabric_AllAndNode(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	// Empty state: both keys present as empty arrays, matching the capture's
	// {"fabrics":[],"nodes":[]} shape.
	all := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/fabrics/all", ticket, "", nil)
	allResp := mustStatus(t, srv, all, http.StatusOK)
	allData, _ := allResp["data"].(map[string]any)
	if allData["fabrics"] == nil || allData["nodes"] == nil {
		t.Fatalf("GET .../fabrics/all = %v, want both \"fabrics\" and \"nodes\" as arrays (not null)", allResp)
	}
	if fabrics, _ := allData["fabrics"].([]any); len(fabrics) != 0 {
		t.Fatalf("fabrics = %v, want empty before any create", fabrics)
	}

	body, _ := json.Marshal(SDNFabricSpec{ID: "fab1", Protocol: "bgp", Redistribute: []string{"connected"}})
	create := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/fabrics/fabric", ticket, csrf, body)
	mustStatus(t, srv, create, http.StatusOK)

	allAfter := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/fabrics/all", ticket, "", nil)
	allAfterResp := mustStatus(t, srv, allAfter, http.StatusOK)
	allAfterData, _ := allAfterResp["data"].(map[string]any)
	fabricsAfter, _ := allAfterData["fabrics"].([]any)
	if len(fabricsAfter) != 1 {
		t.Fatalf("fabrics after create = %v, want 1 entry", fabricsAfter)
	}

	nodeReq := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/fabrics/node", ticket, "", nil)
	nodeResp := mustStatus(t, srv, nodeReq, http.StatusOK)
	nodes, _ := nodeResp["data"].([]any)
	// three-node-vlan.yaml declares no fabric_nodes fixture entries — an
	// explicit empty-but-present array, never null, proves the route is a
	// real (if currently empty) collection rather than an error masquerading
	// as one.
	if nodes == nil {
		t.Fatalf("GET .../fabrics/node data = %v, want an array (possibly empty), not null", nodeResp)
	}
}

// TestSDNPrefixListsAndRouteMapsAreReadOnly pins T-3101's explicit scoping:
// prefix-lists and route-maps are readable and displayed, but carry no
// write path at all. GET succeeds; every mutating method against either
// path fails (no route is registered for it — chi answers 405 for a known
// path with an unregistered method).
func TestSDNPrefixListsAndRouteMapsAreReadOnly(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	for _, path := range []string{
		"/api2/json/cluster/sdn/prefix-lists",
		"/api2/json/cluster/sdn/route-maps",
	} {
		t.Run(path, func(t *testing.T) {
			get := authedRequest(t, http.MethodGet, path, ticket, "", nil)
			mustStatus(t, srv, get, http.StatusOK)

			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
				req := authedRequest(t, method, path, ticket, csrf, []byte(`{"name":"pl1"}`))
				rec, body := doJSON(t, srv, req)
				if rec.Code == http.StatusOK {
					t.Fatalf("%s %s: status = 200, want a failure — this family is read-only, no write handler must exist (body=%v)", method, path, body)
				}
			}
		})
	}
}
