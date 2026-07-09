package ifaces

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/host"
)

const defaultMIIMon = 100

func mutateBondCreate(f *host.File, o BondCreate, changesetID string) error {
	name := o.Target.ID
	if _, ok := findIface(f, name); ok {
		return fmt.Errorf("ifaces: bond.create %q: %w", name, ErrExists)
	}
	ovs := isOVSKind(string(o.Target.Kind))
	method := "manual"
	iface := newIfaceEntry(name, "inet", method)
	body := []host.BodyItem{managedByComment(changesetID)}

	miimon := o.MIIMon
	if miimon == 0 {
		miimon = defaultMIIMon
	}

	if ovs {
		body = append(body, optionItem("ovs_bonds", joinTokens(o.Slaves)))
		body = append(body, optionItem("ovs_type", "OVSBond"))
		if o.Bridge != "" {
			body = append(body, optionItem("ovs_bridge", o.Bridge))
		}
		opts := ovsBondModeOptions(o.Mode)
		if opts != "" {
			body = append(body, optionItem("ovs_options", opts))
		}
	} else {
		body = append(body, optionItem("bond-slaves", joinTokens(o.Slaves)))
		if o.Mode != "" {
			body = append(body, optionItem("bond-mode", o.Mode))
		}
		body = append(body, optionItem("bond-miimon", strconv.Itoa(miimon)))
		if o.LacpRate != "" {
			body = append(body, optionItem("bond-lacp-rate", o.LacpRate))
		}
		if o.XmitHashPolicy != "" {
			body = append(body, optionItem("bond-xmit-hash-policy", o.XmitHashPolicy))
		}
	}
	if o.MTU != 0 {
		body = append(body, optionItem("mtu", strconv.Itoa(o.MTU)))
	}
	if o.Comments != "" {
		body = append(body, host.BodyItem{Kind: host.BodyComment, Raw: fmt.Sprintf("\t#%s\n", o.Comments)})
	}
	iface.Body = body

	if ovs && o.Bridge != "" {
		var prefix *host.Entry
		if o.Autostart {
			p := allowPrefixEntry(o.Bridge, name)
			prefix = &p
		}
		appendStanzaRaw(f, prefix, iface)
		return nil
	}
	appendStanza(f, o.Autostart, name, iface)
	return nil
}

func mutateBondUpdate(f *host.File, o BondUpdate) error {
	idx, ok := findIface(f, o.Target.ID)
	if !ok {
		return fmt.Errorf("ifaces: bond.update %q: %w", o.Target.ID, ErrNotFound)
	}
	e := &f.Entries[idx]
	ovs := isOVSKind(string(o.Target.Kind))
	body := e.Body

	if len(o.Slaves) > 0 {
		key := "bond-slaves"
		if ovs {
			key = "ovs_bonds"
		}
		body = setOption(body, key, joinTokens(o.Slaves))
	}
	if o.Mode != "" && !ovs {
		body = setOption(body, "bond-mode", o.Mode)
	}
	if o.MIIMon != 0 && !ovs {
		body = setOption(body, "bond-miimon", strconv.Itoa(o.MIIMon))
	}
	if !ovs {
		if o.RemoveLacpRate {
			body = removeOptionKey(body, "bond-lacp-rate")
		} else if o.LacpRate != "" {
			body = setOption(body, "bond-lacp-rate", o.LacpRate)
		}
		if o.RemoveXmitHashPolicy {
			body = removeOptionKey(body, "bond-xmit-hash-policy")
		} else if o.XmitHashPolicy != "" {
			body = setOption(body, "bond-xmit-hash-policy", o.XmitHashPolicy)
		}
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

func mutateBondDelete(f *host.File, o BondDelete) error {
	if _, ok := findIface(f, o.Target.ID); !ok {
		return fmt.Errorf("ifaces: bond.delete %q: %w", o.Target.ID, ErrNotFound)
	}
	removeIfaceAndAuto(f, o.Target.ID)
	return nil
}

// joinTokens space-joins tokens, matching interfaces(5)'s
// whitespace-separated list options (bond-slaves, bridge-ports, ...).
func joinTokens(ts []string) string { return strings.Join(ts, " ") }

// ovsBondModeOptions renders the ovs_options value ifupdown2's OVS bonding
// support expects for a given logical bond mode name, matching the
// convention T-102's testdata/interfaces/05-ovs-bond.interfaces fixture
// uses ("bond_mode=balance-slb lacp=active other_config:lacp-time=fast" for
// 802.3ad-equivalent LACP bonding).
func ovsBondModeOptions(mode string) string {
	switch mode {
	case "802.3ad", "lacp":
		return "bond_mode=balance-slb lacp=active other_config:lacp-time=fast"
	case "":
		return ""
	default:
		return "bond_mode=" + mode
	}
}

// setCommentLine replaces (or adds, or removes when text=="") the single
// plain per-stanza comment line PVE's Comments field renders as a
// "\t#<text>" body line, leaving the managed-by-vnprox provenance comment
// (if present) and every other body item untouched. It targets the first
// BodyComment item that isn't the managed-by-vnprox marker.
func setCommentLine(body []host.BodyItem, text string) []host.BodyItem {
	idx := -1
	for i, item := range body {
		if item.Kind == host.BodyComment && !isManagedByComment(item.Raw) {
			idx = i
			break
		}
	}
	if text == "" {
		if idx < 0 {
			return body
		}
		out := make([]host.BodyItem, 0, len(body)-1)
		out = append(out, body[:idx]...)
		out = append(out, body[idx+1:]...)
		return out
	}
	item := host.BodyItem{Kind: host.BodyComment, Raw: fmt.Sprintf("\t#%s\n", text)}
	if idx < 0 {
		return insertBeforeTrailingBlanks(body, []host.BodyItem{item})
	}
	out := append([]host.BodyItem{}, body...)
	out[idx] = item
	return out
}

func isManagedByComment(raw string) bool {
	return strings.Contains(raw, "managed by vnprox")
}
