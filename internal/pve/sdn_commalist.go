// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// commaList marshals to / unmarshals from PVE's comma-separated-string
// convention for the list-shaped SDN zone fields (nodes, exit_nodes, peers).
// Real PVE (validated on a 9.2.4 node) returns these as a single string —
// e.g. "nodes":"pve1,pve2" — NOT a JSON array, and accepts the same string
// form on write. Decoding them into a plain []string fails outright
// ("cannot unmarshal string into Go struct field ... of type []string"),
// which took down every SDN/IPAM read the moment a zone with a node list
// existed. UnmarshalJSON here accepts either the comma-string (real PVE) or a
// JSON array (fixtures / any forward-compatible response); MarshalJSON always
// emits the comma-string PVE expects on create/update.
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
		return fmt.Errorf("pve: decoding comma-list field: %w", err)
	}
	*l = arr
	return nil
}

// splitCommaList splits PVE's "a,b,c" list form into its elements, trimming
// whitespace and dropping empties (so "" yields a nil slice, not [""]).
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

// MarshalJSON/UnmarshalJSON translate SDNZone's node/exit-node/peer lists
// to and from PVE's comma-string convention (see commaList) while keeping the
// exported struct fields as plain []string for every caller. The `alias` type
// strips SDNZone's own methods (avoiding recursion) and its []string list
// fields are shadowed by the outer commaList fields sharing the same JSON tag.
func (z SDNZone) MarshalJSON() ([]byte, error) {
	type alias SDNZone
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

func (z *SDNZone) UnmarshalJSON(data []byte) error {
	type alias SDNZone
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
