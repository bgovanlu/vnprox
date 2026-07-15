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
