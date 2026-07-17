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
// detail (ARP monitor config, ...) this package does not need.
//
// T-804: 802.3ad bonds additionally emit, per slave, a "details actor lacp
// pdu:"/"details partner lacp pdu:" block (each followed by indented
// "system priority"/"system mac address"/"port key" (actor) or "oper key"
// (partner)/"port state" lines) — decoded into BondSlave's Actor*/Partner*
// fields below. lacpBlock tracks which of those two blocks (if either) the
// parser is currently inside, reset whenever a new "Slave Interface:" line
// starts a fresh slave.
func parseBondingProc(data []byte) *BondDetail {
	bd := &BondDetail{}
	var cur *BondSlave
	var lacpBlock string // "", "actor", or "partner"

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
			lacpBlock = ""
			continue
		}

		switch key {
		case "details actor lacp pdu":
			lacpBlock = "actor"
			continue
		case "details partner lacp pdu":
			lacpBlock = "partner"
			continue
		}

		if cur != nil && lacpBlock != "" && isLACPField(key) {
			applyLACPField(cur, lacpBlock, key, val)
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

// isLACPField reports whether key is a recognized field within a "details
// actor/partner lacp pdu:" block. "port priority"/"port number" are part of
// the real /proc format but not needed by this package (no Actor/Partner*
// field carries them) — they're deliberately left unrecognized so the
// tolerant-parser convention (unrecognized lines are ignored) applies to
// them too, rather than adding dead fields nothing reads.
func isLACPField(key string) bool {
	switch key {
	case "system priority", "system mac address", "port key", "oper key", "port state":
		return true
	default:
		return false
	}
}

// applyLACPField decodes one indented line within a "details actor/partner
// lacp pdu:" block (see parseBondingProc's doc comment) into s's Actor*/
// Partner* fields, per which block (actor or partner) the parser is
// currently in. "port key" (actor) and "oper key" (partner) are /proc's own
// asymmetric naming for the same concept — the LACP operational key — so
// both feed ActorKey/PartnerKey respectively. "port state" is only ever
// emitted in the actor block in practice; this package only needs the
// actor's negotiated synchronized/collecting/distributing bits (the signal
// that turns "bond is up" into "bond is negotiated correctly" —
// docs/features/change-management.md §5) so the partner block's own port
// state (redundant with the actor's from the local bond's point of view) is
// intentionally not decoded into a separate field.
func applyLACPField(s *BondSlave, block, key, val string) {
	switch block {
	case "actor":
		switch key {
		case "system mac address":
			s.ActorSystemID = val
		case "system priority":
			s.ActorSystemPriority = atoiOr(val, 0)
		case "port key":
			s.ActorKey = atoiOr(val, 0)
		case "port state":
			n := atoiOr(val, 0)
			s.ActorSynchronized, s.ActorCollecting, s.ActorDistributing = lacpPortStateBits(n)
		}
	case "partner":
		switch key {
		case "system mac address":
			s.PartnerSystemID = val
		case "system priority":
			s.PartnerSystemPriority = atoiOr(val, 0)
		case "oper key":
			s.PartnerKey = atoiOr(val, 0)
		}
	default:
		return
	}
	s.LACPDetailSet = true
}

// atoiOr parses s as a base-10 int, returning def on any parse error
// (malformed/unexpected value in a tolerant-parser context — never worth
// failing the whole read over).
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// lacpPortStateBits decodes an IEEE 802.1AX LACPDU actor_state/
// partner_state octet into the synchronized/collecting/distributing bits
// (bits 3/4/5, LSB-numbered from bit 0 = LACP_Activity) — the Linux bonding
// driver's own AD_STATE_SYNCHRONIZATION/AD_STATE_COLLECTING/
// AD_STATE_DISTRIBUTING bit positions (drivers/net/bonding/bond_3ad.h),
// which /proc/net/bonding's "port state" line and netlink's
// IFLA_BOND_SLAVE_AD_ACTOR_OPER_PORT_STATE attribute both report as the same
// raw integer. Shared by parseBondingProc (the /proc path) and
// netlink_linux.go's applyBondADState (the opportunistic netlink path) so
// the two sources decode identically.
func lacpPortStateBits(state int) (synchronized, collecting, distributing bool) {
	const (
		stateSynchronization = 0x08
		stateCollecting      = 0x10
		stateDistributing    = 0x20
	)
	return state&stateSynchronization != 0, state&stateCollecting != 0, state&stateDistributing != 0
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
