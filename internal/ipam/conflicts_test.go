package ipam_test

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipam"
)

// TestService_Conflicts_TaggedAndConsistentWithGrid proves Conflicts()
// surfaces the same conflicts the per-subnet grid computes (both go through
// mergeSubnet), each tagged with the subnet it belongs to — the raw
// material the unified findings stream consumes.
func TestService_Conflicts_TaggedAndConsistentWithGrid(t *testing.T) {
	svc := newIpamTestService(t)
	ctx := context.Background()

	conflicts, err := svc.Conflicts(ctx)
	if err != nil {
		t.Fatalf("Conflicts: %v", err)
	}
	if len(conflicts) == 0 {
		t.Fatal("expected the ipam-lab fixture to produce at least one conflict")
	}

	validType := map[string]bool{"duplicate_ip": true, "observed_unallocated": true, "allocated_dark": true}
	byCIDR := map[string]int{}
	for _, sc := range conflicts {
		if sc.CIDR == "" {
			t.Errorf("conflict has no subnet CIDR: %+v", sc)
		}
		if !validType[sc.Conflict.Type] {
			t.Errorf("unexpected conflict type %q", sc.Conflict.Type)
		}
		byCIDR[sc.CIDR]++
	}

	// The conflicts reported for 10.50.0.0/24 must equal that subnet's own
	// grid conflict count — same mergeSubnet under both paths.
	grid, err := svc.Allocations(ctx, "10.50.0.0/24", ipam.GridOptions{})
	if err != nil {
		t.Fatalf("Allocations: %v", err)
	}
	if byCIDR["10.50.0.0/24"] != len(grid.Conflicts) {
		t.Errorf("Conflicts() for 10.50.0.0/24 = %d, grid = %d — they must agree", byCIDR["10.50.0.0/24"], len(grid.Conflicts))
	}
}
