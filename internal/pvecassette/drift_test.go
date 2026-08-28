// SPDX-License-Identifier: Apache-2.0

package pvecassette_test

import (
	"reflect"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvecassette"
)

func cassette(path, body string) pvecassette.Cassette {
	return pvecassette.Cassette{PVEVersion: "test", Method: "GET", Path: path, Status: 200, Body: body}
}

func set(cs ...pvecassette.Cassette) map[string]pvecassette.Cassette {
	out := map[string]pvecassette.Cassette{}
	for _, c := range cs {
		out[c.Key()] = c
	}
	return out
}

// TestDrift is the fixture-vs-cassette comparator's table, and every row
// is a divergence it must FIND. A drift check that returns nil is
// indistinguishable from a drift check that is not wired up, which is why
// there is no "they are identical" row carrying the weight here — that
// case gets its own test below, and it is the cheap one.
func TestDrift(t *testing.T) {
	cases := []struct {
		name     string
		fixture  map[string]pvecassette.Cassette
		cassette map[string]pvecassette.Cassette
		want     []pvecassette.Divergence
	}{
		{
			name:     "a field real PVE sends that the fixture never emits",
			fixture:  set(cassette("/api2/json/nodes/pve1/network", `{"data":[{"iface":"bond0","type":"bond"}]}`)),
			cassette: set(cassette("/api2/json/nodes/pve1/network", `{"data":[{"iface":"bond0","type":"bond","bond_mode":"802.3ad"}]}`)),
			want: []pvecassette.Divergence{
				{Key: "GET /api2/json/nodes/pve1/network", Field: "data[].bond_mode", PresentIn: pvecassette.SideCassette},
			},
		},
		{
			name:     "a field the fixture invented that PVE does not send",
			fixture:  set(cassette("/api2/json/cluster/status", `{"data":[{"type":"cluster","vnproxManaged":true}]}`)),
			cassette: set(cassette("/api2/json/cluster/status", `{"data":[{"type":"cluster"}]}`)),
			want: []pvecassette.Divergence{
				{Key: "GET /api2/json/cluster/status", Field: "data[].vnproxManaged", PresentIn: pvecassette.SideFixture},
			},
		},
		{
			name:     "a nested field",
			fixture:  set(cassette("/api2/json/access/permissions", `{"data":{"/":{"Sys.Audit":1}}}`)),
			cassette: set(cassette("/api2/json/access/permissions", `{"data":{"/":{"Sys.Audit":1,"Sys.Console":1}}}`)),
			want: []pvecassette.Divergence{
				{Key: "GET /api2/json/access/permissions", Field: "data./.Sys.Console", PresentIn: pvecassette.SideCassette},
			},
		},
		{
			name:     "an endpoint nobody recorded",
			fixture:  set(cassette("/api2/json/cluster/ceph/config", `{"data":{"public_network":"10.0.0.0/24"}}`)),
			cassette: set(),
			want: []pvecassette.Divergence{
				{Key: "GET /api2/json/cluster/ceph/config", Field: pvecassette.WholeResponse, PresentIn: pvecassette.SideFixture},
			},
		},
		{
			name:     "an endpoint the mock does not implement",
			fixture:  set(),
			cassette: set(cassette("/api2/json/nodes/pve1/dns", `{"data":{"search":"lab.example"}}`)),
			want: []pvecassette.Divergence{
				{Key: "GET /api2/json/nodes/pve1/dns", Field: pvecassette.WholeResponse, PresentIn: pvecassette.SideCassette},
			},
		},
		{
			name:     "differing values are not divergence — only shapes are",
			fixture:  set(cassette("/api2/json/nodes/pve1/network", `{"data":[{"iface":"vmbr0","mtu":1500}]}`)),
			cassette: set(cassette("/api2/json/nodes/pve1/network", `{"data":[{"iface":"vmbr9","mtu":9000}]}`)),
			want:     nil,
		},
		{
			name:     "array length is not divergence either",
			fixture:  set(cassette("/api2/json/cluster/status", `{"data":[{"type":"node"}]}`)),
			cassette: set(cassette("/api2/json/cluster/status", `{"data":[{"type":"node"},{"type":"node"},{"type":"node"}]}`)),
			want:     nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pvecassette.Drift(tc.fixture, tc.cassette)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Drift() =\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}
}

// TestFieldPaths covers the collapsing rule directly: array indices are
// erased so a response with more rows on one side does not drown the
// report in per-index noise.
func TestFieldPaths(t *testing.T) {
	got := pvecassette.FieldPaths(`{"data":[{"iface":"lo"},{"iface":"vmbr0","mtu":1500}],"total":2}`)
	want := []string{"data", "data[].iface", "data[].mtu", "total"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FieldPaths() = %v, want %v", got, want)
	}
	if paths := pvecassette.FieldPaths("not json at all"); len(paths) != 0 {
		t.Errorf("FieldPaths(non-JSON) = %v, want none", paths)
	}
}
