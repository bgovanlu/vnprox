// SPDX-License-Identifier: Apache-2.0

package lacphash

import (
	"errors"
	"net"
	"testing"
)

func mac(s string) net.HardwareAddr {
	m, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return m
}

// TestHash_KnownTuples exercises each policy against tuples chosen so the
// expected hash is hand-verifiable by the XOR-cancellation identity
// (x XOR x == 0), not just re-derived from this package's own code —
// e.g. when SrcMAC == DstMAC, the MAC-XOR term of the formula is
// necessarily zero, so the whole hash collapses to whatever is left
// (packet type ID alone, for layer2). This is what "known tuples" means
// for a formula this package is not able to check against a running
// kernel (see doc.go).
func TestHash_KnownTuples(t *testing.T) {
	sameMAC := mac("02:00:00:00:00:01")
	otherMAC := mac("02:00:00:00:00:02")
	v4a := net.ParseIP("10.0.0.1")
	v4b := net.ParseIP("10.0.0.2")

	tests := []struct {
		wantErr error
		name    string
		policy  Policy
		tuple   FlowTuple
		want    uint32
	}{
		{
			name:   "layer2 identical MACs collapses to EtherType alone",
			policy: PolicyLayer2,
			tuple:  FlowTuple{SrcMAC: sameMAC, DstMAC: sameMAC, DstIP: v4a},
			// MAC XOR MAC == 0, so hash == EtherType, defaulted to IPv4
			// (0x0800) since DstIP is an IPv4 address.
			want: 0x0800,
		},
		{
			name:    "layer2 missing MAC is rejected, not zero-hashed",
			policy:  PolicyLayer2,
			tuple:   FlowTuple{DstIP: v4a},
			wantErr: ErrMACRequired,
		},
		{
			name:   "layer2 explicit EtherType overrides the IPv4 default",
			policy: PolicyLayer2,
			tuple:  FlowTuple{SrcMAC: sameMAC, DstMAC: sameMAC, EtherType: 0x86DD},
			want:   0x86DD,
		},
		{
			name:   "layer2+3 identical MAC and IP leaves only the folded EtherType",
			policy: PolicyLayer23,
			tuple:  FlowTuple{SrcMAC: sameMAC, DstMAC: sameMAC, SrcIP: v4a, DstIP: v4a, EtherType: 0},
			// MAC XOR == 0, EtherType default from DstIP == 0x0800,
			// SrcIP XOR DstIP == 0 (identical) -> pre-fold hash ==
			// 0x0800. foldHash(0x0800): the >>16 step is a no-op
			// (0x0800>>16 == 0), but the >>8 step is not (0x0800>>8 ==
			// 0x08), so the folded result is 0x0800 ^ 0x08 == 0x0808.
			want: 0x0808,
		},
		{
			name:    "layer2+3 missing MAC is rejected even with IPs present",
			policy:  PolicyLayer23,
			tuple:   FlowTuple{SrcIP: v4a, DstIP: v4b},
			wantErr: ErrMACRequired,
		},
		{
			name:    "layer2+3 missing IP is rejected even with MACs present",
			policy:  PolicyLayer23,
			tuple:   FlowTuple{SrcMAC: sameMAC, DstMAC: otherMAC},
			wantErr: ErrIPRequired,
		},
		{
			name:   "layer3+4 identical IPs, no ports, collapses to zero",
			policy: PolicyLayer34,
			tuple:  FlowTuple{SrcIP: v4a, DstIP: v4a},
			want:   0,
		},
		{
			name:   "layer3+4 identical IPs with distinct TCP ports XORs the ports",
			policy: PolicyLayer34,
			tuple:  FlowTuple{SrcIP: v4a, DstIP: v4a, SrcPort: 1000, DstPort: 2024, Proto: 6},
			// IP term cancels to 0, leaving hash = 1000^2024 = 0x400
			// pre-fold. foldHash(0x400): >>16 is a no-op, >>8 is not
			// (0x400>>8 == 0x04), so the folded result is
			// 0x400 ^ 0x04 == 0x404.
			want: 0x404,
		},
		{
			name:   "layer3+4 ports ignored for a non-TCP/UDP protocol",
			policy: PolicyLayer34,
			tuple:  FlowTuple{SrcIP: v4a, DstIP: v4a, SrcPort: 1000, DstPort: 2024, Proto: 1}, // icmp
			want:   0,
		},
		{
			name:    "layer3+4 missing IP is rejected",
			policy:  PolicyLayer34,
			tuple:   FlowTuple{SrcPort: 1, DstPort: 2, Proto: 6},
			wantErr: ErrIPRequired,
		},
		{
			name:   "encap2+3 behaves identically to layer2+3 for an outer-only tuple",
			policy: PolicyEncap23,
			tuple:  FlowTuple{SrcMAC: sameMAC, DstMAC: sameMAC, SrcIP: v4a, DstIP: v4a},
			want:   0x0808,
		},
		{
			name:   "encap3+4 behaves identically to layer3+4 for an outer-only tuple",
			policy: PolicyEncap34,
			tuple:  FlowTuple{SrcIP: v4a, DstIP: v4a, SrcPort: 1000, DstPort: 2024, Proto: 17},
			want:   0x404,
		},
		{
			name:    "unrecognized policy is rejected",
			policy:  Policy("bogus"),
			tuple:   FlowTuple{SrcIP: v4a, DstIP: v4b},
			wantErr: ErrUnknownPolicy,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Hash(tc.policy, tc.tuple)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Hash() err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Hash() unexpected err = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Hash() = %#x, want %#x", got, tc.want)
			}
		})
	}
}

// TestHash_Deterministic proves the property every consumer (Predict,
// SelectSlave) relies on: the same tuple under the same policy always
// produces the same hash, across repeated calls and independent of
// argument aliasing.
func TestHash_Deterministic(t *testing.T) {
	tuple := FlowTuple{
		SrcMAC: mac("02:00:00:00:00:01"), DstMAC: mac("02:00:00:00:00:02"),
		SrcIP: net.ParseIP("192.168.1.5"), DstIP: net.ParseIP("192.168.1.10"),
		SrcPort: 4444, DstPort: 443, Proto: 6,
	}
	for _, p := range []Policy{PolicyLayer2, PolicyLayer23, PolicyLayer34, PolicyEncap23, PolicyEncap34} {
		first, err := Hash(p, tuple)
		if err != nil {
			t.Fatalf("policy %s: unexpected err %v", p, err)
		}
		for i := 0; i < 5; i++ {
			got, err := Hash(p, tuple)
			if err != nil {
				t.Fatalf("policy %s: unexpected err %v", p, err)
			}
			if got != first {
				t.Fatalf("policy %s: Hash() not deterministic: %#x then %#x", p, first, got)
			}
		}
	}
}

// TestHash_IPv6Layer34 exercises the IPv6 fold path (ipXOR's 4-word XOR),
// using the same identical-address cancellation trick as the IPv4 cases
// above so the expected value is hand-verifiable.
func TestHash_IPv6Layer34(t *testing.T) {
	addr := net.ParseIP("fe80::1")
	got, err := Hash(PolicyLayer34, FlowTuple{SrcIP: addr, DstIP: addr})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != 0 {
		t.Fatalf("Hash() = %#x, want 0 (identical src/dst IP must cancel)", got)
	}
}

func TestPolicy_Valid(t *testing.T) {
	valid := []Policy{PolicyLayer2, PolicyLayer23, PolicyLayer34, PolicyEncap23, PolicyEncap34}
	for _, p := range valid {
		if !p.Valid() {
			t.Errorf("Policy(%q).Valid() = false, want true", p)
		}
	}
	invalid := []Policy{"", "layer4", "LAYER2", "layer2 "}
	for _, p := range invalid {
		if p.Valid() {
			t.Errorf("Policy(%q).Valid() = true, want false", p)
		}
	}
}

func TestSelectSlave(t *testing.T) {
	v4a := net.ParseIP("10.0.0.1")
	v4b := net.ParseIP("10.0.0.2")
	tuple := FlowTuple{SrcIP: v4a, DstIP: v4b, SrcPort: 1000, DstPort: 2024, Proto: 6}

	t.Run("zero slaves is rejected", func(t *testing.T) {
		if _, err := SelectSlave(PolicyLayer34, tuple, 0); !errors.Is(err, ErrNoSlaves) {
			t.Fatalf("err = %v, want ErrNoSlaves", err)
		}
	})

	t.Run("negative slave count is rejected", func(t *testing.T) {
		if _, err := SelectSlave(PolicyLayer34, tuple, -1); !errors.Is(err, ErrNoSlaves) {
			t.Fatalf("err = %v, want ErrNoSlaves", err)
		}
	})

	t.Run("one slave always selects index 0", func(t *testing.T) {
		idx, err := SelectSlave(PolicyLayer34, tuple, 1)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if idx != 0 {
			t.Fatalf("idx = %d, want 0", idx)
		}
	})

	t.Run("index is always within range", func(t *testing.T) {
		for n := 1; n <= 8; n++ {
			for port := uint16(0); port < 200; port++ {
				tt := tuple
				tt.SrcPort = port
				idx, err := SelectSlave(PolicyLayer34, tt, n)
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				if idx < 0 || idx >= n {
					t.Fatalf("SelectSlave with %d slaves returned out-of-range index %d", n, idx)
				}
			}
		}
	})

	t.Run("propagates Hash's error for a tuple the policy can't classify", func(t *testing.T) {
		if _, err := SelectSlave(PolicyLayer2, tuple, 2); !errors.Is(err, ErrMACRequired) {
			t.Fatalf("err = %v, want ErrMACRequired", err)
		}
	})
}

// TestSelectSlave_DistributesAcrossSlaves is a sanity check, not a proof:
// a spread of distinct flow tuples across a plausible port range should
// not all collapse onto a single slave index. This catches a gross
// implementation bug (e.g. always returning 0) without claiming to prove
// anything about a real switch's distribution — see doc.go.
func TestSelectSlave_DistributesAcrossSlaves(t *testing.T) {
	v4a := net.ParseIP("10.0.0.1")
	v4b := net.ParseIP("10.0.0.2")
	seen := map[int]bool{}
	for port := uint16(0); port < 500; port += 7 {
		idx, err := SelectSlave(PolicyLayer34, FlowTuple{
			SrcIP: v4a, DstIP: v4b, SrcPort: port, DstPort: 443, Proto: 6,
		}, 4)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		seen[idx] = true
	}
	if len(seen) < 2 {
		t.Fatalf("500 distinct-port flows across 4 slaves all landed on %d slave index/indices: %v", len(seen), seen)
	}
}
