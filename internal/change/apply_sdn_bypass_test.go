package change

// T-701 acceptance criterion 4's "validator-bypassing changeset" test: now
// that this task's own validators (validate_schema.go's
// codeGatewayNotInSubnet, validate_sdn.go's codeSNATRequiresGateway) block
// the snat-without-gateway shape before Apply ever runs — Apply always
// revalidates immediately before transitioning to "applying" (apply.go's
// beginApply) and refuses with *ErrValidationBlocked on any error finding,
// with no override flag that skips this for the sdn class (unlike
// AllowDangerousOps, which only downgrades the *safety* class) — there is
// no supported way to reach StepSDNStage with this exact shape through the
// public Service.Apply API anymore. That is the correct, intended outcome
// of this task, not a gap: adding a bypass hook to production code just to
// make this reachable through the public API would be a genuine safety
// regression.
//
// This file instead drives the executor directly (white-box, package
// change) — skipping only beginApply's validation/locking/status-transition
// prologue, not Apply's actual step-execution/rollback machinery — to prove
// what T-701's card asks for at the layer that still matters: if this shape
// ever *did* reach the executor (a defect in a future validator change, a
// changeset applied by an older vnprox build before this task, etc.),
// pvemock's own new rejection (internal/pvemock/sdn.go's
// subnetGatewayError) still stops it at the sdn_stage step, and T-402's
// existing rollback machinery still restores the changeset's zone/vnet
// creations that already landed. This is deliberately narrow — it does not
// re-implement or bypass PVEGateway's firewall/ipam methods, since the
// pure-SDN changeset below's plan never reaches them.

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// bypassPVEGateway is a minimal PVEGateway wired to a real pvemock server —
// only SDNStageOp/SDNConfig/ApplySDN are implemented for real (the only
// three this test's pure-SDN plan can reach); every other PVEGateway method
// panics if called, so an unexpected call fails loudly instead of silently
// no-opping.
type bypassPVEGateway struct {
	client   *pve.Client
	pollNode string
}

func (g *bypassPVEGateway) SDNStageOp(ctx context.Context, op Op, subnetVnet string) error {
	switch p := op.Params.(type) {
	case *SdnZoneCreateParams:
		return g.client.CreateSDNZone(ctx, pve.SDNZone{
			ID: op.Target.ID, Type: p.Type, Bridge: p.Bridge, Nodes: p.Nodes,
		})
	case *SdnVnetCreateParams:
		return g.client.CreateSDNVnet(ctx, pve.SDNVnet{ID: pve.SDNVnetID(op.Target.ID), Zone: p.Zone, Alias: p.Alias, Tag: p.Tag})
	case *SdnSubnetCreateParams:
		return g.client.CreateSDNSubnet(ctx, pve.SDNVnetID(p.Vnet), pve.SDNSubnet{
			ID: pve.SDNSubnetID(op.Target.ID), Vnet: pve.SDNVnetID(p.Vnet), CIDR: p.CIDR, Gateway: p.Gateway, SNAT: p.SNAT,
		})
	case *SdnZoneDeleteParams:
		return g.client.DeleteSDNZone(ctx, op.Target.ID)
	case *SdnVnetDeleteParams:
		return g.client.DeleteSDNVnet(ctx, pve.SDNVnetID(op.Target.ID))
	case *SdnSubnetDeleteParams:
		return g.client.DeleteSDNSubnet(ctx, pve.SDNVnetID(subnetVnet), pve.SDNSubnetID(op.Target.ID))
	default:
		return fmt.Errorf("bypassPVEGateway: unsupported sdn stage op %q in this narrow test double", op.Type)
	}
}

// ApplySDN is real (not a panic stub, unlike the firewall/ipam methods
// below): rollbackAfterFailure's restoreSDN (apply_sdn.go) calls it after
// staging the inverse ops, to actually apply the reverted config back —
// this test's rollback assertions depend on that real round trip.
func (g *bypassPVEGateway) ApplySDN(ctx context.Context, affectedZones []string) (SDNApplyResult, error) {
	upid, err := g.client.ApplySDN(ctx)
	if err != nil {
		return SDNApplyResult{}, err
	}
	result := SDNApplyResult{UPID: upid, Node: g.pollNode}
	if _, err := g.client.WaitTask(ctx, g.pollNode, upid, pve.WaitOptions{Interval: 5 * time.Millisecond, Timeout: 5 * time.Second}); err != nil {
		return result, err
	}
	for _, zoneID := range affectedZones {
		statuses, err := g.client.GetSDNZoneStatus(ctx, zoneID)
		if err != nil {
			return result, err
		}
		zh := SDNZoneHealth{Zone: zoneID}
		for _, st := range statuses {
			zh.Nodes = append(zh.Nodes, SDNNodeHealth{Node: st.Node, Status: st.Status, Detail: st.Detail})
		}
		result.Zones = append(result.Zones, zh)
	}
	return result, nil
}

func (g *bypassPVEGateway) SDNConfig(ctx context.Context) (SDNConfig, error) {
	zones, err := g.client.ListSDNZones(ctx)
	if err != nil {
		return SDNConfig{}, err
	}
	vnets, err := g.client.ListSDNVnets(ctx)
	if err != nil {
		return SDNConfig{}, err
	}
	var cfg SDNConfig
	for _, z := range zones {
		cfg.Zones = append(cfg.Zones, SDNZoneConfig{ID: z.ID, Type: z.Type, Bridge: z.Bridge, Nodes: z.Nodes})
	}
	for _, v := range vnets {
		refID := v.Zone + "/" + v.ID
		cfg.Vnets = append(cfg.Vnets, SDNVnetConfig{ID: refID, Zone: v.Zone, Alias: v.Alias, Tag: v.Tag})
		subnets, err := g.client.ListSDNSubnets(ctx, v.ID)
		if err != nil {
			return SDNConfig{}, err
		}
		for _, s := range subnets {
			cfg.Subnets = append(cfg.Subnets, SDNSubnetConfig{ID: s.CIDR, Vnet: refID, Gateway: s.Gateway, SNAT: s.SNAT})
		}
	}
	return cfg, nil
}

func (g *bypassPVEGateway) FirewallRuleFields(context.Context, inventory.Ref, int) (FwRuleFields, error) {
	panic("bypassPVEGateway: FirewallRuleFields not implemented — this test's plan carries no fw.* ops")
}
func (g *bypassPVEGateway) ApplyFwOp(context.Context, Op) error {
	panic("bypassPVEGateway: ApplyFwOp not implemented — this test's plan carries no fw.* ops")
}
func (g *bypassPVEGateway) SnapshotFirewallScope(context.Context, inventory.Ref) (string, error) {
	panic("bypassPVEGateway: SnapshotFirewallScope not implemented — this test's plan carries no fw.* ops")
}
func (g *bypassPVEGateway) RestoreFirewallScope(context.Context, inventory.Ref, string) error {
	panic("bypassPVEGateway: RestoreFirewallScope not implemented — this test's plan carries no fw.* ops")
}
func (g *bypassPVEGateway) FirewallCompileStatus(context.Context, string) (FwCompileStatus, error) {
	panic("bypassPVEGateway: FirewallCompileStatus not implemented — this test's plan carries no fw.* ops")
}
func (g *bypassPVEGateway) AllocateIPAMAddress(context.Context, string, string, IpamAllocCreateParams) error {
	panic("bypassPVEGateway: AllocateIPAMAddress not implemented — this test's plan carries no ipam.* ops")
}
func (g *bypassPVEGateway) ReleaseIPAMAddress(context.Context, string, string, string) error {
	panic("bypassPVEGateway: ReleaseIPAMAddress not implemented — this test's plan carries no ipam.* ops")
}

// bypassNodeAgent panics on every call: this test's pure-SDN plan (zone +
// vnet + subnet + sdn.apply, no node-file ops) has an empty
// Plan.affectedNodes(), so nothing should ever call it.
type bypassNodeAgent struct{}

func (bypassNodeAgent) ReadInterfaces(context.Context, string) (string, error) {
	panic("bypassNodeAgent: ReadInterfaces unexpectedly called for a pure-SDN plan")
}
func (bypassNodeAgent) StageInterfaces(context.Context, string, string) error {
	panic("bypassNodeAgent: StageInterfaces unexpectedly called for a pure-SDN plan")
}
func (bypassNodeAgent) ReloadInterfaces(context.Context, string) error {
	panic("bypassNodeAgent: ReloadInterfaces unexpectedly called for a pure-SDN plan")
}
func (bypassNodeAgent) DiscardStaged(context.Context, string) error {
	panic("bypassNodeAgent: DiscardStaged unexpectedly called for a pure-SDN plan")
}

type noopBroadcaster struct{}

func (noopBroadcaster) Broadcast(string, []byte) {}

func TestExecutor_SNATWithoutGatewayShape_FailsAtSDNStageAndRollsBack(t *testing.T) {
	fx, err := pvemock.LoadFixture("../../testdata/clusters/three-node-vlan.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	mock := pvemock.NewServer(fx)
	ts := httptest.NewServer(mock)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	gw := &bypassPVEGateway{client: client, pollNode: "pve1"}
	ctx := context.Background()

	// Pre-state: three-node-vlan.yaml's own zones/vnets, before this test's
	// changeset touches anything — captured directly against pvemock so the
	// post-rollback assertions below are against the fixture's real
	// pre-state, not a guess.
	preZones, err := client.ListSDNZones(ctx)
	if err != nil {
		t.Fatalf("ListSDNZones (pre): %v", err)
	}
	preVnets, err := client.ListSDNVnets(ctx)
	if err != nil {
		t.Fatalf("ListSDNVnets (pre): %v", err)
	}

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc, err := NewService(Config{
		Changesets: store.NewChangesetRepo(db), Audit: store.NewAuditRepo(db),
		Snapshots: store.NewSnapshotRepo(db), Blobs: store.NewBlobRepo(db),
		Nodes: bypassNodeAgent{}, WS: noopBroadcaster{},
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// A real changeset row is needed for snapshot persistence's foreign
	// key — its *stored* ops don't matter (the executor below is driven
	// off the in-memory Changeset value's Ops, set explicitly to the
	// snat-without-gateway shape after this Create call, never
	// revalidated — that's the deliberate "bypass" this test is about).
	cs, err := svc.Create(ctx, "root@pam", "T-701 AC4 bypass test", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ops := []Op{
		{Type: OpSdnZoneCreate, Target: inventory.Ref{Kind: inventory.KindSDNZone, ID: "bypasszone"},
			Params: &SdnZoneCreateParams{Type: "simple", Nodes: []string{"pve1"}}},
		{Type: OpSdnVnetCreate, Target: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "bypasszone/vnet1"},
			Params: &SdnVnetCreateParams{Zone: "bypasszone"}},
		{Type: OpSdnSubnetCreate, Target: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "10.77.0.0/24"},
			// The shape validate_sdn.go's codeSNATRequiresGateway now blocks
			// pre-apply: snat=true, no gateway.
			Params: &SdnSubnetCreateParams{Vnet: "bypasszone/vnet1", CIDR: "10.77.0.0/24", SNAT: true}},
		{Type: OpSdnApply, Params: &SdnApplyParams{}},
	}
	cs.Ops = ops

	plan, err := BuildPlan(ops)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	pre, err := svc.captureSnapshotFull(ctx, cs.ID, snapshotKindPre, plan, gw)
	if err != nil {
		t.Fatalf("captureSnapshotFull: %v", err)
	}

	ex := svc.newExecutor(cs, plan, pre, gw, time.Now().Add(time.Minute).Unix())
	runErr := ex.run(ctx)
	if runErr == nil {
		t.Fatal("run() succeeded, want a failure at the sdn_stage step realizing the subnet")
	}
	if ex.log.FailedStep == nil {
		t.Fatal("apply log has no FailedStep")
	}
	failedStep := ex.log.Steps[*ex.log.FailedStep]
	if failedStep.Kind != StepSDNStage {
		t.Fatalf("failed step kind = %s, want sdn_stage", failedStep.Kind)
	}

	var sawSDNRollback bool
	for _, rb := range ex.log.Rollback {
		if rb.Summary != "" {
			sawSDNRollback = true
			if rb.Status != StepOK {
				t.Errorf("rollback log entry = %+v, want ok", rb)
			}
		}
	}
	if !sawSDNRollback {
		t.Fatalf("apply log carries no rollback entry: %+v", ex.log.Rollback)
	}

	// The zone/vnet the earlier (successful) sdn_stage steps created are
	// gone again; three-node-vlan.yaml's own pre-existing zones/vnets are
	// untouched — asserted against the exact pre-state captured above.
	zonesAfter, err := client.ListSDNZones(ctx)
	if err != nil {
		t.Fatalf("ListSDNZones (after): %v", err)
	}
	for _, z := range zonesAfter {
		if z.ID == "bypasszone" {
			t.Fatalf("zone bypasszone still present after rollback: %+v", z)
		}
	}
	if len(zonesAfter) != len(preZones) {
		t.Fatalf("zone count after rollback = %d, want %d (fixture's pre-state)", len(zonesAfter), len(preZones))
	}
	vnetsAfter, err := client.ListSDNVnets(ctx)
	if err != nil {
		t.Fatalf("ListSDNVnets (after): %v", err)
	}
	if len(vnetsAfter) != len(preVnets) {
		t.Fatalf("vnet count after rollback = %d, want %d (fixture's pre-state)", len(vnetsAfter), len(preVnets))
	}
}
