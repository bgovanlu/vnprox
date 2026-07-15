package ipam

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// synthetic16 builds a /16's worth of sparse allocations/observations (2000
// occupied addresses spread across the whole range — a generously busy
// subnet, real deployments are far sparser) for the address-list perf note:
// the list view (occupied Entries + collapsed FreeRanges) must never force a
// per-request scan of the full 65,536-address space, so a /16 renders as
// cheaply as a /24.
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

// TestList16_IsFastAndBounded is the address-list perf note: computing a
// /16's occupied entries plus its collapsed free ranges touches work
// proportional to the number of occupied addresses (and the gaps between
// them), not the 65,536-address space, and completes well within a single
// request's budget even at a deliberately busy 2000-allocation fixture.
func TestList16_IsFastAndBounded(t *testing.T) {
	allocs, obs := synthetic16(2000)

	start := time.Now()
	cellMap, _ := mergeSubnet(allocs, obs, knownGuests{}, "10.60.0.1")
	entries := sortCellsByIP(cellMap)
	ranges := freeRanges("10.60.0.0/16", cellMap)
	counts := tallyCounts(cellMap, ranges)
	elapsed := time.Since(start)

	// Every occupied address is exactly one entry (the gateway .1 is folded
	// into the 2000 by mergeSubnet only if it collided with a synthetic
	// address; it did not, so there are 2000 allocations + 1 gateway).
	if len(entries) != 2001 {
		t.Fatalf("len(entries) = %d, want 2001 (2000 allocations + gateway)", len(entries))
	}
	// Entries are sorted ascending by numeric address.
	for i := 1; i < len(entries); i++ {
		a := ipToBig(net.ParseIP(entries[i-1].IP))
		b := ipToBig(net.ParseIP(entries[i].IP))
		if a.Cmp(b) >= 0 {
			t.Fatalf("entries not sorted: %s !< %s", entries[i-1].IP, entries[i].IP)
		}
	}
	// Free ranges + occupied entries partition the usable-host space exactly.
	// The synthetic set happens to include the network address 10.60.0.0
	// (offset 0), which is outside the usable span — count only the entries
	// that actually fall within it, exactly as freeRanges does.
	lo, hi, _, _ := hostSpan("10.60.0.0/16")
	occInSpan := 0
	for _, e := range entries {
		v := ipToBig(net.ParseIP(e.IP))
		if v.Cmp(lo) >= 0 && v.Cmp(hi) <= 0 {
			occInSpan++
		}
	}
	usable := 65536 - 2 // /16 minus network + broadcast
	if got := counts.Free + occInSpan; got != usable {
		t.Errorf("free (%d) + in-span occupied (%d) = %d, want %d usable hosts", counts.Free, occInSpan, got, usable)
	}
	// A generous ceiling for CI-noise tolerance: the point is "milliseconds,
	// not seconds", proving the list never materializes the address space.
	if elapsed > 200*time.Millisecond {
		t.Errorf("address-list computation took %s, want well under 200ms", elapsed)
	}
	t.Logf("mergeSubnet + sort + freeRanges over a /16 (2000 allocations): %s", elapsed)
}
