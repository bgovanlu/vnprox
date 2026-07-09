package ifaces

import (
	"fmt"
	"strings"

	"github.com/bgovanlu/vnprox/internal/host"
)

// managedByComment renders the vnprox-provenance marker every newly
// created stanza carries as the first line of its body (task card T-204).
func managedByComment(changesetID string) host.BodyItem {
	return host.BodyItem{
		Kind: host.BodyComment,
		Raw:  fmt.Sprintf("\t# managed by vnprox (changeset %s)\n", changesetID),
	}
}

// optionItem renders a single "\tkey value\n" option line.
func optionItem(key, value string) host.BodyItem {
	return host.BodyItem{
		Kind:  host.BodyOption,
		Key:   key,
		Value: value,
		Raw:   fmt.Sprintf("\t%s %s\n", key, value),
	}
}

// setOptionList replaces every existing key option in body with one option
// item per value (in the given order), at the position of the first
// existing occurrence (later duplicates are dropped, matching how
// interfaces(5)/ifupdown2 only honor the values present — a stanza should
// not accumulate stale duplicate option lines across edits). If key is not
// present at all, the new items are inserted just before any trailing run
// of blank body lines (rather than appended after them) — a stanza parsed
// from the middle of a file often has a trailing BodyBlank item (the blank
// line separating it from the next stanza belongs to the still-open
// stanza's body until the next reserved keyword closes it, see
// interfaces_parse.go), and appending past that would open a blank line in
// the middle of the rendered stanza. Passing an empty values slice removes
// the key entirely (equivalent to removeOptionKey).
func setOptionList(body []host.BodyItem, key string, values []string) []host.BodyItem {
	out := make([]host.BodyItem, 0, len(body)+len(values))
	inserted := false
	for _, item := range body {
		if item.Kind == host.BodyOption && item.Key == key {
			if !inserted {
				for _, v := range values {
					out = append(out, optionItem(key, v))
				}
				inserted = true
			}
			continue
		}
		out = append(out, item)
	}
	if !inserted {
		items := make([]host.BodyItem, len(values))
		for i, v := range values {
			items[i] = optionItem(key, v)
		}
		out = insertBeforeTrailingBlanks(out, items)
	}
	return out
}

// insertBeforeTrailingBlanks inserts items into body just before any
// trailing run of BodyBlank items, or at the end if there is none.
func insertBeforeTrailingBlanks(body []host.BodyItem, items []host.BodyItem) []host.BodyItem {
	end := len(body)
	for end > 0 && body[end-1].Kind == host.BodyBlank {
		end--
	}
	out := make([]host.BodyItem, 0, len(body)+len(items))
	out = append(out, body[:end]...)
	out = append(out, items...)
	out = append(out, body[end:]...)
	return out
}

// setOption is setOptionList for a single scalar option; an empty value
// removes the key (see removeOptionKey).
func setOption(body []host.BodyItem, key, value string) []host.BodyItem {
	if value == "" {
		return removeOptionKey(body, key)
	}
	return setOptionList(body, key, []string{value})
}

// removeOptionKey drops every BodyOption item named key, leaving everything
// else (including comments and blank lines) untouched.
func removeOptionKey(body []host.BodyItem, key string) []host.BodyItem {
	out := make([]host.BodyItem, 0, len(body))
	for _, item := range body {
		if item.Kind == host.BodyOption && item.Key == key {
			continue
		}
		out = append(out, item)
	}
	return out
}

// getOption returns the value of the first BodyOption named key, and
// whether it was found.
func getOption(body []host.BodyItem, key string) (string, bool) {
	for _, item := range body {
		if item.Kind == host.BodyOption && item.Key == key {
			return item.Value, true
		}
	}
	return "", false
}

// addToken appends tok to a space-separated option value if not already
// present, preserving the existing tokens' order (new tokens go last —
// this is the "deterministic ordering" for bridge-ports/bond-slaves/
// ovs_ports appends: it depends only on op application order).
func addToken(value, tok string) string {
	toks := strings.Fields(value)
	for _, t := range toks {
		if t == tok {
			return value
		}
	}
	toks = append(toks, tok)
	return strings.Join(toks, " ")
}

// removeToken removes tok from a space-separated option value, preserving
// the relative order of the remaining tokens.
func removeToken(value, tok string) string {
	toks := strings.Fields(value)
	out := toks[:0:0]
	for _, t := range toks {
		if t != tok {
			out = append(out, t)
		}
	}
	return strings.Join(out, " ")
}

// isOVSKind reports whether k is one of the OVS entity kinds
// (inventory.KindOVSBridge / inventory.KindOVSBond), which select the
// ovs_* option vocabulary instead of the Linux bridge-*/bond-* one.
func isOVSKind(k string) bool {
	return k == "ovs-bridge" || k == "ovs-bond"
}

// newIfaceEntry builds a bare (no body) "iface <name> <family> <method>"
// Entry ready to have body items appended.
func newIfaceEntry(name, family, method string) host.Entry {
	return host.Entry{
		Kind:   host.KindIface,
		Name:   name,
		Family: family,
		Method: method,
		Raw:    fmt.Sprintf("iface %s %s %s\n", name, family, method),
	}
}

// endsWithBlankLine reports whether s (a rendered file, or prefix of one)
// already ends with a blank separator line, i.e. "\n\n" (or "\r\n\r\n" for
// CRLF files, or "" for an empty file — nothing to separate from).
func endsWithBlankLine(s string) bool {
	if s == "" {
		return true
	}
	trimmed := strings.TrimRight(s, "\r\n")
	// Count trailing line terminators stripped; if at least two logical
	// newlines were removed, there was a blank line.
	return len(s)-len(trimmed) >= 2 && strings.Count(s[len(trimmed):], "\n") >= 2
}

// prepareAppend ensures f's rendered content is newline-terminated and ends
// with exactly one blank separator line, so a subsequently appended stanza
// starts cleanly on its own line with one blank line of separation from
// whatever came before — whether that is original file content or a
// previously appended stanza in the same Mutate pass (appendStanza always
// leaves the file ending in one blank line, so a second call here is a
// no-op). A brand-new/empty file is left alone (nothing to separate from).
func prepareAppend(f *host.File) {
	content := f.Render()
	if content == "" {
		return
	}
	if !strings.HasSuffix(content, "\n") {
		f.Entries = append(f.Entries, host.Entry{Kind: host.KindBlank, Raw: "\n"})
		content += "\n"
	}
	if !endsWithBlankLine(content) {
		f.Entries = append(f.Entries, host.Entry{Kind: host.KindBlank, Raw: "\n"})
	}
}

// autoPrefixEntry builds the "auto <name>" entry that precedes a
// newly-created Linux stanza when it should start at boot.
func autoPrefixEntry(name string) host.Entry {
	return host.Entry{Kind: host.KindAuto, Ifaces: []string{name}, Raw: fmt.Sprintf("auto %s\n", name)}
}

// allowPrefixEntry builds the "allow-<class> <name>" entry ifupdown2's OVS
// glue uses in place of "auto <name>" (see testdata/interfaces/
// 04-ovs-bridge.interfaces and 05-ovs-bond.interfaces: "allow-ovs <bridge>"
// for an OVS bridge itself, "allow-<bridge> <port>" for each of its ports,
// including an OVS bond).
func allowPrefixEntry(class, name string) host.Entry {
	return host.Entry{
		Kind: host.KindAllow, Class: class, Ifaces: []string{name},
		Raw: fmt.Sprintf("allow-%s %s\n", class, name),
	}
}

// appendStanzaRaw appends a newly created stanza — an optional preceding
// entry (an "auto"/"allow-*" line; nil to omit it entirely) followed by
// the iface entry itself — to the end of f, separated from whatever
// precedes it by prepareAppend, and leaves exactly one trailing blank line
// after it so the next append call (or end of file) stays visually
// separated.
func appendStanzaRaw(f *host.File, prefix *host.Entry, iface host.Entry) {
	prepareAppend(f)
	if prefix != nil {
		f.Entries = append(f.Entries, *prefix)
	}
	f.Entries = append(f.Entries, iface)
	f.Entries = append(f.Entries, host.Entry{Kind: host.KindBlank, Raw: "\n"})
}

// appendStanza is appendStanzaRaw with a plain "auto <name>" prefix (or
// none, if autostart is false) — the common case for Linux bond/bridge/vlan
// creates.
func appendStanza(f *host.File, autostart bool, name string, iface host.Entry) {
	var prefix *host.Entry
	if autostart {
		p := autoPrefixEntry(name)
		prefix = &p
	}
	appendStanzaRaw(f, prefix, iface)
}

// removeIfaceAndAuto removes every KindIface entry named name (all address
// families/inherits stanzas for that name) plus name's reference from any
// auto/allow-* line: dropped entirely from a single-name line, or stripped
// out (regenerating that line's Raw) from a multi-name line.
func removeIfaceAndAuto(f *host.File, name string) {
	removeAutoReference(f, name)
	out := make([]host.Entry, 0, len(f.Entries))
	for _, e := range f.Entries {
		if e.Kind == host.KindIface && e.Name == name {
			continue
		}
		out = append(out, e)
	}
	f.Entries = out
}

func regenerateIfaceListRaw(e host.Entry) string {
	var kw string
	switch e.Kind {
	case host.KindAuto:
		kw = "auto"
	case host.KindAllow:
		kw = "allow-" + e.Class
	case host.KindNoAutoDown:
		kw = "no-auto-down"
	case host.KindNoScripts:
		kw = "no-scripts"
	}
	return fmt.Sprintf("%s %s\n", kw, strings.Join(e.Ifaces, " "))
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func removeStringToken(ss []string, s string) []string {
	out := make([]string, 0, len(ss))
	for _, v := range ss {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// findIface returns the index of the first KindIface entry named name, and
// whether it was found.
func findIface(f *host.File, name string) (int, bool) {
	for i := range f.Entries {
		if f.Entries[i].Kind == host.KindIface && f.Entries[i].Name == name {
			return i, true
		}
	}
	return 0, false
}
