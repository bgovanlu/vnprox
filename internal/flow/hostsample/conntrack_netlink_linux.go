//go:build linux

// conntrack_netlink_linux.go is T-3711's production ConntrackReader: the
// netlink conntrack socket (netlink.ConntrackTableList, CAP_NET_ADMIN)
// rather than /proc/net/nf_conntrack — PVE 9 kernels ship
// CONFIG_NF_CONNTRACK_PROCFS=n, so that procfs path does not exist even
// though the nf_conntrack module is loaded and the netlink interface
// (what `conntrack -L` itself uses) works fine. Split into a Linux-only
// file, matching internal/host/netlink_linux.go's own convention, because
// github.com/vishvananda/netlink's ConntrackFlow/IPTuple types are only
// defined (with real fields) on Linux — conntrack_netlink_other.go is the
// non-Linux stand-in that keeps `go build ./...`/`go vet ./...` working on
// a contributor's non-Linux development machine.

package hostsample

import (
	"context"
	"errors"
	"fmt"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// netlinkConntrackReader is the production ConntrackReader (T-3711).
type netlinkConntrackReader struct{}

// NewNetlinkConntrackReader builds the production ConntrackReader: the
// live netlink conntrack table (both IPv4 and IPv6), not a procfs text
// file. This is what cmd/vnproxd wires in by default when [flows]
// conntrack_sampling_enabled is set; NewFileConntrackReader remains
// available as a secondary text-format operator override or test double.
func NewNetlinkConntrackReader() ConntrackReader {
	return netlinkConntrackReader{}
}

// ReadEntries implements ConntrackReader by dumping both the IPv4 and IPv6
// conntrack tables over netlink and converting every flow to a
// ConntrackEntry. skipped is always 0 — netlink hands back typed flows,
// not free text with a "malformed line" concept. A per-family read error
// (other than netlink.ErrDumpInterrupted, which still carries a valid — if
// possibly incomplete — partial result per netlink.ConntrackTableList's
// own doc comment) aborts the whole read, classified via
// wrapConntrackNetlinkErr so ConntrackSampler.Run can tell "this node
// cannot provide conntrack at all" apart from a plain transient failure.
func (netlinkConntrackReader) ReadEntries(_ context.Context) ([]ConntrackEntry, int, error) {
	var entries []ConntrackEntry
	for _, family := range [...]netlink.InetFamily{unix.AF_INET, unix.AF_INET6} {
		flows, err := netlink.ConntrackTableList(netlink.ConntrackTable, family)
		if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
			return nil, 0, wrapConntrackNetlinkErr(err)
		}
		for _, f := range flows {
			entries = append(entries, conntrackEntryFromFlow(f))
		}
	}
	return entries, 0, nil
}

// conntrackEntryFromFlow converts one netlink.ConntrackFlow into this
// package's ConntrackEntry, keeping only the original ("Forward") tuple
// and its cumulative counters — this package only ever diffs the original
// direction's Packets/Bytes (see ConntrackEntry's doc comment), the same
// "first occurrence of each key" convention the old text parser used.
// Packets/Bytes are only populated by the kernel when
// net.netfilter.nf_conntrack_acct=1 (accounting) is enabled; zero
// otherwise, which is a valid (if unhelpful) reading, not malformed —
// unchanged from the old parser's documented behavior.
func conntrackEntryFromFlow(f *netlink.ConntrackFlow) ConntrackEntry {
	return ConntrackEntry{
		Proto:   int(f.Forward.Protocol),
		SrcIP:   f.Forward.SrcIP.String(),
		DstIP:   f.Forward.DstIP.String(),
		SrcPort: int(f.Forward.SrcPort),
		DstPort: int(f.Forward.DstPort),
		Packets: int64(f.Forward.Packets),
		Bytes:   int64(f.Forward.Bytes),
	}
}

// wrapConntrackNetlinkErr classifies a netlink.ConntrackTableList error
// into ErrConntrackUnavailable's two documented causes — EPERM/EACCES
// (missing CAP_NET_ADMIN, additionally wrapping ErrConntrackPermissionDenied
// since the operator fix differs) and "the kernel has no nf_conntrack
// netlink support at all" (ENOENT/EPROTONOSUPPORT/EAFNOSUPPORT/
// ENOPROTOOPT) — or leaves any other error (a transient read failure) as a
// plain wrapped error so ConntrackSampler.Run keeps retrying rather than
// treating a blip as permanent unavailability. Mirrors
// internal/host/netlink_linux.go's own wrapConntrackNetlinkErr exactly
// (independent copies, per this package's and internal/host's separate
// readers — see ErrConntrackUnavailable's doc comment).
func wrapConntrackNetlinkErr(err error) error {
	switch {
	case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
		return fmt.Errorf("%w: %w: %w", ErrConntrackUnavailable, ErrConntrackPermissionDenied, err)
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.EPROTONOSUPPORT),
		errors.Is(err, syscall.EAFNOSUPPORT), errors.Is(err, syscall.ENOPROTOOPT):
		return fmt.Errorf("%w: %w", ErrConntrackUnavailable, err)
	default:
		return fmt.Errorf("hostsample: reading conntrack table via netlink: %w", err)
	}
}
