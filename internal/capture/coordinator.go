// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DefaultCaps is the fallback ceiling set when a Config leaves one unset —
// conservative bounds so a misconfigured daemon never captures unbounded.
var DefaultCaps = Caps{
	MaxDurationSec: 300,              // 5 min
	MaxBytes:       50 * 1024 * 1024, // 50 MiB
	MaxPackets:     100_000,
	RetentionHours: 24,
}

// DefaultSweepInterval is how often the auto-purge sweep runs — coarse, like
// internal/metrics' hourly ring prune, since retention is measured in hours.
const DefaultSweepInterval = 15 * time.Minute

// Config configures a Coordinator.
type Config struct {
	Agent                 Agent
	Remote                RemoteCapturer
	Resolver              TargetResolver
	Store                 SessionStore
	Audit                 Auditor
	LocalNode             func() string
	Now                   func() time.Time
	IDGen                 func() string
	Logger                *slog.Logger
	Root                  string
	Ceilings              Caps
	MaxFilterInstructions int
}

// Coordinator orchestrates capture sessions: it validates filters, clamps
// caps, resolves targets, starts one session per node (local via Agent,
// remote via RemoteCapturer), persists intent to Store, audits start/stop,
// and runs the retention sweep. Safe for concurrent use.
type Coordinator struct {
	log   *slog.Logger
	now   func() time.Time
	idgen func() string
	procs map[string]Process
	cfg   Config
	mu    sync.Mutex
}

// New builds a Coordinator, defaulting unset config.
func New(cfg Config) *Coordinator {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.IDGen == nil {
		cfg.IDGen = newID
	}
	if cfg.Resolver == nil {
		cfg.Resolver = RefResolver{}
	}
	if cfg.LocalNode == nil {
		cfg.LocalNode = func() string { return "" }
	}
	cfg.Ceilings = defaultCeilings(cfg.Ceilings)
	if cfg.MaxFilterInstructions <= 0 {
		cfg.MaxFilterInstructions = DefaultMaxFilterInstructions
	}
	return &Coordinator{cfg: cfg, log: cfg.Logger, now: cfg.Now, idgen: cfg.IDGen, procs: map[string]Process{}}
}

func defaultCeilings(c Caps) Caps {
	if c.MaxDurationSec <= 0 {
		c.MaxDurationSec = DefaultCaps.MaxDurationSec
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = DefaultCaps.MaxBytes
	}
	if c.MaxPackets <= 0 {
		c.MaxPackets = DefaultCaps.MaxPackets
	}
	if c.RetentionHours <= 0 {
		c.RetentionHours = DefaultCaps.RetentionHours
	}
	return c
}

// clampCaps applies the un-overridable server ceilings: a requested value at
// or below the ceiling is honored; a zero (unspecified) or over-ceiling value
// is clamped to the ceiling. retentionHours is server-only, never requestable.
// This is the single arithmetic every capturing node runs — the coordinator
// AND every peer re-run it independently (StartLocalSpec), so no layer trusts
// another's clamp.
func (c *Coordinator) clampCaps(reqDur int, reqBytes, reqPackets int64) Caps {
	ceil := c.cfg.Ceilings
	out := Caps{RetentionHours: ceil.RetentionHours}

	out.MaxDurationSec = ceil.MaxDurationSec
	if reqDur > 0 && reqDur < ceil.MaxDurationSec {
		out.MaxDurationSec = reqDur
	}
	out.MaxBytes = ceil.MaxBytes
	if reqBytes > 0 && reqBytes < ceil.MaxBytes {
		out.MaxBytes = reqBytes
	}
	out.MaxPackets = ceil.MaxPackets
	if reqPackets > 0 && reqPackets < ceil.MaxPackets {
		out.MaxPackets = reqPackets
	}
	return out
}

func (c *Coordinator) filePath(sessionID string) string {
	return filepath.Join(c.cfg.Root, sessionID+".pcap")
}

func (c *Coordinator) isLocal(node string) bool {
	return node == c.cfg.LocalNode()
}

// Start begins a (possibly multi-point) capture. It validates the filter and
// resolves every target BEFORE invoking any capture process, so a rejected
// filter or unresolvable target starts nothing (T-1301 AC3). Returns the
// resulting Group.
func (c *Coordinator) Start(ctx context.Context, req StartRequest) (Group, error) {
	refs := make([]string, 0, 1+len(req.PeerTargets))
	if req.TargetRef != "" {
		refs = append(refs, req.TargetRef)
	}
	refs = append(refs, req.PeerTargets...)
	if len(refs) == 0 {
		return Group{}, ErrNoTargets
	}

	// (1) Filter validation — before any capture process, before any target
	// resolution side effect.
	if err := ValidateFilter(req.Filter, c.cfg.MaxFilterInstructions); err != nil {
		return Group{}, err
	}

	// (2) Resolve every target up front; abort the whole request if any is
	// unresolvable (never a partial start).
	targets := make([]Target, 0, len(refs))
	nodeSet := make([]string, 0, len(refs))
	for _, ref := range refs {
		t, err := c.cfg.Resolver.Resolve(ctx, ref)
		if err != nil {
			return Group{}, err
		}
		targets = append(targets, t)
		nodeSet = appendUnique(nodeSet, t.Node)
	}

	caps := c.clampCaps(req.DurationSec, req.MaxBytes, req.MaxPackets)
	groupID := c.idgen()
	startedAt := c.now().Unix()

	for _, t := range targets {
		sid := c.idgen()
		spec := Spec{
			SessionID: sid, GroupID: groupID, TargetRef: t.Ref, Node: t.Node,
			Iface: t.Iface, Filter: req.Filter, Caps: caps, FilePath: c.filePath(sid),
			StartedBy: req.StartedBy, StartedAt: startedAt, Nodes: nodeSet,
		}
		if err := c.startOne(ctx, spec); err != nil {
			// A per-target start failure is recorded on its own row (status
			// error) and audited; it does not unwind sibling sessions —
			// docs/api.md's partial-result convention, mirroring the cluster
			// fan-out envelope.
			c.log.Error("capture: starting session", "session", sid, "node", t.Node, "target", t.Ref, "error", err)
		}
	}
	return c.Get(ctx, groupID)
}

// startOne starts one session on its node (local Agent or remote peer),
// persists its row, and audits capture.start.
func (c *Coordinator) startOne(ctx context.Context, spec Spec) error {
	var (
		res Result
		err error
	)
	if c.isLocal(spec.Node) {
		res, err = c.startLocal(ctx, spec)
	} else {
		if c.cfg.Remote == nil {
			err = ErrNoRemote
		} else {
			res, err = c.cfg.Remote.Start(ctx, spec.Node, spec)
		}
	}

	s := sessionFromSpec(spec)
	if err != nil {
		s.Status = StatusError
		s.StoppedAt = c.now().Unix()
	} else {
		s.Status = res.Status
		s.Packets = res.Packets
		s.FileBytes = res.Bytes
	}
	if perr := c.cfg.Store.Upsert(ctx, s); perr != nil {
		c.log.Error("capture: persisting session row", "session", s.ID, "error", perr)
	}
	c.audit(ctx, "capture.start", spec, err)
	return err
}

// startLocal runs a node-local capture via the Agent and registers its live
// process. It does NOT persist the row (the caller does) so both the
// coordinator path (startOne) and the peer path (StartLocalSpec) share it.
func (c *Coordinator) startLocal(ctx context.Context, spec Spec) (Result, error) {
	if c.cfg.Agent == nil {
		return Result{Status: StatusError}, ErrNoAgent
	}
	if err := os.MkdirAll(filepath.Dir(spec.FilePath), 0o750); err != nil {
		return Result{Status: StatusError}, fmt.Errorf("capture: preparing capture dir: %w", err)
	}
	proc, err := c.cfg.Agent.Start(ctx, spec)
	if err != nil {
		return Result{Status: StatusError}, fmt.Errorf("capture: starting local capture: %w", err)
	}
	c.mu.Lock()
	c.procs[spec.SessionID] = proc
	c.mu.Unlock()
	return proc.Result(), nil
}

// StartLocalSpec is the peer-route entry point: a coordinating daemon asked
// this node to run one node-local capture. It re-validates the filter and
// re-clamps the caps against THIS node's own config (never trusting the
// caller's arithmetic — the un-overridable-cap guarantee holds independently
// per node), runs the capture, persists this node's own row (so this node's
// sweep owns the file), audits, and returns the initial Result.
func (c *Coordinator) StartLocalSpec(ctx context.Context, spec Spec) (Result, error) {
	if err := ValidateFilter(spec.Filter, c.cfg.MaxFilterInstructions); err != nil {
		return Result{Status: StatusError}, err
	}
	spec.Caps = c.clampCaps(spec.Caps.MaxDurationSec, spec.Caps.MaxBytes, spec.Caps.MaxPackets)
	// Never trust a caller-supplied file path or node: the file always lands
	// under THIS node's own [capture] root (bounded-file invariant,
	// docs/security.md), and the row is always tagged with THIS node so this
	// node's retention sweep owns it. An HMAC-authenticated peer request that
	// carried an absolute/traversal FilePath or a foreign Node would otherwise
	// escape both guarantees — the un-overridable-cap discipline applied to
	// path and ownership, not just the numeric caps.
	spec.FilePath = c.filePath(spec.SessionID)
	spec.Node = c.cfg.LocalNode()
	res, err := c.startLocal(ctx, spec)

	s := sessionFromSpec(spec)
	if err != nil {
		s.Status = StatusError
		s.StoppedAt = c.now().Unix()
	} else {
		s.Status = res.Status
		s.Packets = res.Packets
		s.FileBytes = res.Bytes
	}
	if perr := c.cfg.Store.Upsert(ctx, s); perr != nil {
		c.log.Error("capture: persisting peer-local session row", "session", s.ID, "error", perr)
	}
	c.audit(ctx, "capture.start", spec, err)
	if err != nil {
		return Result{Status: StatusError}, err
	}
	return res, nil
}

// StopLocal stops one node-local capture by id (peer-route entry point, and
// the local branch of StopGroup). Returns the final Result.
func (c *Coordinator) StopLocal(ctx context.Context, sessionID string) (Result, error) {
	s, err := c.cfg.Store.Get(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	res := c.stopLocalProc(ctx, sessionID)
	c.applyStop(&s, res)
	if perr := c.cfg.Store.Upsert(ctx, s); perr != nil {
		c.log.Error("capture: persisting stopped session row", "session", sessionID, "error", perr)
	}
	c.audit(ctx, "capture.stop", specFromSession(s), nil)
	return res, nil
}

// stopLocalProc stops the live process for sessionID if present, returning
// its final Result; if no live process exists (e.g. after a restart) it
// returns a synthetic stopped Result so the row can still be finalized.
func (c *Coordinator) stopLocalProc(ctx context.Context, sessionID string) Result {
	c.mu.Lock()
	proc, ok := c.procs[sessionID]
	if ok {
		delete(c.procs, sessionID)
	}
	c.mu.Unlock()
	if !ok {
		return Result{Status: StatusStopped}
	}
	res := proc.Stop(ctx)
	if res.Status == StatusRunning {
		res.Status = StatusStopped
	}
	return res
}

// StatusLocal returns the live accounting for one node-local capture (peer-
// route entry point), reconciling the row from its live process.
func (c *Coordinator) StatusLocal(ctx context.Context, sessionID string) (Result, error) {
	s, err := c.cfg.Store.Get(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	s = c.syncLocal(ctx, s)
	return Result{Packets: s.Packets, Bytes: s.FileBytes, Status: s.Status}, nil
}

// Download returns the raw pcap bytes for one session (never a whole
// group — a multi-point capture has one file per node, so a caller
// downloads each session it wants individually). A local session's file is
// read directly from disk under [capture] root; a session on a peer node is
// proxied via RemoteCapturer — the same cluster-aware contract every other
// host-state read in this codebase honors (CLAUDE.md's "Everything is
// cluster-aware" rule), never a local-only shortcut. A purged
// (retention-expired) or never-written session's file is
// ErrFileUnavailable, not a generic ErrNotFound, so the caller can render an
// accurate reason.
func (c *Coordinator) Download(ctx context.Context, sessionID string) ([]byte, Session, error) {
	s, err := c.cfg.Store.Get(ctx, sessionID)
	if err != nil {
		return nil, Session{}, err
	}
	if s.Status == StatusPurged {
		return nil, s, ErrFileUnavailable
	}
	if c.isLocal(s.Node) {
		if s.FilePath == "" {
			return nil, s, ErrFileUnavailable
		}
		data, rerr := os.ReadFile(s.FilePath)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				return nil, s, ErrFileUnavailable
			}
			return nil, s, fmt.Errorf("capture: reading capture file for session %s: %w", sessionID, rerr)
		}
		return data, s, nil
	}
	if c.cfg.Remote == nil {
		return nil, s, ErrNoRemote
	}
	data, rerr := c.cfg.Remote.Download(ctx, s.Node, s.ID)
	if rerr != nil {
		return nil, s, fmt.Errorf("capture: downloading session %s from node %s: %w", sessionID, s.Node, rerr)
	}
	return data, s, nil
}

// StopGroup stops every non-terminal session in a group (local via the live
// process, remote via the peer). It audits each stop. Returns the resulting
// Group.
func (c *Coordinator) StopGroup(ctx context.Context, groupID, actor string) (Group, error) {
	sessions, err := c.cfg.Store.ByGroup(ctx, groupID)
	if err != nil {
		return Group{}, err
	}
	if len(sessions) == 0 {
		return Group{}, ErrNotFound
	}
	for i := range sessions {
		s := &sessions[i]
		if s.Status.Terminal() {
			continue
		}
		var res Result
		if c.isLocal(s.Node) {
			res = c.stopLocalProc(ctx, s.ID)
		} else if c.cfg.Remote != nil {
			r, rerr := c.cfg.Remote.Stop(ctx, s.Node, s.ID)
			if rerr != nil {
				c.log.Error("capture: stopping remote session", "session", s.ID, "node", s.Node, "error", rerr)
				res = Result{Packets: s.Packets, Bytes: s.FileBytes, Status: StatusStopped}
			} else {
				res = r
				if res.Status == StatusRunning {
					res.Status = StatusStopped
				}
			}
		} else {
			res = Result{Packets: s.Packets, Bytes: s.FileBytes, Status: StatusStopped}
		}
		spec := specFromSession(*s)
		spec.StartedBy = actor
		c.applyStop(s, res)
		if perr := c.cfg.Store.Upsert(ctx, *s); perr != nil {
			c.log.Error("capture: persisting stopped session row", "session", s.ID, "error", perr)
		}
		c.audit(ctx, "capture.stop", spec, nil)
	}
	return c.Get(ctx, groupID)
}

func (c *Coordinator) applyStop(s *Session, res Result) {
	s.Packets = res.Packets
	s.FileBytes = res.Bytes
	s.Status = res.Status
	if s.Status == StatusRunning {
		s.Status = StatusStopped
	}
	s.StoppedAt = c.now().Unix()
}

// Get returns one group by id, reconciling each member's live accounting
// (local processes, and best-effort remote status polls).
func (c *Coordinator) Get(ctx context.Context, groupID string) (Group, error) {
	sessions, err := c.cfg.Store.ByGroup(ctx, groupID)
	if err != nil {
		return Group{}, err
	}
	if len(sessions) == 0 {
		return Group{}, ErrNotFound
	}
	for i := range sessions {
		sessions[i] = c.sync(ctx, sessions[i])
	}
	return toGroup(groupID, sessions), nil
}

// List returns every capture group, newest first.
func (c *Coordinator) List(ctx context.Context) ([]Group, error) {
	all, err := c.cfg.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	byGroup := map[string][]Session{}
	order := []string{}
	for _, s := range all {
		s = c.sync(ctx, s)
		if _, ok := byGroup[s.GroupID]; !ok {
			order = append(order, s.GroupID)
		}
		byGroup[s.GroupID] = append(byGroup[s.GroupID], s)
	}
	groups := make([]Group, 0, len(order))
	for _, gid := range order {
		groups = append(groups, toGroup(gid, byGroup[gid]))
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].StartedAt > groups[j].StartedAt })
	return groups, nil
}

// sync reconciles one session's persisted row against live state: a local
// session against its process, a remote session against a best-effort peer
// status poll. It persists any change so a later read (or a restarted
// daemon) sees the reconciled value.
func (c *Coordinator) sync(ctx context.Context, s Session) Session {
	if c.isLocal(s.Node) {
		return c.syncLocal(ctx, s)
	}
	if s.Status.Terminal() || c.cfg.Remote == nil {
		return s
	}
	res, err := c.cfg.Remote.Status(ctx, s.Node, s.ID)
	if err != nil {
		return s // best-effort: keep the stored value
	}
	return c.applyLive(ctx, s, res)
}

func (c *Coordinator) syncLocal(ctx context.Context, s Session) Session {
	c.mu.Lock()
	proc, ok := c.procs[s.ID]
	c.mu.Unlock()
	if !ok {
		return s
	}
	res := proc.Result()
	s = c.applyLive(ctx, s, res)
	if res.Status.Terminal() {
		// The capture stopped itself on a cap; finalize the row and drop the
		// live process handle.
		c.mu.Lock()
		delete(c.procs, s.ID)
		c.mu.Unlock()
	}
	return s
}

func (c *Coordinator) applyLive(ctx context.Context, s Session, res Result) Session {
	changed := s.Packets != res.Packets || s.FileBytes != res.Bytes || s.Status != res.Status
	s.Packets = res.Packets
	s.FileBytes = res.Bytes
	if res.Status != StatusRunning {
		s.Status = res.Status
		if s.StoppedAt == 0 {
			s.StoppedAt = c.now().Unix()
		}
	}
	if changed {
		if err := c.cfg.Store.Upsert(ctx, s); err != nil {
			c.log.Error("capture: persisting reconciled session row", "session", s.ID, "error", err)
		}
	}
	return s
}

// Sweep deletes the on-disk file of every local session whose age has passed
// its retention window (its Caps.RetentionHours from StartedAt), marking the
// row purged. Using StartedAt as the age reference is deliberate: it purges
// a file orphaned by a daemon restart mid-capture (a row still marked
// running, its live process long gone) exactly once its age cap passes —
// T-1301 AC5. Remote sessions' files are the peer's own sweep's
// responsibility and are never touched here.
func (c *Coordinator) Sweep(ctx context.Context) error {
	sessions, err := c.cfg.Store.List(ctx)
	if err != nil {
		return fmt.Errorf("capture: listing sessions for sweep: %w", err)
	}
	now := c.now().Unix()
	for _, s := range sessions {
		if !c.isLocal(s.Node) || s.Status == StatusPurged || s.FilePath == "" {
			continue
		}
		retentionSec := int64(s.Caps.RetentionHours) * 3600
		if retentionSec <= 0 {
			retentionSec = int64(c.cfg.Ceilings.RetentionHours) * 3600
		}
		if now-s.StartedAt < retentionSec {
			continue
		}
		// Stop any still-live process for a purged capture first, so we never
		// unlink a file being actively written.
		c.mu.Lock()
		if proc, ok := c.procs[s.ID]; ok {
			delete(c.procs, s.ID)
			c.mu.Unlock()
			proc.Stop(ctx)
		} else {
			c.mu.Unlock()
		}
		if rmErr := os.Remove(s.FilePath); rmErr != nil && !os.IsNotExist(rmErr) {
			c.log.Error("capture: purging expired capture file", "session", s.ID, "path", s.FilePath, "error", rmErr)
			continue
		}
		s.Status = StatusPurged
		if s.StoppedAt == 0 {
			s.StoppedAt = now
		}
		if err := c.cfg.Store.Upsert(ctx, s); err != nil {
			c.log.Error("capture: marking session purged", "session", s.ID, "error", err)
		}
		c.log.Info("capture: purged expired capture file", "session", s.ID, "path", s.FilePath)
	}
	return nil
}

// RunSweepLoop runs Sweep every interval until ctx is cancelled — the
// supervised, owned auto-purge goroutine (docs/development.md's "every
// goroutine has an owner and a shutdown path"). Primes immediately so a
// daemon restart purges long-orphaned files without waiting a full interval.
func (c *Coordinator) RunSweepLoop(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	if err := c.Sweep(ctx); err != nil {
		c.log.Error("capture: initial sweep", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.Sweep(ctx); err != nil {
				c.log.Error("capture: sweep", "error", err)
			}
		}
	}
}

// StopAll stops every live local process — the daemon-shutdown hook, so an
// in-flight capture's file is flushed rather than left mid-write. It does not
// audit (shutdown is not an operator stop).
func (c *Coordinator) StopAll(ctx context.Context) {
	c.mu.Lock()
	procs := make([]Process, 0, len(c.procs))
	for id, p := range c.procs {
		procs = append(procs, p)
		delete(c.procs, id)
	}
	c.mu.Unlock()
	for _, p := range procs {
		p.Stop(ctx)
	}
}

func (c *Coordinator) audit(ctx context.Context, action string, spec Spec, capErr error) {
	if c.cfg.Audit == nil {
		return
	}
	result := "ok"
	if capErr != nil {
		result = "error"
	}
	detail := map[string]any{
		"sessionId":      spec.SessionID,
		"groupId":        spec.GroupID,
		"node":           spec.Node,
		"iface":          spec.Iface,
		"filter":         spec.Filter,
		"maxDurationSec": spec.Caps.MaxDurationSec,
		"maxBytes":       spec.Caps.MaxBytes,
		"maxPackets":     spec.Caps.MaxPackets,
		"retentionHours": spec.Caps.RetentionHours,
	}
	if capErr != nil {
		detail["error"] = capErr.Error()
	}
	if err := c.cfg.Audit.AppendCapture(ctx, AuditEvent{
		Actor: spec.StartedBy, Action: action, TargetRef: spec.TargetRef,
		Result: result, Detail: detail, At: c.now().Unix(),
	}); err != nil {
		c.log.Error("capture: appending audit row", "action", action, "session", spec.SessionID, "error", err)
	}
}

func sessionFromSpec(spec Spec) Session {
	return Session{
		ID: spec.SessionID, GroupID: spec.GroupID, TargetRef: spec.TargetRef,
		Node: spec.Node, Filter: spec.Filter, Caps: spec.Caps, Status: StatusRunning,
		StartedBy: spec.StartedBy, StartedAt: spec.StartedAt, FilePath: spec.FilePath,
		Nodes: spec.Nodes,
	}
}

func specFromSession(s Session) Spec {
	return Spec{
		SessionID: s.ID, GroupID: s.GroupID, TargetRef: s.TargetRef, Node: s.Node,
		Filter: s.Filter, Caps: s.Caps, FilePath: s.FilePath, StartedBy: s.StartedBy,
		StartedAt: s.StartedAt, Nodes: s.Nodes,
	}
}

func toGroup(groupID string, sessions []Session) Group {
	g := Group{ID: groupID, Sessions: sessions, Status: groupStatus(sessions)}
	if len(sessions) > 0 {
		g.StartedBy = sessions[0].StartedBy
		g.StartedAt = sessions[0].StartedAt
		g.Caps = sessions[0].Caps
	}
	return g
}

func appendUnique(s []string, v string) []string {
	for _, e := range s {
		if e == v {
			return s
		}
	}
	return append(s, v)
}
