// SPDX-License-Identifier: Apache-2.0

package migration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/migration"
	"github.com/bgovanlu/vnprox/internal/pve"
)

func ref(kind inventory.Kind, node, id string) inventory.Ref {
	return inventory.Ref{Kind: kind, Node: node, ID: id}
}

// buildTwoNodeGraph builds a minimal fixture: pve1/pve2 each carry a
// vmbr0 bridge riding a single eno1 PhysNic at speedMbps, and pve1 hosts
// one qemu guest (vmid 100, ref vm100).
func buildTwoNodeGraph(t *testing.T, speedMbps int) *inventory.Graph {
	t.Helper()
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: ref(inventory.KindNode, "pve1", "pve1"), Name: "pve1", Status: "online"},
		&inventory.Node{Ref: ref(inventory.KindNode, "pve2", "pve2"), Name: "pve2", Status: "online"},
		&inventory.Guest{Ref: ref(inventory.KindGuest, "pve1", "100"), VMID: 100, Node: "pve1", Name: "vm100", Type: "qemu", Status: "running"},
	})
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.PhysNic{Ref: ref(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1", SpeedMbps: speedMbps, LinkUp: true},
		&inventory.PhysNic{Ref: ref(inventory.KindPhysNic, "pve2", "eno1"), Name: "eno1", SpeedMbps: speedMbps, LinkUp: true},
	})
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{}, []inventory.Entity{
		&inventory.Bridge{
			Ref: ref(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0", Virt: inventory.BridgeLinux,
			DeclaredPortNames: []string{"eno1"},
		},
		&inventory.Bridge{
			Ref: ref(inventory.KindBridge, "pve2", "vmbr0"), Name: "vmbr0", Virt: inventory.BridgeLinux,
			DeclaredPortNames: []string{"eno1"},
		},
	})
	return g
}

var vm100 = ref(inventory.KindGuest, "pve1", "100")

// fakeGuestConfig is a GuestConfigReader test double returning a fixed
// "memory" (MiB) value for every guest, mirroring real PVE's stringified
// config shape (internal/pve.stringifyConfigValue).
type fakeGuestConfig struct {
	err      error
	memoryMB string
}

func (f *fakeGuestConfig) GetGuestConfig(_ context.Context, _ string, _ pve.GuestKind, _ int) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return map[string]string{"memory": f.memoryMB}, nil
}

// fakeMesh is a MeshProvider test double returning a fixed link list.
type fakeMesh struct {
	err   error
	links []latmesh.LinkHeat
}

func (f *fakeMesh) Heatmap(_ context.Context) ([]latmesh.LinkHeat, error) {
	return f.links, f.err
}

// fakeTraffic is a MigrationTrafficProvider test double.
type fakeTraffic struct {
	mbps float64
	ok   bool
}

func (f *fakeTraffic) MigrationTrafficMbps(_ context.Context, _ string) (float64, bool) {
	return f.mbps, f.ok
}

// TestPlan_AmpleHeadroom_OK — AC1: a fixture with ample migration-network
// headroom returns verdict "ok" with correct headroomMbps/
// estimatedTransferSec arithmetic. capacity=1000Mbps (single eno1 @
// 1000Mbps), 0 current utilization, 0 mesh loss/rtt, guest RAM 2048 MiB:
// headroom = 1000; dirtyRate = 2048*8*0.01 = 163.84 (< 2x under headroom);
// estimatedTransferSec = 2048*8/1000 = 16.384 -> rounds to 16.38.
func TestPlan_AmpleHeadroom_OK(t *testing.T) {
	g := buildTwoNodeGraph(t, 1000)
	p := migration.New(migration.Config{
		Graph:       g,
		GuestConfig: &fakeGuestConfig{memoryMB: "2048"},
		Mesh: &fakeMesh{links: []latmesh.LinkHeat{
			{LinkID: "corosync|pve1->pve2", Fabric: latmesh.FabricCorosync, FromNode: "pve1", ToNode: "pve2", RollingLossPct: 0, RollingRttMs: 5},
		}},
		Traffic: &fakeTraffic{mbps: 0, ok: true},
	})

	got := p.Plan(context.Background(), vm100, "pve2")

	if got.Verdict != migration.VerdictOK {
		t.Errorf("Verdict = %q, want %q (caveats: %v)", got.Verdict, migration.VerdictOK, got.Caveats)
	}
	if got.HeadroomMbps != 1000 {
		t.Errorf("HeadroomMbps = %v, want 1000", got.HeadroomMbps)
	}
	if got.EstimatedTransferSec != 16.38 {
		t.Errorf("EstimatedTransferSec = %v, want 16.38", got.EstimatedTransferSec)
	}
	if !got.BestEffort {
		t.Error("BestEffort must always be true")
	}
}

// When the guest's RAM can't be read, the transfer/dirty-rate estimate is
// meaningless — so an otherwise-healthy link must NOT report a clean "ok"
// verdict (downstream T-1604/T-1103 branch on it). It degrades to "tight"
// with an explanatory caveat (review-T-1507).
func TestPlan_UnreadableRAM_DegradesToTight(t *testing.T) {
	g := buildTwoNodeGraph(t, 1000)
	p := migration.New(migration.Config{
		Graph:       g,
		GuestConfig: &fakeGuestConfig{memoryMB: ""}, // no "memory" value -> unreadable
		Mesh: &fakeMesh{links: []latmesh.LinkHeat{
			{LinkID: "corosync|pve1->pve2", Fabric: latmesh.FabricCorosync, FromNode: "pve1", ToNode: "pve2", RollingLossPct: 0, RollingRttMs: 5},
		}},
		Traffic: &fakeTraffic{mbps: 0, ok: true},
	})

	got := p.Plan(context.Background(), vm100, "pve2")

	if got.Verdict != migration.VerdictTight {
		t.Errorf("Verdict = %q, want %q (unreadable RAM must not read as ok; caveats: %v)", got.Verdict, migration.VerdictTight, got.Caveats)
	}
	if !got.BestEffort {
		t.Error("BestEffort must always be true")
	}
}

// latmeshFixtureRow mirrors testdata/latmesh/*.json's per-tick shape
// (internal/latmesh/pairs_test.go, internal/findings/health_latmesh_test.go).
type latmeshFixtureRow struct {
	At      int64   `json:"at"`
	RttMs   float64 `json:"rttMs"`
	LossPct float64 `json:"lossPct"`
}

func loadLatmeshFixture(t *testing.T, name string) []latmeshFixtureRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "latmesh", name))
	if err != nil {
		t.Fatalf("reading testdata/latmesh/%s: %v", name, err)
	}
	var out []latmeshFixtureRow
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parsing testdata/latmesh/%s: %v", name, err)
	}
	return out
}

// TestPlan_SaturatedMeshAndLargeGuest_Insufficient — AC2: the same route
// against a fixture with saturated mesh link data — reusing T-1303's own
// synthetic lossy.json fixture (5% rolling loss, its documented breach
// value, testdata/latmesh/lossy.json) — and a large guest RAM size returns
// verdict "insufficient" with an explanatory caveat.
func TestPlan_SaturatedMeshAndLargeGuest_Insufficient(t *testing.T) {
	rows := loadLatmeshFixture(t, "lossy.json")
	breach := rows[3] // the fixture's own breach row: lossPct=5
	if breach.LossPct != 5 {
		t.Fatalf("fixture row 3 lossPct = %v, want 5 (fixture shape changed?)", breach.LossPct)
	}

	g := buildTwoNodeGraph(t, 1000)
	p := migration.New(migration.Config{
		Graph:       g,
		GuestConfig: &fakeGuestConfig{memoryMB: "524288"}, // 512 GiB
		Mesh: &fakeMesh{links: []latmesh.LinkHeat{
			{
				LinkID: "corosync|pve1->pve2", Fabric: latmesh.FabricCorosync, FromNode: "pve1", ToNode: "pve2",
				RollingLossPct: breach.LossPct, RollingRttMs: breach.RttMs,
			},
		}},
		Traffic: &fakeTraffic{mbps: 0, ok: true},
	})

	got := p.Plan(context.Background(), vm100, "pve2")

	if got.Verdict != migration.VerdictInsufficient {
		t.Fatalf("Verdict = %q, want %q (headroom=%v, transferSec=%v, caveats=%v)",
			got.Verdict, migration.VerdictInsufficient, got.HeadroomMbps, got.EstimatedTransferSec, got.Caveats)
	}
	if len(got.Caveats) == 0 {
		t.Error("expected at least one explanatory caveat for an insufficient verdict")
	}
	foundExplain := false
	for _, c := range got.Caveats {
		if strings.Contains(c, "dirty-page rate would exceed") {
			foundExplain = true
		}
	}
	if !foundExplain {
		t.Errorf("expected a caveat explaining the dirty-rate-exceeds-headroom reason, got: %v", got.Caveats)
	}
}

// TestPlan_BestEffort_AlwaysTrue — AC3: BestEffort is set unconditionally
// across every input combination this arc's data sources can produce
// (guest config present/absent, mesh present/absent, traffic
// present/absent) — never anything but true, since this arc never has live
// guest instrumentation.
func TestPlan_BestEffort_AlwaysTrue(t *testing.T) {
	g := buildTwoNodeGraph(t, 1000)
	cases := []struct {
		name string
		cfg  migration.Config
	}{
		{"full data", migration.Config{Graph: g, GuestConfig: &fakeGuestConfig{memoryMB: "4096"}, Mesh: &fakeMesh{}, Traffic: &fakeTraffic{ok: true}}},
		{"no guest config", migration.Config{Graph: g, Mesh: &fakeMesh{}, Traffic: &fakeTraffic{ok: true}}},
		{"no mesh", migration.Config{Graph: g, GuestConfig: &fakeGuestConfig{memoryMB: "4096"}, Traffic: &fakeTraffic{ok: true}}},
		{"no traffic", migration.Config{Graph: g, GuestConfig: &fakeGuestConfig{memoryMB: "4096"}, Mesh: &fakeMesh{}}},
		{"nothing wired", migration.Config{Graph: g}},
		{"guest config errors", migration.Config{Graph: g, GuestConfig: &fakeGuestConfig{err: context.DeadlineExceeded}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := migration.New(tc.cfg)
			got := p.Plan(context.Background(), vm100, "pve2")
			if !got.BestEffort {
				t.Errorf("BestEffort = false, want true (this arc never has live guest instrumentation)")
			}
		})
	}

	// And the unresolvable-guest / no-graph degenerate paths too.
	p := migration.New(migration.Config{Graph: g})
	got := p.Plan(context.Background(), ref(inventory.KindGuest, "pve1", "does-not-exist"), "pve2")
	if !got.BestEffort {
		t.Error("BestEffort = false for an unresolvable guest, want true")
	}
}

// TestAssessment_JSONSchema_Golden — AC4: Assessment's JSON encoding
// matches docs/api.md's Migration planner section's pinned response shape
// exactly (field set, names, order) — the stability contract T-1604/T-1103
// depend on.
func TestAssessment_JSONSchema_Golden(t *testing.T) {
	a := migration.Assessment{
		HeadroomMbps:         123.45,
		EstimatedTransferSec: 67.89,
		Verdict:              migration.VerdictTight,
		BestEffort:           true,
		Caveats:              []string{"example caveat"},
	}
	got, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"headroomMbps":123.45,"estimatedTransferSec":67.89,"verdict":"tight","bestEffort":true,"caveats":["example caveat"]}`
	if string(got) != want {
		t.Errorf("Assessment JSON =\n%s\nwant\n%s", got, want)
	}

	// Caveats must never be null (nil) in the wire shape — an empty slice
	// serializes to `[]`, not omitted, not `null`.
	empty := migration.Assessment{Caveats: []string{}}
	gotEmpty, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if !strings.Contains(string(gotEmpty), `"caveats":[]`) {
		t.Errorf("empty Caveats must serialize as [], got: %s", gotEmpty)
	}
}

// TestGuestConfigReader_MethodSet_ReadOnly — AC5 (mechanical half): the
// only PVE-facing interface this package defines has exactly one method,
// GetGuestConfig — a read. There is no way to express a call to any
// migration-start/evacuate endpoint through this package's own type
// surface, mirroring T-1501's "every method on internal/k8s.Client is
// GET-only" reflection regression.
func TestGuestConfigReader_MethodSet_ReadOnly(t *testing.T) {
	typ := reflect.TypeOf((*migration.GuestConfigReader)(nil)).Elem()
	if typ.NumMethod() != 1 {
		t.Fatalf("GuestConfigReader has %d methods, want exactly 1 (GetGuestConfig)", typ.NumMethod())
	}
	if typ.Method(0).Name != "GetGuestConfig" {
		t.Fatalf("GuestConfigReader's only method is %q, want GetGuestConfig", typ.Method(0).Name)
	}
}

// TestPackage_NoMigrationTriggerCalls — AC5 (textual half): no non-test
// source file in this package references a PVE migration-start/evacuate
// call by name (a defense-in-depth grep alongside the interface reflection
// test above — internal/pve has no such method today, but this guards
// against one being added and wired in here without a deliberate, reviewed
// decision).
func TestPackage_NoMigrationTriggerCalls(t *testing.T) {
	forbidden := []string{"Migrate(", "MigrateGuest", "Evacuate(", "StartMigration", "/migrate\""}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/migration: %v", err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		checked++
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		text := string(src)
		for _, f := range forbidden {
			if strings.Contains(text, f) {
				t.Errorf("%s: found forbidden migration-trigger reference %q", e.Name(), f)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test .go files found to check — test setup is broken")
	}
}
