// SPDX-License-Identifier: Apache-2.0

package snmp

// T-4013: read-only end to end. This file is this package's analogue of
// internal/mcp's stageonly.go (a closed set that the encoder rejects
// everything outside of, asserted exhaustively) and internal/plugin's
// frozen_interfaces_test.go (a reflection-shaped enumeration test, adapted
// here to a byte-tag enum since a wire PDU type is not a Go interface with a
// method set to reflect over). Both assertions are kept, same as those two
// files: the exhaustive behavioral one below, and a literal source scan a
// human reviewer can reproduce by hand with
// `grep -ri 'setrequest\|0xa3' internal/snmp/*.go`.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEncodePDU_RejectsEveryUndeclaredType is the exhaustive closed-set
// assertion: encodePDU must accept exactly the four PDUType values this
// package declares (GetRequestPDU, GetNextRequestPDU, GetBulkRequestPDU,
// GetResponsePDU) and reject every one of the other 252 byte values —
// SetRequest's 0xA3, InformRequest's 0xA6, SNMPv2-Trap's 0xA7, and Report's
// 0xA8 included, along with every value no SNMP PDU has ever used. Widening
// the allowed set (e.g. to add SetRequest support) requires editing the
// `allowed` map below in the same commit as the encoder, which is exactly
// the friction this test exists to add.
func TestEncodePDU_RejectsEveryUndeclaredType(t *testing.T) {
	allowed := map[PDUType]bool{
		GetRequestPDU:     true,
		GetNextRequestPDU: true,
		GetBulkRequestPDU: true,
		GetResponsePDU:    true,
	}
	oid := MustParseOID("1.3.6.1.2.1.1.1.0")
	for v := 0; v <= 0xff; v++ {
		pt := PDUType(v)
		_, err := encodePDU(pt, 1, 0, 0, []Varbind{{Name: oid}})
		switch {
		case allowed[pt] && err != nil:
			t.Errorf("PDUType 0x%02x is declared allowed but encodePDU rejected it: %v", byte(pt), err)
		case !allowed[pt] && err == nil:
			t.Errorf("PDUType 0x%02x is NOT declared allowed but encodePDU accepted it — "+
				"a write/trap/report PDU type must never be encodable", byte(pt))
		case !allowed[pt] && !errors.Is(err, ErrUnsupportedPDUType):
			t.Errorf("PDUType 0x%02x rejected with %v, want %v", byte(pt), err, ErrUnsupportedPDUType)
		}
	}
}

// TestEncodePDU_SetRequestTagSpecifically pins the one PDU type this card
// most needs to never be reachable: 0xA3, SetRequest-PDU's real wire tag
// (RFC 3416 §4), by name rather than only via the exhaustive loop above.
func TestEncodePDU_SetRequestTagSpecifically(t *testing.T) {
	const setRequestTag PDUType = 0xA3
	_, err := encodePDU(setRequestTag, 1, 0, 0, nil)
	if !errors.Is(err, ErrUnsupportedPDUType) {
		t.Fatalf("encoding SetRequest (0xA3) = %v, want %v", err, ErrUnsupportedPDUType)
	}
}

// TestSourceScan_NoSetPDUReachable is the grep-checkable half: no non-test
// .go file in this package's directory may mention "SetRequest" (by name)
// or "0xa3" (its tag, case-insensitive) anywhere — not in code, not in a
// comment referring to a future TODO. This runs on every `go test ./...`,
// so it is a stronger guarantee than a reviewer remembering to grep by hand,
// while still being reproducible by hand the same way.
func TestSourceScan_NoSetPDUReachable(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}
	forbidden := []string{"setrequest", "0xa3"}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		lower := strings.ToLower(string(b))
		for _, f := range forbidden {
			if strings.Contains(lower, f) {
				t.Errorf("%s: contains forbidden token %q — SNMP write PDUs must never be reachable from this read-only client", name, f)
			}
		}
	}
	if checked == 0 {
		t.Fatal("scanned zero .go files — test is not actually checking anything")
	}
}
