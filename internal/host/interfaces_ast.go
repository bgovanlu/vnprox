// SPDX-License-Identifier: Apache-2.0

package host

// This file defines a lossless AST for /etc/network/interfaces(5)
// (ifupdown2 stanza syntax). "Lossless" here means: every byte of an input
// file that Parse accepts is retained somewhere in the tree (in an Entry's
// or BodyItem's Raw field), so File.Render() reproduces the original file
// byte-for-byte when nothing in the tree has been mutated. This is the
// property T-204 (the future interfaces writer/differ) depends on: it can
// read structured fields (Name/Family/Method/Key/Value) to decide what to
// change, but rendering always falls back to raw text for anything it did
// not touch.
//
// Per interfaces(5): indentation is cosmetic only ("options are usually
// indented for clarity ... but are not required to be"). What determines
// structure is a fixed set of reserved leading keywords (auto, allow-*,
// no-auto-down, no-scripts, rename, source, source-directory, mapping,
// iface); any other non-blank, non-comment line occurring while a
// mapping/iface stanza is open is an option line belonging to that stanza.

// EntryKind identifies which of the interfaces(5) stanza forms an Entry
// represents.
type EntryKind int

const (
	// KindBlank is a blank (whitespace-only) line.
	KindBlank EntryKind = iota
	// KindComment is a line whose first non-whitespace character is '#'.
	// interfaces(5) does not support end-of-line comments, so any line
	// recognized as a comment is a comment in its entirety.
	KindComment
	// KindAuto is an "auto <ifaces...>" line.
	KindAuto
	// KindAllow is an "allow-<class> <ifaces...>" line (e.g.
	// allow-hotplug, allow-auto, allow-ovs).
	KindAllow
	// KindNoAutoDown is a "no-auto-down <ifaces...>" line.
	KindNoAutoDown
	// KindNoScripts is a "no-scripts <ifaces...>" line.
	KindNoScripts
	// KindRename is a "rename CUR=NEW ..." line.
	KindRename
	// KindSource is a "source <path>" line.
	KindSource
	// KindSourceDirectory is a "source-directory <path>" line.
	KindSourceDirectory
	// KindMapping is a "mapping <pattern...>" stanza header; its body
	// (script/map and any other lines) is held in Body.
	KindMapping
	// KindIface is an "iface <name> <family> <method> [inherits ...]"
	// stanza header; its option lines are held in Body.
	KindIface
)

// String returns a short human-readable name for k, used in error messages
// and tests.
func (k EntryKind) String() string {
	switch k {
	case KindBlank:
		return "blank"
	case KindComment:
		return "comment"
	case KindAuto:
		return "auto"
	case KindAllow:
		return "allow"
	case KindNoAutoDown:
		return "no-auto-down"
	case KindNoScripts:
		return "no-scripts"
	case KindRename:
		return "rename"
	case KindSource:
		return "source"
	case KindSourceDirectory:
		return "source-directory"
	case KindMapping:
		return "mapping"
	case KindIface:
		return "iface"
	default:
		return "unknown"
	}
}

// BodyItemKind identifies what kind of line one BodyItem represents inside
// an iface/mapping stanza.
type BodyItemKind int

const (
	// BodyBlank is a blank line inside a stanza body.
	BodyBlank BodyItemKind = iota
	// BodyComment is a comment line inside a stanza body (e.g. the
	// per-interface "\t#comment" lines PVE renders for the Comments
	// field).
	BodyComment
	// BodyOption is a "key value..." option line inside a stanza body
	// (bridge-ports, bond-mode, mtu, ovs_type, ovs_bonds, wireless-*,
	// and any other option regardless of whether this package knows its
	// semantics).
	BodyOption
)

// BodyItem is one line inside an iface or mapping stanza's body, in
// original file order.
type BodyItem struct {
	Raw   string
	Key   string
	Value string
	Kind  BodyItemKind
}

// Entry is one top-level construct in an interfaces(5) file, in original
// file order.
type Entry struct {
	Raw      string
	Class    string
	Path     string
	Pattern  string
	Name     string
	Family   string
	Method   string
	Ifaces   []string
	Renames  []string
	Inherits []string
	Body     []BodyItem
	Kind     EntryKind
}

// Options returns the BodyOption items in e.Body, skipping comments and
// blank lines. It is a convenience accessor for consumers that only care
// about the parsed key/value options of an iface or mapping stanza.
func (e *Entry) Options() []BodyItem {
	out := make([]BodyItem, 0, len(e.Body))
	for _, item := range e.Body {
		if item.Kind == BodyOption {
			out = append(out, item)
		}
	}
	return out
}

// Get returns the value of the first option named key in e's body (case
// sensitive, matching interfaces(5) option names), and whether it was
// found. For repeated options (e.g. multiple "up" script lines), use
// Options() and filter manually.
func (e *Entry) Get(key string) (string, bool) {
	for _, item := range e.Body {
		if item.Kind == BodyOption && item.Key == key {
			return item.Value, true
		}
	}
	return "", false
}

// File is a fully-parsed /etc/network/interfaces(5) document.
type File struct {
	// Entries holds every top-level construct in the file, in original
	// order, including blank lines and comments outside any stanza.
	Entries []Entry
}

// Ifaces returns every KindIface entry in the file, in original order.
// Because interfaces(5) allows multiple stanzas for the same logical name
// (one per address family, or template extension via "inherits"), this may
// contain more than one Entry per name.
func (f *File) Ifaces() []*Entry {
	var out []*Entry
	for i := range f.Entries {
		if f.Entries[i].Kind == KindIface {
			out = append(out, &f.Entries[i])
		}
	}
	return out
}

// Iface returns the first KindIface entry named name, and whether it was
// found.
func (f *File) Iface(name string) (*Entry, bool) {
	for i := range f.Entries {
		if f.Entries[i].Kind == KindIface && f.Entries[i].Name == name {
			return &f.Entries[i], true
		}
	}
	return nil, false
}

// AutoIfaces returns the union of all interface names/patterns named by
// "auto" and "allow-auto" lines (the two are synonyms per interfaces(5)),
// in original order, without deduplication.
func (f *File) AutoIfaces() []string {
	var out []string
	for _, e := range f.Entries {
		switch {
		case e.Kind == KindAuto:
			out = append(out, e.Ifaces...)
		case e.Kind == KindAllow && e.Class == "auto":
			out = append(out, e.Ifaces...)
		}
	}
	return out
}

// Sources returns the paths named by "source" and "source-directory"
// lines, in original order.
func (f *File) Sources() []Entry {
	var out []Entry
	for _, e := range f.Entries {
		if e.Kind == KindSource || e.Kind == KindSourceDirectory {
			out = append(out, e)
		}
	}
	return out
}

// Render reproduces the file's source text. For a File produced by Parse
// without any subsequent mutation of its Entries/BodyItems, Render(f) ==
// the original input, byte for byte: every Entry and BodyItem carries its
// own exact source text in Raw, and Render does nothing but concatenate
// those in order. This is deliberate — see the package-level comment above
// for why raw text, not regenerated formatting, is the source of truth.
func (f *File) Render() string {
	var n int
	for _, e := range f.Entries {
		n += len(e.Raw)
		for _, b := range e.Body {
			n += len(b.Raw)
		}
	}
	buf := make([]byte, 0, n)
	for _, e := range f.Entries {
		buf = append(buf, e.Raw...)
		for _, b := range e.Body {
			buf = append(buf, b.Raw...)
		}
	}
	return string(buf)
}
