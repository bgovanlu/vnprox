package pvemock

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// pveBool mirrors internal/pve's numeric-boolean handling so the mock speaks
// real PVE's wire format for bool-valued SDN fields (a subnet's snat, a vnet's
// vlan_aware): on the JSON API it emits 0/1 and accepts a number, a bool, or a
// quoted form. Real PVE (9.2.4) returns these as 0/1, not JSON booleans;
// modeling them as bool is what let that decode bug ship. YAML fixture loading
// is unaffected (it uses the yaml tags, not these JSON methods).
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
		var n json.Number
		if err := json.Unmarshal([]byte(s), &n); err == nil {
			if f, ferr := n.Float64(); ferr == nil {
				*b = f != 0
				return nil
			}
		}
		return fmt.Errorf("pvemock: cannot decode %s as a bool", s)
	}
	return nil
}

func (s SDNSubnetSpec) MarshalJSON() ([]byte, error) {
	type alias SDNSubnetSpec
	return json.Marshal(struct {
		alias
		SNAT pveBool `json:"snat,omitempty"`
	}{alias: alias(s), SNAT: pveBool(s.SNAT)})
}

func (s *SDNSubnetSpec) UnmarshalJSON(data []byte) error {
	type alias SDNSubnetSpec
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

func (v SDNVnetSpec) MarshalJSON() ([]byte, error) {
	type alias SDNVnetSpec
	return json.Marshal(struct {
		alias
		VlanAware pveBool `json:"vlan_aware,omitempty"`
	}{alias: alias(v), VlanAware: pveBool(v.VlanAware)})
}

func (v *SDNVnetSpec) UnmarshalJSON(data []byte) error {
	type alias SDNVnetSpec
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

// MarshalJSON/UnmarshalJSON do the same for FwRuleSpec's Enabled flag
// (T-3202 real-hardware finding, planning/reports/blocked-validation.md):
// this mock's own decoding of a real client's POST/PUT .../rules body was
// still modeling "enable" as a JSON bool, which is exactly the bug
// internal/pve's own FirewallRule/FirewallOptions carried until this same
// session — real PVE both returns 0/1 on read and rejects a literal JSON
// true/false on write. Fixing the mock to match, not the client to match
// the mock (CLAUDE.md: "a fixture's job is to match what pvecube says").
func (r FwRuleSpec) MarshalJSON() ([]byte, error) {
	type alias FwRuleSpec
	return json.Marshal(struct {
		alias
		Enabled pveBool `json:"enable"`
	}{alias: alias(r), Enabled: pveBool(r.Enabled)})
}

func (r *FwRuleSpec) UnmarshalJSON(data []byte) error {
	type alias FwRuleSpec
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
