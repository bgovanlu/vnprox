package pvemock

import (
	"bytes"
	"encoding/json"
	"strings"
)

// commaList mirrors internal/pve's comma-string handling so the mock speaks
// real PVE's wire format for SDN zone node/exit-node/peer lists: on the JSON
// API it emits a comma-separated string ("pve1,pve2") and accepts either that
// or a JSON array. Real PVE (9.2.4) returns these as strings, not arrays;
// modeling them as arrays here is exactly what let that decode bug ship
// unnoticed. Kept local — pvemock deliberately does not import internal/pve.
// YAML fixture loading is unaffected (it uses the yaml tags, not these JSON
// methods), so fixtures keep writing node lists as ordinary YAML sequences.
type commaList []string

func (l commaList) MarshalJSON() ([]byte, error) {
	return json.Marshal(strings.Join(l, ","))
}

func (l *commaList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*l = nil
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*l = splitCommaList(s)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(trimmed, &arr); err != nil {
		return err
	}
	*l = arr
	return nil
}

func splitCommaList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// MarshalJSON/UnmarshalJSON give SDNZoneSpec real PVE's comma-string wire
// form for its list fields on the JSON API (see commaList), keeping the
// struct fields plain []string for the mock's own YAML/state handling. The
// `alias` type strips SDNZoneSpec's methods (avoids recursion); its []string
// list fields are shadowed by the outer commaList fields with the same tag.
func (z SDNZoneSpec) MarshalJSON() ([]byte, error) {
	type alias SDNZoneSpec
	return json.Marshal(struct {
		Nodes     commaList `json:"nodes,omitempty"`
		ExitNodes commaList `json:"exit_nodes,omitempty"`
		Peers     commaList `json:"peers,omitempty"`
		alias
	}{
		Nodes:     commaList(z.Nodes),
		ExitNodes: commaList(z.ExitNodes),
		Peers:     commaList(z.Peers),
		alias:     alias(z),
	})
}

func (z *SDNZoneSpec) UnmarshalJSON(data []byte) error {
	type alias SDNZoneSpec
	aux := struct {
		*alias
		Nodes     commaList `json:"nodes,omitempty"`
		ExitNodes commaList `json:"exit_nodes,omitempty"`
		Peers     commaList `json:"peers,omitempty"`
	}{alias: (*alias)(z)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	z.Nodes = aux.Nodes
	z.ExitNodes = aux.ExitNodes
	z.Peers = aux.Peers
	return nil
}
