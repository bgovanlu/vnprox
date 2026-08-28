// SPDX-License-Identifier: Apache-2.0

package findings

import "testing"

type stubCapacity struct{ fs []Finding }

func (s stubCapacity) Findings() []Finding { return s.fs }

func TestCapacityFindings_NilSafe(t *testing.T) {
	if got := capacityFindings(nil); got != nil {
		t.Errorf("capacityFindings(nil) = %v, want nil", got)
	}
}

func TestEngine_IncludesCapacityFindings(t *testing.T) {
	f := Finding{
		ID:       "capacity:capacity_link_forecast|iface:pve1:vmbr1",
		Source:   SourceCapacity,
		Check:    "capacity_link_forecast",
		Severity: SeverityWarning,
		Detail:   "link iface:pve1:vmbr1 utilization is trending up",
		Nodes:    []string{"pve1"},
		Refs:     []string{"iface:pve1:vmbr1"},
	}
	e := New(Config{Capacity: stubCapacity{fs: []Finding{f}}})

	got := e.Findings()
	var found bool
	for _, x := range got {
		if x.ID == f.ID && x.Source == SourceCapacity {
			found = true
		}
	}
	if !found {
		t.Fatalf("engine Findings() did not include the capacity finding; got %d findings", len(got))
	}
}
