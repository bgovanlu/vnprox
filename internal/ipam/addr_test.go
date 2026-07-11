package ipam

import "testing"

func TestHostAddresses_24ExcludesNetworkAndBroadcast(t *testing.T) {
	addrs, ok := hostAddresses("10.50.0.0/24")
	if !ok {
		t.Fatal("hostAddresses: not ok")
	}
	if len(addrs) != 254 {
		t.Fatalf("len = %d, want 254", len(addrs))
	}
	if addrs[0] != "10.50.0.1" {
		t.Errorf("first = %s, want 10.50.0.1 (network address excluded)", addrs[0])
	}
	if addrs[len(addrs)-1] != "10.50.0.254" {
		t.Errorf("last = %s, want 10.50.0.254 (broadcast excluded)", addrs[len(addrs)-1])
	}
}

func TestHostAddresses_32IncludesTheOneAddress(t *testing.T) {
	addrs, ok := hostAddresses("10.50.0.5/32")
	if !ok || len(addrs) != 1 || addrs[0] != "10.50.0.5" {
		t.Fatalf("addrs = %v, ok = %v, want [10.50.0.5]", addrs, ok)
	}
}

func TestHostAddresses_InvalidCIDR(t *testing.T) {
	if _, ok := hostAddresses("not-a-cidr"); ok {
		t.Fatal("expected ok=false for an invalid CIDR")
	}
}

func TestBlockCIDRs_16SplitsInto256Blocks(t *testing.T) {
	blocks, ok := blockCIDRs("10.50.0.0/16")
	if !ok {
		t.Fatal("blockCIDRs: not ok")
	}
	if len(blocks) != 256 {
		t.Fatalf("len = %d, want 256", len(blocks))
	}
	if blocks[0] != "10.50.0.0/24" {
		t.Errorf("first block = %s, want 10.50.0.0/24", blocks[0])
	}
	if blocks[255] != "10.50.255.0/24" {
		t.Errorf("last block = %s, want 10.50.255.0/24", blocks[255])
	}
}

func TestBlockCIDRs_24IsAlreadyDirectRender(t *testing.T) {
	if _, ok := blockCIDRs("10.50.0.0/24"); ok {
		t.Fatal("a /24 should not be split into blocks — it's already the direct-render threshold")
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
