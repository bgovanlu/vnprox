// SPDX-License-Identifier: Apache-2.0

package capture_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/capture"
	"github.com/bgovanlu/vnprox/internal/capturemock"
)

// --- fakes ------------------------------------------------------------------

type fakeStore struct {
	rows map[string]capture.Session
	mu   sync.Mutex
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]capture.Session{}} }

func (s *fakeStore) Upsert(_ context.Context, sess capture.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[sess.ID] = sess
	return nil
}
func (s *fakeStore) Get(_ context.Context, id string) (capture.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return capture.Session{}, capture.ErrNotFound
	}
	return r, nil
}
func (s *fakeStore) ByGroup(_ context.Context, groupID string) ([]capture.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []capture.Session
	for _, r := range s.rows {
		if r.GroupID == groupID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *fakeStore) ListGroups(_ context.Context) ([]string, error) { return nil, nil }
func (s *fakeStore) List(_ context.Context) ([]capture.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []capture.Session
	for _, r := range s.rows {
		out = append(out, r)
	}
	return out, nil
}
func (s *fakeStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	return nil
}

// countingAgent delegates to the scripted capturemock agent but counts how
// many times Start is invoked — the AC3 "zero capture-process calls in the
// negative case" assertion.
type countingAgent struct {
	inner  capture.Agent
	mu     sync.Mutex
	starts int
}

func (a *countingAgent) Start(ctx context.Context, spec capture.Spec) (capture.Process, error) {
	a.mu.Lock()
	a.starts++
	a.mu.Unlock()
	return a.inner.Start(ctx, spec)
}
func (a *countingAgent) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.starts
}

type mapResolver map[string]capture.Target

func (m mapResolver) Resolve(_ context.Context, ref string) (capture.Target, error) {
	t, ok := m[ref]
	if !ok {
		return capture.Target{}, capture.ErrUnresolvableTarget
	}
	return t, nil
}

type fakeAuditor struct {
	events []capture.AuditEvent
	mu     sync.Mutex
}

func (a *fakeAuditor) AppendCapture(_ context.Context, e capture.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}
func (a *fakeAuditor) byAction(action string) []capture.AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []capture.AuditEvent
	for _, e := range a.events {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

type fakeRemote struct {
	started      []string
	stopped      []string
	downloaded   []string
	downloadErr  error
	downloadData []byte
	mu           sync.Mutex
}

func (r *fakeRemote) Start(_ context.Context, node string, _ capture.Spec) (capture.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, node)
	return capture.Result{Status: capture.StatusRunning}, nil
}
func (r *fakeRemote) Stop(_ context.Context, node, _ string) (capture.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = append(r.stopped, node)
	return capture.Result{Status: capture.StatusStopped}, nil
}
func (r *fakeRemote) Status(_ context.Context, _ string, _ string) (capture.Result, error) {
	return capture.Result{Status: capture.StatusRunning}, nil
}
func (r *fakeRemote) Download(_ context.Context, node, sessionID string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.downloaded = append(r.downloaded, node+"/"+sessionID)
	if r.downloadErr != nil {
		return nil, r.downloadErr
	}
	return r.downloadData, nil
}

// --- harness ----------------------------------------------------------------

type harness struct {
	coord   *capture.Coordinator
	store   *fakeStore
	agent   *countingAgent
	audit   *fakeAuditor
	remote  *fakeRemote
	nowUnix *int64
	root    string
}

func newHarness(t *testing.T, ceilings capture.Caps, resolver capture.TargetResolver) *harness {
	t.Helper()
	root := t.TempDir()
	st := newFakeStore()
	ag := &countingAgent{inner: capturemock.NewAgent()}
	au := &fakeAuditor{}
	rm := &fakeRemote{}
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC).Unix()
	nowPtr := &now
	coord := capture.New(capture.Config{
		Ceilings:  ceilings,
		Root:      root,
		Agent:     ag,
		Remote:    rm,
		Resolver:  resolver,
		Store:     st,
		Audit:     au,
		LocalNode: func() string { return "pve1" },
		Now:       func() time.Time { return time.Unix(*nowPtr, 0).UTC() },
	})
	return &harness{coord: coord, store: st, agent: ag, audit: au, remote: rm, nowUnix: nowPtr, root: root}
}

var bridgeRefs = mapResolver{
	"bridge:pve1:vmbr0": {Ref: "bridge:pve1:vmbr0", Node: "pve1", Iface: "vmbr0"},
	"bridge:pve2:vmbr0": {Ref: "bridge:pve2:vmbr0", Node: "pve2", Iface: "vmbr0"},
}

// --- Peer entry point re-derives path & node (review-T-1301 MAJOR-1) -------

// A peer-routed StartLocalSpec must never honor a caller-supplied FilePath or
// Node: the .pcap always lands under THIS node's own [capture] root and the
// row is tagged with THIS node, so an HMAC-authenticated peer can't steer the
// write to a traversal path or make another node's sweep own (and delete) the
// file.
func TestStartLocalSpecIgnoresCallerFilePathAndNode(t *testing.T) {
	ceil := capture.Caps{MaxDurationSec: 60, MaxBytes: 1 << 20, MaxPackets: 1 << 20, RetentionHours: 24}
	h := newHarness(t, ceil, bridgeRefs)

	_, err := h.coord.StartLocalSpec(context.Background(), capture.Spec{
		SessionID: "evil1", GroupID: "g1", TargetRef: "bridge:pve1:vmbr0",
		Node:  "attacker-controlled",
		Iface: "vmbr0", Filter: "tcp", StartedBy: "root@pam",
		FilePath: "/etc/vnprox/keys/session.key", // traversal/overwrite attempt
		Caps:     ceil,
	})
	if err != nil {
		t.Fatalf("StartLocalSpec: %v", err)
	}
	row, err := h.store.Get(context.Background(), "evil1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Node != "pve1" {
		t.Errorf("row.Node = %q, want the local node pve1 (caller value must be ignored)", row.Node)
	}
	if got := row.FilePath; got == "/etc/vnprox/keys/session.key" || filepath.Dir(got) != h.root {
		t.Errorf("row.FilePath = %q, want a file under the local capture root %q (caller path must be ignored)", got, h.root)
	}
}

// --- AC2: server-side, un-overridable caps ---------------------------------

func TestCapsClampedToConfigServerSide(t *testing.T) {
	ceil := capture.Caps{MaxDurationSec: 60, MaxBytes: 4096, MaxPackets: 5, RetentionHours: 24}
	h := newHarness(t, ceil, bridgeRefs)

	// A request asking for far more than every ceiling.
	g, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "bridge:pve1:vmbr0", Filter: "tcp",
		DurationSec: 999999, MaxBytes: 1 << 40, MaxPackets: 1 << 30, StartedBy: "root@pam",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(g.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(g.Sessions))
	}
	c := g.Sessions[0].Caps
	if c.MaxDurationSec > ceil.MaxDurationSec || c.MaxBytes > ceil.MaxBytes || c.MaxPackets > ceil.MaxPackets {
		t.Errorf("effective caps %+v exceed ceiling %+v", c, ceil)
	}
	if c.MaxDurationSec != ceil.MaxDurationSec || c.MaxBytes != ceil.MaxBytes || c.MaxPackets != ceil.MaxPackets {
		t.Errorf("over-ceiling request should clamp to exactly the ceiling; got %+v want %+v", c, ceil)
	}
	if c.RetentionHours != ceil.RetentionHours {
		t.Errorf("retentionHours = %d, want config %d (never requestable)", c.RetentionHours, ceil.RetentionHours)
	}
}

func TestCapsHonorLowerRequestButNeverHigher(t *testing.T) {
	ceil := capture.Caps{MaxDurationSec: 60, MaxBytes: 4096, MaxPackets: 100, RetentionHours: 24}
	h := newHarness(t, ceil, bridgeRefs)
	g, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "bridge:pve1:vmbr0", DurationSec: 10, MaxBytes: 1 << 40, MaxPackets: 3, StartedBy: "u",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	c := g.Sessions[0].Caps
	if c.MaxDurationSec != 10 { // below ceiling: honored
		t.Errorf("MaxDurationSec = %d, want 10 (below-ceiling request honored)", c.MaxDurationSec)
	}
	if c.MaxPackets != 3 {
		t.Errorf("MaxPackets = %d, want 3 (below-ceiling request honored)", c.MaxPackets)
	}
	if c.MaxBytes != ceil.MaxBytes { // above ceiling: clamped
		t.Errorf("MaxBytes = %d, want clamp to ceiling %d", c.MaxBytes, ceil.MaxBytes)
	}
}

// --- AC3: filter validation before any capture process ---------------------

func TestUnsafeFilterRejectedBeforeAnyCaptureProcess(t *testing.T) {
	ceil := capture.DefaultCaps
	cases := []struct {
		name   string
		filter string
	}{
		{"shellInjection", "tcp; rm -rf /"},
		{"pipe", "tcp || cat /etc/passwd"},
		{"unknownToken", "tcp and evilkeyword"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, ceil, bridgeRefs)
			_, err := h.coord.Start(context.Background(), capture.StartRequest{
				TargetRef: "bridge:pve1:vmbr0", Filter: tc.filter, StartedBy: "u",
			})
			if err == nil {
				t.Fatalf("expected rejection for filter %q", tc.filter)
			}
			if h.agent.count() != 0 {
				t.Errorf("capture agent was invoked %d times; want 0 for a rejected filter", h.agent.count())
			}
		})
	}
}

func TestOversizedFilterRejectedBeforeAnyCaptureProcess(t *testing.T) {
	h := newHarness(t, capture.DefaultCaps, bridgeRefs)
	// Far more primitives than DefaultMaxFilterInstructions.
	oversized := "host 1.1.1.1" + strings.Repeat(" or host 1.1.1.1", 80)
	_, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "bridge:pve1:vmbr0", Filter: oversized, StartedBy: "u",
	})
	if err == nil {
		t.Fatal("expected oversized filter to be rejected")
	}
	if h.agent.count() != 0 {
		t.Errorf("capture agent invoked %d times; want 0 for an oversized filter", h.agent.count())
	}
}

func TestUnresolvableTargetRejectedBeforeAnyCaptureProcess(t *testing.T) {
	h := newHarness(t, capture.DefaultCaps, bridgeRefs)
	_, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "guest-nic:pve1:100/net0", Filter: "tcp", StartedBy: "u",
	})
	if err == nil {
		t.Fatal("expected unresolvable target to be rejected")
	}
	if h.agent.count() != 0 {
		t.Errorf("capture agent invoked %d times; want 0 for an unresolvable target", h.agent.count())
	}
}

// --- AC4: multi-point coordination -----------------------------------------

func TestMultiPointCorrelatedSessionsAndGroupStop(t *testing.T) {
	h := newHarness(t, capture.DefaultCaps, bridgeRefs)
	g, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef:   "bridge:pve1:vmbr0",
		PeerTargets: []string{"bridge:pve2:vmbr0"},
		Filter:      "tcp port 443", StartedBy: "root@pam",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(g.Sessions) != 2 {
		t.Fatalf("want 2 correlated sessions, got %d", len(g.Sessions))
	}
	rows, _ := h.store.ByGroup(context.Background(), g.ID)
	if len(rows) != 2 {
		t.Fatalf("want 2 capture_sessions rows sharing group id, got %d", len(rows))
	}
	nodes := map[string]bool{}
	for _, r := range rows {
		if r.GroupID != g.ID {
			t.Errorf("row %s group_id = %q, want %q", r.ID, r.GroupID, g.ID)
		}
		nodes[r.Node] = true
	}
	if !nodes["pve1"] || !nodes["pve2"] {
		t.Errorf("expected sessions on both pve1 and pve2, got %v", nodes)
	}
	if len(h.remote.started) != 1 || h.remote.started[0] != "pve2" {
		t.Errorf("remote start calls = %v, want [pve2]", h.remote.started)
	}

	// Stopping the group stops both members.
	stopped, err := h.coord.StopGroup(context.Background(), g.ID, "root@pam")
	if err != nil {
		t.Fatalf("StopGroup: %v", err)
	}
	for _, s := range stopped.Sessions {
		if !s.Status.Terminal() {
			t.Errorf("session %s on %s not terminal after group stop: %s", s.ID, s.Node, s.Status)
		}
	}
	if len(h.remote.stopped) != 1 || h.remote.stopped[0] != "pve2" {
		t.Errorf("remote stop calls = %v, want [pve2]", h.remote.stopped)
	}
}

// --- AC5: auto-purge sweep --------------------------------------------------

func TestSweepPurgesFilePastRetention(t *testing.T) {
	ceil := capture.Caps{MaxDurationSec: 60, MaxBytes: 1 << 20, MaxPackets: 1 << 20, RetentionHours: 1}
	h := newHarness(t, ceil, bridgeRefs)
	g, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "bridge:pve1:vmbr0", Filter: "tcp", StartedBy: "u",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	row := g.Sessions[0]
	if _, statErr := os.Stat(row.FilePath); statErr != nil {
		t.Fatalf("capture file should exist after start: %v", statErr)
	}

	// Not yet past retention: sweep is a no-op.
	if err := h.coord.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, statErr := os.Stat(row.FilePath); statErr != nil {
		t.Fatalf("file purged before retention elapsed: %v", statErr)
	}

	// Advance the injected clock past retention_hours and sweep.
	*h.nowUnix += int64(2 * 3600)
	if err := h.coord.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, statErr := os.Stat(row.FilePath); !os.IsNotExist(statErr) {
		t.Fatalf("file should be purged past retention, stat err = %v", statErr)
	}
	after, _ := h.store.Get(context.Background(), row.ID)
	if after.Status != capture.StatusPurged {
		t.Errorf("row status = %s, want purged", after.Status)
	}
}

func TestSweepPurgesOrphanFileAfterRestartMidCapture(t *testing.T) {
	ceil := capture.Caps{MaxDurationSec: 60, MaxBytes: 1 << 20, MaxPackets: 1 << 20, RetentionHours: 1}
	h := newHarness(t, ceil, bridgeRefs)
	g, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "bridge:pve1:vmbr0", Filter: "tcp", StartedBy: "u",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	row := g.Sessions[0]

	// Simulate a daemon restart mid-capture: a brand-new coordinator over the
	// same store + root, with no live process registered — the file is
	// orphaned. Its own sweep must still purge it once the age cap passes.
	now2 := *h.nowUnix + int64(2*3600)
	restarted := capture.New(capture.Config{
		Ceilings: ceil, Root: h.root, Agent: h.agent, Resolver: bridgeRefs,
		Store: h.store, Audit: h.audit, LocalNode: func() string { return "pve1" },
		Now: func() time.Time { return time.Unix(now2, 0).UTC() },
	})
	if _, statErr := os.Stat(row.FilePath); statErr != nil {
		t.Fatalf("orphan file should still exist before sweep: %v", statErr)
	}
	if err := restarted.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep after restart: %v", err)
	}
	if _, statErr := os.Stat(row.FilePath); !os.IsNotExist(statErr) {
		t.Fatalf("orphan file should be purged after restart+age, stat err = %v", statErr)
	}
}

// --- AC6: audit on every start/stop ----------------------------------------

func TestAuditOnStartAndStop(t *testing.T) {
	h := newHarness(t, capture.DefaultCaps, bridgeRefs)
	g, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "bridge:pve1:vmbr0", Filter: "udp port 53", StartedBy: "root@pam",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	starts := h.audit.byAction("capture.start")
	if len(starts) != 1 {
		t.Fatalf("want exactly 1 capture.start audit row, got %d", len(starts))
	}
	se := starts[0]
	if se.Actor != "root@pam" || se.TargetRef != "bridge:pve1:vmbr0" {
		t.Errorf("start audit actor/target = %q/%q", se.Actor, se.TargetRef)
	}
	if se.Detail["filter"] != "udp port 53" {
		t.Errorf("start audit detail filter = %v, want %q", se.Detail["filter"], "udp port 53")
	}
	for _, k := range []string{"maxDurationSec", "maxBytes", "maxPackets", "retentionHours"} {
		if _, ok := se.Detail[k]; !ok {
			t.Errorf("start audit detail missing effective cap %q", k)
		}
	}

	if _, err := h.coord.StopGroup(context.Background(), g.ID, "root@pam"); err != nil {
		t.Fatalf("StopGroup: %v", err)
	}
	stops := h.audit.byAction("capture.stop")
	if len(stops) != 1 {
		t.Fatalf("want exactly 1 capture.stop audit row, got %d", len(stops))
	}
}

// --- AC7: payload bytes never persist beyond the capture file --------------

func TestNoPayloadBytesPersistedOutsideCaptureFile(t *testing.T) {
	h := newHarness(t, capture.DefaultCaps, bridgeRefs)
	g, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "bridge:pve1:vmbr0", Filter: "udp", StartedBy: "u",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The scripted frames embed these payload markers (frames.go). They must
	// appear ONLY in the .pcap file, never in a persisted session row.
	markers := []string{"vnprox-udp", "vnprox"}

	fileBytes, err := os.ReadFile(g.Sessions[0].FilePath)
	if err != nil {
		t.Fatalf("reading capture file: %v", err)
	}
	if !strings.Contains(string(fileBytes), "vnprox-udp") {
		t.Fatal("sanity: expected payload marker in the capture file")
	}

	all, _ := h.store.List(context.Background())
	for _, s := range all {
		blob := s.TargetRef + s.Filter + s.StartedBy + string(s.Status) + s.GroupID + strings.Join(s.Nodes, ",")
		for _, m := range markers {
			if strings.Contains(blob, m) {
				t.Errorf("payload marker %q leaked into persisted session row %s", m, s.ID)
			}
		}
	}
	for _, e := range h.audit.byAction("capture.start") {
		if strings.Contains(e.Detail["filter"].(string), "vnprox-udp") {
			t.Error("payload marker leaked into audit detail")
		}
	}
}

// --- T-1302: per-session pcap download --------------------------------------

// A local session's Download reads the exact bytes on disk.
func TestDownloadLocalSessionReadsFile(t *testing.T) {
	h := newHarness(t, capture.DefaultCaps, bridgeRefs)
	g, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "bridge:pve1:vmbr0", Filter: "udp", StartedBy: "u",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want, err := os.ReadFile(g.Sessions[0].FilePath)
	if err != nil {
		t.Fatalf("reading capture file directly: %v", err)
	}
	got, sess, err := h.coord.Download(context.Background(), g.Sessions[0].ID)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Download returned %d bytes, want the %d on-disk bytes", len(got), len(want))
	}
	if sess.ID != g.Sessions[0].ID {
		t.Errorf("Download session.ID = %q, want %q", sess.ID, g.Sessions[0].ID)
	}
	if len(h.remote.downloaded) != 0 {
		t.Errorf("a local download must never call RemoteCapturer.Download, got %v", h.remote.downloaded)
	}
}

// A session on a peer node is proxied via RemoteCapturer.Download — the
// cluster-aware contract (CLAUDE.md: "any feature that reads or writes node
// state must work when that node is a peer, not just localhost").
func TestDownloadRemoteSessionProxiesViaRemoteCapturer(t *testing.T) {
	h := newHarness(t, capture.DefaultCaps, bridgeRefs)
	h.remote.downloadData = []byte("remote pcap bytes")
	g, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "bridge:pve2:vmbr0", Filter: "udp", StartedBy: "u",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, sess, err := h.coord.Download(context.Background(), g.Sessions[0].ID)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != "remote pcap bytes" {
		t.Errorf("Download = %q, want the remote-proxied bytes", got)
	}
	if sess.Node != "pve2" {
		t.Errorf("session.Node = %q, want pve2", sess.Node)
	}
	if len(h.remote.downloaded) != 1 || h.remote.downloaded[0] != "pve2/"+g.Sessions[0].ID {
		t.Errorf("RemoteCapturer.Download not called as expected: %v", h.remote.downloaded)
	}
}

// A purged session's file is gone — Download reports ErrFileUnavailable, a
// distinct reason from "session unknown".
func TestDownloadPurgedSessionReturnsFileUnavailable(t *testing.T) {
	ceil := capture.Caps{MaxDurationSec: 60, MaxBytes: 1 << 20, MaxPackets: 1 << 20, RetentionHours: 1}
	h := newHarness(t, ceil, bridgeRefs)
	g, err := h.coord.Start(context.Background(), capture.StartRequest{
		TargetRef: "bridge:pve1:vmbr0", Filter: "tcp", StartedBy: "u",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	*h.nowUnix += int64(2 * 3600)
	if sweepErr := h.coord.Sweep(context.Background()); sweepErr != nil {
		t.Fatalf("Sweep: %v", sweepErr)
	}
	_, _, err = h.coord.Download(context.Background(), g.Sessions[0].ID)
	if !errors.Is(err, capture.ErrFileUnavailable) {
		t.Errorf("Download error = %v, want ErrFileUnavailable", err)
	}
}

// An unknown session id is ErrNotFound (distinct from ErrFileUnavailable).
func TestDownloadUnknownSessionReturnsNotFound(t *testing.T) {
	h := newHarness(t, capture.DefaultCaps, bridgeRefs)
	_, _, err := h.coord.Download(context.Background(), "no-such-session")
	if !errors.Is(err, capture.ErrNotFound) {
		t.Errorf("Download error = %v, want ErrNotFound", err)
	}
}
