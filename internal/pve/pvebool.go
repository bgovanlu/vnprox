package pve

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// pveBool marshals to / unmarshals from PVE's numeric-boolean convention for
// bool-valued SDN fields (a subnet's snat, a vnet's vlan_aware). Real PVE
// (validated on a 9.2.4 node) returns these as the NUMBER 0 or 1 — e.g.
// "snat":1 — not a JSON boolean, and accepts 1/0 on write. Decoding them into
// a Go bool fails outright ("cannot unmarshal number into Go struct field ...
// of type bool"), which broke SDN subnet listing the moment a subnet with
// snat=1 existed. UnmarshalJSON accepts a number (0/1), a JSON boolean, or a
// quoted "0"/"1"/"true"/"false"; MarshalJSON emits 1/0, the form PVE expects.
type pveBool bool

func (b pveBool) MarshalJSON() ([]byte, error) {
	if b {
		return []byte("1"), nil
	}
	return []byte("0"), nil
}

func (b *pveBool) UnmarshalJSON(data []byte) error {
	switch s := string(bytes.TrimSpace(data)); s {
	case "", "null":
		*b = false
	case "true", `"true"`, "1", `"1"`:
		*b = true
	case "false", `"false"`, "0", `"0"`:
		*b = false
	default:
		// Any other numeric form: non-zero is true.
		var n json.Number
		if err := json.Unmarshal([]byte(s), &n); err == nil {
			if f, ferr := n.Float64(); ferr == nil {
				*b = f != 0
				return nil
			}
		}
		return fmt.Errorf("pve: cannot decode %s as a bool", s)
	}
	return nil
}

// MarshalJSON/UnmarshalJSON translate SDNSubnet's SNAT flag to and from PVE's
// numeric 0/1 convention (see pveBool) while keeping the exported field a
// plain bool for callers. `alias` strips SDNSubnet's own methods (avoiding
// recursion); its bool SNAT field is shadowed by the outer pveBool with the
// same JSON tag.
func (s SDNSubnet) MarshalJSON() ([]byte, error) {
	type alias SDNSubnet
	return json.Marshal(struct {
		alias
		SNAT pveBool `json:"snat,omitempty"`
	}{alias: alias(s), SNAT: pveBool(s.SNAT)})
}

func (s *SDNSubnet) UnmarshalJSON(data []byte) error {
	type alias SDNSubnet
	aux := struct {
		*alias
		SNAT pveBool `json:"snat,omitempty"`
	}{alias: (*alias)(s)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	s.SNAT = bool(aux.SNAT)
	return nil
}

// MarshalJSON/UnmarshalJSON do the same for SDNVnet's VlanAware flag.
func (v SDNVnet) MarshalJSON() ([]byte, error) {
	type alias SDNVnet
	return json.Marshal(struct {
		alias
		VlanAware pveBool `json:"vlan_aware,omitempty"`
	}{alias: alias(v), VlanAware: pveBool(v.VlanAware)})
}

func (v *SDNVnet) UnmarshalJSON(data []byte) error {
	type alias SDNVnet
	aux := struct {
		*alias
		VlanAware pveBool `json:"vlan_aware,omitempty"`
	}{alias: (*alias)(v)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	v.VlanAware = bool(aux.VlanAware)
	return nil
}

// MarshalJSON/UnmarshalJSON do the same for FirewallOptions' Enable flag
// (T-3202 real-hardware finding, planning/reports/blocked-validation.md):
// GET /cluster/firewall/options and GET /nodes/{node}/firewall/options both
// return "enable" as the number 0/1 on a real PVE 9.2.10 node, exactly the
// same convention snat/vlan_aware already needed pveBool for — decoding
// into a plain bool failed outright, silently breaking every periodic
// firewall-options collector poll on real hardware (degraded gracefully,
// "skipping", but never actually read the real value) rather than crashing
// visibly, which is why it went unnoticed until the first real
// write+read-back cycle against real hardware.
func (o FirewallOptions) MarshalJSON() ([]byte, error) {
	type alias FirewallOptions
	return json.Marshal(struct {
		alias
		Enable pveBool `json:"enable"`
	}{alias: alias(o), Enable: pveBool(o.Enable)})
}

func (o *FirewallOptions) UnmarshalJSON(data []byte) error {
	type alias FirewallOptions
	aux := struct {
		*alias
		Enable pveBool `json:"enable"`
	}{alias: (*alias)(o)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	o.Enable = bool(aux.Enable)
	return nil
}

// MarshalJSON/UnmarshalJSON do the same for FirewallRule's Enabled flag —
// see FirewallRule's own doc comment for the write-side discovery (a
// literal JSON true/false rejected outright by POST .../rules) that
// motivated this, distinct from (though the same root convention as)
// FirewallOptions.Enable above.
func (r FirewallRule) MarshalJSON() ([]byte, error) {
	type alias FirewallRule
	return json.Marshal(struct {
		alias
		Enabled pveBool `json:"enable"`
	}{alias: alias(r), Enabled: pveBool(r.Enabled)})
}

func (r *FirewallRule) UnmarshalJSON(data []byte) error {
	type alias FirewallRule
	aux := struct {
		*alias
		Enabled pveBool `json:"enable"`
	}{alias: (*alias)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	r.Enabled = bool(aux.Enabled)
	return nil
}
