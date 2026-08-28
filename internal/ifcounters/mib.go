// SPDX-License-Identifier: Apache-2.0

package ifcounters

import "github.com/bgovanlu/vnprox/internal/snmp"

// IF-MIB (RFC 2863) and its 64-bit-counter extension table (ifXTable, same
// RFC) OIDs this package reads. These are public IETF standard OIDs, not
// vendor-specific or undocumented behavior — unlike a Proxmox API shape,
// there is no live device whose behavior needs observing to pin these down;
// the RFC is the specification. T-4013's card names exactly six counters
// (ifInErrors/ifOutErrors/ifInDiscards/ifOutDiscards/ifHCInOctets/
// ifHCOutOctets); ifOperStatus is added because "reachable but the port is
// administratively/operationally down" is one of this card's required
// honest states, and ifDescr/ifName are added purely as correlation keys
// (see resolve.go) — this package polls no other column of either table,
// and "Explicitly not in this phase" in planning/tasks/phase-40.md is
// explicit that no SNMP use beyond these counters is in scope.
var (
	oidIfDescr       = snmp.MustParseOID("1.3.6.1.2.1.2.2.1.2")  // ifTable, correlation only
	oidIfOperStatus  = snmp.MustParseOID("1.3.6.1.2.1.2.2.1.8")  // ifTable
	oidIfInDiscards  = snmp.MustParseOID("1.3.6.1.2.1.2.2.1.13") // ifTable
	oidIfInErrors    = snmp.MustParseOID("1.3.6.1.2.1.2.2.1.14") // ifTable
	oidIfOutDiscards = snmp.MustParseOID("1.3.6.1.2.1.2.2.1.19") // ifTable
	oidIfOutErrors   = snmp.MustParseOID("1.3.6.1.2.1.2.2.1.20") // ifTable

	oidIfName        = snmp.MustParseOID("1.3.6.1.2.1.31.1.1.1.1")  // ifXTable, correlation only
	oidIfHCInOctets  = snmp.MustParseOID("1.3.6.1.2.1.31.1.1.1.6")  // ifXTable
	oidIfHCOutOctets = snmp.MustParseOID("1.3.6.1.2.1.31.1.1.1.10") // ifXTable
)

// ifOperStatusUp is IF-MIB's ifOperStatus(1) enumeration value "up" (RFC
// 2863 §3: 1=up, 2=down, 3=testing, 4=unknown, 5=dormant, 6=notPresent,
// 7=lowerLayerDown) — the only value this package treats as "the link is
// up"; everything else (down included) is reported as not up.
const ifOperStatusUp = 1

// maxWalkRows bounds the ifDescr/ifName GETBULK correlation walk (resolve.go)
// — generous enough for any real chassis switch's port count with headroom,
// small enough to bound one poll cycle's cost against a device that returns
// far more rows than expected (defensive: this package never trusts a
// device's table size).
const maxWalkRows = 4096

// maxRepetitionsPerBulk bounds how many rows a single GetBulkRequest asks
// for — kept well under maxWalkRows so a walk needs a bounded number of
// round trips rather than one request claiming an enormous repetition
// count.
const maxRepetitionsPerBulk = 64
