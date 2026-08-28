// SPDX-License-Identifier: Apache-2.0

package ifaces

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func mutateVlanCreate(f *host.File, o VlanCreate, changesetID, nl string) error {
	name := o.Target.ID
	if name == "" && !o.OVS {
		name = VlanName(o.Parent, o.VID)
	}
	if _, ok := findIface(f, name); ok {
		return fmt.Errorf("ifaces: vlan.create %q: %w", name, ErrExists)
	}
	method := "manual"
	if len(o.Addresses) > 0 {
		method = "static"
	}
	iface := newIfaceEntry(name, "inet", method, nl)
	body := []host.BodyItem{managedByComment(changesetID, nl)}
	for _, a := range o.Addresses {
		body = append(body, optionItem("address", a, nl))
	}
	if o.Gateway != "" {
		body = append(body, optionItem("gateway", o.Gateway, nl))
	}

	if o.OVS {
		body = append(body, optionItem("ovs_type", "OVSIntPort", nl))
		body = append(body, optionItem("ovs_bridge", o.Parent, nl))
		if opts := ovsIntPortOptions(o.VID, o.Trunks); opts != "" {
			body = append(body, optionItem("ovs_options", opts, nl))
		}
	} else {
		body = append(body, optionItem("vlan-raw-device", o.Parent, nl))
	}
	if o.MTU != 0 {
		body = append(body, optionItem("mtu", strconv.Itoa(o.MTU), nl))
	}
	if o.Comments != "" {
		body = append(body, host.BodyItem{Kind: host.BodyComment, Raw: fmt.Sprintf("\t#%s%s", o.Comments, nl)})
	}
	iface.Body = body

	if o.OVS && o.Parent != "" {
		// OVS Int Ports are brought up via "allow-<bridge> <name>", not
		// "auto <name>" (see bridge.go's mutateBridgeCreate and
		// testdata/interfaces/04-ovs-bridge.interfaces's "allow-vmbr0
		// vlan20"): both encode "start this at boot", but ifupdown2's OVS
		// glue specifically keys off allow-<bridge>.
		var prefix *host.Entry
		if o.Autostart {
			p := allowPrefixEntry(o.Parent, name, nl)
			prefix = &p
		}
		appendStanzaRaw(f, prefix, iface, nl)
		return nil
	}
	appendStanza(f, o.Autostart, name, iface, nl)
	return nil
}

// ovsIntPortOptions renders the ovs_options value for an OVS Int Port's
// VLAN tag/trunk config: "tag=<vid>" when tagged, "trunks=<ranges>" when
// trunked, both space-separated when both are set (OVS's
// native-tagged/native-untagged vlan_mode use case), "" for a pure
// untagged/native access port (vid 0, no trunks).
func ovsIntPortOptions(vid int, trunks []inventory.VidRange) string {
	var parts []string
	if vid != 0 {
		parts = append(parts, "tag="+strconv.Itoa(vid))
	}
	if len(trunks) > 0 {
		parts = append(parts, "trunks="+vidRangesString(trunks))
	}
	return strings.Join(parts, " ")
}

func mutateVlanUpdate(f *host.File, o VlanUpdate, nl string) error {
	idx, ok := findIface(f, o.Target.ID)
	if !ok {
		return fmt.Errorf("ifaces: vlan.update %q: %w", o.Target.ID, ErrNotFound)
	}
	e := &f.Entries[idx]
	body := e.Body
	if len(o.Addresses) > 0 {
		body = setOptionList(body, "address", o.Addresses, nl)
	}
	if o.MTU != 0 {
		body = setOption(body, "mtu", strconv.Itoa(o.MTU), nl)
	}
	if o.Comments != nil {
		body = setCommentLine(body, *o.Comments, nl)
	}
	e.Body = body
	return nil
}

func mutateVlanDelete(f *host.File, o VlanDelete, nl string) error {
	if _, ok := findIface(f, o.Target.ID); !ok {
		return fmt.Errorf("ifaces: vlan.delete %q: %w", o.Target.ID, ErrNotFound)
	}
	removeIfaceAndAuto(f, o.Target.ID, nl)
	return nil
}
