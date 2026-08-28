// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/tcmirror"
)

// hostTcMirrorGateway is the production change.TcMirrorGateway (T-4014): it
// renders (internal/tcmirror.RenderTC/RenderTCTeardown) and execs each
// tc.mirror.* op's on-node tc commands with a fixed argv array (no dynamic
// shell interpolation, mirroring hostQosGateway/hostWGGateway/execIfreload's
// identical convention elsewhere in this file's sibling gateways), and
// persists the session's own intent+accounting row
// (store.TcMirrorSessionRepo), including the expires_at deadline
// internal/change/tcmirror_expiry.go's sweep enforces.
//
// NEEDS HARDWARE VALIDATION: the exec path runs a real `tc` binary against
// a real kernel clsact/mirred stack; it is exercised only by this
// package's own tests with an injected no-op execTC, never against a live
// tc — the read-only constraint on T-4014's task card forbade running
// `tc qdisc add`/`tc filter add` against pvecube (see
// planning/reports/evidence/pve-9.2.4-tc-mirred-2026-08-28.txt for what
// WAS observed read-only). Peer-node tc.mirror apply is not yet routed (an
// op targeting another node errors cleanly) — the same single-node scope
// hostQosGateway's own doc comment documents until cluster routing for
// this op family lands.
type hostTcMirrorGateway struct {
	repo      *store.TcMirrorSessionRepo
	localNode func() string
	now       func() time.Time
	log       *slog.Logger
	execTC    func(ctx context.Context, argv []string) error
}

var _ change.TcMirrorGateway = (*hostTcMirrorGateway)(nil)

func newHostTcMirrorGateway(repo *store.TcMirrorSessionRepo, localNode func() string, logger *slog.Logger) *hostTcMirrorGateway {
	return &hostTcMirrorGateway{
		repo: repo, localNode: localNode, now: time.Now, log: logger,
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

// newDevTcMirrorGateway is the sandboxed dev variant ([safety]
// dev_interfaces_dir, mirroring newDevQosGateway's identical rationale):
// the real store-backed session persistence, but every tc invocation is a
// logged no-op — a `make dev` daemon must never install a real clsact/
// mirred SPAN against the developer's own traffic any more than it may
// rewrite /etc/network/interfaces or reconfigure real tc/HTB.
func newDevTcMirrorGateway(repo *store.TcMirrorSessionRepo, localNode func() string, logger *slog.Logger) *hostTcMirrorGateway {
	return &hostTcMirrorGateway{
		repo: repo, localNode: localNode, now: time.Now, log: logger,
		execTC: func(_ context.Context, argv []string) error {
			logger.Info("tcmirror: dev sandbox gateway — skipping real tc exec", "argv", strings.Join(argv, " "))
			return nil
		},
	}
}

func (g *hostTcMirrorGateway) ensureLocal(node string) error {
	if g.localNode == nil {
		return nil
	}
	if local := g.localNode(); local != "" && node != local {
		return fmt.Errorf("tcmirror: cannot apply a tc.mirror op for peer node %q from %q — cluster tc.mirror routing is not yet implemented (needs a follow-up task)", node, local)
	}
	return nil
}

func (g *hostTcMirrorGateway) exec(ctx context.Context, lines [][]string) error {
	for _, argv := range lines {
		if err := g.execTC(ctx, argv); err != nil {
			return fmt.Errorf("tcmirror: %w", err)
		}
	}
	return nil
}

func storeSessionToTcMirror(s store.TcMirrorSession) tcmirror.Session {
	maxMbit := 0
	if s.MaxMbit != nil {
		maxMbit = *s.MaxMbit
	}
	return tcmirror.Session{ID: s.ID, Node: s.Node, SourceIface: s.SourceIface, DestIface: s.DestIface, MaxMbit: maxMbit}
}

// ApplyTcMirrorOp implements change.TcMirrorGateway.
func (g *hostTcMirrorGateway) ApplyTcMirrorOp(ctx context.Context, op change.Op) error {
	if err := g.ensureLocal(op.Target.Node); err != nil {
		return err
	}
	switch p := op.Params.(type) {
	case *change.TcMirrorCreateParams:
		return g.createSession(ctx, op, p)
	case *change.TcMirrorUpdateParams:
		return g.updateSession(ctx, op, p)
	case *change.TcMirrorDeleteParams:
		return g.deleteSession(ctx, op)
	default:
		return fmt.Errorf("tcmirror: unsupported op %s", op.Type)
	}
}

func (g *hostTcMirrorGateway) createSession(ctx context.Context, op change.Op, p *change.TcMirrorCreateParams) error {
	sess := tcmirror.Session{ID: op.Target.ID, Node: op.Target.Node, SourceIface: p.SourceIface, DestIface: p.DestIface}
	if p.MaxMbit != nil {
		sess.MaxMbit = *p.MaxMbit
	}
	lines, err := tcmirror.RenderTC(sess)
	if err != nil {
		return fmt.Errorf("tcmirror: rendering session %s: %w", op.Target.ID, err)
	}
	if err := g.exec(ctx, lines); err != nil {
		return err
	}
	startedAt := g.now().Unix()
	return g.repo.Insert(ctx, store.TcMirrorSession{
		ID: op.Target.ID, Node: op.Target.Node, SourceIface: p.SourceIface, DestIface: p.DestIface,
		MaxMbit: p.MaxMbit, MaxDurationSec: p.MaxDurationSec, Status: store.TcMirrorSessionActive,
		CreatedBy: op.Target.Node, StartedAt: startedAt, ExpiresAt: startedAt + int64(p.MaxDurationSec),
	})
}

// updateSession implements tc.mirror.update's ONLY mutable field
// (MaxDurationSec) — pure store bookkeeping, no tc re-render, per
// params_tcmirror.go's TcMirrorUpdateParams doc comment (source/dest are
// immutable after create).
func (g *hostTcMirrorGateway) updateSession(ctx context.Context, op change.Op, p *change.TcMirrorUpdateParams) error {
	if p.MaxDurationSec == nil {
		return nil
	}
	s, err := g.repo.Get(ctx, op.Target.ID)
	if err != nil {
		return fmt.Errorf("tcmirror: loading session %s to update: %w", op.Target.ID, err)
	}
	return g.repo.UpdateDuration(ctx, op.Target.ID, *p.MaxDurationSec, s.StartedAt+int64(*p.MaxDurationSec))
}

func (g *hostTcMirrorGateway) deleteSession(ctx context.Context, op change.Op) error {
	s, err := g.repo.Get(ctx, op.Target.ID)
	if err != nil {
		return fmt.Errorf("tcmirror: loading session %s to delete: %w", op.Target.ID, err)
	}
	if err := g.exec(ctx, tcmirror.RenderTCTeardown(storeSessionToTcMirror(s))); err != nil {
		g.log.Warn("tcmirror: tearing down session (continuing — the filter/qdisc may already be gone)", "session", op.Target.ID, "error", err)
	}
	return g.repo.SetStatus(ctx, op.Target.ID, store.TcMirrorSessionStopped, g.now().Unix())
}

// SnapshotTcMirror implements change.TcMirrorGateway: serialize node's full
// active session set.
func (g *hostTcMirrorGateway) SnapshotTcMirror(ctx context.Context, node string) (string, error) {
	sessions, err := g.repo.ActiveByNode(ctx, node)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(sessions)
	return string(b), err
}

// RestoreTcMirror implements change.TcMirrorGateway: reconcile node's
// session set back to a SnapshotTcMirror output — tear down sessions absent
// from the snapshot, re-apply/re-store sessions present in it. Callable
// unattended (no user ticket).
func (g *hostTcMirrorGateway) RestoreTcMirror(ctx context.Context, node, snapshot string) error {
	if err := g.ensureLocal(node); err != nil {
		return err
	}
	var want []store.TcMirrorSession
	if err := json.Unmarshal([]byte(snapshot), &want); err != nil {
		return fmt.Errorf("tcmirror: decoding snapshot for node %s: %w", node, err)
	}
	wantByID := make(map[string]store.TcMirrorSession, len(want))
	for _, s := range want {
		wantByID[s.ID] = s
	}
	live, err := g.repo.ActiveByNode(ctx, node)
	if err != nil {
		return fmt.Errorf("tcmirror: listing live sessions on %s: %w", node, err)
	}
	for _, s := range live {
		if _, ok := wantByID[s.ID]; ok {
			continue
		}
		if err := g.exec(ctx, tcmirror.RenderTCTeardown(storeSessionToTcMirror(s))); err != nil {
			g.log.Warn("tcmirror: tearing down session not in restore target (continuing)", "session", s.ID, "error", err)
		}
		if err := g.repo.SetStatus(ctx, s.ID, store.TcMirrorSessionStopped, g.now().Unix()); err != nil {
			return err
		}
	}
	for _, s := range want {
		lines, err := tcmirror.RenderTC(storeSessionToTcMirror(s))
		if err != nil {
			return fmt.Errorf("tcmirror: rendering restored session %s: %w", s.ID, err)
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
		if err := g.repo.UpdateDuration(ctx, s.ID, s.MaxDurationSec, s.ExpiresAt); err != nil {
			return err
		}
	}
	return nil
}
