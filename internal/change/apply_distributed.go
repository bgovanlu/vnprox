// SPDX-License-Identifier: Apache-2.0

package change

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/bgovanlu/vnprox/internal/peer"
)

// cancelNodeTimers fans out CancelTimer to every node concurrently (bounded
// by each call's own client-side timeout, so N nodes cost roughly one round
// trip's worth of wall time, not N), returning one NodeTimerLog per node:
// NodeTimerStatusCancelled on success, or NodeTimerStatusUnknown (with the
// error recorded) when the node couldn't be reached — that node's own timer
// is still armed and is the only thing standing between it and a spurious
// rollback of a change the user already confirmed (see Confirm's doc
// comment).
func (s *Service) cancelNodeTimers(ctx context.Context, changesetID string, nodes []string) []NodeTimerLog {
	out := make([]NodeTimerLog, len(nodes))
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Add(1)
		go func(i int, node string) {
			defer wg.Done()
			rec, err := s.nodeTimers.CancelTimer(ctx, changesetID, node)
			if err != nil {
				if errors.Is(err, peer.ErrTimerNotFound) {
					// This node was never armed (e.g. it holds no steps of
					// its own — shouldn't happen for a node in
					// affectedNodes, but degrade gracefully) — nothing to
					// cancel, not a safety concern.
					out[i] = NodeTimerLog{Node: node, Status: NodeTimerStatusCancelled}
					return
				}
				s.log.Warn("change: cancelling node timer", "changeset_id", changesetID, "node", node, "error", err)
				out[i] = NodeTimerLog{Node: node, Status: NodeTimerStatusUnknown, Error: err.Error()}
				return
			}
			out[i] = nodeTimerLogFromRecord(rec)
		}(i, node)
	}
	wg.Wait()
	return out
}

// restoreAllDistributed is restoreAll's cluster-aware counterpart (used
// instead of it whenever s.nodeTimers is configured): it restores every
// node's pre-apply file state exactly like restoreAll, but distinguishes a
// node that couldn't be reached at all (peer.ErrPeerUnreachable — that
// node's own local timer, armed before its reload ran, is expected to
// self-restore independently) from a node that was reached but genuinely
// failed to restore. Only the latter counts toward anyHardFailed (the
// existing "couldn't even fully roll back" -> StatusFailed rule); an
// unreachable node instead gets NodeTimerStatusUnknown, pending Reconcile.
func (s *Service) restoreAllDistributed(ctx context.Context, changesetID string, plan Plan, pre []snapshotFile) ([]RollbackLog, []NodeTimerLog, bool) {
	preByNode := make(map[string]string, len(pre))
	for _, f := range pre {
		preByNode[f.Node] = f.Content
	}
	nodes := plan.affectedNodes()
	rbLogs := make([]RollbackLog, 0, len(nodes))
	nodeTimers := make([]NodeTimerLog, 0, len(nodes))
	anyHardFailed := false

	for i := len(nodes) - 1; i >= 0; i-- {
		node := nodes[i]
		rb := RollbackLog{
			Node:    node,
			At:      s.now().Unix(),
			Status:  StepOK,
			Summary: fmt.Sprintf("Restore %s on %s from pre-apply snapshot and reload", interfacesPath, node),
		}
		err := s.restoreNode(ctx, node, preByNode[node])
		switch {
		case err == nil:
			nodeTimers = append(nodeTimers, NodeTimerLog{Node: node, Status: NodeTimerStatusRolledBack, ResolvedAt: s.now().Unix()})
		case errors.Is(err, peer.ErrPeerUnreachable):
			// Best-effort push failed to even reach the node; its own
			// deadline-armed timer is the safety net now — not a hard
			// failure of the changeset's rollback.
			rb.Status = StepFailed
			rb.Error = err.Error()
			rb.Summary = fmt.Sprintf("Could not reach %s to push a restore; its own local timer will self-restore (or already has)", node)
			nodeTimers = append(nodeTimers, NodeTimerLog{Node: node, Status: NodeTimerStatusUnknown, Error: err.Error()})
		default:
			rb.Status = StepFailed
			rb.Error = err.Error()
			anyHardFailed = true
			nodeTimers = append(nodeTimers, NodeTimerLog{Node: node, Status: NodeTimerStatusRollbackFailed, Error: err.Error(), ResolvedAt: s.now().Unix()})
		}
		rbLogs = append(rbLogs, rb)
	}
	return rbLogs, nodeTimers, anyHardFailed
}

// mergeNodeTimerLogs returns existing with every entry in updates replacing
// (by Node) the prior entry for that node, appending any node in updates
// not already present. Order of existing (minus replaced-in-place moves) is
// preserved so the persisted log reads in a stable, plan-derived order.
func mergeNodeTimerLogs(existing, updates []NodeTimerLog) []NodeTimerLog {
	byNode := make(map[string]int, len(existing))
	out := make([]NodeTimerLog, len(existing))
	copy(out, existing)
	for i, e := range out {
		byNode[e.Node] = i
	}
	for _, u := range updates {
		if i, ok := byNode[u.Node]; ok {
			out[i] = u
		} else {
			byNode[u.Node] = len(out)
			out = append(out, u)
		}
	}
	return out
}

func nodeTimerLogFromRecord(rec peer.TimerRecord) NodeTimerLog {
	return NodeTimerLog{
		Node: rec.Node, Status: NodeTimerStatus(rec.Status), Error: rec.Error,
		Deadline: rec.Deadline, ResolvedAt: rec.ResolvedAt,
	}
}

// updateApplyLog persists a changeset's apply_log_json in isolation (a
// read-modify-write over the current row) — used by the post-terminal
// bookkeeping (Confirm's cancellation fan-out, Reconcile) that updates the
// log after the changeset's own status transition has already been
// persisted and doesn't need to touch anything else about the row.
func (s *Service) updateApplyLog(ctx context.Context, id string, log ApplyLog) error {
	cs, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	logJSON, err := json.Marshal(log)
	if err != nil {
		return fmt.Errorf("change: marshaling apply log for changeset %s: %w", id, err)
	}
	cs.ApplyLog = logJSON
	return s.persist(ctx, cs)
}

// Reconcile scans terminal changesets (rolled_back/failed) whose apply log
// still carries a NodeTimerStatusUnknown entry — a node the coordinator lost
// contact with mid-window, per docs/features/change-management.md §4's
// "coordinator-side reconciliation on reconnect" — and, for each, asks that
// node directly what happened to its timer, updating the log in place. It
// never changes a changeset's terminal Status (the state machine has no
// transition out of a terminal state, and the coordinator's own decision —
// rolled_back once its own deadline elapsed — already stands); it only
// completes the per-node detail the T-304 card's AC2 asks for. It returns
// how many (changeset, node) entries it resolved, for callers (a periodic
// loop, or a test) that want to know whether there's more reconciling to do.
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	if s.nodeTimers == nil {
		return 0, nil
	}
	resolved := 0
	for _, status := range []Status{StatusRolledBack, StatusFailed} {
		changesets, err := s.List(ctx, string(status))
		if err != nil {
			return resolved, fmt.Errorf("change: reconcile: listing %s changesets: %w", status, err)
		}
		for _, cs := range changesets {
			n, err := s.reconcileOne(ctx, cs)
			if err != nil {
				s.log.Warn("change: reconcile: changeset", "changeset_id", cs.ID, "error", err)
				continue
			}
			resolved += n
		}
	}
	return resolved, nil
}

func (s *Service) reconcileOne(ctx context.Context, cs Changeset) (int, error) {
	log := decodeApplyLog(cs.ApplyLog)
	var pending []string
	for _, nt := range log.NodeTimers {
		if nt.Status == NodeTimerStatusUnknown {
			pending = append(pending, nt.Node)
		}
	}
	if len(pending) == 0 {
		return 0, nil
	}

	resolvedCount := 0
	var updates []NodeTimerLog
	for _, node := range pending {
		rec, err := s.nodeTimers.TimerStatus(ctx, cs.ID, node)
		if err != nil {
			if errors.Is(err, peer.ErrTimerNotFound) {
				// The node answered but has no memory of this changeset at
				// all (e.g. it never received the arm call in the first
				// place) — leave it unknown rather than guessing.
				continue
			}
			s.log.Warn("change: reconcile: querying node timer status", "changeset_id", cs.ID, "node", node, "error", err)
			continue
		}
		if rec.Status == peer.TimerArmed {
			continue // still genuinely pending — this node hasn't hit its own deadline yet
		}
		updates = append(updates, nodeTimerLogFromRecord(rec))
		resolvedCount++
	}
	if len(updates) == 0 {
		return 0, nil
	}

	log.NodeTimers = mergeNodeTimerLogs(log.NodeTimers, updates)
	if err := s.updateApplyLog(ctx, cs.ID, log); err != nil {
		return 0, err
	}
	s.appendAudit(ctx, systemRollbackActor, "changeset.reconcile", "resolved", cs.ID, map[string]any{"nodes": pending})
	return resolvedCount, nil
}
