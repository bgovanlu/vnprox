// SPDX-License-Identifier: Apache-2.0

package blueprint_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	return db
}

type fakeInventorySource struct{ g *inventory.Graph }

func (f fakeInventorySource) Snapshot() inventory.Snapshot { return f.g.Snapshot() }

func newTestService(t *testing.T, g *inventory.Graph) *blueprint.Service {
	t.Helper()
	db := openTestDB(t)
	return blueprint.New(blueprint.Config{
		Repo:      store.NewBlueprintRepo(db),
		Inventory: fakeInventorySource{g: g},
	})
}

func TestService_List_IncludesStartersAndSaved(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, newGraphWithNodes("pve1"))

	saved, err := svc.Save(ctx, "alice", &blueprint.Blueprint{
		Name: "My custom bp",
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindBridge, IDTemplate: "vmbr5", Fields: map[string]any{}},
		},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("Save did not assign an id")
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var foundStarter, foundSaved bool
	for _, bp := range list {
		if bp.ID == blueprint.StarterSingleNICHomelab {
			foundStarter = true
		}
		if bp.ID == saved.ID {
			foundSaved = true
		}
	}
	if !foundStarter {
		t.Error("List did not include a bundled starter")
	}
	if !foundSaved {
		t.Error("List did not include the saved blueprint")
	}
}

func TestService_Save_RejectsStarterID(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, newGraphWithNodes("pve1"))
	_, err := svc.Save(ctx, "alice", &blueprint.Blueprint{
		ID: blueprint.StarterSingleNICHomelab, Name: "hijack",
		Entities: []blueprint.EntityTemplate{{Kind: blueprint.KindBridge, IDTemplate: "vmbr0"}},
	})
	if !errors.Is(err, blueprint.ErrReadOnly) {
		t.Fatalf("got err = %v, want ErrReadOnly", err)
	}
}

func TestService_Delete_RejectsStarterID(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, newGraphWithNodes("pve1"))
	if err := svc.Delete(ctx, blueprint.StarterSingleNICHomelab); !errors.Is(err, blueprint.ErrReadOnly) {
		t.Fatalf("got err = %v, want ErrReadOnly", err)
	}
}

func TestService_Get_UnknownID(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, newGraphWithNodes("pve1"))
	if _, err := svc.Get(ctx, "no-such-id"); !errors.Is(err, blueprint.ErrNotFound) {
		t.Fatalf("got err = %v, want ErrNotFound", err)
	}
}

func TestService_Instantiate_Starter(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, newGraphWithNodes("pve1"))

	ops, title, err := svc.Instantiate(ctx, blueprint.StarterSingleNICHomelab, blueprint.InstantiateRequest{
		Nodes: []string{"pve1"},
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if len(ops) != 1 || ops[0].Type != change.OpBridgeCreate {
		t.Fatalf("got %v, want a single bridge.create", opTypes(ops))
	}
	if title == "" {
		t.Error("expected a non-empty default title")
	}
}

func TestService_CaptureThenSave(t *testing.T) {
	ctx := context.Background()
	g := newGraphWithNodes("pve1")
	applyBridge(g, "pve1", "vmbr0", bridgeOpts{ports: []string{"eno1"}, addresses: []string{"192.168.1.10/24"}})
	svc := newTestService(t, g)

	captured, err := svc.Capture("pve1")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	saved, err := svc.Save(ctx, "alice", captured)
	if err != nil {
		t.Fatalf("Save(captured): %v", err)
	}
	if saved.ID == "" {
		t.Fatal("captured-then-saved blueprint has no id")
	}

	got, err := svc.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Entities) != 1 || got.Entities[0].Kind != blueprint.KindBridge {
		t.Fatalf("unexpected roundtripped entities: %+v", got.Entities)
	}
}

// T-603 AC5: export a blueprint (its saved JSON form) from one daemon's
// store, import it into a *second*, independent in-memory/tempdir daemon
// instance (its own Service, its own store.BlueprintRepo, its own
// inventory snapshot), and instantiate it there — proving the format is
// portable across daemons, not just round-trippable through one store.
func TestService_ExportImportInstantiate_SecondDaemon(t *testing.T) {
	ctx := context.Background()

	// Daemon 1: author and "export" (marshal to the same JSON bytes a
	// GET /blueprints/{id} response, or a file download, would carry).
	daemon1 := newTestService(t, newGraphWithNodes("pve1"))
	original := &blueprint.Blueprint{
		Name: "Portable bp",
		Params: []blueprint.ParamDef{
			{Name: "bridgeName", Type: blueprint.ParamString, Default: "vmbr7", Required: true},
		},
		NodeSelector: blueprint.NodeSelector{Mode: blueprint.SelectAll},
		Entities: []blueprint.EntityTemplate{
			{Kind: blueprint.KindBridge, IDTemplate: "{{bridgeName}}", Fields: map[string]any{
				"vlanAware": true,
			}},
		},
	}
	saved, err := daemon1.Save(ctx, "alice", original)
	if err != nil {
		t.Fatalf("daemon1 Save: %v", err)
	}
	exported, err := json.Marshal(saved)
	if err != nil {
		t.Fatalf("marshaling exported blueprint: %v", err)
	}

	// Daemon 2: an entirely separate Service/store/inventory (a different
	// tempdir SQLite file, a different *inventory.Graph) — "a second test
	// daemon" per the acceptance criterion.
	daemon2 := newTestService(t, newGraphWithNodes("pve9"))

	var imported blueprint.Blueprint
	if unmarshalErr := json.Unmarshal(exported, &imported); unmarshalErr != nil {
		t.Fatalf("unmarshaling imported blueprint: %v", unmarshalErr)
	}
	// Importing as a *new* blueprint on daemon2 (the documented "import"
	// action creates a fresh saved blueprint from the file, it does not
	// assume the id already exists there) — clear the id so Save mints a
	// new one, exactly like a from-scratch author/save would.
	imported.ID = ""
	imported2, err := daemon2.Save(ctx, "bob", &imported)
	if err != nil {
		t.Fatalf("daemon2 Save(imported): %v", err)
	}

	ops, _, err := daemon2.Instantiate(ctx, imported2.ID, blueprint.InstantiateRequest{Nodes: []string{"pve9"}})
	if err != nil {
		t.Fatalf("daemon2 Instantiate: %v", err)
	}
	if len(ops) != 1 || ops[0].Type != change.OpBridgeCreate {
		t.Fatalf("got %v, want a single bridge.create", opTypes(ops))
	}
	if ops[0].Target.Node != "pve9" || ops[0].Target.ID != "vmbr7" {
		t.Fatalf("target = %s, want bridge:pve9:vmbr7", ops[0].Target)
	}
	create := ops[0].Params.(*change.BridgeCreateParams)
	if !create.VlanAware {
		t.Fatal("expected VlanAware carried through export/import")
	}
}
