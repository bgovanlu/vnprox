// SPDX-License-Identifier: Apache-2.0

package ipam_test

import (
	"context"
	"reflect"
	"testing"
)

// TestService_GuestIPs is T-1305's guest->IP resolution seam, exercised
// against ipam-lab.yaml's deliberately messy scenario (see that fixture's
// header comment): web1's agent-reported and IPAM-allocated addresses
// agree (one address, deduplicated); web2's agent-reported address
// diverges from its IPAM allocation (MAC-matched) — both surface; web3 has
// no IPAM allocation for its MAC at all, so only its agent-reported
// addresses surface. An unknown guest ref resolves to no addresses, not an
// error.
func TestService_GuestIPs(t *testing.T) {
	svc := newIpamTestService(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		guestRef  string
		wantAddrs []string
	}{
		{
			name:      "web1: agent and IPAM agree, deduplicated",
			guestRef:  "guest:pve1:300",
			wantAddrs: []string{"10.50.0.10"},
		},
		{
			name:      "web2: agent-reported and IPAM-allocated addresses diverge, both surface",
			guestRef:  "guest:pve1:301",
			wantAddrs: []string{"10.50.0.20", "10.50.0.77"},
		},
		{
			name:      "web3: no IPAM allocation for its MAC, only agent-reported addresses",
			guestRef:  "guest:pve1:302",
			wantAddrs: []string{"10.50.0.77", "10.50.0.88"},
		},
		{
			name:      "unknown guest ref resolves to nothing, not an error",
			guestRef:  "guest:pve1:999",
			wantAddrs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.GuestIPs(ctx, tt.guestRef)
			if err != nil {
				t.Fatalf("GuestIPs(%s): %v", tt.guestRef, err)
			}
			if !reflect.DeepEqual(got, tt.wantAddrs) {
				t.Errorf("GuestIPs(%s) = %v, want %v", tt.guestRef, got, tt.wantAddrs)
			}
		})
	}
}
