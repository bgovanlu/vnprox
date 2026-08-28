// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"encoding/json"
	"testing"
)

// TestSDNSubnet_DecodesRealPVENumericSnat is the regression test for the
// hardware-validation gap that broke SDN subnet listing: real PVE (9.2.4)
// returns a subnet's `snat` as the NUMBER 1, not a JSON boolean. This is the
// exact body observed from GET /cluster/sdn/vnets/{vnet}/subnets on a live
// node.
func TestSDNSubnet_DecodesRealPVENumericSnat(t *testing.T) {
	const body = `{"cidr":"10.99.0.0/24","gateway":"10.99.0.1","snat":1,"subnet":"labz-10.99.0.0-24","type":"subnet","vnet":"labnet","zone":"labz"}`
	var s SDNSubnet
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("decoding real-PVE subnet: %v", err)
	}
	if s.ID != "labz-10.99.0.0-24" || s.CIDR != "10.99.0.0/24" || s.Gateway != "10.99.0.1" {
		t.Errorf("subnet = %+v, want the labz 10.99.0.0/24 fields", s)
	}
	if !s.SNAT {
		t.Errorf("SNAT = false, want true (decoded from the number 1)")
	}
}

func TestSDNSubnet_DecodesBoolAndZeroSnat(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"cidr":"10.0.0.0/24","snat":1}`, true},
		{`{"cidr":"10.0.0.0/24","snat":0}`, false},
		{`{"cidr":"10.0.0.0/24","snat":true}`, true},   // fixtures / forward-compat
		{`{"cidr":"10.0.0.0/24","snat":false}`, false}, // fixtures / forward-compat
		{`{"cidr":"10.0.0.0/24"}`, false},              // absent
	}
	for _, c := range cases {
		var s SDNSubnet
		if err := json.Unmarshal([]byte(c.body), &s); err != nil {
			t.Fatalf("decoding %s: %v", c.body, err)
		}
		if s.SNAT != c.want {
			t.Errorf("%s: SNAT = %v, want %v", c.body, s.SNAT, c.want)
		}
	}
}

// TestSDNSubnet_MarshalsSnatAsNumber proves the write path sends PVE the 1/0
// it expects on create/update, not a JSON boolean.
func TestSDNSubnet_MarshalsSnatAsNumber(t *testing.T) {
	out, err := json.Marshal(SDNSubnet{ID: "s", CIDR: "10.0.0.0/24", SNAT: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if got, ok := m["snat"].(float64); !ok || got != 1 {
		t.Errorf("snat wire value = %#v, want the number 1", m["snat"])
	}
	// snat=false is omitted, not sent as 0/false.
	out2, _ := json.Marshal(SDNSubnet{ID: "s", CIDR: "10.0.0.0/24"})
	var m2 map[string]any
	_ = json.Unmarshal(out2, &m2)
	if _, present := m2["snat"]; present {
		t.Errorf("snat=false should be omitted, got %v", m2["snat"])
	}
}

// TestSDNVnet_NumericVlanAware covers the vnet's vlan_aware flag, the other
// SDN field PVE sends as 0/1.
func TestSDNVnet_NumericVlanAware(t *testing.T) {
	var v SDNVnet
	if err := json.Unmarshal([]byte(`{"vnet":"v1","zone":"z","vlan_aware":1}`), &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !v.VlanAware {
		t.Errorf("VlanAware = false, want true (from the number 1)")
	}
	out, _ := json.Marshal(SDNVnet{ID: "v1", Zone: "z", VlanAware: true})
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if got, ok := m["vlan_aware"].(float64); !ok || got != 1 {
		t.Errorf("vlan_aware wire value = %#v, want the number 1", m["vlan_aware"])
	}
}

// TestFirewallOptions_DecodesRealPVENumericEnable is the regression test
// for the hardware-validation gap T-3202 found: real PVE 9.2.10 returns
// GET /cluster/firewall/options and GET /nodes/{node}/firewall/options'
// "enable" as the NUMBER 0 or 1, not a JSON boolean, exactly like
// SDNSubnet.SNAT/SDNVnet.VlanAware above — the exact bodies observed from a
// live node.
func TestFirewallOptions_DecodesRealPVENumericEnable(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"digest":"bb9ed91416c7ca9fe0bdb701360217e37ab4497f","enable":1}`, true},
		{`{"digest":"2c1759ebec624b1e511ba7f635915ab2df354cba","enable":0}`, false},
		{`{"digest":"da39a3ee5e6b4b0d3255bfef95601890afd80709"}`, false}, // enable absent entirely (never-configured node)
	}
	for _, c := range cases {
		var o FirewallOptions
		if err := json.Unmarshal([]byte(c.body), &o); err != nil {
			t.Fatalf("decoding %s: %v", c.body, err)
		}
		if o.Enable != c.want {
			t.Errorf("%s: Enable = %v, want %v", c.body, o.Enable, c.want)
		}
	}
}

// TestFirewallOptions_MarshalsEnableAsNumber proves the write path (fw.
// options.update's apply, PUT .../firewall/options) sends PVE the 1/0 it
// expects, not a JSON boolean.
func TestFirewallOptions_MarshalsEnableAsNumber(t *testing.T) {
	out, err := json.Marshal(FirewallOptions{Enable: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(out, &m); unmarshalErr != nil {
		t.Fatalf("re-decode: %v", unmarshalErr)
	}
	if got, ok := m["enable"].(float64); !ok || got != 1 {
		t.Errorf("enable wire value = %#v, want the number 1", m["enable"])
	}

	out2, err := json.Marshal(FirewallOptions{Enable: false})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m2 map[string]any
	if unmarshalErr := json.Unmarshal(out2, &m2); unmarshalErr != nil {
		t.Fatalf("re-decode: %v", unmarshalErr)
	}
	if got, ok := m2["enable"].(float64); !ok || got != 0 {
		t.Errorf("enable=false wire value = %#v, want the number 0 (enable has no omitempty — it must always be sent explicitly, unlike snat/vlan_aware)", m2["enable"])
	}
}
