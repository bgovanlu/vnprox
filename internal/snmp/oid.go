// SPDX-License-Identifier: Apache-2.0

package snmp

import (
	"fmt"
	"strconv"
	"strings"
)

// maxOIDComponents bounds both parsed and decoded OIDs. Real MIB OIDs never
// come close to this (IF-MIB table entries are ~11 components); the bound
// exists purely to keep a hostile/buggy device's response from growing an
// unbounded slice (defensive parsing per this package's doc comment).
const maxOIDComponents = 128

// OID is a dotted object identifier, e.g. 1.3.6.1.2.1.2.2.1.14 (ifInErrors).
type OID []uint32

// ParseOID parses a dotted-decimal string ("1.3.6.1.2.1.2.2.1.14") into an
// OID. Returns an error for anything that isn't a bounded sequence of
// non-negative integers with at least two components (BER's x*40+y encoding
// of the first two sub-identifiers requires at least that many).
func ParseOID(s string) (OID, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), ".")
	if s == "" {
		return nil, fmt.Errorf("snmp: empty OID")
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("snmp: OID %q has fewer than 2 components", s)
	}
	if len(parts) > maxOIDComponents {
		return nil, fmt.Errorf("snmp: OID %q has too many components (%d > %d)", s, len(parts), maxOIDComponents)
	}
	out := make(OID, len(parts))
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("snmp: OID %q: component %q is not a valid non-negative integer: %w", s, p, err)
		}
		out[i] = uint32(v)
	}
	return out, nil
}

// MustParseOID is ParseOID for the fixed, compile-time-known IF-MIB OIDs
// this codebase names (mib.go) — panics on error, so it must never be
// called with anything derived from network input or user input.
func MustParseOID(s string) OID {
	oid, err := ParseOID(s)
	if err != nil {
		panic(err)
	}
	return oid
}

// String renders the OID in dotted-decimal form.
func (o OID) String() string {
	if len(o) == 0 {
		return ""
	}
	var b strings.Builder
	for i, v := range o {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.FormatUint(uint64(v), 10))
	}
	return b.String()
}

// Append returns a new OID with extra components appended — used to build a
// table-column OID's per-row instance (e.g. ifInErrors + ifIndex).
func (o OID) Append(components ...uint32) OID {
	out := make(OID, len(o)+len(components))
	copy(out, o)
	copy(out[len(o):], components)
	return out
}

// Equal reports whether o and other name the same OID.
func (o OID) Equal(other OID) bool {
	if len(o) != len(other) {
		return false
	}
	for i := range o {
		if o[i] != other[i] {
			return false
		}
	}
	return true
}

// HasPrefix reports whether o starts with every component of prefix, in
// order — used to check a walked OID is still within the table column being
// walked.
func (o OID) HasPrefix(prefix OID) bool {
	if len(o) < len(prefix) {
		return false
	}
	for i := range prefix {
		if o[i] != prefix[i] {
			return false
		}
	}
	return true
}
