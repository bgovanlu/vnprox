package ipam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipam"
)

// TestService_V6Plan_ProposesAlignedBlocksForV4OnlyVnet is T-1404
// acceptance criterion 5: against a /56-delegated prefix, the ipam-lab.yaml
// fixture's one v4-only VNet (vnet10, zone labz, no v6 subnet configured
// yet) gets proposed the delegated prefix's first /64-aligned block.
func TestService_V6Plan_ProposesAlignedBlocksForV4OnlyVnet(t *testing.T) {
	svc := newIpamTestService(t)

	resp, err := svc.V6Plan(context.Background(), "2001:db8:50::/56")
	if err != nil {
		t.Fatalf("V6Plan: %v", err)
	}
	if resp.PrefixLen != 56 || resp.BlockPrefixLen != 64 {
		t.Fatalf("PrefixLen/BlockPrefixLen = %d/%d, want 56/64", resp.PrefixLen, resp.BlockPrefixLen)
	}
	if resp.TotalBlocks != 256 {
		t.Fatalf("TotalBlocks = %d, want 256 (2^(64-56))", resp.TotalBlocks)
	}
	if len(resp.Blocks) != 256 {
		t.Fatalf("len(Blocks) = %d, want 256", len(resp.Blocks))
	}

	var proposed []ipam.V6PlanBlock
	for _, b := range resp.Blocks {
		if b.State == "proposed" {
			proposed = append(proposed, b)
		}
	}
	if len(proposed) != 1 {
		t.Fatalf("got %d proposed blocks, want 1 (one v4-only VNet): %+v", len(proposed), proposed)
	}
	p := proposed[0]
	if p.Vnet != "vnet10" || p.Zone != "labz" {
		t.Errorf("proposed block = %+v, want vnet=vnet10 zone=labz", p)
	}
	if p.CIDR != "2001:db8:50::/64" {
		t.Errorf("proposed block CIDR = %q, want the delegated prefix's first /64 block", p.CIDR)
	}

	// Every other block is free (nothing pre-allocated in this fixture's
	// delegated range) and none are "allocated" (no v6 subnet exists yet
	// at all).
	for _, b := range resp.Blocks {
		if b.State == "allocated" {
			t.Errorf("unexpected allocated block %+v — fixture has no v6 subnets yet", b)
		}
	}
}

// TestService_V6Plan_InvalidPrefix400Class is a regression guard: a
// malformed or non-v6 prefix returns ErrInvalidPrefix, never a panic or a
// fabricated plan.
func TestService_V6Plan_InvalidPrefix400Class(t *testing.T) {
	svc := newIpamTestService(t)
	cases := []string{"not-a-prefix", "10.0.0.0/8", "2001:db8::/72"}
	for _, c := range cases {
		if _, err := svc.V6Plan(context.Background(), c); !errors.Is(err, ipam.ErrInvalidPrefix) {
			t.Errorf("V6Plan(%q) error = %v, want ErrInvalidPrefix", c, err)
		}
	}
}

// TestService_V6Plan_OversizedPrefixRejected: a delegation broader than
// this package's enumeration cap is rejected rather than building an
// unbounded response.
func TestService_V6Plan_OversizedPrefixRejected(t *testing.T) {
	svc := newIpamTestService(t)
	if _, err := svc.V6Plan(context.Background(), "2001:db8::/32"); !errors.Is(err, ipam.ErrPrefixTooLarge) {
		t.Errorf("V6Plan(/32) error = %v, want ErrPrefixTooLarge", err)
	}
}
