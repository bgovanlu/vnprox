// SPDX-License-Identifier: Apache-2.0

// capture.go implements "blueprint-ify" (docs/features/blueprints.md §1:
// "capture from current state ... parameterizing addresses"): given a
// live inventory.Snapshot and one node, produce an (unsaved) Blueprint
// whose entities mirror that node's bonds/bridges/vlan interfaces, with
// every declared address turned into a named, address-suggest-eligible
// CIDR param (everything else — names, port/slave membership, VLAN ids —
// stays literal, since the point of a capture is "reproduce this node's
// network on another node", not a fully generic template).
//
// SDN entities are deliberately not captured: they are cluster-scoped, not
// per-node, so "blueprint-ify this node" (a per-node action) has no single
// node's SDN state to capture — the five starters cover SDN authoring
// instead.

package blueprint

import (
	"fmt"
	"net"
	"regexp"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Capture builds a Blueprint from node's bonds, bridges, and VLAN
// interfaces in snap. It never touches the store; the caller (Service, or
// a test) decides whether/how to save the result.
func Capture(snap inventory.Snapshot, node string) (*Blueprint, error) {
	if node == "" {
		return nil, fmt.Errorf("%w: capture requires a node name", ErrInvalidParams)
	}

	var bonds []*inventory.Bond
	var bridges []*inventory.Bridge
	var vlans []*inventory.VlanIface
	found := false
	for _, e := range snap.All() {
		ref := e.GetRef()
		if ref.Node != node {
			continue
		}
		switch v := e.(type) {
		case *inventory.Bond:
			bonds = append(bonds, v)
			found = true
		case *inventory.Bridge:
			bridges = append(bridges, v)
			found = true
		case *inventory.VlanIface:
			vlans = append(vlans, v)
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: node %q has no captured entities in inventory", ErrNotFound, node)
	}
	sort.Slice(bonds, func(i, j int) bool { return bonds[i].Name < bonds[j].Name })
	sort.Slice(bridges, func(i, j int) bool { return bridges[i].Name < bridges[j].Name })
	sort.Slice(vlans, func(i, j int) bool { return vlans[i].Name < vlans[j].Name })

	bp := &Blueprint{
		BlueprintVersion: CurrentBlueprintVersion,
		Name:             fmt.Sprintf("Captured from %s", node),
		Description:      fmt.Sprintf("Blueprint-ified from node %q's live network state.", node),
		NodeSelector:     NodeSelector{Mode: SelectAll},
	}

	var params []ParamDef
	addAddrParam := func(entityName string, idx int, addr string) string {
		name := addressParamName(entityName, idx)
		params = append(params, ParamDef{
			Name: name, Type: ParamCIDR, Label: fmt.Sprintf("%s address", entityName),
			Default: addr, Required: true, AddressSuggest: true, Subnet: containingCIDR(addr),
		})
		return "{{" + name + "}}"
	}

	for _, b := range bonds {
		fields := map[string]any{}
		if b.Mode != "" {
			fields["mode"] = b.Mode
		}
		if b.LACPRate != "" {
			fields["lacpRate"] = b.LACPRate
		}
		if b.XmitHashPolicy != "" {
			fields["xmitHashPolicy"] = b.XmitHashPolicy
		}
		if len(b.DeclaredSlaves) > 0 {
			fields["slaves"] = toAnySlice(b.DeclaredSlaves)
		}
		if b.MTUDeclared != 0 {
			fields["mtu"] = b.MTUDeclared
		}
		bp.Entities = append(bp.Entities, EntityTemplate{Kind: KindBond, IDTemplate: b.Name, Fields: fields})
	}

	for _, br := range bridges {
		fields := map[string]any{}
		if len(br.DeclaredPortNames) > 0 {
			fields["ports"] = toAnySlice(br.DeclaredPortNames)
		}
		if br.VlanAwareSet {
			fields["vlanAware"] = br.VlanAware
		}
		if len(br.Vids) > 0 {
			vids := make([]any, 0, len(br.Vids))
			for _, v := range br.Vids {
				for id := v.Low; id <= v.High; id++ {
					vids = append(vids, id)
				}
			}
			fields["vids"] = vids
		}
		if br.MTUDeclared != 0 {
			fields["mtu"] = br.MTUDeclared
		}
		if br.Gateway != "" {
			fields["gateway"] = br.Gateway
		}
		if br.Comments != "" {
			fields["comments"] = br.Comments
		}
		if br.STP {
			fields["stp"] = br.STP
		}
		if len(br.Addresses) > 0 {
			addrs := make([]any, len(br.Addresses))
			for i, a := range br.Addresses {
				addrs[i] = addAddrParam(br.Name, i, a)
			}
			fields["addresses"] = addrs
		}
		bp.Entities = append(bp.Entities, EntityTemplate{Kind: KindBridge, IDTemplate: br.Name, Fields: fields})
	}

	for _, vl := range vlans {
		fields := map[string]any{"parent": vl.ParentName, "vid": vl.Vid}
		if vl.MTUDeclared != 0 {
			fields["mtu"] = vl.MTUDeclared
		}
		if len(vl.Addresses) > 0 {
			addrs := make([]any, len(vl.Addresses))
			for i, a := range vl.Addresses {
				addrs[i] = addAddrParam(vl.Name, i, a)
			}
			fields["addresses"] = addrs
		}
		bp.Entities = append(bp.Entities, EntityTemplate{Kind: KindVlan, IDTemplate: vl.Name, Fields: fields})
	}

	bp.Params = params
	return bp, nil
}

var nonIdentPattern = regexp.MustCompile(`[^A-Za-z0-9_]`)

func addressParamName(entityName string, idx int) string {
	base := "addr_" + nonIdentPattern.ReplaceAllString(entityName, "_")
	if idx == 0 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, idx)
}

// containingCIDR returns addr's containing network at its own prefix
// length (e.g. "192.168.1.10/24" -> "192.168.1.0/24"), or "" if addr isn't
// parseable as a CIDR — used to seed a captured address param's Subnet so
// SuggestForParam has a pool to search without the caller having to
// re-derive it from Default every time.
func containingCIDR(addr string) string {
	_, ipnet, err := net.ParseCIDR(addr)
	if err != nil {
		return ""
	}
	return ipnet.String()
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
