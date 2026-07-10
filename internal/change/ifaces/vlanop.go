package ifaces

import (
	"fmt"
	"strconv"

	"github.com/bgovanlu/vnprox/internal/host"
)

func mutateVlanCreate(f *host.File, o VlanCreate, changesetID, nl string) error {
	name := o.Target.ID
	if name == "" {
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
	body = append(body, optionItem("vlan-raw-device", o.Parent, nl))
	if o.MTU != 0 {
		body = append(body, optionItem("mtu", strconv.Itoa(o.MTU), nl))
	}
	if o.Comments != "" {
		body = append(body, host.BodyItem{Kind: host.BodyComment, Raw: fmt.Sprintf("\t#%s%s", o.Comments, nl)})
	}
	iface.Body = body

	appendStanza(f, o.Autostart, name, iface, nl)
	return nil
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
