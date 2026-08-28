// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

// TestConntrackEntryFromFlow is T-3711's netlink->entry conversion
// coverage: real netlink.ConntrackFlow values built directly (not a
// fixture of this project's own text), exercising the real type the
// production path (readNetlinkConntrackTable) actually converts.
func TestConntrackEntryFromFlow(t *testing.T) {
	tests := []struct {
		name string
		flow *netlink.ConntrackFlow
		want ConntrackEntry
	}{
		{
			name: "tcp established, no NAT (reply mirrors original exactly)",
			flow: &netlink.ConntrackFlow{
				Forward: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.10"), DstIP: net.ParseIP("192.168.1.20"),
					SrcPort: 54321, DstPort: 443, Protocol: 6,
				},
				Reverse: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.20"), DstIP: net.ParseIP("192.168.1.10"),
					SrcPort: 443, DstPort: 54321, Protocol: 6,
				},
				TimeOut:   431999,
				ProtoInfo: &netlink.ProtoInfoTCP{State: nl.TCP_CONNTRACK_ESTABLISHED},
			},
			want: ConntrackEntry{
				Proto: 6, SrcIP: "192.168.1.10", DstIP: "192.168.1.20", SrcPort: 54321, DstPort: 443,
				State: "ESTABLISHED", TimeoutSec: 431999,
			},
		},
		{
			name: "tcp time_wait",
			flow: &netlink.ConntrackFlow{
				Forward: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.15"), DstIP: net.ParseIP("192.168.1.25"),
					SrcPort: 55000, DstPort: 80, Protocol: 6,
				},
				Reverse: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.25"), DstIP: net.ParseIP("192.168.1.15"),
					SrcPort: 80, DstPort: 55000, Protocol: 6,
				},
				TimeOut:   30,
				ProtoInfo: &netlink.ProtoInfoTCP{State: nl.TCP_CONNTRACK_TIME_WAIT},
			},
			want: ConntrackEntry{
				Proto: 6, SrcIP: "192.168.1.15", DstIP: "192.168.1.25", SrcPort: 55000, DstPort: 80,
				State: "TIME_WAIT", TimeoutSec: 30,
			},
		},
		{
			name: "udp has no ProtoInfo — state left empty, not fabricated",
			flow: &netlink.ConntrackFlow{
				Forward: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.11"), DstIP: net.ParseIP("192.168.1.30"),
					SrcPort: 51000, DstPort: 53, Protocol: 17,
				},
				Reverse: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.30"), DstIP: net.ParseIP("192.168.1.11"),
					SrcPort: 53, DstPort: 51000, Protocol: 17,
				},
				TimeOut: 29,
			},
			want: ConntrackEntry{
				Proto: 17, SrcIP: "192.168.1.11", DstIP: "192.168.1.30", SrcPort: 51000, DstPort: 53,
				TimeoutSec: 29,
			},
		},
		{
			name: "icmp, no reverse tuple at all — no NAT reported, nothing to compare against",
			flow: &netlink.ConntrackFlow{
				Forward: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.50"), DstIP: net.ParseIP("192.168.1.60"), Protocol: 1,
				},
				TimeOut: 29,
			},
			want: ConntrackEntry{
				Proto: 1, SrcIP: "192.168.1.50", DstIP: "192.168.1.60", TimeoutSec: 29,
			},
		},
		{
			name: "SNAT: reply dst diverges from original src",
			flow: &netlink.ConntrackFlow{
				Forward: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.5"), DstIP: net.ParseIP("8.8.8.8"),
					SrcPort: 44444, DstPort: 443, Protocol: 6,
				},
				Reverse: netlink.IPTuple{
					SrcIP: net.ParseIP("8.8.8.8"), DstIP: net.ParseIP("203.0.113.10"),
					SrcPort: 443, DstPort: 44444, Protocol: 6,
				},
				TimeOut:   431999,
				ProtoInfo: &netlink.ProtoInfoTCP{State: nl.TCP_CONNTRACK_ESTABLISHED},
			},
			want: ConntrackEntry{
				Proto: 6, SrcIP: "192.168.1.5", DstIP: "8.8.8.8", SrcPort: 44444, DstPort: 443,
				State: "ESTABLISHED", TimeoutSec: 431999,
				NatSrc: &NatAddr{IP: "203.0.113.10", Port: 44444},
			},
		},
		{
			name: "DNAT: reply src diverges from original dst",
			flow: &netlink.ConntrackFlow{
				Forward: netlink.IPTuple{
					SrcIP: net.ParseIP("203.0.113.5"), DstIP: net.ParseIP("203.0.113.10"),
					SrcPort: 51000, DstPort: 8080, Protocol: 6,
				},
				Reverse: netlink.IPTuple{
					SrcIP: net.ParseIP("192.168.1.100"), DstIP: net.ParseIP("203.0.113.5"),
					SrcPort: 80, DstPort: 51000, Protocol: 6,
				},
				TimeOut:   431999,
				ProtoInfo: &netlink.ProtoInfoTCP{State: nl.TCP_CONNTRACK_ESTABLISHED},
			},
			want: ConntrackEntry{
				Proto: 6, SrcIP: "203.0.113.5", DstIP: "203.0.113.10", SrcPort: 51000, DstPort: 8080,
				State: "ESTABLISHED", TimeoutSec: 431999,
				NatDst: &NatAddr{IP: "192.168.1.100", Port: 80},
			},
		},
		{
			name: "unrecognized tcp state byte defensively leaves State empty",
			flow: &netlink.ConntrackFlow{
				Forward: netlink.IPTuple{
					SrcIP: net.ParseIP("10.0.0.1"), DstIP: net.ParseIP("10.0.0.2"),
					SrcPort: 1, DstPort: 2, Protocol: 6,
				},
				ProtoInfo: &netlink.ProtoInfoTCP{State: 200}, // not in nl.TCP_CONNTRACK_*
			},
			want: ConntrackEntry{
				Proto: 6, SrcIP: "10.0.0.1", DstIP: "10.0.0.2", SrcPort: 1, DstPort: 2,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := conntrackEntryFromFlow(tc.flow)
			if got.Proto != tc.want.Proto || got.SrcIP != tc.want.SrcIP || got.DstIP != tc.want.DstIP ||
				got.SrcPort != tc.want.SrcPort || got.DstPort != tc.want.DstPort ||
				got.State != tc.want.State || got.TimeoutSec != tc.want.TimeoutSec {
				t.Fatalf("conntrackEntryFromFlow() = %+v, want %+v", got, tc.want)
			}
			if (got.NatSrc == nil) != (tc.want.NatSrc == nil) || (got.NatSrc != nil && *got.NatSrc != *tc.want.NatSrc) {
				t.Errorf("NatSrc = %+v, want %+v", got.NatSrc, tc.want.NatSrc)
			}
			if (got.NatDst == nil) != (tc.want.NatDst == nil) || (got.NatDst != nil && *got.NatDst != *tc.want.NatDst) {
				t.Errorf("NatDst = %+v, want %+v", got.NatDst, tc.want.NatDst)
			}
		})
	}
}

// TestWrapConntrackNetlinkErr covers the three-way classification
// wrapConntrackNetlinkErr makes: a permission failure wraps both
// ErrConntrackUnavailable and ErrConntrackPermissionDenied; a
// "subsystem/family absent" failure wraps only ErrConntrackUnavailable; any
// other error is wrapped plainly (so a transient blip does not get treated
// as permanent unavailability — see the sampler's report-once-then-stop
// behavior in internal/flow/hostsample, which keys off ErrConntrackUnavailable).
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

// TestReal_Conntrack_DoesNotDependOnProcfsPath is T-3711's required
// regression test: it fails against the pre-fix code, because
// NewReal().ConntrackPath used to default to DefaultConntrackPath
// ("/proc/net/nf_conntrack") and Conntrack() always read that file first —
// exactly the assumption that does not hold on a PVE 9 kernel
// (CONFIG_NF_CONNTRACK_PROCFS=n). After the fix, ConntrackPath is empty by
// default and the production read goes through netlink instead: any error
// it returns must not name the procfs path, and — since this test process
// is not guaranteed to hold CAP_NET_ADMIN (it doesn't, in this sandbox) —
// must be a netlink-shaped failure wrapping ErrConntrackUnavailable, never
// a bare os.ReadFile "no such file"/"permission denied" on the procfs path.
func TestReal_Conntrack_DoesNotDependOnProcfsPath(t *testing.T) {
	r := NewReal()
	if r.ConntrackPath != "" {
		t.Fatalf("NewReal().ConntrackPath = %q, want empty — the default production reader must not target a procfs path (T-3711)", r.ConntrackPath)
	}

	_, err := r.Conntrack(context.Background(), "")
	if err == nil {
		// This test process happened to hold CAP_NET_ADMIN (e.g. running
		// as root with the capability) — the netlink read succeeded
		// outright, which is the AC1 case and needs nothing further
		// checked here.
		return
	}
	if strings.Contains(err.Error(), DefaultConntrackPath) {
		t.Fatalf("Conntrack() error mentions the procfs path %s: %v — production path still depends on it", DefaultConntrackPath, err)
	}
	if !errors.Is(err, ErrConntrackUnavailable) {
		t.Fatalf("Conntrack() error = %v, want it to wrap ErrConntrackUnavailable (a netlink-shaped failure)", err)
	}
}
