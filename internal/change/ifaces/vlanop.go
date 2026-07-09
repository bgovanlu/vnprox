package ifaces

import (
	"fmt"
	"strconv"

	"github.com/bgovanlu/vnprox/internal/host"
)

func mutateVlanCreate(f *host.File, o VlanCreate, changesetID string) error {
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
	iface := newIfaceEntry(name, "inet", method)
	body := []host.BodyItem{managedByComment(changesetID)}
	for _, a := range o.Addresses {
		body = append(body, optionItem("address", a))
	}
	body = append(body, optionItem("vlan-raw-device", o.Parent))
	if o.MTU != 0 {
		body = append(body, optionItem("mtu", strconv.Itoa(o.MTU)))
	}
	if o.Comments != "" {
		body = append(body, host.BodyItem{Kind: host.BodyComment, Raw: fmt.Sprintf("\t#%s\n", o.Comments)})
	}
	iface.Body = body

	appendStanza(f, o.Autostart, name, iface)
	return nil
}

func mutateVlanUpdate(f *host.File, o VlanUpdate) error {
	idx, ok := findIface(f, o.Target.ID)
	if !ok {
		return fmt.Errorf("ifaces: vlan.update %q: %w", o.Target.ID, ErrNotFound)
	}
	e := &f.Entries[idx]
	body := e.Body
	if len(o.Addresses) > 0 {
		body = setOptionList(body, "address", o.Addresses)
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

func mutateVlanDelete(f *host.File, o VlanDelete) error {
	if _, ok := findIface(f, o.Target.ID); !ok {
		return fmt.Errorf("ifaces: vlan.delete %q: %w", o.Target.ID, ErrNotFound)
	}
	removeIfaceAndAuto(f, o.Target.ID)
	return nil
}
