//go:build linux

package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

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
	// OVSVSCtlPath is the ovs-vsctl binary name/path OVSStatus invokes;
	// defaults to "ovs-vsctl" (resolved via PATH). Overridable for tests.
	OVSVSCtlPath string
	// DHCPLeaseGlob is the filesystem glob DHCPLeases reads every matched
	// file from (T-406, docs/features/sdn.md §5); defaults to
	// "/var/lib/misc/dnsmasq.*.leases" — PVE SDN's own per-zone dnsmasq
	// instances each write their lease file under this convention.
	// Overridable for tests. **Needs hardware validation**: this glob is
	// this codebase's own best inference (PVE's exact dnsmasq lease-file
	// naming is not otherwise documented in this repo) rather than
	// verified against a live PVE cluster — see this task's completion
	// report.
	DHCPLeaseGlob string
	// LLDPCommand is the argv used to fetch LLDP neighbor data as JSON;
	// defaults to `lldpctl -f json`. Overridable for tests/environments
	// where lldpd is installed under a different name or path.
	LLDPCommand []string
	// BGPSummaryCommand is the argv used to fetch FRR's BGP peering
	// summary as JSON; defaults to `vtysh -c "show bgp summary json"`
	// (T-404, docs/features/sdn.md §3). Overridable for tests.
	BGPSummaryCommand []string
	// EVPNVNICommand is the argv used to fetch FRR's EVPN VNI table as
	// JSON; defaults to `vtysh -c "show evpn vni json"`. Overridable for
	// tests.
	EVPNVNICommand []string
	// CorosyncStatusCommand is the argv used to fetch corosync's live ring
	// status; defaults to `corosync-cfgtool -s` (T-803, docs/features/
	// monitoring.md §5). Overridable for tests.
	CorosyncStatusCommand []string
}

// NewReal constructs a Real reader with the standard Debian/Proxmox paths
// and lldpd/vtysh/ovs-vsctl commands.
func NewReal() *Real {
	return &Real{
		InterfacesPath:        "/etc/network/interfaces",
		InterfacesPendingPath: "/etc/network/interfaces.new",
		LLDPCommand:           []string{"lldpctl", "-f", "json"},
		BGPSummaryCommand:     []string{"vtysh", "-c", "show bgp summary json"},
		EVPNVNICommand:        []string{"vtysh", "-c", "show evpn vni json"},
		CorosyncStatusCommand: []string{"corosync-cfgtool", "-s"},
		OVSVSCtlPath:          "ovs-vsctl",
		DHCPLeaseGlob:         "/var/lib/misc/dnsmasq.*.leases",
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

// FRRBGPSummary implements Reader by shelling out to FRR's vtysh in JSON
// mode. Like LLDP, there is no netlink/procfs source for BGP session
// state — it is strictly FRR's own userspace daemon state, reachable only
// via vtysh — so this is necessarily exec-based.
func (r *Real) FRRBGPSummary(ctx context.Context, _ string) ([]byte, error) {
	return r.runFRRCommand(ctx, r.BGPSummaryCommand)
}

// FRREVPNVNI implements Reader by shelling out to FRR's vtysh in JSON mode.
func (r *Real) FRREVPNVNI(ctx context.Context, _ string) ([]byte, error) {
	return r.runFRRCommand(ctx, r.EVPNVNICommand)
}

// runFRRCommand runs a fixed-argv vtysh invocation (never shell-
// interpolated) and distinguishes "vtysh is not installed at all" (FRR
// entirely absent on this node — docs/features/sdn.md §3's documented
// graceful-degradation case) from any other failure (permission, timeout,
// FRR installed but erroring), exactly like LLDP's own exec.ErrNotFound
// detection.
func (r *Real) runFRRCommand(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("host: frr: no command configured")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // fixed, config-supplied argv, not user input
	out, err := cmd.Output()
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			return nil, fmt.Errorf("host: %w: %v", ErrFRRUnavailable, err)
		}
		return nil, fmt.Errorf("host: running %v: %w", argv, err)
	}
	return out, nil
}

// CorosyncStatus implements Reader (T-803) by shelling out to
// corosync-cfgtool in status mode. Like FRR/LLDP, there is no netlink/procfs
// source for corosync's own live ring health — it is strictly corosync's own
// userspace daemon state, reachable only via corosync-cfgtool — so this is
// necessarily exec-based, and distinguishes "corosync-cfgtool is not
// installed at all" (ErrCorosyncUnavailable) from any other failure exactly
// like runFRRCommand's exec.ErrNotFound detection.
func (r *Real) CorosyncStatus(ctx context.Context, _ string) ([]byte, error) {
	argv := r.CorosyncStatusCommand
	if len(argv) == 0 {
		return nil, fmt.Errorf("host: corosync: no command configured")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // fixed, config-supplied argv, not user input
	out, err := cmd.Output()
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			return nil, fmt.Errorf("host: %w: %v", ErrCorosyncUnavailable, err)
		}
		return nil, fmt.Errorf("host: running %v: %w", argv, err)
	}
	return out, nil
}

// DHCPLeases implements Reader (T-406) by globbing DHCPLeaseGlob and
// concatenating every matched file's raw content. A single unreadable
// lease file (permissions, or a race with dnsmasq mid-rewrite) is skipped
// rather than failing the whole read — one zone's lease file being
// temporarily unavailable should not blank every other zone's leases. No
// matches (the common case: no DHCP-managed SDN zone configured on this
// node at all) returns an empty, non-error result.
func (r *Real) DHCPLeases(_ context.Context, _ string) ([]byte, error) {
	glob := r.DHCPLeaseGlob
	if glob == "" {
		glob = "/var/lib/misc/dnsmasq.*.leases"
	}
	matches, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("host: dhcp leases: globbing %s: %w", glob, err)
	}
	sort.Strings(matches)
	var buf bytes.Buffer
	for _, path := range matches {
		content, readErr := os.ReadFile(path) //nolint:gosec // fixed glob pattern, not user input
		if readErr != nil {
			continue
		}
		buf.Write(content)
		if len(content) > 0 && content[len(content)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes(), nil
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
		ls.VFs = make([]VF, len(attrs.Vfs))
		for i, vf := range attrs.Vfs {
			ls.VFs[i] = VF{
				ID:         vf.ID,
				MacAddr:    vf.Mac.String(),
				VLAN:       vf.Vlan,
				SpoofCheck: vf.Spoofchk,
				Trust:      vf.Trust != 0,
				// PCIAddr is best-effort (needs-hardware-validation): the
				// exact virtfnN sysfs symlink naming/ordering is this
				// package's own inference from the kernel's SR-IOV sysfs
				// convention, not verified against real SR-IOV hardware —
				// see planning/reports/needs-hardware-validation.md.
				PCIAddr: sysfsVFPCIAddr(attrs.Name, vf.ID),
			}
		}
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
			applyBondADState(bd, links)
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

// applyBondADState opportunistically overlays per-slave 802.3ad actor
// port-state bits from netlink onto bd's already-/proc-parsed slaves, when
// the running kernel exposes them: an enslaved link's own LinkAttrs.Slave
// carries a *netlink.BondSlave with AdActorOperPortState/
// AdPartnerOperPortState (IFLA_BOND_SLAVE_AD_ACTOR_OPER_PORT_STATE /
// IFLA_BOND_SLAVE_AD_PARTNER_OPER_PORT_STATE) whenever the slave is part of
// a live 802.3ad aggregator — genuinely parsed by
// github.com/vishvananda/netlink v1.3.1 (parseBondSlaveData), unlike the
// bond-level IFLA_BOND_AD_INFO nested attribute (Bond.AdInfo), which that
// library version leaves an explicit "// TODO: implement" stub. That means
// actor/partner system ID and key are NOT recoverable from netlink today —
// only from /proc/net/bonding's "details actor/partner lacp pdu" block
// (readBondDetail) — so this overlay covers just the synchronized/
// collecting/distributing bits netlink can genuinely give us, preferring
// them over whatever readBondDetail already decoded from /proc (T-804: "a
// netlink-sourced read when the kernel exposes it, falling back to /proc
// otherwise"). Best-effort: a bond mode other than 802.3ad, or a kernel/
// driver that doesn't populate the attribute, leaves both fields zero, in
// which case /proc's own decode (if any) is left untouched. Needs hardware
// validation: exact AD-info attribute availability across the kernel
// versions vnprox targets (see planning/reports/needs-hardware-validation.md).
func applyBondADState(bd *BondDetail, links []netlink.Link) {
	if bd == nil {
		return
	}
	byName := make(map[string]netlink.Link, len(links))
	for _, l := range links {
		byName[l.Attrs().Name] = l
	}
	for i := range bd.Slaves {
		l, ok := byName[bd.Slaves[i].Name]
		if !ok {
			continue
		}
		ns, ok := l.Attrs().Slave.(*netlink.BondSlave)
		if !ok || ns == nil {
			continue
		}
		if ns.AdActorOperPortState == 0 && ns.AdPartnerOperPortState == 0 {
			// Both zero almost always means "the kernel never populated
			// this attribute" (no 802.3ad aggregator on this slave) rather
			// than a genuinely all-clear-bits negotiated state — leave
			// whatever /proc already decoded (if anything) alone.
			continue
		}
		sync, collecting, distributing := lacpPortStateBits(int(ns.AdActorOperPortState))
		bd.Slaves[i].ActorSynchronized = sync
		bd.Slaves[i].ActorCollecting = collecting
		bd.Slaves[i].ActorDistributing = distributing
		bd.Slaves[i].LACPDetailSet = true
	}
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
			Stale:     n.State&netlink.NUD_STALE != 0,
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

var _ OVSReader = (*Real)(nil)

// OVSStatus implements OVSReader by shelling out to ovs-vsctl three times
// (fixed argv: -f json --columns=<fixed list> list <table>, once each for
// Bridge/Port/Interface) and joining the results — see ovsvsctl.go's doc
// comment for why this cannot reuse Links()'s netlink path. Node is
// currently unused (like every other Real method, it always reports this
// node's own state — see Reader's doc comment on routing a peer read).
func (r *Real) OVSStatus(ctx context.Context, _ string) ([]OVSBridgeStatus, error) {
	bridgeJSON, err := r.runOVSVSCtl(ctx, "Bridge", ovsBridgeColumns)
	if err != nil {
		return nil, err
	}
	portJSON, err := r.runOVSVSCtl(ctx, "Port", ovsPortColumns)
	if err != nil {
		return nil, err
	}
	ifaceJSON, err := r.runOVSVSCtl(ctx, "Interface", ovsInterfaceColumns)
	if err != nil {
		return nil, err
	}
	return BuildOVSBridgeStatus(bridgeJSON, portJSON, ifaceJSON)
}

// runOVSVSCtl runs `ovs-vsctl -f json --columns=<columns> list <table>` and
// returns its stdout, wrapping "binary not found" in ErrOVSUnavailable so
// callers can degrade gracefully (T-407 AC4) instead of treating an absent
// ovs-vsctl the same as any other failure.
func (r *Real) runOVSVSCtl(ctx context.Context, table string, columns []string) ([]byte, error) {
	path := r.OVSVSCtlPath
	if path == "" {
		path = "ovs-vsctl"
	}
	args := []string{"-f", "json", "--columns=" + strings.Join(columns, ","), "list", table}
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // fixed, config-supplied argv, not user input
	out, err := cmd.Output()
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			return nil, fmt.Errorf("host: %w: %v", ErrOVSUnavailable, err)
		}
		return nil, fmt.Errorf("host: running %s %v: %w", path, args, err)
	}
	return out, nil
}
