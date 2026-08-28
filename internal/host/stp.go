// SPDX-License-Identifier: Apache-2.0

package host

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// T-3901's STP/RSTP reader: root bridge and per-port role/state, observed
// against a live PVE 9.2.4 host before any type here was written — see
// planning/reports/evidence/pve-9.2.4-bridge-stp-2026-08-27.txt.
//
// Two things that transcript found, load-bearing for everything below:
//
//   - github.com/vishvananda/netlink v1.3.1's Bridge struct carries no STP
//     attribute at all (no IFLA_BR_STP_STATE, no root/port state) — unlike
//     VlanFiltering, which netlink_linux.go's buildBridgeDetail already
//     reads that way. So, like /proc/net/bonding for BondDetail
//     (bonding.go) and sysfs for speed/duplex (ethtool.go), this is a
//     sysfs read: /sys/class/net/<bridge>/bridge/* and
//     /sys/class/net/<bridge>/brif/<port>/* (equivalently reachable per
//     port as /sys/class/net/<port>/brport/*, confirmed identical via
//     readlink -f in the evidence transcript).
//   - The kernel's built-in bridge STP is classic 802.1D, not RSTP: it
//     exposes no native per-port "role" attribute the way a real RSTP/MSTP
//     switch (or a userspace RSTP daemon like mstpd, which PVE does not
//     ship) would. Role is therefore *derived* here from
//     root_id/bridge_id/root_port/state, documented in
//     deriveBridgePortRole below — never read verbatim, because there is
//     no verbatim field to read.

// BridgePortRole is the classic 802.1D port role this package derives for
// one bridge port (deriveBridgePortRole's doc comment explains why it is
// derived rather than read).
type BridgePortRole string

const (
	RoleRoot       BridgePortRole = "root"
	RoleDesignated BridgePortRole = "designated"
	RoleBlocking   BridgePortRole = "blocking"
	RoleDisabled   BridgePortRole = "disabled"
)

// BridgePortSTPState is the kernel's own per-port state vocabulary
// (net/bridge/br_private.h's BR_STATE_* enum), rendered as the same
// strings `bridge -d link show` prints — confirmed against pvecube
// (planning/reports/evidence/pve-9.2.4-bridge-stp-2026-08-27.txt): an
// up/forwarding port reads "forwarding", a down port reads "disabled",
// independent of whether STP is administratively on or off.
type BridgePortSTPState string

const (
	PortStateDisabled   BridgePortSTPState = "disabled"
	PortStateListening  BridgePortSTPState = "listening"
	PortStateLearning   BridgePortSTPState = "learning"
	PortStateForwarding BridgePortSTPState = "forwarding"
	PortStateBlocking   BridgePortSTPState = "blocking"
)

// bridgePortStateFromInt maps sysfs brport/state's raw int (0..4) onto
// BridgePortSTPState. Returns "" for any value outside that range —
// tolerant, never guessed, matching this package's other defensive
// parsers (e.g. host.FDBEntry's Stale bit).
func bridgePortStateFromInt(n int) BridgePortSTPState {
	switch n {
	case 0:
		return PortStateDisabled
	case 1:
		return PortStateListening
	case 2:
		return PortStateLearning
	case 3:
		return PortStateForwarding
	case 4:
		return PortStateBlocking
	default:
		return ""
	}
}

// BridgeSTP is one bridge's live STP/RSTP protocol state: which bridge in
// this L2 domain is currently root, this bridge's own ID, and per-port
// role/state (T-3901). Host-netlink-only, like BridgeDetail.FDB — see
// internal/inventory.Bridge.STPState's doc comment for why it is copied
// straight through rather than merged via the field-provenance machinery.
//
// StpState is the bridge's raw sysfs stp_state value: 0 (off), 1 (kernel
// STP), 2 (RSTP, only ever set by a userspace RSTP daemon — the kernel
// itself only ever sets 0 or 1). Every bridge observed on pvecube reads 0
// (docs/features/topology.md's default PVE bridge has bridge-stp off) —
// values 1/2 are accepted per the kernel's own br_stp_set_enabled()
// range, not fabricated. IsRoot is RootID == BridgeID, which — the
// evidence transcript's headline surprise — is also (and misleadingly)
// true for *every* bridge when StpState is 0: with STP disabled there is
// no protocol running to elect a root, so the kernel simply leaves each
// bridge reporting itself. Callers must gate any "is root"/"port role"
// display on StpState != 0, not on IsRoot alone (internal/topology/
// project.go's badgesOf and stpPortBadges do this).
type BridgeSTP struct {
	RootID       string
	BridgeID     string
	Ports        []BridgePortSTP
	StpState     int
	Priority     int
	RootPort     int
	RootPathCost int
	IsRoot       bool
}

// BridgePortSTP is one bridge port's live STP role/state.
type BridgePortSTP struct {
	Port             string
	DesignatedRoot   string
	DesignatedBridge string
	State            BridgePortSTPState
	Role             BridgePortRole
	PortNo           int
	PathCost         int
	Priority         int
	DesignatedCost   int
}

// sysClassNetSTPBridgeFields / sysClassNetSTPPortFields name the exact
// sysfs files readBridgeSTP reads, restricted to the subset T-3901 needs
// (root/port role+state) — see the evidence transcript's "Field-name/value
// summary" section for the full observed field set, most of which (timers,
// hello/max-age/forward-delay, config_pending, ...) this card has no use
// for.
var (
	sysClassNetSTPBridgeFields = []string{"root_id", "bridge_id", "stp_state", "priority", "root_port", "root_path_cost"}
	sysClassNetSTPPortFields   = []string{"designated_root", "designated_bridge", "designated_cost", "state", "path_cost", "priority", "port_no"}
)

// readBridgeSTP reads /sys/class/net/<name>/bridge/* (bridge-level) and
// every port under /sys/class/net/<name>/brif/*/ (per-port) into a
// BridgeSTP. Best-effort like readIfaceStats: an unreadable bridge sysfs
// directory is an error (this bridge has no STP state to report at all,
// worth surfacing to the caller's decision of whether to log it), but an
// individual missing/unreadable field or port is simply left at its zero
// value / skipped — never fatal, since not every field is populated on
// every kernel version and a port can vanish mid-read.
func readBridgeSTP(name string) (*BridgeSTP, error) {
	brDir := filepath.Join(sysClassNetDir, name, "bridge")
	if _, err := os.Stat(brDir); err != nil {
		return nil, fmt.Errorf("host: reading bridge STP state for %s: %w", name, err)
	}
	bvals := readSysfsFields(brDir, sysClassNetSTPBridgeFields)

	portNames, _ := listBridgePorts(name)
	portVals := make(map[string]map[string]string, len(portNames))
	for _, p := range portNames {
		pdir := filepath.Join(sysClassNetDir, name, "brif", p)
		portVals[p] = readSysfsFields(pdir, sysClassNetSTPPortFields)
	}
	return parseBridgeSTP(bvals, portVals), nil
}

// listBridgePorts lists the port names under /sys/class/net/<name>/brif/.
func listBridgePorts(name string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(sysClassNetDir, name, "brif"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// readSysfsFields reads each named file in dir into a map, skipping (never
// erroring on) any file that is missing, unreadable, or empty — the same
// per-field tolerance readIfaceStats (stats.go) already establishes for
// sysfs counter reads.
func readSysfsFields(dir string, files []string) map[string]string {
	out := make(map[string]string, len(files))
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(data)); v != "" {
			out[f] = v
		}
	}
	return out
}

// parseBridgeSTP is readBridgeSTP's pure counterpart: given already-read
// sysfs key/value maps (bridge-level and per-port, keyed by the same file
// names sysClassNetSTPBridgeFields/sysClassNetSTPPortFields name), it
// derives the structured BridgeSTP, including each port's Role — a value
// the kernel does not expose (see this file's doc comment). Independent of
// I/O, so it is table-tested directly against maps built from the evidence
// transcript's real values rather than requiring a live sysfs tree.
func parseBridgeSTP(bvals map[string]string, portVals map[string]map[string]string) *BridgeSTP {
	b := &BridgeSTP{
		RootID:       bvals["root_id"],
		BridgeID:     bvals["bridge_id"],
		StpState:     atoiOr(bvals["stp_state"], 0),
		Priority:     atoiOr(bvals["priority"], 0),
		RootPort:     atoiOr(bvals["root_port"], 0),
		RootPathCost: atoiOr(bvals["root_path_cost"], 0),
	}
	b.IsRoot = b.RootID != "" && b.RootID == b.BridgeID

	names := make([]string, 0, len(portVals))
	for p := range portVals {
		names = append(names, p)
	}
	sort.Strings(names)

	for _, p := range names {
		v := portVals[p]
		portNo := int(parseSysfsInt(v["port_no"]))
		state := bridgePortStateFromInt(atoiOr(v["state"], -1))
		port := BridgePortSTP{
			Port:             p,
			State:            state,
			DesignatedRoot:   v["designated_root"],
			DesignatedBridge: v["designated_bridge"],
			DesignatedCost:   atoiOr(v["designated_cost"], 0),
			PathCost:         atoiOr(v["path_cost"], 0),
			Priority:         atoiOr(v["priority"], 0),
			PortNo:           portNo,
		}
		port.Role = deriveBridgePortRole(state, portNo, b.IsRoot, b.RootPort)
		b.Ports = append(b.Ports, port)
	}
	return b
}

// deriveBridgePortRole computes the classic 802.1D port role for one port.
// The Linux kernel's built-in bridge STP exposes no native role attribute
// (RSTP alternate/backup roles are a userspace-RSTP-daemon concept PVE
// does not ship — see this file's doc comment); root_id/bridge_id/
// root_port/state is all there is to derive from:
//
//   - a port whose kernel state is "disabled" (down, or STP has not yet
//     brought it up) is RoleDisabled;
//   - a port whose kernel state is "blocking" is RoleBlocking — the answer
//     to "which port is this bridge blocking to prevent a loop";
//   - otherwise, on a non-root bridge, the port whose port_no matches the
//     bridge's own root_port is RoleRoot (the port this bridge reaches the
//     root through);
//   - every other forwarding/listening/learning port is RoleDesignated.
//
// portNo == 0 never matches bridgeRootPort (0 is sysfs's "no root port",
// i.e. this bridge doesn't have one — it's the root itself) even in the
// pathological case bridgeRootPort is also 0, which is exactly the
// bridgeIsRoot case and never reaches this branch.
func deriveBridgePortRole(state BridgePortSTPState, portNo int, bridgeIsRoot bool, bridgeRootPort int) BridgePortRole {
	switch state {
	case PortStateDisabled:
		return RoleDisabled
	case PortStateBlocking:
		return RoleBlocking
	}
	if !bridgeIsRoot && portNo != 0 && portNo == bridgeRootPort {
		return RoleRoot
	}
	return RoleDesignated
}

// parseSysfsInt parses a sysfs integer field that may be rendered in
// decimal or, like port_no/port_id, "0x"-prefixed hex (base 0 auto-detects
// which). Returns 0 on any parse failure, never an error — this package's
// standard "unreported, not guessed" convention for a best-effort field.
func parseSysfsInt(s string) int64 {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		return 0
	}
	return n
}
