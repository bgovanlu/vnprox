package ifaces

import (
	"fmt"
	"strconv"

	"github.com/bgovanlu/vnprox/internal/host"
)

func mutateIfaceUpdate(f *host.File, o IfaceUpdate, nl string) error {
	idx, ok := findIface(f, o.Target.ID)
	if !ok {
		return fmt.Errorf("ifaces: iface.update %q: %w", o.Target.ID, ErrNotFound)
	}
	e := &f.Entries[idx]
	body := e.Body

	if o.RemoveAddress && len(o.Addresses) == 0 {
		body = removeOptionKey(body, "address")
	} else if len(o.Addresses) > 0 {
		body = setOptionList(body, "address", o.Addresses, nl)
	}
	if o.RemoveGateway && o.Gateway == nil {
		body = removeOptionKey(body, "gateway")
	} else if o.Gateway != nil {
		body = setOption(body, "gateway", *o.Gateway, nl)
	}
	if o.MTU != nil {
		body = setOption(body, "mtu", strconv.Itoa(*o.MTU), nl)
	}
	if o.Comments != nil {
		body = setCommentLine(body, *o.Comments, nl)
	}
	e.Body = body

	if o.Autostart != nil {
		setAutostart(f, o.Target.ID, *o.Autostart, nl)
	}
	return nil
}

// setAutostart adds or removes name from the file's auto/allow-* wiring so
// its intended boot-time state matches want, editing existing multi-name
// lines minimally (see removeIfaceAndAuto) and inserting a fresh
// single-name "auto <name>" line immediately before its iface stanza when
// turning autostart on for a name that had none.
func setAutostart(f *host.File, name string, want bool, nl string) {
	hasAuto := false
	for _, e := range f.Entries {
		if (e.Kind == host.KindAuto || (e.Kind == host.KindAllow && e.Class == "auto")) && containsString(e.Ifaces, name) {
			hasAuto = true
			break
		}
	}
	if want == hasAuto {
		return
	}
	if !want {
		removeAutoReference(f, name, nl)
		return
	}
	idx, ok := findIface(f, name)
	if !ok {
		return
	}
	entry := host.Entry{Kind: host.KindAuto, Ifaces: []string{name}, Raw: fmt.Sprintf("auto %s%s", name, nl)}
	out := make([]host.Entry, 0, len(f.Entries)+1)
	out = append(out, f.Entries[:idx]...)
	out = append(out, entry)
	out = append(out, f.Entries[idx:]...)
	f.Entries = out
}

// removeAutoReference strips name out of any auto/allow-* line without
// touching its iface stanza (unlike removeIfaceAndAuto, used by delete
// ops).
func removeAutoReference(f *host.File, name, nl string) {
	out := make([]host.Entry, 0, len(f.Entries))
	for _, e := range f.Entries {
		switch e.Kind {
		case host.KindAuto, host.KindAllow, host.KindNoAutoDown, host.KindNoScripts:
			if containsString(e.Ifaces, name) {
				remaining := removeStringToken(e.Ifaces, name)
				if len(remaining) == 0 {
					continue
				}
				e.Ifaces = remaining
				e.Raw = regenerateIfaceListRaw(e, nl)
			}
		}
		out = append(out, e)
	}
	f.Entries = out
}
