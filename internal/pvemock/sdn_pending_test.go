// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"encoding/json"
	"net/http"
	"testing"
)

// sdn_pending_test.go pins T-3101-followup-01's mock addition: real PVE
// 9.2.4's "?pending=1" view (planning/reports/evidence/
// pve-9.2.4-sdn-pending-state.txt, read directly from PVE's own perl
// source and confirmed live against pvecube) — a THIRD view alongside the
// default (staged) view and "?running=1" (T-401), distinct from both:
//   - an in-sync object carries no "state"/"pending" keys at all (confirmed
//     live: pvecube's synced "labz" zone under --pending 1 has neither).
//   - a staged-but-unapplied object gets a top-level "state"
//     ("new"|"changed"|"deleted") plus a "pending" object of its field
//     values.
//
// This is the mechanism internal/change's foreign-pending detection
// (apply_sdn_foreign.go) is built on: it is the ONLY view real PVE exposes
// that distinguishes "staged" from "in sync" at all (the plain default
// view — confirmed against pvecube — carries no such marker whatsoever).

func TestSDNPending_ZoneNewShowsStateAndFields(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	// A brand-new zone, staged but never applied — modeling exactly what a
	// foreign (GUI-staged) edit looks like from PVE's own point of view:
	// PVE tracks no "who staged this" attribution at all (the evidence
	// file's has_pending_changes() finding), so a direct create through
	// this same HTTP API IS what a foreign edit looks like on the wire.
	zoneBody, _ := json.Marshal(SDNZoneSpec{ID: "pendz", Type: "simple", Nodes: []string{"pve1"}})
	create := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/zones", ticket, csrf, zoneBody)
	mustStatus(t, srv, create, http.StatusOK)

	// The plain default view carries no state/pending marker at all — this
	// is the real-PVE behaviour the evidence file's §1 documents (contrary
	// to internal/pve.SDNZone.Pending's own assumption, which this task's
	// fix deliberately does not rely on).
	def := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones", ticket, "", nil)
	body := mustStatus(t, srv, def, http.StatusOK)
	list, _ := body["data"].([]any)
	found := false
	for _, raw := range list {
		obj, _ := raw.(map[string]any)
		if obj["zone"] != "pendz" {
			continue
		}
		found = true
		if _, hasState := obj["state"]; hasState {
			t.Fatalf("default view carries a 'state' key for pendz: %+v (real PVE's default view never does)", obj)
		}
	}
	if !found {
		t.Fatalf("pendz missing from default zones list")
	}

	// The "?pending=1" view: state=new, and a "pending" sub-object.
	pending := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones?pending=1", ticket, "", nil)
	body = mustStatus(t, srv, pending, http.StatusOK)
	list, _ = body["data"].([]any)
	found = false
	for _, raw := range list {
		obj, _ := raw.(map[string]any)
		if obj["zone"] != "pendz" {
			continue
		}
		found = true
		if obj["state"] != "new" {
			t.Fatalf("pendz state = %v, want %q", obj["state"], "new")
		}
		fields, ok := obj["pending"].(map[string]any)
		if !ok {
			t.Fatalf("pendz has no 'pending' fields object: %+v", obj)
		}
		if fields["type"] != "simple" {
			t.Fatalf("pendz pending.type = %v, want %q", fields["type"], "simple")
		}
	}
	if !found {
		t.Fatalf("pendz missing from ?pending=1 zones list")
	}
}

func TestSDNPending_InSyncObjectCarriesNoStateOrFields(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, _ := login(t, srv, "netops@pve", "netops")

	// three-node-vlan.yaml's fixture zones are already applied (Pending
	// "") — confirming the "in sync" half of real PVE's behaviour, exactly
	// mirroring what pvesh get /cluster/sdn/zones --pending 1 showed live
	// against pvecube's synced "labz" zone (no state/pending keys).
	pending := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones?pending=1", ticket, "", nil)
	body := mustStatus(t, srv, pending, http.StatusOK)
	list, _ := body["data"].([]any)
	if len(list) == 0 {
		t.Fatalf("expected at least one zone in the fixture")
	}
	for _, raw := range list {
		obj, _ := raw.(map[string]any)
		if _, hasState := obj["state"]; hasState {
			t.Fatalf("in-sync zone %v carries a 'state' key: %+v", obj["zone"], obj)
		}
		if _, hasPending := obj["pending"]; hasPending {
			t.Fatalf("in-sync zone %v carries a 'pending' key: %+v", obj["zone"], obj)
		}
	}
}

func TestSDNPending_ApplyClearsState(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	zoneBody, _ := json.Marshal(SDNZoneSpec{ID: "pendz2", Type: "simple", Nodes: []string{"pve1"}})
	create := authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/zones", ticket, csrf, zoneBody)
	mustStatus(t, srv, create, http.StatusOK)

	applyReq := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn", ticket, csrf, nil)
	mustStatus(t, srv, applyReq, http.StatusOK)

	pending := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/zones?pending=1", ticket, "", nil)
	body := mustStatus(t, srv, pending, http.StatusOK)
	list, _ := body["data"].([]any)
	for _, raw := range list {
		obj, _ := raw.(map[string]any)
		if obj["zone"] != "pendz2" {
			continue
		}
		if _, hasState := obj["state"]; hasState {
			t.Fatalf("pendz2 still carries 'state' after apply: %+v", obj)
		}
	}
}

// TestSDNPending_VnetAndSubnet exercises the same "?pending=1" mechanism
// for vnets and subnets (T-3101-followup-01's fix reads all three
// families) — a single, smaller check that the vnet/subnet endpoints wire
// the same sdnObjectPendingWire helper the zone tests above exercise more
// thoroughly.
func TestSDNPending_VnetAndSubnet(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	zoneBody, _ := json.Marshal(SDNZoneSpec{ID: "pzone", Type: "simple", Nodes: []string{"pve1"}})
	mustStatus(t, srv, authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/zones", ticket, csrf, zoneBody), http.StatusOK)

	vnetBody, _ := json.Marshal(SDNVnetSpec{ID: "pvnet", Zone: "pzone"})
	mustStatus(t, srv, authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets", ticket, csrf, vnetBody), http.StatusOK)

	subnetBody, _ := json.Marshal(SDNSubnetSpec{ID: "10.77.0.0-24", CIDR: "10.77.0.0/24"})
	mustStatus(t, srv, authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/vnets/pvnet/subnets", ticket, csrf, subnetBody), http.StatusOK)

	vnetPending := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/vnets?pending=1", ticket, "", nil)
	body := mustStatus(t, srv, vnetPending, http.StatusOK)
	list, _ := body["data"].([]any)
	var sawVnet bool
	for _, raw := range list {
		obj, _ := raw.(map[string]any)
		if obj["vnet"] == "pvnet" && obj["state"] == "new" {
			sawVnet = true
		}
	}
	if !sawVnet {
		t.Fatalf("pvnet not reported as state=new under ?pending=1: %+v", list)
	}

	subnetPending := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/vnets/pvnet/subnets?pending=1", ticket, "", nil)
	body = mustStatus(t, srv, subnetPending, http.StatusOK)
	list, _ = body["data"].([]any)
	var sawSubnet bool
	for _, raw := range list {
		obj, _ := raw.(map[string]any)
		if obj["subnet"] == "10.77.0.0-24" && obj["state"] == "new" {
			sawSubnet = true
		}
	}
	if !sawSubnet {
		t.Fatalf("subnet not reported as state=new under ?pending=1: %+v", list)
	}
}

// TestSDNPending_ControllerAndFabric (debt-sweep 2026-08-19,
// "SDNController.Pending and SDNFabric.Pending have the same gap [as
// SDNZone.Pending]") exercises the same "?pending=1" mechanism for
// controllers and fabrics — confirmed against pvecube's own perl source
// (planning/reports/evidence/pve-9.2.4-sdn-pending-state.txt §6) to be the
// exact same pending_config() mechanism the zone/vnet/subnet tests above
// already pin, not merely assumed from that precedent.
func TestSDNPending_ControllerAndFabric(t *testing.T) {
	srv := newTestServer(t, "three-node-vlan.yaml")
	ticket, csrf := login(t, srv, "netops@pve", "netops")

	// faucet needs no type-conditional fields (sdnControllerTypeFields's
	// empty entry) — the smallest valid controller body.
	ctlBody, _ := json.Marshal(SDNControllerSpec{ID: "pctl", Type: "faucet"})
	mustStatus(t, srv, authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/controllers", ticket, csrf, ctlBody), http.StatusOK)

	// bgp needs no required conditional fields either (only "redistribute"
	// is bgp-allowed, and it's optional) — the smallest valid fabric body.
	fabBody, _ := json.Marshal(SDNFabricSpec{ID: "pfab", Protocol: "bgp"})
	mustStatus(t, srv, authedRequest(t, http.MethodPost, "/api2/json/cluster/sdn/fabrics/fabric", ticket, csrf, fabBody), http.StatusOK)

	// The plain default view carries no state/pending marker — same
	// real-PVE behaviour the zone test above pins, now checked for
	// controllers/fabrics too.
	defCtl := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/controllers", ticket, "", nil)
	body := mustStatus(t, srv, defCtl, http.StatusOK)
	for _, raw := range body["data"].([]any) {
		obj, _ := raw.(map[string]any)
		if obj["controller"] == "pctl" {
			if _, hasState := obj["state"]; hasState {
				t.Fatalf("default controllers view carries a 'state' key for pctl: %+v", obj)
			}
		}
	}

	ctlPending := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/controllers?pending=1", ticket, "", nil)
	body = mustStatus(t, srv, ctlPending, http.StatusOK)
	list, _ := body["data"].([]any)
	var sawCtl bool
	for _, raw := range list {
		obj, _ := raw.(map[string]any)
		if obj["controller"] != "pctl" {
			continue
		}
		if obj["state"] != "new" {
			t.Fatalf("pctl state = %v, want %q", obj["state"], "new")
		}
		fields, ok := obj["pending"].(map[string]any)
		if !ok {
			t.Fatalf("pctl has no 'pending' fields object: %+v", obj)
		}
		if fields["type"] != "faucet" {
			t.Fatalf("pctl pending.type = %v, want %q", fields["type"], "faucet")
		}
		sawCtl = true
	}
	if !sawCtl {
		t.Fatalf("pctl missing from ?pending=1 controllers list: %+v", list)
	}

	fabPending := authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/fabrics/fabric?pending=1", ticket, "", nil)
	body = mustStatus(t, srv, fabPending, http.StatusOK)
	list, _ = body["data"].([]any)
	var sawFab bool
	for _, raw := range list {
		obj, _ := raw.(map[string]any)
		if obj["id"] != "pfab" {
			continue
		}
		if obj["state"] != "new" {
			t.Fatalf("pfab state = %v, want %q", obj["state"], "new")
		}
		fields, ok := obj["pending"].(map[string]any)
		if !ok {
			t.Fatalf("pfab has no 'pending' fields object: %+v", obj)
		}
		if fields["protocol"] != "bgp" {
			t.Fatalf("pfab pending.protocol = %v, want %q", fields["protocol"], "bgp")
		}
		sawFab = true
	}
	if !sawFab {
		t.Fatalf("pfab missing from ?pending=1 fabrics list: %+v", list)
	}

	// Apply clears both — mirroring TestSDNPending_ApplyClearsState.
	applyReq := authedRequest(t, http.MethodPut, "/api2/json/cluster/sdn", ticket, csrf, nil)
	mustStatus(t, srv, applyReq, http.StatusOK)

	ctlPending = authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/controllers?pending=1", ticket, "", nil)
	body = mustStatus(t, srv, ctlPending, http.StatusOK)
	for _, raw := range body["data"].([]any) {
		obj, _ := raw.(map[string]any)
		if obj["controller"] == "pctl" {
			if _, hasState := obj["state"]; hasState {
				t.Fatalf("pctl still carries 'state' after apply: %+v", obj)
			}
		}
	}
	fabPending = authedRequest(t, http.MethodGet, "/api2/json/cluster/sdn/fabrics/fabric?pending=1", ticket, "", nil)
	body = mustStatus(t, srv, fabPending, http.StatusOK)
	for _, raw := range body["data"].([]any) {
		obj, _ := raw.(map[string]any)
		if obj["id"] == "pfab" {
			if _, hasState := obj["state"]; hasState {
				t.Fatalf("pfab still carries 'state' after apply: %+v", obj)
			}
		}
	}
}
