//go:build linux

package host

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// Real is vnprox's production Reader: it reads this node's actual
// /etc/network/interfaces file, live netlink link/address/neighbor state,
// /proc/net/bonding/*, /sys/class/net/*/statistics, and LLDP neighbor data
// via lldpd's lldpctl. It always reports its own node's state — routing a
// read to a cluster peer is the caller's responsibility (see the Reader
// doc comment in reader.go).
type Real struct {
	// InterfacesPath is /etc/network/interfaces, overridable for tests.
	InterfacesPath string
	// InterfacesPendingPath is the ifupdown2 staged-config file
	// (/etc/network/interfaces.new), overridable for tests.
	InterfacesPendingPath string
	// LLDPCommand is the argv used to fetch LLDP neighbor data as JSON;
	// defaults to `lldpctl -f json`. Overridable for tests/environments
	// where lldpd is installed under a different name or path.
	LLDPCommand []string
}

// NewReal constructs a Real reader with the standard Debian/Proxmox paths
// and lldpd command.
func NewReal() *Real {
	return &Real{
		InterfacesPath:        "/etc/network/interfaces",
		InterfacesPendingPath: "/etc/network/interfaces.new",
		LLDPCommand:           []string{"lldpctl", "-f", "json"},
	}
}

var _ Reader = (*Real)(nil)

// InterfacesFile implements Reader.
func (r *Real) InterfacesFile(_ context.Context, _ string, includePending bool) (string, error) {
	path := r.InterfacesPath
	if includePending {
		if _, err := os.Stat(r.InterfacesPendingPath); err == nil {
			path = r.InterfacesPendingPath
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("host: reading %s: %w", path, err)
	}
	return string(data), nil
}

// Stats implements Reader by reading /sys/class/net/*/statistics for
// every interface sysfs knows about.
func (r *Real) Stats(_ context.Context, _ string) (map[string]IfaceStats, error) {
	names, err := listIfaceNames()
	if err != nil {
		return nil, err
	}
	out := make(map[string]IfaceStats, len(names))
	for _, n := range names {
		s, err := readIfaceStats(n)
		if err != nil {
			// Rare (e.g. an interface mid-teardown between the
			// listing and the read): skip rather than fail the whole
			// snapshot.
			continue
		}
		out[n] = s
	}
	return out, nil
}

// LLDP implements Reader by shelling out to lldpd's lldpctl in JSON mode.
// There is no in-kernel/netlink source for LLDP neighbor data — it is
// strictly a userspace daemon protocol — so this is necessarily
// exec-based, unlike the rest of this package's real implementation.
func (r *Real) LLDP(ctx context.Context, _ string) ([]byte, error) {
	if len(r.LLDPCommand) == 0 {
		return nil, fmt.Errorf("host: LLDP: no command configured")
	}
	cmd := exec.CommandContext(ctx, r.LLDPCommand[0], r.LLDPCommand[1:]...) //nolint:gosec // fixed, config-supplied argv, not user input
	out, err := cmd.Output()
	if err != nil {
		// Distinguish "lldpd/lldpctl is not installed at all" (the
		// documented graceful-degradation case,
		// docs/features/lldp-discovery.md §1) from any other failure
		// (permission, timeout, lldpd running but erroring) so callers can
		// tell the two apart without string-matching. exec.LookPath (which
		// CommandContext calls internally to resolve argv[0]) returns
		// *exec.Error wrapping exec.ErrNotFound when the binary is missing
		// from PATH.
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			return nil, fmt.Errorf("host: %w: %v", ErrLLDPUnavailable, err)
		}
		return nil, fmt.Errorf("host: running %v: %w", r.LLDPCommand, err)
	}
	return out, nil
}

// Links implements Reader using github.com/vishvananda/netlink for link,
// address, bridge-VLAN, and FDB state, /proc/net/bonding for bond runtime
// detail, and the ethtool ioctl/sysfs paths (ethtool.go) for speed/duplex
// and driver/bus info.
func (r *Real) Links(_ context.Context, _ string) ([]LinkState, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("host: netlink link list: %w", err)
	}

	byIndex := make(map[int]netlink.Link, len(links))
	for _, l := range links {
		byIndex[l.Attrs().Index] = l
	}

	// Best-effort: absent on kernels/permission levels that don't
	// support these dumps, in which case the corresponding detail is
	// simply left empty rather than failing the whole read.
	vlanTable, _ := netlink.BridgeVlanList()
	fdb, _ := netlink.NeighList(0, unix.AF_BRIDGE)

	out := make([]LinkState, 0, len(links))
	for _, l := range links {
		out = append(out, buildLinkState(l, links, byIndex, vlanTable, fdb))
	}
	return out, nil
}

func buildLinkState(
	l netlink.Link,
	links []netlink.Link,
	byIndex map[int]netlink.Link,
	vlanTable map[int32][]*nl.BridgeVlanInfo,
	fdb []netlink.Neigh,
) LinkState {
	attrs := l.Attrs()
	ls := LinkState{
		Name:      attrs.Name,
		Index:     attrs.Index,
		MTU:       attrs.MTU,
		Mac:       attrs.HardwareAddr.String(),
		OperState: attrs.OperState.String(),
	}
	ls.LinkUp = attrs.OperState == netlink.OperUp ||
		(attrs.OperState == netlink.OperUnknown && attrs.Flags&net.FlagRunning != 0)

	if attrs.MasterIndex != 0 {
		if m, ok := byIndex[attrs.MasterIndex]; ok {
			ls.Master = m.Attrs().Name
		}
	}
	if len(attrs.Vfs) > 0 {
		ls.SRIOVNumVFs = len(attrs.Vfs)
	}

	switch v := l.(type) {
	case *netlink.Bridge:
		ls.Kind = "bridge"
		ls.Bridge = buildBridgeDetail(attrs.Index, v, links, byIndex, vlanTable, fdb)
		ls.Members = membersOf(attrs.Index, links)
	case *netlink.Bond:
		ls.Kind = "bond"
		ls.Members = membersOf(attrs.Index, links)
		if bd, err := readBondDetail(attrs.Name); err == nil {
			ls.Bond = bd
		}
	case *netlink.Vlan:
		ls.Kind = "vlan"
		ls.VlanID = v.VlanId
		if p, ok := byIndex[attrs.ParentIndex]; ok {
			ls.VlanParent = p.Attrs().Name
		}
	case *netlink.Veth:
		ls.Kind = "veth"
	case *netlink.Vxlan:
		ls.Kind = "vxlan"
	case *netlink.Dummy:
		ls.Kind = "dummy"
	case *netlink.GenericLink:
		ls.Kind = v.LinkType
	default:
		if l.Type() == "device" {
			ls.Kind = "physical"
		} else {
			ls.Kind = l.Type()
		}
	}

	if addrs, err := netlink.AddrList(l, netlink.FAMILY_ALL); err == nil {
		for _, a := range addrs {
			if a.IPNet != nil {
				ls.Addresses = append(ls.Addresses, a.IPNet.String())
			}
		}
	}

	ls.SpeedMbps, ls.Duplex = speedDuplex(attrs.Name)
	ls.Driver, ls.PCIAddr = driverInfo(attrs.Name)

	return ls
}

// membersOf returns the names of every link enslaved to masterIndex
// (a bridge's ports or a bond's slaves).
func membersOf(masterIndex int, links []netlink.Link) []string {
	var out []string
	for _, l := range links {
		if l.Attrs().MasterIndex == masterIndex {
			out = append(out, l.Attrs().Name)
		}
	}
	return out
}

// buildBridgeDetail assembles VLAN-awareness, the bridge's self VLAN
// table, per-port VLAN membership, and FDB entries for one bridge link.
func buildBridgeDetail(
	bridgeIndex int,
	b *netlink.Bridge,
	links []netlink.Link,
	byIndex map[int]netlink.Link,
	vlanTable map[int32][]*nl.BridgeVlanInfo,
	fdb []netlink.Neigh,
) *BridgeDetail {
	bd := &BridgeDetail{}
	if b.VlanFiltering != nil {
		bd.VlanAware = *b.VlanFiltering
	}

	if vlanTable != nil {
		if self, ok := vlanTable[int32(bridgeIndex)]; ok {
			bd.VLANs = selfVlanRanges(self)
		}
		bd.PortVLANs = make(map[string][]PortVlan)
		for _, l := range links {
			if l.Attrs().MasterIndex != bridgeIndex {
				continue
			}
			entries, ok := vlanTable[int32(l.Attrs().Index)]
			if !ok {
				continue
			}
			bd.PortVLANs[l.Attrs().Name] = portVlans(entries)
		}
	}

	for _, n := range fdb {
		port, isMember := "", n.LinkIndex == bridgeIndex
		if p, ok := byIndex[n.LinkIndex]; ok {
			if p.Attrs().MasterIndex == bridgeIndex {
				isMember = true
				port = p.Attrs().Name
			}
		}
		if !isMember {
			// NeighList(0, AF_BRIDGE) returns every bridge's FDB
			// system-wide; only keep entries that belong to this
			// bridge (its own address or one of its ports).
			continue
		}
		bd.FDB = append(bd.FDB, FDBEntry{
			Mac:       n.HardwareAddr.String(),
			Port:      port,
			Vlan:      n.Vlan,
			Master:    n.Flags&netlink.NTF_MASTER != 0,
			Permanent: n.State&netlink.NUD_PERMANENT != 0,
		})
	}

	return bd
}

// vlanSpan is one contiguous VLAN span from a bridge VLAN table dump,
// together with the BRIDGE_VLAN_INFO_* flags that applied to it.
type vlanSpan struct {
	Range VidRange
	Flags uint16
}

// vlanSpans decodes a raw per-ifindex bridge VLAN table entry list into
// contiguous spans. The kernel itself compacts a contiguous run of VIDs
// (e.g. "2-4094") into exactly two entries — one flagged
// BRIDGE_VLAN_INFO_RANGE_BEGIN, one BRIDGE_VLAN_INFO_RANGE_END — rather
// than one entry per VID, so a naive "collapse consecutive integers"
// reducer over the raw entries would badly under-count a large range
// (seeing only its two boundary VIDs, far apart). This walks begin/end
// pairs explicitly instead, and treats any entry with neither flag as a
// single-VID span.
func vlanSpans(entries []*nl.BridgeVlanInfo) []vlanSpan {
	if len(entries) == 0 {
		return nil
	}
	out := make([]vlanSpan, 0, len(entries))
	pendingStart := -1
	var pendingFlags uint16
	for _, e := range entries {
		switch {
		case e.Flags&nl.BRIDGE_VLAN_INFO_RANGE_BEGIN != 0:
			pendingStart = int(e.Vid)
			pendingFlags = e.Flags
		case e.Flags&nl.BRIDGE_VLAN_INFO_RANGE_END != 0:
			start := pendingStart
			flags := pendingFlags | e.Flags
			if start == -1 {
				// Defensive: an END with no preceding BEGIN (malformed
				// or truncated dump) — treat as a single-VID span
				// rather than dropping it.
				start = int(e.Vid)
				flags = e.Flags
			}
			out = append(out, vlanSpan{Range: VidRange{Low: start, High: int(e.Vid)}, Flags: flags})
			pendingStart = -1
		default:
			out = append(out, vlanSpan{Range: VidRange{Low: int(e.Vid), High: int(e.Vid)}, Flags: e.Flags})
		}
	}
	return out
}

// selfVlanRanges extracts just the VID ranges from a bridge-vlan-table
// entry list, for a bridge's own (self) VLAN table.
func selfVlanRanges(entries []*nl.BridgeVlanInfo) []VidRange {
	spans := vlanSpans(entries)
	if len(spans) == 0 {
		return nil
	}
	out := make([]VidRange, len(spans))
	for i, s := range spans {
		out[i] = s.Range
	}
	return out
}

// portVlans extracts per-port VLAN membership (with PVID/untagged flags)
// from a bridge-vlan-table entry list.
func portVlans(entries []*nl.BridgeVlanInfo) []PortVlan {
	spans := vlanSpans(entries)
	if len(spans) == 0 {
		return nil
	}
	out := make([]PortVlan, len(spans))
	for i, s := range spans {
		out[i] = PortVlan{
			Vids:     s.Range,
			PVID:     s.Flags&nl.BRIDGE_VLAN_INFO_PVID != 0,
			Untagged: s.Flags&nl.BRIDGE_VLAN_INFO_UNTAGGED != 0,
		}
	}
	return out
}
