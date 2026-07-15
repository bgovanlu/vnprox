package ipam

import (
	"net"
	"testing"
)

func TestHostSpan_24ExcludesNetworkAndBroadcast(t *testing.T) {
	lo, hi, byteLen, ok := hostSpan("10.50.0.0/24")
	if !ok {
		t.Fatal("hostSpan: not ok")
	}
	if byteLen != 4 {
		t.Errorf("byteLen = %d, want 4", byteLen)
	}
	if got := bigToIP(lo, byteLen).String(); got != "10.50.0.1" {
		t.Errorf("lo = %s, want 10.50.0.1 (network address excluded)", got)
	}
	if got := bigToIP(hi, byteLen).String(); got != "10.50.0.254" {
		t.Errorf("hi = %s, want 10.50.0.254 (broadcast excluded)", got)
	}
}

func TestHostSpan_31And32SpanEveryAddress(t *testing.T) {
	// /32: the single address is the whole span.
	lo, hi, bl, ok := hostSpan("10.50.0.5/32")
	if !ok || bigToIP(lo, bl).String() != "10.50.0.5" || bigToIP(hi, bl).String() != "10.50.0.5" {
		t.Fatalf("/32 span = [%s, %s], want [10.50.0.5, 10.50.0.5]", bigToIP(lo, bl), bigToIP(hi, bl))
	}
	// /31: both addresses are usable (no network/broadcast pair to drop).
	lo, hi, bl, ok = hostSpan("10.50.0.4/31")
	if !ok || bigToIP(lo, bl).String() != "10.50.0.4" || bigToIP(hi, bl).String() != "10.50.0.5" {
		t.Fatalf("/31 span = [%s, %s], want [10.50.0.4, 10.50.0.5]", bigToIP(lo, bl), bigToIP(hi, bl))
	}
}

func TestHostSpan_InvalidCIDR(t *testing.T) {
	if _, _, _, ok := hostSpan("not-a-cidr"); ok {
		t.Fatal("expected ok=false for an invalid CIDR")
	}
}

func occupiedSet(ips ...string) map[string]Cell {
	m := make(map[string]Cell, len(ips))
	for _, ip := range ips {
		m[ip] = Cell{IP: ip, State: CellAllocated}
	}
	return m
}

func TestFreeRanges_EmptySubnetIsOneFullRange(t *testing.T) {
	ranges := freeRanges("10.50.0.0/24", nil)
	if len(ranges) != 1 {
		t.Fatalf("ranges = %+v, want exactly one full-subnet range", ranges)
	}
	got := ranges[0]
	if got.Start != "10.50.0.1" || got.End != "10.50.0.254" || got.Count != 254 {
		t.Errorf("full range = %+v, want {10.50.0.1 10.50.0.254 254}", got)
	}
}

func TestFreeRanges_GapsBetweenOccupied(t *testing.T) {
	// Occupied: .1 (gateway-ish), .10, .11, .254 (top host). Expect gaps
	// .2-.9, .12-.253.
	ranges := freeRanges("10.50.0.0/24", occupiedSet("10.50.0.1", "10.50.0.10", "10.50.0.11", "10.50.0.254"))
	want := []FreeRange{
		{Start: "10.50.0.2", End: "10.50.0.9", Count: 8},
		{Start: "10.50.0.12", End: "10.50.0.253", Count: 242},
	}
	if len(ranges) != len(want) {
		t.Fatalf("ranges = %+v, want %+v", ranges, want)
	}
	for i, w := range want {
		if ranges[i] != w {
			t.Errorf("range[%d] = %+v, want %+v", i, ranges[i], w)
		}
	}
}

func TestFreeRanges_ConsecutiveOccupiedLeaveNoGap(t *testing.T) {
	// .1 and .2 occupied back-to-back: only the trailing .3-.254 gap remains.
	ranges := freeRanges("10.50.0.0/24", occupiedSet("10.50.0.1", "10.50.0.2"))
	if len(ranges) != 1 || ranges[0].Start != "10.50.0.3" {
		t.Fatalf("ranges = %+v, want a single range starting at 10.50.0.3", ranges)
	}
}

func TestFreeRanges_FullyAllocatedHasNoRanges(t *testing.T) {
	occ := map[string]Cell{}
	for i := 1; i <= 254; i++ {
		ip := net.IPv4(10, 50, 0, byte(i)).String()
		occ[ip] = Cell{IP: ip, State: CellAllocated}
	}
	if ranges := freeRanges("10.50.0.0/24", occ); len(ranges) != 0 {
		t.Fatalf("ranges = %+v, want none (subnet is full)", ranges)
	}
}

func TestSubnetAddrCount(t *testing.T) {
	cases := []struct {
		cidr   string
		count  int
		prefix int
	}{
		{"10.0.0.0/24", 256, 24},
		{"10.0.0.0/16", 65536, 16},
		{"10.0.0.5/32", 1, 32},
	}
	for _, c := range cases {
		count, prefix, ok := subnetAddrCount(c.cidr)
		if !ok || count != c.count || prefix != c.prefix {
			t.Errorf("subnetAddrCount(%s) = (%d, %d, %v), want (%d, %d, true)", c.cidr, count, prefix, ok, c.count, c.prefix)
		}
	}
}
