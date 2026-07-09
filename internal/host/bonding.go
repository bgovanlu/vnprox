package host

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// procNetBondingDir is /proc/net/bonding, overridable in tests.
var procNetBondingDir = "/proc/net/bonding"

// readBondDetail reads and parses /proc/net/bonding/<name> for bond
// runtime detail that netlink's IFLA_BOND attributes do not portably
// expose (this package targets kernels old and new; the /proc text format
// has been stable across bonding driver versions since well before any
// kernel vnprox needs to support).
func readBondDetail(name string) (*BondDetail, error) {
	data, err := os.ReadFile(procNetBondingDir + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("host: reading bonding state for %s: %w", name, err)
	}
	return parseBondingProc(data), nil
}

// parseBondingProc parses the text format of /proc/net/bonding/<name>. It
// is deliberately tolerant: unrecognized lines are ignored rather than
// treated as errors, since the file carries plenty of driver/mode-specific
// detail (802.3ad partner/actor state, ARP monitor config, ...) this
// package does not need.
func parseBondingProc(data []byte) *BondDetail {
	bd := &BondDetail{}
	var cur *BondSlave

	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		key, val, ok := splitColon(line)
		if !ok {
			continue
		}

		if key == "Slave Interface" {
			bd.Slaves = append(bd.Slaves, BondSlave{Name: val})
			cur = &bd.Slaves[len(bd.Slaves)-1]
			continue
		}

		if cur != nil && isSlaveField(key) {
			applySlaveField(cur, key, val)
			continue
		}

		switch key {
		case "Bonding Mode":
			bd.Mode = normalizeBondMode(val)
		case "Transmit Hash Policy":
			bd.XmitHashPolicy = firstToken(val)
		case "MII Status":
			// The bond-level MII Status line appears before any "Slave
			// Interface:" line; once cur != nil this key belongs to a
			// slave block instead and is handled above.
			bd.MIIStatus = val
		case "LACP rate":
			bd.LACPRate = val
		case "Currently Active Slave":
			bd.ActiveSlave = val
		}
	}

	// Approximate per-slave "active" when the bond doesn't report a
	// single "Currently Active Slave" (e.g. 802.3ad, balance-xor): treat
	// every slave whose MII status is up as active. For active-backup
	// and similar modes, ActiveSlave names exactly one slave instead.
	if bd.ActiveSlave == "" {
		for i := range bd.Slaves {
			bd.Slaves[i].Active = strings.EqualFold(bd.Slaves[i].MIIStatus, "up")
		}
	} else {
		for i := range bd.Slaves {
			bd.Slaves[i].Active = bd.Slaves[i].Name == bd.ActiveSlave
		}
	}

	return bd
}

func isSlaveField(key string) bool {
	switch key {
	case "MII Status", "Speed", "Duplex", "Link Failure Count", "Permanent HW addr":
		return true
	default:
		return false
	}
}

func applySlaveField(s *BondSlave, key, val string) {
	switch key {
	case "MII Status":
		s.MIIStatus = val
	case "Link Failure Count":
		if n, err := strconv.Atoi(val); err == nil {
			s.LinkFailureCount = n
		}
	case "Permanent HW addr":
		s.PermHWAddr = val
	}
}

// splitColon splits a "Key: value" line, trimming both sides. It reports
// ok=false for lines with no colon (section headers, blank lines, the
// "802.3ad info" banner, etc).
func splitColon(line string) (key, val string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	val = strings.TrimSpace(line[idx+1:])
	return key, val, true
}

func firstToken(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return s
	}
	return f[0]
}

// normalizeBondMode maps /proc/net/bonding's verbose mode description to
// the short mode name PVE's bond_mode network option, and
// docs/data-model.md's Bond.mode field, use.
func normalizeBondMode(desc string) string {
	lower := strings.ToLower(desc)
	switch {
	case strings.Contains(lower, "802.3ad"):
		return "802.3ad"
	case strings.Contains(lower, "active-backup"):
		return "active-backup"
	case strings.Contains(lower, "round-robin"):
		return "balance-rr"
	case strings.Contains(lower, "xor"):
		return "balance-xor"
	case strings.Contains(lower, "broadcast"):
		return "broadcast"
	case strings.Contains(lower, "adaptive transmit load balancing") || strings.Contains(lower, "balance-tlb"):
		return "balance-tlb"
	case strings.Contains(lower, "adaptive load balancing") || strings.Contains(lower, "balance-alb"):
		return "balance-alb"
	default:
		return desc
	}
}
