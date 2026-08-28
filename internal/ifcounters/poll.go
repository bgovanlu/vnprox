// SPDX-License-Identifier: Apache-2.0

package ifcounters

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/snmp"
)

// dialFunc opens an SNMP client — internal/snmp.Dial in production, a fake
// in tests (mirrors internal/mtuprobe.Config.Prober's injectable-seam
// pattern).
type dialFunc func(addr string, community []byte, timeout time.Duration) (snmpClient, error)

// snmpClient is the subset of *snmp.Client this package calls — narrowed so
// tests can substitute a fake without a real UDP socket.
type snmpClient interface {
	Get(ctx context.Context, oids []snmp.OID) ([]snmp.Varbind, error)
	GetBulk(ctx context.Context, nonRepeaters, maxRepetitions int32, oids []snmp.OID) ([]snmp.Varbind, error)
	Close() error
}

func realDial(addr string, community []byte, timeout time.Duration) (snmpClient, error) {
	return snmp.Dial(addr, community, timeout)
}

// pollChassisCounters polls one switch (target) for the counters of every
// port named by group's LLDP neighbors, returning a map keyed by the
// LLDP-advertised port id. A non-nil error means the switch could not be
// reached/queried at all (StateUnreachable for every port in group); a port
// simply absent from the returned map (nil error) means the switch answered
// but that specific port's counters could not be obtained (StateNoCounters).
func pollChassisCounters(ctx context.Context, dial dialFunc, target Target, group []*inventory.LldpNeighbor, timeout time.Duration) (map[string]Counters, error) {
	addr := target.MgmtAddr
	if addr == "" {
		addr = firstMgmtIP(group)
	}
	if addr == "" {
		return nil, fmt.Errorf("ifcounters: switch %s: no management address (neither pinned nor LLDP-advertised)", target.ChassisID)
	}
	port := target.Port
	if port <= 0 {
		port = snmp.DefaultPort
	}

	client, err := dial(net.JoinHostPort(addr, strconv.Itoa(port)), target.Community, timeout)
	if err != nil {
		return nil, fmt.Errorf("ifcounters: dialing switch %s at %s: %w", target.ChassisID, addr, err)
	}
	defer func() { _ = client.Close() }()

	wanted := map[string]string{} // portID -> portIDType
	for _, n := range group {
		if n.PortID != "" {
			wanted[n.PortID] = n.PortIDType
		}
	}
	ifIndexes, err := resolveIfIndexes(ctx, client, wanted)
	if err != nil {
		return nil, fmt.Errorf("ifcounters: switch %s: resolving port ifIndexes: %w", target.ChassisID, err)
	}
	if len(ifIndexes) == 0 {
		return map[string]Counters{}, nil
	}

	oids := make([]snmp.OID, 0, len(ifIndexes)*7)
	order := make([]string, 0, len(ifIndexes))
	for portID, idx := range ifIndexes {
		order = append(order, portID)
		oids = append(oids,
			oidIfOperStatus.Append(idx),
			oidIfInErrors.Append(idx),
			oidIfOutErrors.Append(idx),
			oidIfInDiscards.Append(idx),
			oidIfOutDiscards.Append(idx),
			oidIfHCInOctets.Append(idx),
			oidIfHCOutOctets.Append(idx),
		)
	}
	vbs, err := client.Get(ctx, oids)
	if err != nil {
		return nil, fmt.Errorf("ifcounters: switch %s: reading counters: %w", target.ChassisID, err)
	}

	out := map[string]Counters{}
	for i, portID := range order {
		base := i * 7
		if base+7 > len(vbs) {
			break // short/malformed response; whatever we already parsed stands
		}
		c, ok := parseCounterRow(vbs[base : base+7])
		if ok {
			out[portID] = c
		}
	}
	return out, nil
}

// parseCounterRow parses the 7 varbinds pollChassisCounters requested per
// port, in the fixed order it requested them. Any exception value (RFC
// 3416: the interface was removed/reindexed since this poll's ifIndex
// resolution) makes the whole row unusable — ok is false, and the caller
// treats that port as StateNoCounters rather than publishing a partial
// reading.
func parseCounterRow(vbs []snmp.Varbind) (Counters, bool) {
	var c Counters
	get := func(v snmp.Value) (uint64, bool) {
		if v.IsException() {
			return 0, false
		}
		return v.UInt, true
	}
	// ifOperStatus is IF-MIB's INTEGER {up(1), down(2), ...} enumeration,
	// decoded as snmp.KindInteger (signed, in Value.Int) — not one of the
	// Counter32/Gauge32/TimeTicks application types get() above reads from
	// Value.UInt, so it needs its own read.
	if vbs[0].Value.IsException() {
		return Counters{}, false
	}
	c.OperUp = vbs[0].Value.Int == ifOperStatusUp
	var ok bool
	if c.InErrors, ok = get(vbs[1].Value); !ok {
		return Counters{}, false
	}
	if c.OutErrors, ok = get(vbs[2].Value); !ok {
		return Counters{}, false
	}
	if c.InDiscards, ok = get(vbs[3].Value); !ok {
		return Counters{}, false
	}
	if c.OutDiscards, ok = get(vbs[4].Value); !ok {
		return Counters{}, false
	}
	if c.InOctets, ok = get(vbs[5].Value); !ok {
		return Counters{}, false
	}
	if c.OutOctets, ok = get(vbs[6].Value); !ok {
		return Counters{}, false
	}
	return c, true
}

func firstMgmtIP(group []*inventory.LldpNeighbor) string {
	for _, n := range group {
		for _, ip := range n.MgmtIPs {
			if ip != "" {
				return ip
			}
		}
	}
	return ""
}
