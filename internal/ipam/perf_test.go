package ipam

import (
	"fmt"
	"testing"
	"time"
)

// synthetic16 builds a /16's worth of sparse allocations/observations (2000
// occupied addresses spread across the whole range — a generously busy
// subnet, real deployments are far sparser) for the paging perf note in
// this task's report: docs/features/ipam.md §2's "larger subnets render as
// paged block summaries" must not force a per-request scan of the full
// 65,536-address space.
func synthetic16(n int) ([]Allocation, []Observation) {
	allocs := make([]Allocation, 0, n)
	obs := make([]Observation, 0, n/2)
	for i := 0; i < n; i++ {
		// offset*31 mod 65536 visits all 65536 /16 addresses exactly once as
		// i ranges 0..65535 (31 is odd, hence coprime with the power-of-two
		// modulus) — n well under that never repeats an address.
		offset := (i * 31) % 65536
		hi := offset / 256
		lo := offset % 256
		ip := fmt.Sprintf("10.60.%d.%d", hi, lo)
		allocs = append(allocs, Allocation{IP: ip, MAC: fmt.Sprintf("aa:bb:cc:dd:%02x:%02x", hi, lo), VMID: 1000 + i})
		if i%3 == 0 {
			obs = append(obs, Observation{IP: ip, MAC: fmt.Sprintf("aa:bb:cc:dd:%02x:%02x", hi, lo), Source: "guest-agent"})
		}
	}
	return allocs, obs
}

// TestPaged16_BlockSummaryIsFastAndBounded is T-405 acceptance criterion 5's
// backend-side perf note: a /16's block-summary computation (the default,
// no-`?block=` response for a subnet this large) touches work proportional
// to the number of occupied addresses, not the 65,536-address space, and
// completes well within a single request's budget even at a deliberately
// busy 2000-allocation fixture.
func TestPaged16_BlockSummaryIsFastAndBounded(t *testing.T) {
	allocs, obs := synthetic16(2000)
	known := knownGuests{}

	start := time.Now()
	cellMap, _ := mergeSubnet(allocs, obs, known, "10.60.0.1")
	blocks, ok := blockCIDRs("10.60.0.0/16")
	if !ok {
		t.Fatal("blockCIDRs: not ok")
	}
	summaries := bucketIntoBlocks(blocks, cellMap)
	elapsed := time.Since(start)

	if len(summaries) != 256 {
		t.Fatalf("len(summaries) = %d, want 256", len(summaries))
	}
	var totalAllocated int
	for _, s := range summaries {
		totalAllocated += s.Allocated
	}
	if totalAllocated != 2000 {
		t.Errorf("sum of per-block Allocated = %d, want 2000 (every occupied address accounted for exactly once)", totalAllocated)
	}
	// A generous ceiling for CI-noise tolerance, not a tight perf assertion:
	// the point is "milliseconds, not seconds" — see this task's report for
	// the measured wall-clock number on the dev machine this was authored
	// on.
	if elapsed > 200*time.Millisecond {
		t.Errorf("block-summary computation took %s, want well under 200ms", elapsed)
	}
	t.Logf("mergeSubnet + bucketIntoBlocks over a /16 (2000 allocations): %s", elapsed)
}

// TestPaged16_OneBlockRenderIsBoundedTo256Cells confirms drilling into one
// block of a /16 (the `?block=` path) always renders exactly one /24's
// worth of cells, regardless of how busy the rest of the /16 is — the
// frontend never receives more than 256 Cell objects in a single response,
// whatever the parent subnet's size.
func TestPaged16_OneBlockRenderIsBoundedTo256Cells(t *testing.T) {
	allocs, obs := synthetic16(2000)
	target := "10.60.7.0/24"
	blockAllocs := filterAllocsForCIDR(allocs, target)
	blockObs := observationsForCIDR(target, obs)
	cellMap, _ := mergeSubnet(blockAllocs, blockObs, knownGuests{}, "")
	addrs, ok := hostAddresses(target)
	if !ok || len(addrs) != 254 {
		t.Fatalf("hostAddresses(%s) = %v, %v", target, addrs, ok)
	}
	if len(cellMap) > len(addrs) {
		t.Fatalf("block cell map has %d entries, more than the block's %d addresses", len(cellMap), len(addrs))
	}
}
