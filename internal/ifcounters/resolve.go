// SPDX-License-Identifier: Apache-2.0

package ifcounters

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/snmp"
)

// isLocalPortIDType reports whether an LLDP PortIDType names IEEE 802.1AB's
// "local" subtype (7) — the switch's own agent is advertising its ifIndex
// directly as the port id, in which case no ifDescr/ifName walk is needed at
// all: the LLDP PortID string *is* the switch's SNMP ifIndex. lldpd
// (internal/host/lldp.go's producer) reports this as the literal string
// "local" in the type field it parses off lldpctl's own JSON.
func isLocalPortIDType(portIDType string) bool {
	return strings.EqualFold(strings.TrimSpace(portIDType), "local")
}

// resolveIfIndexes correlates each wanted LLDP-advertised port id to the
// switch's SNMP ifIndex. Ports whose PortIDType is "local" resolve directly
// (the port id numeral IS the ifIndex — RFC 802.1AB's own convention);
// every other port is resolved by walking ifName then ifDescr (GETBULK,
// bounded to maxWalkRows/maxRepetitionsPerBulk) and matching exactly against
// the wanted port id strings. A port that cannot be resolved by either path
// is simply absent from the returned map — callers treat that as
// StateNoCounters for that port, not as a walk failure (the walk itself may
// have succeeded perfectly; this port's id just doesn't appear in either
// table, e.g. a stale LLDP entry for a port that was renumbered).
func resolveIfIndexes(ctx context.Context, client snmpClient, wanted map[string]string) (map[string]uint32, error) {
	out := make(map[string]uint32, len(wanted))
	remaining := make(map[string]string, len(wanted))
	for portID, portIDType := range wanted {
		if isLocalPortIDType(portIDType) {
			if idx, err := strconv.ParseUint(portID, 10, 32); err == nil {
				out[portID] = uint32(idx)
				continue
			}
			// PortIDType claimed "local" but the value isn't actually
			// numeric — fall through to the walk rather than trust the
			// (possibly buggy) device's type tag.
		}
		remaining[portID] = portIDType
	}
	if len(remaining) == 0 {
		return out, nil
	}

	byIfName, err := walkColumn(ctx, client, oidIfName)
	if err != nil {
		return nil, fmt.Errorf("walking ifName: %w", err)
	}
	for portID := range remaining {
		if idx, ok := byIfName[portID]; ok {
			out[portID] = idx
			delete(remaining, portID)
		}
	}
	if len(remaining) == 0 {
		return out, nil
	}

	byIfDescr, err := walkColumn(ctx, client, oidIfDescr)
	if err != nil {
		return nil, fmt.Errorf("walking ifDescr: %w", err)
	}
	for portID := range remaining {
		if idx, ok := byIfDescr[portID]; ok {
			out[portID] = idx
		}
	}
	return out, nil
}

// walkColumn GETBULK-walks one IF-MIB/ifXTable OCTET STRING column
// (ifDescr or ifName), returning a map from the decoded string value to its
// row's ifIndex (the column's own last OID sub-identifier — both tables are
// indexed directly by ifIndex, RFC 2863 §3.1.4/RFC 2863's ifXTable, so no
// separate ifIndex lookup is needed per row). Bounded by maxWalkRows total
// rows and stops the moment a returned OID leaves the column (a
// lexicographically smaller-or-equal OID, or one no longer sharing the
// column's prefix, or an EndOfMibView) — the standard SNMP walk-termination
// and non-increasing-OID safety checks, defensive against a misbehaving or
// hostile agent looping the walk forever.
func walkColumn(ctx context.Context, client snmpClient, column snmp.OID) (map[string]uint32, error) {
	out := map[string]uint32{}
	next := column
	rows := 0
	for rows < maxWalkRows {
		vbs, err := client.GetBulk(ctx, 0, maxRepetitionsPerBulk, []snmp.OID{next})
		if err != nil {
			return nil, err
		}
		if len(vbs) == 0 {
			break
		}
		progressed := false
		for _, vb := range vbs {
			rows++
			if rows > maxWalkRows {
				break
			}
			if !vb.Name.HasPrefix(column) || vb.Value.Kind == snmp.KindEndOfMibView {
				return out, nil // walked past the column's rows
			}
			if len(vb.Name) <= len(column) {
				continue // malformed/unexpected shape; skip defensively
			}
			ifIndex := vb.Name[len(vb.Name)-1]
			if vb.Value.Kind == snmp.KindOctetString {
				out[string(vb.Value.Str)] = ifIndex
			}
			next = vb.Name
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return out, nil
}
