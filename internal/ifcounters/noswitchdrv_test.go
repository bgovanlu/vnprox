// SPDX-License-Identifier: Apache-2.0

package ifcounters

// T-4013 acceptance criterion 2: "A grep-checkable proof that this card's
// code path never calls switchdrv.SwitchDriver.SetPortConfig or any other
// write method — read path and guarded-push path share no call edge."
//
// The strongest version of that guarantee is architectural: this package
// does not import internal/switchdrv at all, so there is no Go value of
// type switchdrv.SwitchDriver in scope anywhere in it for a call to
// SetPortConfig (or PortConfig, PortNeighbor, Close) to even type-check
// against. TestSourceScan_NeverMentionsSwitchdrv checks that mechanically,
// on every `go test` run, the same shape as internal/snmp's
// noset_test.go — a reviewer can reproduce it by hand with
// `grep -ri switchdrv internal/ifcounters/*.go`.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceScan_NeverMentionsSwitchdrv(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue // includes _test.go files too: this file's own doc
			// comment mentions "switchdrv" in prose, which is exactly why
			// it is excluded below rather than here — see the explicit
			// skip.
		}
		if name == "noswitchdrv_test.go" || name == "doc.go" {
			continue // this file, and the package doc comment, both discuss
			// the absence of that dependency in prose by name — excluded so
			// they don't trip their own scan. Every other file in the
			// package (the actual implementation) has no legitimate reason
			// to mention it at all.
		}
		checked++
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if strings.Contains(strings.ToLower(string(b)), "switchdrv") {
			t.Errorf("%s: mentions switchdrv — T-4013's read path and switchdrv's guarded-push "+
				"path must share no call edge (AC2)", name)
		}
	}
	if checked == 0 {
		t.Fatal("scanned zero .go files — test is not actually checking anything")
	}
}
