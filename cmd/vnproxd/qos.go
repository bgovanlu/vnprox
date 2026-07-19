package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/qos"
	"github.com/bgovanlu/vnprox/internal/store"
)

// hostQosGateway is the production change.QosGateway (T-1505): it renders
// (internal/qos.RenderTC/RenderTCTeardown) and execs each qos.shape.* op's
// on-node tc commands with a fixed argv array (no dynamic shell
// interpolation, mirroring hostWGGateway/execIfreload's identical
// convention elsewhere in this file's sibling gateways), and persists the
// shape's own intent row (store.QosShapeRepo).
//
// NEEDS HARDWARE VALIDATION: the exec path runs a real `tc` binary against
// a real kernel HTB/u32 stack; it is exercised only by this package's own
// tests with an injected no-op execTC, never against a live tc. Peer-node
// qos apply is not yet routed (an op targeting another node errors
// cleanly) — the same single-node scope hostNodeAgent's own doc comment
// documents until cluster routing for this op family lands.
type hostQosGateway struct {
	repo      *store.QosShapeRepo
	localNode func() string
	log       *slog.Logger
	execTC    func(ctx context.Context, argv []string) error
}

var _ change.QosGateway = (*hostQosGateway)(nil)

func newHostQosGateway(repo *store.QosShapeRepo, localNode func() string, logger *slog.Logger) *hostQosGateway {
	return &hostQosGateway{
		repo: repo, localNode: localNode, log: logger,
		execTC: func(ctx context.Context, argv []string) error {
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
			}
			return nil
		},
	}
}

// newDevQosGateway is the sandboxed dev variant ([safety] dev_interfaces_dir,
// mirroring newDevNodeAgent's identical rationale): the real store-backed
// shape persistence, but every tc invocation is a logged no-op — a `make
// dev` daemon must never reconfigure the developer's real kernel queueing
// discipline any more than it may rewrite /etc/network/interfaces.
func newDevQosGateway(repo *store.QosShapeRepo, localNode func() string, logger *slog.Logger) *hostQosGateway {
	return &hostQosGateway{
		repo: repo, localNode: localNode, log: logger,
		execTC: func(_ context.Context, argv []string) error {
			logger.Info("qos: dev sandbox gateway — skipping real tc exec", "argv", strings.Join(argv, " "))
			return nil
		},
	}
}

func (g *hostQosGateway) ensureLocal(node string) error {
	if g.localNode == nil {
		return nil
	}
	if local := g.localNode(); local != "" && node != local {
		return fmt.Errorf("qos: cannot apply a qos op for peer node %q from %q — cluster qos routing is not yet implemented (needs a follow-up task)", node, local)
	}
	return nil
}

func (g *hostQosGateway) exec(ctx context.Context, lines [][]string) error {
	for _, argv := range lines {
		if err := g.execTC(ctx, argv); err != nil {
			return fmt.Errorf("qos: %w", err)
		}
	}
	return nil
}

func storeShapeToQos(s store.QosShape) qos.Shape {
	return qos.Shape{
		ID: s.ID, Node: s.Node, Bridge: s.Bridge, MatchCIDR: s.MatchCIDR,
		MatchVlan: s.MatchVlan, RateMbit: s.RateMbit, CeilMbit: s.CeilMbit, Priority: s.Priority,
	}
}

// ApplyQosOp implements change.QosGateway.
func (g *hostQosGateway) ApplyQosOp(ctx context.Context, op change.Op) error {
	if err := g.ensureLocal(op.Target.Node); err != nil {
		return err
	}
	switch p := op.Params.(type) {
	case *change.QosShapeCreateParams:
		return g.createShape(ctx, op, p)
	case *change.QosShapeUpdateParams:
		return g.updateShape(ctx, op, p)
	case *change.QosShapeDeleteParams:
		return g.deleteShape(ctx, op)
	default:
		return fmt.Errorf("qos: unsupported op %s", op.Type)
	}
}

func (g *hostQosGateway) createShape(ctx context.Context, op change.Op, p *change.QosShapeCreateParams) error {
	shape := qos.Shape{
		ID: op.Target.ID, Node: op.Target.Node, Bridge: p.Bridge, MatchCIDR: p.MatchCIDR,
		MatchVlan: p.MatchVlan, RateMbit: p.RateMbit, CeilMbit: p.CeilMbit, Priority: p.Priority,
	}
	lines, err := qos.RenderTC(shape)
	if err != nil {
		return fmt.Errorf("qos: rendering shape %s: %w", op.Target.ID, err)
	}
	if err := g.exec(ctx, lines); err != nil {
		return err
	}
	return g.repo.Insert(ctx, store.QosShape{
		ID: op.Target.ID, Node: op.Target.Node, Bridge: p.Bridge, MatchCIDR: p.MatchCIDR,
		MatchVlan: p.MatchVlan, RateMbit: p.RateMbit, CeilMbit: p.CeilMbit, Priority: p.Priority,
		CreatedBy: op.Target.Node, CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
	})
}

func (g *hostQosGateway) updateShape(ctx context.Context, op change.Op, p *change.QosShapeUpdateParams) error {
	s, err := g.repo.Get(ctx, op.Target.ID)
	if err != nil {
		return fmt.Errorf("qos: loading shape %s to update: %w", op.Target.ID, err)
	}
	// Tear down the OLD rendering first: a match-criteria change (bridge/
	// matchCidr/matchVlan) installs a *different* tc filter selector, and
	// bare `tc filter replace` with no explicit handle adds rather than
	// replaces when the match content itself differs — see RenderTC's doc
	// comment. Tearing down unconditionally (even for a rate/ceil-only
	// update, where it's a harmless extra churn cycle) keeps this one
	// code path correct for every field combination rather than needing to
	// detect "did the selector change" itself.
	if teardownErr := g.exec(ctx, qos.RenderTCTeardown(storeShapeToQos(s))); teardownErr != nil {
		g.log.Warn("qos: tearing down previous shape rendering before update (continuing)", "shape", op.Target.ID, "error", teardownErr)
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
	lines, err := qos.RenderTC(storeShapeToQos(s))
	if err != nil {
		return fmt.Errorf("qos: rendering updated shape %s: %w", op.Target.ID, err)
	}
	if err := g.exec(ctx, lines); err != nil {
		return err
	}
	s.UpdatedAt = time.Now().Unix()
	return g.repo.Update(ctx, s)
}

func (g *hostQosGateway) deleteShape(ctx context.Context, op change.Op) error {
	s, err := g.repo.Get(ctx, op.Target.ID)
	if err != nil {
		return fmt.Errorf("qos: loading shape %s to delete: %w", op.Target.ID, err)
	}
	if err := g.exec(ctx, qos.RenderTCTeardown(storeShapeToQos(s))); err != nil {
		g.log.Warn("qos: tearing down shape (continuing — the class/filter may already be gone)", "shape", op.Target.ID, "error", err)
	}
	return g.repo.Delete(ctx, op.Target.ID)
}

// SnapshotQos implements change.QosGateway: serialize node's full shape set.
func (g *hostQosGateway) SnapshotQos(ctx context.Context, node string) (string, error) {
	shapes, err := g.repo.List(ctx, node)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(shapes)
	return string(b), err
}

// RestoreQos implements change.QosGateway: reconcile node's shape set back
// to a SnapshotQos output — tear down shapes absent from the snapshot,
// re-apply/re-store shapes present in it. Callable unattended (no user
// ticket).
func (g *hostQosGateway) RestoreQos(ctx context.Context, node, snapshot string) error {
	if err := g.ensureLocal(node); err != nil {
		return err
	}
	var want []store.QosShape
	if err := json.Unmarshal([]byte(snapshot), &want); err != nil {
		return fmt.Errorf("qos: decoding snapshot for node %s: %w", node, err)
	}
	wantByID := make(map[string]store.QosShape, len(want))
	for _, s := range want {
		wantByID[s.ID] = s
	}
	live, err := g.repo.List(ctx, node)
	if err != nil {
		return fmt.Errorf("qos: listing live shapes on %s: %w", node, err)
	}
	for _, s := range live {
		if _, ok := wantByID[s.ID]; ok {
			continue
		}
		if err := g.exec(ctx, qos.RenderTCTeardown(storeShapeToQos(s))); err != nil {
			g.log.Warn("qos: tearing down shape not in restore target (continuing)", "shape", s.ID, "error", err)
		}
		if err := g.repo.Delete(ctx, s.ID); err != nil {
			return err
		}
	}
	for _, s := range want {
		lines, err := qos.RenderTC(storeShapeToQos(s))
		if err != nil {
			return fmt.Errorf("qos: rendering restored shape %s: %w", s.ID, err)
		}
		if err := g.exec(ctx, lines); err != nil {
			return err
		}
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

// --- read service (api.QosShapeSource / api.QosShapeListService) ----------

// qosReadService serves the read-only QoS surface: GET /qos/shapes, the
// simulator's shape-awareness input (api.QosShapeSource), and GET
// /topology's shaping-active badge input, all reading this daemon's own
// node's store — the same single-node scope hostQosGateway's own doc
// comment documents until cluster routing lands.
type qosReadService struct {
	repo *store.QosShapeRepo
}

var (
	_ api.QosShapeListService = (*qosReadService)(nil)
	_ api.QosShapeSource      = (*qosReadService)(nil)
)

func newQosReadService(repo *store.QosShapeRepo) *qosReadService {
	return &qosReadService{repo: repo}
}

func (s *qosReadService) ListShapes(ctx context.Context) ([]api.QosShapeView, error) {
	shapes, err := s.repo.List(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]api.QosShapeView, 0, len(shapes))
	for _, sh := range shapes {
		out = append(out, api.QosShapeView{
			ID: sh.ID, Node: sh.Node, Bridge: sh.Bridge, MatchCIDR: sh.MatchCIDR,
			MatchVlan: sh.MatchVlan, RateMbit: sh.RateMbit, CeilMbit: sh.CeilMbit, Priority: sh.Priority,
		})
	}
	return out, nil
}

// ShapedBridgeRefs implements api.QosShapeSource: every currently-stored
// shape's bridge, as a bridge Ref (a shape's Bridge field names a bridge
// interface — validate_referential.go's checkQosBridge is this exact
// contract's write-side enforcement, so every stored shape's bridge is
// known to have existed as a bridge at apply time).
func (s *qosReadService) ShapedBridgeRefs(ctx context.Context) (map[inventory.Ref]bool, error) {
	shapes, err := s.repo.List(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make(map[inventory.Ref]bool, len(shapes))
	for _, sh := range shapes {
		out[inventory.Ref{Kind: inventory.KindBridge, Node: sh.Node, ID: sh.Bridge}] = true
	}
	return out, nil
}
