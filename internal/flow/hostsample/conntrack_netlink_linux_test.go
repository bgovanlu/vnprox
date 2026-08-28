//go:build linux

// SPDX-License-Identifier: Apache-2.0

package hostsample

import (
	"errors"
	"net"
	"syscall"
	"testing"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

// TestConntrackEntryFromFlow is T-3711's netlink->entry conversion
// coverage: real netlink.ConntrackFlow values built directly (not a
// fixture of this project's own text), exercising the real type the
// production reader (netlinkConntrackReader) actually converts. Unlike
// internal/host's own ConntrackEntry, this package's entry type carries no
// State/NAT — only the original-direction tuple plus cumulative counters
// (see ConntrackEntry's doc comment), so this only checks those fields.
func TestConntrackEntryFromFlow(t *testing.T) {
	tests := []struct {
		name string
		flow *netlink.ConntrackFlow
		want ConntrackEntry
	}{
		{
			name: "tcp, with accounting counters",
			flow: &netlink.ConntrackFlow{
				Forward: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.10"), DstIP: net.ParseIP("192.168.1.20"),
					SrcPort: 54321, DstPort: 443, Protocol: 6,
					Packets: 12, Bytes: 1500,
				},
				Reverse: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.20"), DstIP: net.ParseIP("192.168.1.10"),
					SrcPort: 443, DstPort: 54321, Protocol: 6,
				},
				ProtoInfo: &netlink.ProtoInfoTCP{State: nl.TCP_CONNTRACK_ESTABLISHED},
			},
			want: ConntrackEntry{
				Proto: 6, SrcIP: "192.168.1.10", DstIP: "192.168.1.20", SrcPort: 54321, DstPort: 443,
				Packets: 12, Bytes: 1500,
			},
		},
		{
			name: "udp, no accounting (nf_conntrack_acct=0) — zero counters, not an error",
			flow: &netlink.ConntrackFlow{
				Forward: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.11"), DstIP: net.ParseIP("192.168.1.30"),
					SrcPort: 51000, DstPort: 53, Protocol: 17,
				},
			},
			want: ConntrackEntry{
				Proto: 17, SrcIP: "192.168.1.11", DstIP: "192.168.1.30", SrcPort: 51000, DstPort: 53,
			},
		},
		{
			name: "icmp: no ports",
			flow: &netlink.ConntrackFlow{
				Forward: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.50"), DstIP: net.ParseIP("192.168.1.60"), Protocol: 1,
					Packets: 1, Bytes: 84,
				},
			},
			want: ConntrackEntry{
				Proto: 1, SrcIP: "192.168.1.50", DstIP: "192.168.1.60", Packets: 1, Bytes: 84,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := conntrackEntryFromFlow(tc.flow)
			if got != tc.want {
				t.Fatalf("conntrackEntryFromFlow() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestWrapConntrackNetlinkErr mirrors internal/host's own coverage for its
// independent copy of this classification (see ErrConntrackUnavailable's
// doc comment on why the two packages each keep one).
func TestWrapConntrackNetlinkErr(t *testing.T) {
	tests := []struct {
		in              error
		name            string
		wantUnavailable bool
		wantPermission  bool
	}{
		{name: "EPERM", in: syscall.EPERM, wantUnavailable: true, wantPermission: true},
		{name: "EACCES", in: syscall.EACCES, wantUnavailable: true, wantPermission: true},
		{name: "ENOENT (subsystem absent)", in: syscall.ENOENT, wantUnavailable: true, wantPermission: false},
		{name: "EPROTONOSUPPORT", in: syscall.EPROTONOSUPPORT, wantUnavailable: true, wantPermission: false},
		{name: "other/transient (ETIMEDOUT)", in: syscall.ETIMEDOUT, wantUnavailable: false, wantPermission: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := wrapConntrackNetlinkErr(tc.in)
			if got := errors.Is(err, ErrConntrackUnavailable); got != tc.wantUnavailable {
				t.Errorf("errors.Is(_, ErrConntrackUnavailable) = %v, want %v (err=%v)", got, tc.wantUnavailable, err)
			}
			if got := errors.Is(err, ErrConntrackPermissionDenied); got != tc.wantPermission {
				t.Errorf("errors.Is(_, ErrConntrackPermissionDenied) = %v, want %v (err=%v)", got, tc.wantPermission, err)
			}
			if !errors.Is(err, tc.in) {
				t.Errorf("wrapped error does not errors.Is the original %v: %v", tc.in, err)
			}
		})
	}
}
