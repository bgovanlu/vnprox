package ifaces

import (
	"fmt"
	"strings"
)

// OpSummary is one op's card in the review screen's Summary tab
// (docs/features/change-management.md §3): a human-readable one-liner plus
// the machine-readable op type and target, matching the "structured op
// summaries" half of docs/api.md's diff endpoint shape.
type OpSummary struct {
	Op      string `json:"op"`
	Target  string `json:"target"`
	Node    string `json:"node"`
	Summary string `json:"summary"`
}

// Summarize renders op as one OpSummary.
func Summarize(op Op) OpSummary {
	ref := op.Ref()
	return OpSummary{
		Op:      string(op.Kind()),
		Target:  ref.String(),
		Node:    ref.Node,
		Summary: summaryText(op),
	}
}

func summaryText(op Op) string {
	switch o := op.(type) {
	case IfaceUpdate:
		return fmt.Sprintf("Update interface %s (%s)", o.Target.ID, updateFieldList(o))
	case BondCreate:
		return fmt.Sprintf("Create bond %s (%s) from %s", o.Target.ID, orDash(o.Mode), strings.Join(o.Slaves, ", "))
	case BondUpdate:
		return fmt.Sprintf("Update bond %s", o.Target.ID)
	case BondDelete:
		return fmt.Sprintf("Delete bond %s", o.Target.ID)
	case BridgeCreate:
		kind := "bridge"
		if isOVSKind(string(o.Target.Kind)) {
			kind = "OVS bridge"
		}
		return fmt.Sprintf("Create %s %s with ports %s", kind, o.Target.ID, strings.Join(o.Ports, ", "))
	case BridgeUpdate:
		return fmt.Sprintf("Update bridge %s", o.Target.ID)
	case BridgeDelete:
		return fmt.Sprintf("Delete bridge %s", o.Target.ID)
	case BridgePortAdd:
		return fmt.Sprintf("Add port %s to bridge %s", o.Port, o.Target.ID)
	case BridgePortRemove:
		return fmt.Sprintf("Remove port %s from bridge %s", o.Port, o.Target.ID)
	case VlanCreate:
		return fmt.Sprintf("Create VLAN %s (vid %d on %s)", o.Target.ID, o.VID, o.Parent)
	case VlanUpdate:
		return fmt.Sprintf("Update VLAN %s", o.Target.ID)
	case VlanDelete:
		return fmt.Sprintf("Delete VLAN %s", o.Target.ID)
	case IfaceRawReplace:
		return fmt.Sprintf("Replace /etc/network/interfaces on %s (raw edit, %d bytes)", o.Target.Node, len(o.Content))
	default:
		return fmt.Sprintf("%s %s", op.Kind(), op.Ref().ID)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func updateFieldList(o IfaceUpdate) string {
	var fields []string
	if o.MTU != nil {
		fields = append(fields, fmt.Sprintf("mtu=%d", *o.MTU))
	}
	if len(o.Addresses) > 0 {
		fields = append(fields, "addresses")
	} else if o.RemoveAddress {
		fields = append(fields, "clear addresses")
	}
	if o.Gateway != nil {
		fields = append(fields, "gateway")
	} else if o.RemoveGateway {
		fields = append(fields, "clear gateway")
	}
	if o.Autostart != nil {
		fields = append(fields, fmt.Sprintf("autostart=%v", *o.Autostart))
	}
	if o.Comments != nil {
		fields = append(fields, "comments")
	}
	if len(fields) == 0 {
		return "no changes"
	}
	return strings.Join(fields, ", ")
}
