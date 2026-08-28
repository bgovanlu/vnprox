// SPDX-License-Identifier: Apache-2.0

package ifcounters

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/snmp"
)

// fakeNeighbors is a NeighborLister test double.
type fakeNeighbors struct {
	neighbors []*inventory.LldpNeighbor
}

func (f fakeNeighbors) LLDPNeighbors() []*inventory.LldpNeighbor { return f.neighbors }

// fakeTargets is a TargetStore test double.
type fakeTargets struct {
	err     error
	targets []Target
}

func (f fakeTargets) ListEnabled(context.Context) ([]Target, error) { return f.targets, f.err }

// fakeSNMPClient is a snmpClient test double — no real UDP socket, table
// driven per switch by ifIndex.
type fakeSNMPClient struct {
	ifName   map[uint32]string
	ifDescr  map[uint32]string
	counters map[uint32]Counters
	getErr   error
	closed   bool
}

func (f *fakeSNMPClient) Close() error { f.closed = true; return nil }

// GetBulk simulates GETBULK semantics against ifName/ifDescr: return every
// row whose full instance OID sorts strictly after the requested starting
// OID (oids[0], which is either the bare column on the first call, or the
// previously-returned row's OID on a follow-up call — resolve.go's
// walkColumn passes exactly one of those two shapes), ascending. Ignoring
// maxRepetitions here is fine for these small test tables: returning
// "everything left" in one response and an empty response on the next call
// is a valid (if degenerate) GETBULK reply, and exercises walkColumn's own
// termination-on-empty-response path.
func (f *fakeSNMPClient) GetBulk(_ context.Context, _, _ int32, oids []snmp.OID) ([]snmp.Varbind, error) {
	if len(oids) != 1 {
		return nil, errors.New("fakeSNMPClient: GetBulk expects exactly one OID")
	}
	start := oids[0]
	var table map[uint32]string
	var col snmp.OID
	switch {
	case start.HasPrefix(oidIfName):
		table, col = f.ifName, oidIfName
	case start.HasPrefix(oidIfDescr):
		table, col = f.ifDescr, oidIfDescr
	default:
		return nil, nil
	}
	var indexes []uint32
	for idx := range table {
		indexes = append(indexes, idx)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })

	var out []snmp.Varbind
	for _, idx := range indexes {
		row := col.Append(idx)
		if compareOID(row, start) <= 0 {
			continue
		}
		out = append(out, snmp.Varbind{Name: row, Value: snmp.Value{Kind: snmp.KindOctetString, Str: []byte(table[idx])}})
	}
	return out, nil
}

func (f *fakeSNMPClient) Get(_ context.Context, oids []snmp.OID) ([]snmp.Varbind, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := make([]snmp.Varbind, len(oids))
	for i, oid := range oids {
		idx := oid[len(oid)-1]
		col := oid[:len(oid)-1]
		c, ok := f.counters[idx]
		if !ok {
			out[i] = snmp.Varbind{Name: oid, Value: snmp.Value{Kind: snmp.KindNoSuchInstance}}
			continue
		}
		switch {
		case col.Equal(oidIfOperStatus):
			status := uint64(2)
			if c.OperUp {
				status = 1
			}
			out[i] = snmp.Varbind{Name: oid, Value: snmp.Value{Kind: snmp.KindInteger, Int: int64(status)}}
		case col.Equal(oidIfInErrors):
			out[i] = counterVarbind(oid, c.InErrors)
		case col.Equal(oidIfOutErrors):
			out[i] = counterVarbind(oid, c.OutErrors)
		case col.Equal(oidIfInDiscards):
			out[i] = counterVarbind(oid, c.InDiscards)
		case col.Equal(oidIfOutDiscards):
			out[i] = counterVarbind(oid, c.OutDiscards)
		case col.Equal(oidIfHCInOctets):
			out[i] = counterVarbind(oid, c.InOctets)
		case col.Equal(oidIfHCOutOctets):
			out[i] = counterVarbind(oid, c.OutOctets)
		default:
			out[i] = snmp.Varbind{Name: oid, Value: snmp.Value{Kind: snmp.KindNoSuchObject}}
		}
	}
	return out, nil
}

func counterVarbind(oid snmp.OID, v uint64) snmp.Varbind {
	return snmp.Varbind{Name: oid, Value: snmp.Value{Kind: snmp.KindCounter32, UInt: v}}
}

func compareOID(a, b snmp.OID) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func neighbor(chassisID, node, localIface, portID, portIDType string, mgmtIP string) *inventory.LldpNeighbor {
	n := &inventory.LldpNeighbor{
		ChassisID:   chassisID,
		ChassisName: "sw-" + chassisID,
		Node:        node,
		LocalIface:  localIface,
		PortID:      portID,
		PortIDType:  portIDType,
	}
	if mgmtIP != "" {
		n.MgmtIPs = []string{mgmtIP}
	}
	return n
}

func TestService_Tick_NoNeighbors_ClearsResults(t *testing.T) {
	svc := New(Config{Neighbors: fakeNeighbors{}, Now: func() time.Time { return time.Unix(100, 0) }})
	svc.Tick(context.Background())
	if got := svc.Results(); len(got) != 0 {
		t.Fatalf("Results = %v, want empty", got)
	}
}

func TestService_Tick_NotConfigured(t *testing.T) {
	n := neighbor("aa:bb", "pve1", "eth0", "24", "local", "10.0.0.1")
	svc := New(Config{
		Neighbors: fakeNeighbors{neighbors: []*inventory.LldpNeighbor{n}},
		Targets:   fakeTargets{},
		Now:       func() time.Time { return time.Unix(100, 0) },
	})
	svc.Tick(context.Background())
	results := svc.Results()
	if len(results) != 1 {
		t.Fatalf("Results = %d, want 1", len(results))
	}
	if results[0].State != StateNotConfigured {
		t.Errorf("state = %s, want %s", results[0].State, StateNotConfigured)
	}
}

func TestService_Tick_TargetsListError_TreatsAllAsNotConfigured(t *testing.T) {
	n := neighbor("aa:bb", "pve1", "eth0", "24", "local", "10.0.0.1")
	svc := New(Config{
		Neighbors: fakeNeighbors{neighbors: []*inventory.LldpNeighbor{n}},
		Targets:   fakeTargets{err: errors.New("store unavailable")},
		Now:       func() time.Time { return time.Unix(100, 0) },
	})
	svc.Tick(context.Background())
	results := svc.Results()
	if len(results) != 1 || results[0].State != StateNotConfigured {
		t.Fatalf("Results = %+v, want one StateNotConfigured", results)
	}
}

func TestService_Tick_Unreachable(t *testing.T) {
	n := neighbor("aa:bb", "pve1", "eth0", "24", "local", "10.0.0.1")
	svc := New(Config{
		Neighbors: fakeNeighbors{neighbors: []*inventory.LldpNeighbor{n}},
		Targets:   fakeTargets{targets: []Target{{ChassisID: "aa:bb", Community: []byte("public")}}},
		Now:       func() time.Time { return time.Unix(100, 0) },
	})
	svc.cfg.dial = func(string, []byte, time.Duration) (snmpClient, error) {
		return nil, errors.New("connection refused")
	}
	svc.Tick(context.Background())
	results := svc.Results()
	if len(results) != 1 || results[0].State != StateUnreachable {
		t.Fatalf("Results = %+v, want one StateUnreachable", results)
	}
}

func TestService_Tick_OK_LocalPortIDType(t *testing.T) {
	n := neighbor("aa:bb", "pve1", "eth0", "24", "local", "10.0.0.1")
	svc := New(Config{
		Neighbors: fakeNeighbors{neighbors: []*inventory.LldpNeighbor{n}},
		Targets:   fakeTargets{targets: []Target{{ChassisID: "aa:bb", Community: []byte("public")}}},
		Now:       func() time.Time { return time.Unix(100, 0) },
	})
	fake := &fakeSNMPClient{counters: map[uint32]Counters{
		24: {InErrors: 5, OutErrors: 6, InDiscards: 7, OutDiscards: 8, InOctets: 9000, OutOctets: 9100, OperUp: true},
	}}
	svc.cfg.dial = func(string, []byte, time.Duration) (snmpClient, error) { return fake, nil }
	svc.Tick(context.Background())
	results := svc.Results()
	if len(results) != 1 {
		t.Fatalf("Results = %d, want 1", len(results))
	}
	r := results[0]
	if r.State != StateOK {
		t.Fatalf("state = %s, want %s", r.State, StateOK)
	}
	if r.InErrors != 5 || r.OutErrors != 6 || r.InDiscards != 7 || r.OutDiscards != 8 || !r.OperUp {
		t.Errorf("counters = %+v", r.Counters)
	}
	if !fake.closed {
		t.Error("snmp client was not closed after poll")
	}
}

func TestService_Tick_NoCounters_PortNotFoundOnSwitch(t *testing.T) {
	n := neighbor("aa:bb", "pve1", "eth0", "99", "local", "10.0.0.1")
	svc := New(Config{
		Neighbors: fakeNeighbors{neighbors: []*inventory.LldpNeighbor{n}},
		Targets:   fakeTargets{targets: []Target{{ChassisID: "aa:bb", Community: []byte("public")}}},
		Now:       func() time.Time { return time.Unix(100, 0) },
	})
	fake := &fakeSNMPClient{counters: map[uint32]Counters{}} // ifIndex 99 doesn't exist
	svc.cfg.dial = func(string, []byte, time.Duration) (snmpClient, error) { return fake, nil }
	svc.Tick(context.Background())
	results := svc.Results()
	// ifIndex 99 resolves directly (PortIDType "local"), but the switch's Get
	// response has no such row -> NoSuchInstance -> StateNoCounters, not StateOK.
	if len(results) != 1 || results[0].State != StateNoCounters {
		t.Fatalf("Results = %+v, want one StateNoCounters", results)
	}
}

func TestService_Tick_WalksIfNameForNonLocalPortID(t *testing.T) {
	n := neighbor("aa:bb", "pve1", "eth0", "Gi1/0/24", "interface name", "10.0.0.1")
	svc := New(Config{
		Neighbors: fakeNeighbors{neighbors: []*inventory.LldpNeighbor{n}},
		Targets:   fakeTargets{targets: []Target{{ChassisID: "aa:bb", Community: []byte("public")}}},
		Now:       func() time.Time { return time.Unix(100, 0) },
	})
	fake := &fakeSNMPClient{
		ifName:   map[uint32]string{24: "Gi1/0/24", 25: "Gi1/0/25"},
		counters: map[uint32]Counters{24: {InErrors: 1, OperUp: true}},
	}
	svc.cfg.dial = func(string, []byte, time.Duration) (snmpClient, error) { return fake, nil }
	svc.Tick(context.Background())
	results := svc.Results()
	if len(results) != 1 || results[0].State != StateOK {
		t.Fatalf("Results = %+v, want one StateOK (resolved via ifName walk)", results)
	}
	if results[0].InErrors != 1 {
		t.Errorf("InErrors = %d, want 1", results[0].InErrors)
	}
}

func TestService_Tick_MultiplePortsSameChassis_OnePollEach(t *testing.T) {
	n1 := neighbor("aa:bb", "pve1", "eth0", "24", "local", "10.0.0.1")
	n2 := neighbor("aa:bb", "pve1", "eth1", "25", "local", "10.0.0.1")
	svc := New(Config{
		Neighbors: fakeNeighbors{neighbors: []*inventory.LldpNeighbor{n1, n2}},
		Targets:   fakeTargets{targets: []Target{{ChassisID: "aa:bb", Community: []byte("public")}}},
		Now:       func() time.Time { return time.Unix(100, 0) },
	})
	dialCount := 0
	fake := &fakeSNMPClient{counters: map[uint32]Counters{
		24: {InErrors: 1, OperUp: true},
		25: {InErrors: 2, OperUp: true},
	}}
	svc.cfg.dial = func(string, []byte, time.Duration) (snmpClient, error) {
		dialCount++
		return fake, nil
	}
	svc.Tick(context.Background())
	if dialCount != 1 {
		t.Errorf("dial count = %d, want 1 (both ports on the same chassis, one poll)", dialCount)
	}
	results := svc.Results()
	if len(results) != 2 {
		t.Fatalf("Results = %d, want 2", len(results))
	}
	byIface := map[string]Result{}
	for _, r := range results {
		byIface[r.LocalIface] = r
	}
	if byIface["eth0"].InErrors != 1 || byIface["eth1"].InErrors != 2 {
		t.Errorf("results = %+v", byIface)
	}
}

func TestService_Tick_SkipsNeighborWithNoChassisID(t *testing.T) {
	n := neighbor("", "pve1", "eth0", "24", "local", "10.0.0.1")
	svc := New(Config{
		Neighbors: fakeNeighbors{neighbors: []*inventory.LldpNeighbor{n}},
		Now:       func() time.Time { return time.Unix(100, 0) },
	})
	svc.Tick(context.Background())
	if got := svc.Results(); len(got) != 0 {
		t.Fatalf("Results = %v, want empty (no ChassisID to key on)", got)
	}
}
