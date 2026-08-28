// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// churnServer boots the mock on the three-node VLAN fixture — the fixture
// docs/development.md requires every feature to work against, and the one
// T-2504's soak run itself drives — already authenticated.
func churnServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")
	return srv, ticket
}

func decodeData[T any](t *testing.T, srv *Server, ticket, path string) T {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, authedRequest(t, http.MethodGet, path, ticket, "", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", path, rec.Code, rec.Body.String())
	}
	var body struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding GET %s: %v (body %s)", path, err, rec.Body.String())
	}
	return body.Data
}

func TestSetNodeOnlineFlapsClusterStatus(t *testing.T) {
	srv, ticket := churnServer(t)
	st := srv.State()

	type entry struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Online int    `json:"online"`
	}
	onlineOf := func(node string) int {
		for _, e := range decodeData[[]entry](t, srv, ticket, "/api2/json/cluster/status") {
			if e.Type == "node" && e.Name == node {
				return e.Online
			}
		}
		t.Fatalf("node %q missing from cluster status", node)
		return -1
	}

	if got := onlineOf("pve2"); got != 1 {
		t.Fatalf("pve2 online = %d before churn, want 1 (the fixture value)", got)
	}
	if !st.SetNodeOnline("pve2", false) {
		t.Fatal("SetNodeOnline reported pve2 is not a cluster member")
	}
	if got := onlineOf("pve2"); got != 0 {
		t.Errorf("pve2 online = %d after flapping offline, want 0", got)
	}
	// The resources view must agree — a member reported online by one
	// endpoint and offline by the other is a mock that teaches the daemon
	// something untrue.
	type resource struct {
		Type   string `json:"type"`
		Node   string `json:"node"`
		Status string `json:"status"`
	}
	for _, r := range decodeData[[]resource](t, srv, ticket, "/api2/json/cluster/resources") {
		if r.Type == "node" && r.Node == "pve2" && r.Status != "offline" {
			t.Errorf("cluster/resources reports pve2 %q, want offline", r.Status)
		}
	}

	st.ClearNodeOnlineOverride("pve2")
	if got := onlineOf("pve2"); got != 1 {
		t.Errorf("pve2 online = %d after clearing the override, want the fixture's 1", got)
	}
	if st.SetNodeOnline("pve9", false) {
		t.Error("SetNodeOnline accepted a node that is not a cluster member")
	}
}

func TestSetAndRemoveGuest(t *testing.T) {
	srv, ticket := churnServer(t)
	st := srv.State()

	type resource struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Node string `json:"node"`
	}
	has := func(id string) bool {
		for _, r := range decodeData[[]resource](t, srv, ticket, "/api2/json/cluster/resources") {
			if r.ID == id {
				return true
			}
		}
		return false
	}

	tests := []struct {
		name string
		kind GuestKind
		vmid string
		id   string
	}{
		{name: "qemu guest", kind: GuestQemu, vmid: "900", id: "qemu/900"},
		{name: "lxc guest", kind: GuestLXC, vmid: "901", id: "lxc/901"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if has(tc.id) {
				t.Fatalf("%s already present before churn", tc.id)
			}
			if !st.SetGuest("pve2", tc.kind, tc.vmid, GuestSpec{
				Name:   "soak-" + tc.vmid,
				Status: "running",
				Config: map[string]string{"net0": "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0"},
			}) {
				t.Fatal("SetGuest reported failure")
			}
			if !has(tc.id) {
				t.Fatalf("%s not visible in cluster/resources after SetGuest", tc.id)
			}
			if !st.RemoveGuest("pve2", tc.vmid) {
				t.Fatal("RemoveGuest reported nothing removed")
			}
			if has(tc.id) {
				t.Fatalf("%s still visible after RemoveGuest", tc.id)
			}
			if st.RemoveGuest("pve2", tc.vmid) {
				t.Error("RemoveGuest reported a second removal of the same guest")
			}
		})
	}

	if st.SetGuest("nosuchnode", GuestQemu, "902", GuestSpec{}) {
		t.Error("SetGuest accepted an unknown node")
	}
	if st.SetGuest("pve2", GuestKind("container"), "903", GuestSpec{}) {
		t.Error("SetGuest accepted an unknown guest kind")
	}
	if st.SetGuest("pve2", GuestQemu, "", GuestSpec{}) {
		t.Error("SetGuest accepted an empty vmid")
	}
}

func TestSetAndRemoveSDNVnet(t *testing.T) {
	srv, ticket := churnServer(t)
	st := srv.State()

	type vnet struct {
		Vnet string `json:"vnet"`
		Zone string `json:"zone"`
	}
	zoneOf := func(path, id string) (string, bool) {
		for _, v := range decodeData[[]vnet](t, srv, ticket, path) {
			if v.Vnet == id {
				return v.Zone, true
			}
		}
		return "", false
	}

	st.SetSDNVnet(SDNVnetSpec{ID: "soakvnet", Zone: "zone-that-does-not-exist", Tag: 4000})
	zone, ok := zoneOf("/api2/json/cluster/sdn/vnets", "soakvnet")
	if !ok {
		t.Fatal("soakvnet missing from the staged view after SetSDNVnet")
	}
	if zone != "zone-that-does-not-exist" {
		t.Errorf("soakvnet zone = %q, want the orphaning zone name", zone)
	}
	if _, ok := zoneOf("/api2/json/cluster/sdn/vnets?running=1", "soakvnet"); !ok {
		t.Error("soakvnet missing from the running view after SetSDNVnet")
	}

	if !st.RemoveSDNVnet("soakvnet") {
		t.Fatal("RemoveSDNVnet reported nothing removed")
	}
	if _, ok := zoneOf("/api2/json/cluster/sdn/vnets", "soakvnet"); ok {
		t.Error("soakvnet still in the staged view after RemoveSDNVnet")
	}
	if _, ok := zoneOf("/api2/json/cluster/sdn/vnets?running=1", "soakvnet"); ok {
		t.Error("soakvnet still in the running view after RemoveSDNVnet")
	}
	if st.RemoveSDNVnet("soakvnet") {
		t.Error("RemoveSDNVnet reported a second removal of the same vnet")
	}
	// The fixture's own vnet must be untouched by all of this.
	if zone, ok := zoneOf("/api2/json/cluster/sdn/vnets", "vnet100"); !ok || zone != "vlanz" {
		t.Errorf("fixture vnet100 zone = %q (present %v), want vlanz", zone, ok)
	}
}
