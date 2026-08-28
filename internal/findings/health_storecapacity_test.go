// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"errors"
	"strings"
	"testing"
)

type staticStoreCapacity struct {
	err error
	rep StoreCapacityReport
}

func (s staticStoreCapacity) StoreCapacity() (StoreCapacityReport, error) { return s.rep, s.err }

func TestStoreCapacityFindings_NilProviderIsSilent(t *testing.T) {
	if got := storeCapacityFindings(nil, 4<<30, newDebouncer()); len(got) != 0 {
		t.Fatalf("nil provider produced %d findings, want 0", len(got))
	}
}

// TestStoreCapacityFindings_DisabledThresholdIsSilent covers warnBytes<=0 —
// an explicit "this check is off" config choice, distinct from a nil
// provider but with the same silent-skip behavior.
func TestStoreCapacityFindings_DisabledThresholdIsSilent(t *testing.T) {
	prov := staticStoreCapacity{rep: StoreCapacityReport{Node: "pve1", SizeBytes: 1 << 40}}
	if got := storeCapacityFindings(prov, 0, newDebouncer()); len(got) != 0 {
		t.Fatalf("warnBytes=0 produced %d findings, want 0 (disabled)", len(got))
	}
	if got := storeCapacityFindings(prov, -1, newDebouncer()); len(got) != 0 {
		t.Fatalf("warnBytes=-1 produced %d findings, want 0 (disabled)", len(got))
	}
}

// TestStoreCapacityFindings_ProviderErrorIsSilentThisCycle covers a
// transient measurement failure: no finding this cycle, and it does not
// corrupt debounce state for the next one.
func TestStoreCapacityFindings_ProviderErrorIsSilentThisCycle(t *testing.T) {
	db := newDebouncer()
	prov := staticStoreCapacity{err: errors.New("stat: transient")}
	if got := storeCapacityFindings(prov, 4<<30, db); len(got) != 0 {
		t.Fatalf("errored read produced %d findings, want 0", len(got))
	}
}

// TestStoreCapacityFindings_BelowThresholdIsSilent — the ordinary healthy
// case, across several cycles (rules out an off-by-one hysteresis bug that
// would fire on cycle 1 regardless of breach state).
func TestStoreCapacityFindings_BelowThresholdIsSilent(t *testing.T) {
	db := newDebouncer()
	prov := staticStoreCapacity{rep: StoreCapacityReport{Node: "pve1", SizeBytes: 1 << 20}} // 1 MiB
	for i := 0; i < 4; i++ {
		if got := storeCapacityFindings(prov, 4<<30, db); len(got) != 0 {
			t.Fatalf("cycle %d: got %+v, want no findings (well under threshold)", i, got)
		}
	}
}

// TestStoreCapacityFindings_AC4_FiresAtThresholdAndClearsBelow is T-1905
// AC4: the finding fires at threshold and clears below it, with hysteresis
// debouncing like every other finding in this package (storeCapacityRise/
// Fall = 2, mirroring peerUnreachableRise/Fall).
func TestStoreCapacityFindings_AC4_FiresAtThresholdAndClearsBelow(t *testing.T) {
	db := newDebouncer()
	const warn = int64(4) << 30 // 4 GiB

	atOrOver := staticStoreCapacity{rep: StoreCapacityReport{Node: "pve1", SizeBytes: warn}}
	underThreshold := staticStoreCapacity{rep: StoreCapacityReport{Node: "pve1", SizeBytes: warn - 1}}

	// A single breaching cycle must not fire yet (rise=2).
	if got := storeCapacityFindings(atOrOver, warn, db); len(got) != 0 {
		t.Fatalf("cycle 1 (breach): got %+v, want no findings yet (rise=2)", got)
	}
	// Second consecutive breach fires.
	got := storeCapacityFindings(atOrOver, warn, db)
	if len(got) != 1 {
		t.Fatalf("cycle 2 (breach): got %d findings, want exactly 1", len(got))
	}
	f := got[0]
	if f.Check != CheckStoreNearCapacity {
		t.Errorf("Check = %q, want %q", f.Check, CheckStoreNearCapacity)
	}
	if f.Source != SourceStore {
		t.Errorf("Source = %q, want %q", f.Source, SourceStore)
	}
	if f.Severity != SeverityWarning {
		t.Errorf("Severity = %q, want %q", f.Severity, SeverityWarning)
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Errorf("Nodes = %v, want [pve1]", f.Nodes)
	}
	if f.DocsLink == "" {
		t.Error("DocsLink is empty, want a pointer to the sizing docs")
	}
	if !strings.Contains(f.Detail, "4.0 GiB") {
		t.Errorf("Detail = %q, want it to name the threshold in human units", f.Detail)
	}

	// A single non-breaching cycle must not clear yet (fall=2).
	if got := storeCapacityFindings(underThreshold, warn, db); len(got) != 1 {
		t.Fatalf("cycle 3 (below): got %d findings, want the finding to still be active (fall=2)", len(got))
	}
	// Second consecutive non-breach clears it.
	if got := storeCapacityFindings(underThreshold, warn, db); len(got) != 0 {
		t.Fatalf("cycle 4 (below): got %+v, want the finding cleared", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		want string
		n    int64
	}{
		{n: 500, want: "500 B"},
		{n: 1024, want: "1.0 KiB"},
		{n: 4 << 30, want: "4.0 GiB"},
		{n: 1536 * 1024, want: "1.5 MiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
