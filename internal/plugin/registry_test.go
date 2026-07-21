package plugin

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/switchdrv"
	"github.com/bgovanlu/vnprox/internal/switchmock"
)

// fakeRepo is an in-memory pluginRepo for registry tests.
type fakeRepo struct {
	rows map[string]store.PluginRow
}

func newFakeRepo() *fakeRepo { return &fakeRepo{rows: map[string]store.PluginRow{}} }

func (f *fakeRepo) Upsert(_ context.Context, p store.PluginRow) error {
	f.rows[p.ID] = p
	return nil
}
func (f *fakeRepo) SetEnabled(_ context.Context, id string, enabled bool) error {
	r, ok := f.rows[id]
	if !ok {
		return store.ErrNotFound
	}
	r.Enabled = enabled
	f.rows[id] = r
	return nil
}
func (f *fakeRepo) Delete(_ context.Context, id string) error {
	if _, ok := f.rows[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}
func (f *fakeRepo) Get(_ context.Context, id string) (store.PluginRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return store.PluginRow{}, store.ErrNotFound
	}
	return r, nil
}
func (f *fakeRepo) List(_ context.Context) ([]store.PluginRow, error) {
	out := make([]store.PluginRow, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

// fakeAudit records appended entries.
type fakeAudit struct {
	entries []store.AuditEntry
}

func (a *fakeAudit) Append(_ context.Context, e store.AuditEntry) (int64, error) {
	a.entries = append(a.entries, e)
	return int64(len(a.entries)), nil
}

func (a *fakeAudit) actionDetail(action string) (map[string]any, bool) {
	for _, e := range a.entries {
		if e.Action == action && e.DetailJSON.Valid {
			var m map[string]any
			if json.Unmarshal([]byte(e.DetailJSON.String), &m) == nil {
				return m, true
			}
		}
	}
	return nil, false
}

func newTestRegistry() (*Registry, *fakeRepo, *fakeAudit) {
	repo := newFakeRepo()
	audit := &fakeAudit{}
	reg := NewRegistry(Config{Repo: repo, Audit: audit})
	return reg, repo, audit
}

// switchDriverReg builds a full manifest+registration for an in-process switch
// driver plugin backed by drv.
func switchDriverReg(id string, drv SwitchDriver) Registration {
	return Registration{
		Manifest: Manifest{
			ID:              id,
			Name:            "test switch driver",
			APIVersion:      APIVersion,
			Transport:       TransportInProcess,
			ExtensionPoints: []ExtensionPoint{ExtSwitchDriver},
			Capabilities:    []string{"netRead", "netWrite"},
		},
		SwitchDriver: drv,
	}
}

// TestRegistry_LifecycleAudited is the audit half of T-1702 AC5: install/enable/
// disable/uninstall each write an audit row recording the plugin's capabilities.
func TestRegistry_LifecycleAudited(t *testing.T) {
	ctx := context.Background()
	reg, repo, audit := newTestRegistry()
	id := "com.test.sw"

	if err := reg.Install(ctx, "alice", switchDriverReg(id, switchmock.New())); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, ok := repo.rows[id]; !ok {
		t.Fatal("Install did not persist a plugin row")
	}
	detail, ok := audit.actionDetail(AuditInstall)
	if !ok {
		t.Fatal("no plugin.install audit entry")
	}
	caps, _ := detail["capabilities"].([]any)
	if len(caps) != 2 {
		t.Errorf("install audit capabilities = %v, want netRead+netWrite", detail["capabilities"])
	}

	if err := reg.Disable(ctx, "alice", id); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if repo.rows[id].Enabled {
		t.Error("Disable did not clear the persisted enabled flag")
	}
	if _, ok := reg.SwitchDriver(id); ok {
		t.Error("a disabled plugin's switch driver is still dispatched")
	}

	if err := reg.Enable(ctx, "alice", id); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if _, ok := reg.SwitchDriver(id); !ok {
		t.Error("an enabled plugin's switch driver is not dispatched")
	}

	if err := reg.Uninstall(ctx, "alice", id); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, ok := repo.rows[id]; ok {
		t.Error("Uninstall did not delete the persisted row")
	}
	for _, action := range []string{AuditInstall, AuditDisable, AuditEnable, AuditUninstall} {
		if _, ok := audit.actionDetail(action); !ok {
			t.Errorf("missing audit entry for %q", action)
		}
	}
}

// TestRegistry_InstallRejectsUnderScopedPlugin proves an install fails when the
// declared scope does not cover an attached extension point's ceiling — the
// registry refuses rather than installing a mis-scoped plugin.
func TestRegistry_InstallRejectsUnderScopedPlugin(t *testing.T) {
	ctx := context.Background()
	reg, _, _ := newTestRegistry()
	r := switchDriverReg("com.test.underscoped", switchmock.New())
	r.Manifest.Capabilities = []string{"netRead"} // switchDriver needs netWrite
	if err := reg.Install(ctx, "alice", r); err == nil {
		t.Fatal("Install accepted a switch-driver plugin scoped netRead-only; want error")
	}
}

// TestRegistry_SwitchDriverGolden is T-1702 AC4: T-1205's switch driver, obtained
// back through the plugin registry, produces output identical to a direct call.
func TestRegistry_SwitchDriverGolden(t *testing.T) {
	ctx := context.Background()
	const port = "swp1"
	seed := switchmock.New()
	want := goldenPortConfig()
	seed.SetPort(port, want)
	seed.SetNeighbor(port, goldenNeighbor())

	// Direct (pre-migration) form.
	directCfg, err := seed.PortConfig(ctx, port)
	if err != nil {
		t.Fatalf("direct PortConfig: %v", err)
	}
	directNbr, err := seed.PortNeighbor(ctx, port)
	if err != nil {
		t.Fatalf("direct PortNeighbor: %v", err)
	}

	// Re-registered through the plugin registry.
	reg, _, _ := newTestRegistry()
	if instErr := reg.Install(ctx, "alice", switchDriverReg("com.test.sw.golden", seed)); instErr != nil {
		t.Fatalf("Install: %v", instErr)
	}
	drv, ok := reg.SwitchDriver("com.test.sw.golden")
	if !ok {
		t.Fatal("switch driver not retrievable from registry")
	}
	regCfg, err := drv.PortConfig(ctx, port)
	if err != nil {
		t.Fatalf("registry PortConfig: %v", err)
	}
	regNbr, err := drv.PortNeighbor(ctx, port)
	if err != nil {
		t.Fatalf("registry PortNeighbor: %v", err)
	}

	if !reflect.DeepEqual(directCfg, regCfg) {
		t.Errorf("PortConfig diverged after re-registration: direct=%+v registry=%+v", directCfg, regCfg)
	}
	if !reflect.DeepEqual(directNbr, regNbr) {
		t.Errorf("PortNeighbor diverged after re-registration: direct=%+v registry=%+v", directNbr, regNbr)
	}
}

// TestRegistry_IngressMergeNeverShadowsBuiltin proves a plugin cannot override a
// built-in ingress Kind already present in the base registry.
func TestRegistry_IngressMergeNeverShadowsBuiltin(t *testing.T) {
	ctx := context.Background()
	reg, _, _ := newTestRegistry()

	base := ingress.Registry{ingress.KindHAProxy: stubDiscoverer{}}
	pluginDisc := stubDiscoverer{}
	r := Registration{
		Manifest: Manifest{
			ID: "com.test.ingress", Name: "ing", APIVersion: APIVersion,
			Transport:       TransportInProcess,
			ExtensionPoints: []ExtensionPoint{ExtIngressDiscoverer},
			Capabilities:    []string{"netRead"},
		},
		IngressDiscoverer: pluginDisc,
		IngressKind:       ingress.KindHAProxy, // tries to claim a built-in Kind
	}
	if err := reg.Install(ctx, "alice", r); err != nil {
		t.Fatalf("Install: %v", err)
	}
	merged := reg.IngressRegistry(base)
	if merged[ingress.KindHAProxy] != base[ingress.KindHAProxy] {
		t.Error("plugin shadowed a built-in ingress discoverer; built-ins must win")
	}
}

type stubDiscoverer struct{}

func (stubDiscoverer) Discover(_ context.Context, t ingress.Target) (ingress.ProxyState, error) {
	return ingress.ProxyState{TargetID: t.ID}, nil
}

func goldenPortConfig() switchdrv.PortConfig {
	return switchdrv.PortConfig{
		LACP:        switchdrv.LACPConfig{Mode: switchdrv.LACPActive, Rate: switchdrv.LACPRateSlow},
		Description: "uplink-to-core",
		Tagged:      []int{100, 200, 300},
		Untagged:    1,
	}
}

func goldenNeighbor() switchdrv.Neighbor {
	return switchdrv.Neighbor{ChassisID: "11:22:33:44:55:66", PortID: "swp1"}
}
