package ifaces

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// vidRangesString renders a list of inclusive VID ranges as a
// bridge-vids-style comma-separated value ("2-4094", "10,20,30-40"), in the
// given order (not sorted — determinism here means "same input order
// produces the same output", not a canonical sort).
func vidRangesString(vids []inventory.VidRange) string {
	parts := make([]string, len(vids))
	for i, v := range vids {
		parts[i] = v.String()
	}
	return strings.Join(parts, ",")
}

func mutateBridgeCreate(f *host.File, o BridgeCreate, changesetID string) error {
	name := o.Target.ID
	if _, ok := findIface(f, name); ok {
		return fmt.Errorf("ifaces: bridge.create %q: %w", name, ErrExists)
	}
	ovs := isOVSKind(string(o.Target.Kind))
	method := "manual"
	if len(o.Addresses) > 0 {
		method = "static"
	}
	iface := newIfaceEntry(name, "inet", method)
	body := []host.BodyItem{managedByComment(changesetID)}

	for _, a := range o.Addresses {
		body = append(body, optionItem("address", a))
	}
	if o.Gateway != "" {
		body = append(body, optionItem("gateway", o.Gateway))
	}

	if ovs {
		body = append(body, optionItem("ovs_type", "OVSBridge"))
		body = append(body, optionItem("ovs_ports", joinTokens(o.Ports)))
	} else {
		body = append(body, optionItem("bridge-ports", joinTokens(o.Ports)))
		if o.VlanAware {
			body = append(body, optionItem("bridge-vlan-aware", "yes"))
		}
		if len(o.Vids) > 0 {
			body = append(body, optionItem("bridge-vids", vidRangesString(o.Vids)))
		}
		if o.STP {
			body = append(body, optionItem("bridge-stp", "on"))
		} else {
			body = append(body, optionItem("bridge-stp", "off"))
			body = append(body, optionItem("bridge-fd", "0"))
		}
	}
	if o.MTU != 0 {
		body = append(body, optionItem("mtu", strconv.Itoa(o.MTU)))
	}
	if o.Comments != "" {
		body = append(body, host.BodyItem{Kind: host.BodyComment, Raw: fmt.Sprintf("\t#%s\n", o.Comments)})
	}
	iface.Body = body

	if ovs {
		// OVS bridges are brought up via "allow-ovs <name>", not "auto
		// <name>" (see testdata/interfaces/04-ovs-bridge.interfaces,
		// 05-ovs-bond.interfaces): both encode "start this at boot", but
		// ifupdown2's OVS glue specifically keys off allow-ovs.
		var prefix *host.Entry
		if o.Autostart {
			p := allowPrefixEntry("ovs", name)
			prefix = &p
		}
		appendStanzaRaw(f, prefix, iface)
		return nil
	}
	appendStanza(f, o.Autostart, name, iface)
	return nil
}

func mutateBridgeUpdate(f *host.File, o BridgeUpdate) error {
	idx, ok := findIface(f, o.Target.ID)
	if !ok {
		return fmt.Errorf("ifaces: bridge.update %q: %w", o.Target.ID, ErrNotFound)
	}
	e := &f.Entries[idx]
	ovs := isOVSKind(string(o.Target.Kind))
	body := e.Body

	if len(o.Ports) > 0 {
		key := "bridge-ports"
		if ovs {
			key = "ovs_ports"
		}
		body = setOption(body, key, joinTokens(o.Ports))
	}
	if !ovs {
		if o.VlanAware != nil {
			if *o.VlanAware {
				body = setOption(body, "bridge-vlan-aware", "yes")
			} else {
				body = removeOptionKey(body, "bridge-vlan-aware")
			}
		}
		if o.RemoveVids {
			body = removeOptionKey(body, "bridge-vids")
		} else if len(o.Vids) > 0 {
			body = setOption(body, "bridge-vids", vidRangesString(o.Vids))
		}
		if o.STP != nil {
			if *o.STP {
				body = setOption(body, "bridge-stp", "on")
			} else {
				body = setOption(body, "bridge-stp", "off")
			}
		}
	}
	if len(o.Addresses) > 0 {
		body = setOptionList(body, "address", o.Addresses)
	}
	if o.RemoveGateway {
		body = removeOptionKey(body, "gateway")
	} else if o.Gateway != nil && *o.Gateway != "" {
		body = setOption(body, "gateway", *o.Gateway)
	}
	if o.MTU != 0 {
		body = setOption(body, "mtu", strconv.Itoa(o.MTU))
	}
	if o.Comments != nil {
		body = setCommentLine(body, *o.Comments)
	}
	e.Body = body
	return nil
}

func mutateBridgeDelete(f *host.File, o BridgeDelete) error {
	if _, ok := findIface(f, o.Target.ID); !ok {
		return fmt.Errorf("ifaces: bridge.delete %q: %w", o.Target.ID, ErrNotFound)
	}
	removeIfaceAndAuto(f, o.Target.ID)
	return nil
}

func mutateBridgePortAdd(f *host.File, o BridgePortAdd) error {
	idx, ok := findIface(f, o.Target.ID)
	if !ok {
		return fmt.Errorf("ifaces: bridge.port.add %q: %w", o.Target.ID, ErrNotFound)
	}
	e := &f.Entries[idx]
	key := portsKeyFor(e)
	cur, _ := getOption(e.Body, key)
	e.Body = setOption(e.Body, key, addToken(cur, o.Port))
	return nil
}

func mutateBridgePortRemove(f *host.File, o BridgePortRemove) error {
	idx, ok := findIface(f, o.Target.ID)
	if !ok {
		return fmt.Errorf("ifaces: bridge.port.remove %q: %w", o.Target.ID, ErrNotFound)
	}
	e := &f.Entries[idx]
	key := portsKeyFor(e)
	cur, _ := getOption(e.Body, key)
	e.Body = setOption(e.Body, key, removeToken(cur, o.Port))
	return nil
}

// portsKeyFor detects which port-list option key an existing bridge stanza
// uses (ovs_ports for OVS bridges, bridge-ports for Linux ones), from the
// stanza's own options rather than from the op's target Kind, since the
// entity already on disk is the ground truth for how it was declared.
func portsKeyFor(e *host.Entry) string {
	if _, ok := getOption(e.Body, "ovs_ports"); ok {
		return "ovs_ports"
	}
	if _, ok := getOption(e.Body, "ovs_type"); ok {
		return "ovs_ports"
	}
	return "bridge-ports"
}
