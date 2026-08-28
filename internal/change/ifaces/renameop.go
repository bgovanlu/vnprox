// SPDX-License-Identifier: Apache-2.0

package ifaces

import (
	"fmt"
	"strings"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// IfaceRename renames a logical iface stanza (bridge/bond/vlan) in place. It
// is deliberately minimal-edit and formatting-preserving: it swaps only the
// whole-token occurrences of the old name in the stanza header, its
// auto/allow-* lines, and the in-file reference options that name it
// (bridge-ports/ovs_ports/bond-slaves/ovs_bonds/ovs_bridge/vlan-raw-device),
// leaving every other byte of those lines — indentation, inline comments,
// unrelated tokens — untouched. See OpIfaceRename's doc comment for why
// physnic (udev) renames and guest bridge= bindings are out of scope.
type IfaceRename struct {
	Target  inventory.Ref
	NewName string
}

func (o IfaceRename) Kind() OpType       { return OpIfaceRename }
func (o IfaceRename) Ref() inventory.Ref { return o.Target }

// referenceKeys are the interfaces(5) option keys whose value is (or
// contains) another interface's name — the ones a rename must rewrite. An
// unrelated option that happens to contain the same text (an address, a
// comment, an "up" script) is intentionally left alone: only these keys are
// scanned, and only whole-token matches within them are swapped.
var referenceKeys = map[string]bool{
	"bridge-ports":    true,
	"ovs_ports":       true,
	"bond-slaves":     true,
	"ovs_bonds":       true,
	"ovs_bridge":      true,
	"vlan-raw-device": true,
}

func mutateIfaceRename(f *host.File, o IfaceRename, nl string) error {
	old := o.Target.ID
	newName := o.NewName
	if newName == "" {
		return fmt.Errorf("ifaces: iface.rename %q: new name must not be empty", old)
	}
	if _, ok := findIface(f, old); !ok {
		return fmt.Errorf("ifaces: iface.rename %q: %w", old, ErrNotFound)
	}
	if newName == old {
		return nil // no-op rename
	}

	for i := range f.Entries {
		e := &f.Entries[i]
		switch e.Kind {
		case host.KindIface:
			if e.Name == old {
				// The stanza being renamed: rewrite its header name.
				e.Name = newName
				e.Raw = replaceWholeToken(e.Raw, old, newName)
			}
			// Any stanza (including the renamed one) may reference `old` in a
			// port/slave/raw-device option — rewrite those in place.
			renameBodyReferences(e, old, newName)
		case host.KindAuto, host.KindAllow, host.KindNoAutoDown, host.KindNoScripts:
			renamed := false
			for j, name := range e.Ifaces {
				if name == old {
					e.Ifaces[j] = newName
					renamed = true
				}
			}
			if renamed {
				e.Raw = replaceWholeToken(e.Raw, old, newName)
			}
		}
	}
	return nil
}

// renameBodyReferences rewrites whole-token occurrences of old in e's
// reference-carrying option lines (referenceKeys), preserving each line's
// original formatting.
func renameBodyReferences(e *host.Entry, old, newName string) {
	for i := range e.Body {
		item := &e.Body[i]
		if item.Kind != host.BodyOption || !referenceKeys[item.Key] {
			continue
		}
		newValue, changed := replaceValueToken(item.Value, old, newName)
		if !changed {
			continue
		}
		item.Value = newValue
		item.Raw = replaceWholeToken(item.Raw, old, newName)
	}
}

// replaceValueToken replaces every whitespace-delimited token equal to old
// in value with newName, returning the rewritten value and whether anything
// changed. Order and single-space joining match how the parser reads these
// lists back.
func replaceValueToken(value, old, newName string) (string, bool) {
	toks := strings.Fields(value)
	changed := false
	for i, t := range toks {
		if t == old {
			toks[i] = newName
			changed = true
		}
	}
	if !changed {
		return value, false
	}
	return strings.Join(toks, " "), true
}

// replaceWholeToken replaces occurrences of old in s that are bounded by
// whitespace or a string edge (i.e. whole tokens), leaving substrings that
// merely contain old — e.g. "vmbr0.100" when renaming "vmbr0" — untouched.
// It preserves every other byte (indentation, inline comments, the trailing
// newline), so a renamed line differs from the original only in the token.
func replaceWholeToken(s, old, newTok string) string {
	if old == "" {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		j := strings.Index(s[i:], old)
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		j += i
		b.WriteString(s[i:j])
		beforeOK := j == 0 || isTokenBoundary(s[j-1])
		after := j + len(old)
		afterOK := after == len(s) || isTokenBoundary(s[after])
		if beforeOK && afterOK {
			b.WriteString(newTok)
		} else {
			b.WriteString(old)
		}
		i = after
	}
	return b.String()
}

func isTokenBoundary(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
