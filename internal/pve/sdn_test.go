// SPDX-License-Identifier: Apache-2.0

package pve

import "testing"

func TestSDNSubnetID(t *testing.T) {
	tests := []struct{ cidr, want string }{
		{"10.0.0.0/24", "10.0.0.0-24"},
		{"10.0.0.0-24", "10.0.0.0-24"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := SDNSubnetID(tt.cidr); got != tt.want {
			t.Errorf("SDNSubnetID(%q) = %q, want %q", tt.cidr, got, tt.want)
		}
	}
}

func TestSDNVnetID(t *testing.T) {
	tests := []struct{ refID, want string }{
		{"zone1/vnet1", "vnet1"},
		{"vnet1", "vnet1"},
		{"", ""},
		{"a/b/c", "c"},
	}
	for _, tt := range tests {
		if got := SDNVnetID(tt.refID); got != tt.want {
			t.Errorf("SDNVnetID(%q) = %q, want %q", tt.refID, got, tt.want)
		}
	}
}
