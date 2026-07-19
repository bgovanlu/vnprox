package change_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// qosTestVmbr0 seeds pve1's vmbr0 bridge into the harness's InventorySource
// (newHarness's Inventory is nil by default — see its doc comment) so the
// referential validator's bridge-existence check
// (validate_referential.go's checkQosBridge) has something to resolve
// against; three-node-vlan's real fixture does carry a vmbr0 on pve1, this
// is just the minimal stand-in newHarness's own withInventory convention
// calls for.
func qosTestVmbr0() *inventory.Bridge {
	return &inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0"}
}

// fakeQosGateway is a store-backed change.QosGateway double used by the QoS
// lifecycle/rollback tests (T-1505 AC1). It mirrors the production
// gateway's shape custody: a qos.shape.create/update/delete op is persisted
// to store.QosShapeRepo (the on-node tc/HTB exec itself is production-only —
// see cmd/vnproxd's hostQosGateway — this fake only needs to prove the
// change-engine lifecycle, not real kernel state).
type fakeQosGateway struct {
	repo *store.QosShapeRepo
}

func newFakeQosGateway(db *store.DB) *fakeQosGateway {
	return &fakeQosGateway{repo: store.NewQosShapeRepo(db)}
}

func (g *fakeQosGateway) ApplyQosOp(ctx context.Context, op change.Op) error {
	switch p := op.Params.(type) {
	case *change.QosShapeCreateParams:
		return g.repo.Insert(ctx, store.QosShape{
			ID: op.Target.ID, Node: op.Target.Node, Bridge: p.Bridge,
			MatchCIDR: p.MatchCIDR, MatchVlan: p.MatchVlan,
			RateMbit: p.RateMbit, CeilMbit: p.CeilMbit, Priority: p.Priority,
			CreatedBy: "root@pam", CreatedAt: 1, UpdatedAt: 1,
		})
	case *change.QosShapeUpdateParams:
		s, err := g.repo.Get(ctx, op.Target.ID)
		if err != nil {
			return err
		}
		if p.Bridge != nil {
			s.Bridge = *p.Bridge
		}
		if p.MatchCIDR != nil {
			s.MatchCIDR = *p.MatchCIDR
		}
		if p.MatchVlan != nil {
			s.MatchVlan = p.MatchVlan
		}
		if p.RateMbit != nil {
			s.RateMbit = *p.RateMbit
		}
		if p.CeilMbit != nil {
			s.CeilMbit = p.CeilMbit
		}
		if p.Priority != nil {
			s.Priority = p.Priority
		}
		s.UpdatedAt = 2
		return g.repo.Update(ctx, s)
	case *change.QosShapeDeleteParams:
		return g.repo.Delete(ctx, op.Target.ID)
	default:
		return fmt.Errorf("fakeQosGateway: unsupported op %s", op.Type)
	}
}

func (g *fakeQosGateway) SnapshotQos(ctx context.Context, node string) (string, error) {
	shapes, err := g.repo.List(ctx, node)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(shapes)
	return string(b), err
}

func (g *fakeQosGateway) RestoreQos(ctx context.Context, node, snapshot string) error {
	var want []store.QosShape
	if err := json.Unmarshal([]byte(snapshot), &want); err != nil {
		return err
	}
	wantIDs := map[string]bool{}
	for _, s := range want {
		wantIDs[s.ID] = true
	}
	live, err := g.repo.List(ctx, node)
	if err != nil {
		return err
	}
	for _, s := range live {
		if !wantIDs[s.ID] {
			if err := g.repo.Delete(ctx, s.ID); err != nil {
				return err
			}
		}
	}
	for _, s := range want {
		if _, getErr := g.repo.Get(ctx, s.ID); getErr != nil {
			if err := g.repo.Insert(ctx, s); err != nil {
				return err
			}
			continue
		}
		if err := g.repo.Update(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

var _ change.QosGateway = (*fakeQosGateway)(nil)

// TestApply_QosShapeLifecycle_CreateConfirm is T-1505 acceptance criterion
// 1's happy path: a qos.shape.create against three-node-vlan stages,
// validates, and applies cleanly, landing the shape in the QosGateway's
// store — the ordinary stage→validate→apply→confirm lifecycle, no second
// mutation path.
func TestApply_QosShapeLifecycle_CreateConfirm(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	gw := newFakeQosGateway(h.db)
	cfg := change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, Qos: gw,
	}
	withInventory(qosTestVmbr0())(&cfg)
	svc := newService(t, cfg)
	ctx := context.Background()

	ops := opsFromJSON(t, `[{"op":"qos.shape.create","target":"qos-shape:pve1:shape1","params":{"bridge":"vmbr0","rateMbit":10,"ceilMbit":20}}]`)
	cs, err := svc.Create(ctx, "root@pam", "qos shape", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	validated, err := svc.Validate(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, f := range validated.Findings {
		if f.Severity == change.SeverityError {
			t.Fatalf("Validate found a blocking error: %+v", f)
		}
	}

	// Diff (T-205's node-file diff surface has nothing to say about a
	// qos.* op — it isn't a node-file op — but the call itself must not
	// error against a changeset that carries one).
	if _, diffErr := svc.Diff(ctx, cs.ID); diffErr != nil {
		t.Fatalf("Diff: %v", diffErr)
	}

	applied, err := svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status = %s, want awaiting_confirm", applied.Status)
	}

	shape, err := gw.repo.Get(ctx, "shape1")
	if err != nil {
		t.Fatalf("GetShape: %v", err)
	}
	if shape.Bridge != "vmbr0" || shape.RateMbit != 10 || shape.CeilMbit == nil || *shape.CeilMbit != 20 {
		t.Fatalf("stored shape = %+v, want bridge=vmbr0 rate=10 ceil=20", shape)
	}

	confirmed, err := svc.Confirm(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.Status != change.StatusCommitted {
		t.Fatalf("status = %s, want committed", confirmed.Status)
	}
	if _, err := gw.repo.Get(ctx, "shape1"); err != nil {
		t.Fatalf("shape should survive a committed apply: %v", err)
	}
}

// TestApply_QosShapeLifecycle_RollbackOnTimeout is T-1505 acceptance
// criterion 1's rollback path: a qos.shape.create that reaches
// awaiting_confirm and then times out un-confirmed is fully reverted on the
// unattended auto-rollback path (the QosGateway is daemon-level, no user
// ticket needed — T-205's existing inverse-order rollback contract).
func TestApply_QosShapeLifecycle_RollbackOnTimeout(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	gw := newFakeQosGateway(h.db)
	cfg := change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, Qos: gw,
	}
	withInventory(qosTestVmbr0())(&cfg)
	svc := newService(t, cfg)
	ctx := context.Background()

	ops := opsFromJSON(t, `[{"op":"qos.shape.create","target":"qos-shape:pve1:shape1","params":{"bridge":"vmbr0","rateMbit":10}}]`)
	cs, err := svc.Create(ctx, "root@pam", "qos shape", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, applyErr := svc.Apply(ctx, cs.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}
	if _, getErr := gw.repo.Get(ctx, "shape1"); getErr != nil {
		t.Fatalf("shape should exist after apply: %v", getErr)
	}

	// Deadline elapses with no confirm -> auto-rollback.
	h.timers.fireLatest(t)

	got, err := svc.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != change.StatusRolledBack {
		t.Fatalf("status = %s, want rolled_back", got.Status)
	}
	if _, getErr := gw.repo.Get(ctx, "shape1"); getErr == nil {
		t.Fatal("shape should be removed after rollback — no orphaned shape left behind")
	}
}

// TestApply_QosShapeLifecycle_UpdateAndDelete rounds out AC1's "staged,
// validated, diffed, applied, and rolled back cleanly" with the other two
// members of the op group: an update against the shape TestApply_
// QosShapeLifecycle_CreateConfirm's create leaves behind, then a delete,
// each its own ordinary changeset.
func TestApply_QosShapeLifecycle_UpdateAndDelete(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	gw := newFakeQosGateway(h.db)
	cfg := change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, Qos: gw,
	}
	withInventory(qosTestVmbr0())(&cfg)
	svc := newService(t, cfg)
	ctx := context.Background()

	createOps := opsFromJSON(t, `[{"op":"qos.shape.create","target":"qos-shape:pve1:shape1","params":{"bridge":"vmbr0","rateMbit":10}}]`)
	cs1, err := svc.Create(ctx, "root@pam", "create", createOps)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, applyErr := svc.Apply(ctx, cs1.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("Apply create: %v", applyErr)
	}
	if _, confirmErr := svc.Confirm(ctx, cs1.ID, "root@pam"); confirmErr != nil {
		t.Fatalf("Confirm create: %v", confirmErr)
	}

	updateOps := opsFromJSON(t, `[{"op":"qos.shape.update","target":"qos-shape:pve1:shape1","params":{"rateMbit":25,"ceilMbit":50}}]`)
	cs2, err := svc.Create(ctx, "root@pam", "update", updateOps)
	if err != nil {
		t.Fatalf("Create update: %v", err)
	}
	if _, applyErr := svc.Apply(ctx, cs2.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("Apply update: %v", applyErr)
	}
	if _, confirmErr := svc.Confirm(ctx, cs2.ID, "root@pam"); confirmErr != nil {
		t.Fatalf("Confirm update: %v", confirmErr)
	}
	shape, err := gw.repo.Get(ctx, "shape1")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if shape.RateMbit != 25 || shape.CeilMbit == nil || *shape.CeilMbit != 50 {
		t.Fatalf("shape after update = %+v, want rate=25 ceil=50", shape)
	}

	deleteOps := opsFromJSON(t, `[{"op":"qos.shape.delete","target":"qos-shape:pve1:shape1","params":{}}]`)
	cs3, err := svc.Create(ctx, "root@pam", "delete", deleteOps)
	if err != nil {
		t.Fatalf("Create delete: %v", err)
	}
	if _, err := svc.Apply(ctx, cs3.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("Apply delete: %v", err)
	}
	if _, err := svc.Confirm(ctx, cs3.ID, "root@pam"); err != nil {
		t.Fatalf("Confirm delete: %v", err)
	}
	if _, getErr := gw.repo.Get(ctx, "shape1"); getErr == nil {
		t.Fatal("shape should be gone after qos.shape.delete commits")
	}
}
