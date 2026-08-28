// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestSDNIpam_CRUD exercises full create/read/update/delete for an ipam
// plugin instance, plus the cluster apply endpoint clearing its pending
// marker — the ipam-shaped counterpart of TestSDNFabric_CRUD
// (sdn_fabric_test.go).
func TestSDNIpam_CRUD(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	body, _ := json.Marshal(SDNIpamSpec{ID: "nb1", Type: "netbox", URL: "https://netbox.example.com", Token: "secret-token"})
	create := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/ipams", ticket, csrf, body)
	mustStatus(t, srv, create, http.StatusOK)

	get := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/nb1", ticket, "", nil)
	resp := mustStatus(t, srv, get, http.StatusOK)
	data, _ := resp["data"].(map[string]any)
	if data["pending"] != string(PendingNew) {
		t.Fatalf("new ipam pending = %v, want %q", data["pending"], PendingNew)
	}
	if data["type"] != "netbox" {
		t.Fatalf("type = %v, want netbox", data["type"])
	}
	if _, hasToken := data["token"]; hasToken {
		t.Fatalf("GET response carries a \"token\" key = %v; a read must never echo a configured secret back", data["token"])
	}

	updateBody, _ := json.Marshal(SDNIpamSpec{URL: "https://netbox2.example.com", Token: "secret-token"})
	update := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn/ipams/nb1", ticket, csrf, updateBody)
	mustStatus(t, srv, update, http.StatusOK)

	// Type is not editable on update — sending a different type on the wire
	// must not change the ipam's stored type (mirrors
	// handleSDNFabricUpdate/handleSDNControllerUpdate's own "preserve
	// existing type" behavior).
	// Include url+token so the request stays valid once type is forced back
	// to "netbox" (this handler replaces the whole object per request, like
	// handleSDNFabricUpdate/handleSDNControllerUpdate — a bare {"type":"pve"}
	// body would otherwise 400 on netbox's own required-fields check, which
	// would test the wrong thing here).
	sneakyType, _ := json.Marshal(SDNIpamSpec{Type: "pve", URL: "https://netbox2.example.com", Token: "secret-token"})
	updateSneaky := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn/ipams/nb1", ticket, csrf, sneakyType)
	mustStatus(t, srv, updateSneaky, http.StatusOK)
	getAfterSneaky := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/nb1", ticket, "", nil)
	afterSneaky := mustStatus(t, srv, getAfterSneaky, http.StatusOK)
	sneakyData, _ := afterSneaky["data"].(map[string]any)
	if sneakyData["type"] != "netbox" {
		t.Fatalf("type after update = %v, want netbox (type must not be editable)", sneakyData["type"])
	}

	applyReq := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn", ticket, csrf, nil)
	mustStatus(t, srv, applyReq, http.StatusOK)

	getAfterApply := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/nb1", ticket, "", nil)
	afterApply := mustStatus(t, srv, getAfterApply, http.StatusOK)
	afterData, _ := afterApply["data"].(map[string]any)
	if afterData["pending"] != "" && afterData["pending"] != nil {
		t.Fatalf("ipam pending after apply = %v, want empty", afterData["pending"])
	}

	runningGet := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/nb1?running=1", ticket, "", nil)
	runningResp := mustStatus(t, srv, runningGet, http.StatusOK)
	runningData, _ := runningResp["data"].(map[string]any)
	if runningData["url"] != "https://netbox2.example.com" {
		t.Fatalf("running url after apply = %v, want the applied url", runningData["url"])
	}

	del := authedRequest(t, http.MethodDelete, "/api2/json/cluster/sdn/ipams/nb1", ticket, csrf, nil)
	mustStatus(t, srv, del, http.StatusOK)
	applyReq2 := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn", ticket, csrf, nil)
	mustStatus(t, srv, applyReq2, http.StatusOK)

	getGone := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams/nb1", ticket, "", nil)
	mustStatus(t, srv, getGone, http.StatusNotFound)
}

// TestSDNIpam_TypeConditionalFields pins this task's own documented
// inference of which fields apply to which ipam type (params_sdn_ipam.go's
// doc comment: the capture gives no per-type breakdown for this family,
// unlike fabrics/controllers) — netbox/phpipam require url+token; pve
// accepts neither.
func TestSDNIpam_TypeConditionalFields(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	// url is not valid for a pve-type ipam.
	badBody, _ := json.Marshal(SDNIpamSpec{ID: "pveipam", Type: "pve", URL: "https://example.com"})
	bad := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/ipams", ticket, csrf, badBody)
	mustStatus(t, srv, bad, http.StatusBadRequest)

	// netbox requires both url and token — url alone is rejected.
	missingToken, _ := json.Marshal(SDNIpamSpec{ID: "nbnotoken", Type: "netbox", URL: "https://netbox.example.com"})
	bad2 := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/ipams", ticket, csrf, missingToken)
	mustStatus(t, srv, bad2, http.StatusBadRequest)

	// The matching combination for each type is accepted.
	for _, tc := range []SDNIpamSpec{
		{ID: "pveipam2", Type: "pve"},
		{ID: "nb2", Type: "netbox", URL: "https://netbox.example.com", Token: "tok"},
		{ID: "php2", Type: "phpipam", URL: "https://phpipam.example.com", Token: "tok", Section: 2},
	} {
		t.Run(tc.Type, func(t *testing.T) {
			body, _ := json.Marshal(tc)
			req := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/ipams", ticket, csrf, body)
			mustStatus(t, srv, req, http.StatusOK)
		})
	}

	// An unrecognized type is rejected.
	unknownBody, _ := json.Marshal(SDNIpamSpec{ID: "badtype", Type: "solarwinds"})
	unknown := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/ipams", ticket, csrf, unknownBody)
	mustStatus(t, srv, unknown, http.StatusBadRequest)
}

// TestSDNIpam_IDPattern pins the captured `--ipam` pattern
// ([a-zA-Z][a-zA-Z0-9]*[a-zA-Z0-9] — no underscores or hyphens, unlike a
// controller id).
func TestSDNIpam_IDPattern(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	for _, tc := range []struct {
		name string
		id   string
		want int
	}{
		{"single letter too short", "a", http.StatusBadRequest},
		{"leading digit rejected", "1abc", http.StatusBadRequest},
		{"interior hyphen rejected", "a-b", http.StatusBadRequest},
		{"valid two chars", "ab", http.StatusOK},
		{"valid longer id", "netbox1", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(SDNIpamSpec{ID: tc.id, Type: "pve"})
			req := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/ipams", ticket, csrf, body)
			mustStatus(t, srv, req, tc.want)
		})
	}
}

// TestSDNIpam_ListGrowsWithRealCreates exercises effectiveIpamsLocked
// against a fixture (three-node-vlan.yaml) that already declares its own
// "pve" ipam (testdata/clusters/three-node-vlan.yaml's sdn.ipams section) —
// a real create alongside it must show up as a second, distinct entry, not
// replace or hide the fixture-seeded one.
func TestSDNIpam_ListGrowsWithRealCreates(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	list := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams", ticket, "", nil)
	resp := mustStatus(t, srv, list, http.StatusOK)
	items, _ := resp["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("ipams before any create = %v, want exactly the fixture-seeded \"pve\" entry", items)
	}
	first, _ := items[0].(map[string]any)
	if first["ipam"] != defaultIpamID || first["type"] != "pve" {
		t.Fatalf("fixture-seeded entry = %v, want {ipam: %q, type: pve}", first, defaultIpamID)
	}

	body, _ := json.Marshal(SDNIpamSpec{ID: "nb1", Type: "netbox", URL: "https://netbox.example.com", Token: "tok"})
	create := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/ipams", ticket, csrf, body)
	mustStatus(t, srv, create, http.StatusOK)

	listAfter := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/ipams", ticket, "", nil)
	respAfter := mustStatus(t, srv, listAfter, http.StatusOK)
	itemsAfter, _ := respAfter["data"].([]any)
	if len(itemsAfter) != 2 {
		t.Fatalf("ipams after one real create = %v, want 2 (the fixture-seeded \"pve\" entry plus the new one)", itemsAfter)
	}
}

// TestSDNIpam_DefaultSynthesizedWhenFixtureDeclaresNone pins
// effectiveIpamsLocked's fallback directly at the map level (no fixture in
// this repo's testdata happens to declare zero ipams and also define the
// "netops@pve" user TestSDNIpam_CRUD etc. rely on, so this exercises the
// function itself rather than routing a fixture-driven HTTP request through
// it): with an empty sdn.ipams/ipamsRunning map, both the staged and
// running reads synthesize the built-in "pve" entry (ipam.go's
// defaultIpamID doc comment).
func TestSDNIpam_DefaultSynthesizedWhenFixtureDeclaresNone(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	srv.state.sdn.mu.Lock()
	srv.state.sdn.ipams = map[string]SDNIpamSpec{}
	srv.state.sdn.ipamsRunning = map[string]SDNIpamSpec{}
	srv.state.sdn.mu.Unlock()

	got := srv.effectiveIpams()
	if len(got) != 1 || got[0].ID != defaultIpamID || got[0].Type != "pve" {
		t.Fatalf("effectiveIpams() with an empty map = %+v, want exactly the synthesized {ID: %q, Type: pve}", got, defaultIpamID)
	}
}
