// neighbors.go implements T-805's ARP/neighbor-table read: Reader.Neighbors
// (docs/features/ipam.md §1's "known gap" — internal/ipam.NeighborSource had
// no host-level data source until this task). The real, per-node collection
// is OS-specific (neighbors_linux.go / neighbors_other.go, mirroring
// netlink_linux.go / netlink_other.go's split for Links) and reads two
// distinct kernel tables — /proc/net/arp for IPv4 (parsed here, no build
// tag, so the parser is unit-testable on any GOOS exactly like
// ParseDHCPLeases/ParseCorosyncConf) and the IPv6 neighbor table via
// netlink (Linux-only, in neighbors_linux.go, since there is no /proc text
// equivalent for IPv6 neighbors the way ARP gives IPv4 one). FixtureReader's
// Neighbors (below) needs no OS-specific split at all: pvemock's fixture
// data is already structured, not raw kernel text.

package host

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// Neighbor is one resolved ARP/IPv6-neighbor-table entry
// (docs/features/ipam.md §1's ARP/neighbor enrichment source): an IP
// address paired with the MAC currently believed to own it, the interface
// it was learned on, and its kernel neighbor-cache state.
type Neighbor struct {
	IP    string
	MAC   string
	Iface string
	// State is the kernel's neighbor-cache state, normalized to the
	// uppercase names below. Reader.Neighbors only ever returns
	// NeighborReachable/NeighborStale/NeighborPermanent entries — FAILED and
	// INCOMPLETE are dropped by isResolvedNeighborState before a Neighbor
	// value is ever constructed, so this field's value space at the call
	// site is smaller than the full set of constants defines.
	State string
}

// Kernel neighbor-cache states (Linux's NUD_* naming, uppercased). Only
// Reachable/Stale/Permanent are ever returned by Reader.Neighbors —
// Failed/Incomplete exist here so the parser and the netlink-backed IPv6
// read (neighbors_linux.go) share one vocabulary before filtering, rather
// than each inventing its own "not resolved yet" spelling.
const (
	NeighborReachable  = "REACHABLE"
	NeighborStale      = "STALE"
	NeighborPermanent  = "PERMANENT"
	NeighborFailed     = "FAILED"
	NeighborIncomplete = "INCOMPLETE"
)

// isResolvedNeighborState reports whether state is one of the three states
// Reader.Neighbors surfaces (docs/features/ipam.md §1, T-805's task card:
// "filtered to resolved states (REACHABLE/STALE/PERMANENT) — FAILED/
// INCOMPLETE entries excluded").
func isResolvedNeighborState(state string) bool {
	switch state {
	case NeighborReachable, NeighborStale, NeighborPermanent:
		return true
	default:
		return false
	}
}

// procNetARPPath is /proc/net/arp, overridable in tests — the same
// override-a-package-var-for-testability convention procNetBondingDir
// (bonding.go) and DefaultCorosyncConfPath's callers already use.
var procNetARPPath = "/proc/net/arp"

// arpFlagComplete/arpFlagPermanent are /proc/net/arp's Flags column bits
// (Linux's ATF_COMPLETE/ATF_PERM from <linux/if_arp.h>). /proc/net/arp's
// text format only ever reports a resolved entry as "complete" (optionally
// also "permanent") or as all-zero flags for an entry the kernel hasn't
// resolved yet (no MAC known) — the file has no bit distinguishing
// NUD_REACHABLE from NUD_STALE the way netlink's fuller neighbor dump does,
// so ParseProcNetARP normalizes every resolved, non-permanent entry to
// NeighborReachable. This is an inherent limitation of the /proc/net/arp
// text format, not a parsing gap: the IPv6 neighbor read (neighbors_linux.go)
// goes through netlink instead and gets the full REACHABLE/STALE
// distinction real ARP entries lack here.
const (
	arpFlagComplete  = 0x2
	arpFlagPermanent = 0x4
)

// ParseProcNetARP parses /proc/net/arp's fixed-width text table:
//
//	IP address       HW type     Flags       HW address            Mask     Device
//	192.168.1.10     0x1         0x2         08:00:27:12:34:56     *        eth0
//
// into resolved Neighbor values, silently skipping the header line and any
// entry whose Flags bit shows it isn't resolved yet (ATF_COMPLETE unset —
// the kernel's "still waiting on ARP" INCOMPLETE state, ip/mac known only as
// the placeholder 00:00:00:00:00:00) — T-805 acceptance criterion 1's
// "FAILED/INCOMPLETE entries excluded" (/proc/net/arp cannot represent a
// terminally-FAILED entry distinctly from INCOMPLETE; the kernel simply
// keeps such rows at all-zero flags either way, so both collapse to the one
// "not yet resolved, skip it" case here). A line with fewer than six
// whitespace-separated fields is skipped defensively rather than failing
// the whole parse, the same tolerant-corpus posture ParseDHCPLeases takes.
func ParseProcNetARP(data []byte) ([]Neighbor, error) {
	var out []Neighbor
	scanner := bufio.NewScanner(bytes.NewReader(data))
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if first {
			// Header line ("IP address  HW type  Flags  HW address  Mask
			// Device") — always present in a real /proc/net/arp read, and
			// harmless to skip unconditionally even if a test fixture
			// omits it (an all-field-header line never parses as a valid
			// entry below anyway).
			first = false
			if line == "" || strings.HasPrefix(line, "IP address") {
				continue
			}
		}
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		flags, err := strconv.ParseInt(strings.TrimPrefix(fields[2], "0x"), 16, 64)
		if err != nil {
			continue
		}
		if flags&arpFlagComplete == 0 {
			// Not yet resolved (INCOMPLETE) — excluded per this function's
			// doc comment.
			continue
		}
		state := NeighborReachable
		if flags&arpFlagPermanent != 0 {
			state = NeighborPermanent
		}
		out = append(out, Neighbor{IP: fields[0], MAC: fields[3], Iface: fields[5], State: state})
	}
	return out, scanner.Err()
}

// readProcNetARP reads and parses procNetARPPath. Extracted from
// neighbors_linux.go's Real.Neighbors so it's exercised by any GOOS build
// (the read itself simply fails cleanly with a file-not-found error on a
// non-Linux dev machine or in a container without /proc/net/arp, same as
// every other os.ReadFile-based read in this package).
func readProcNetARP() ([]Neighbor, error) {
	data, err := os.ReadFile(procNetARPPath)
	if err != nil {
		return nil, err
	}
	return ParseProcNetARP(data)
}

// Neighbors implements Reader for FixtureReader (T-805): delegates to
// pvemock's own fixture-declared neighbor table, converting
// pvemock.Neighbor to this package's Neighbor and filtering to resolved
// states — pvemock intentionally returns every fixture-declared entry
// unfiltered (see pvemock/neighbors.go's doc comment), so the filtering
// contract Reader.Neighbors documents is exercised here, the same seam
// every other Reader implementation applies it at.
func (f *FixtureReader) Neighbors(ctx context.Context, node string) ([]Neighbor, error) {
	nr, ok := f.r.(interface {
		Neighbors(ctx context.Context, node string) ([]pvemock.Neighbor, error)
	})
	if !ok {
		// Every production pvemockReader (*pvemock.FixtureHostReader)
		// implements this; only a hand-rolled test double missing the
		// method would land here, in which case "no neighbors" is the
		// right degraded answer rather than a panic.
		return nil, nil
	}
	raw, err := nr.Neighbors(ctx, node)
	if err != nil {
		return nil, wrapFixtureErr(err)
	}
	out := make([]Neighbor, 0, len(raw))
	for _, n := range raw {
		if !isResolvedNeighborState(n.State) {
			continue
		}
		out = append(out, Neighbor{IP: n.IP, MAC: n.Mac, Iface: n.Iface, State: n.State})
	}
	return out, nil
}
