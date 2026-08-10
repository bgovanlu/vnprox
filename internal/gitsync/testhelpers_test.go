package gitsync_test

// Shared doubles and fixtures for T-2701's acceptance tests.
//
// The two that matter most:
//
//   - fakeStager is the change-engine double. It satisfies
//     gitsync.ChangesetStager (compile-time assertion below) AND carries an
//     Apply method the interface does not have — that method exists only so
//     the "apply was never called" assertions have a control leg proving the
//     counter they read actually moves (T-2701 AC1). An assertion on a
//     counter nothing can increment proves nothing.
//   - buildFixtureGraph builds a real inventory.Graph from a pvemock cluster
//     fixture through one full collect cycle, the same helper internal/spec's
//     own tests use, so spec.Import diffs against a realistic snapshot rather
//     than a hand-built one.

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/spec"
	"github.com/bgovanlu/vnprox/internal/store"
)

const (
	fixtureThreeNode  = "../../testdata/clusters/three-node-vlan.yaml"
	fixtureSingleNode = "../../testdata/clusters/single-node.yaml"
)

// --- change-engine double --------------------------------------------------

// fakeStager records every change-engine call gitsync makes.
//
//nolint:govet // fieldalignment: test double; the call counters are grouped together deliberately.
type fakeStager struct {
	mu          sync.Mutex
	changesets  map[string]change.Changeset
	order       []string
	createCalls int
	updateCalls int
	listCalls   int
	applyCalls  int
	nextID      int
	createErr   error
	updateErr   error
	listErr     error
}

// The seam gitsync holds is exactly this and no more.
var _ gitsync.ChangesetStager = (*fakeStager)(nil)

func newFakeStager() *fakeStager {
	return &fakeStager{changesets: map[string]change.Changeset{}}
}

func (f *fakeStager) CreateWithOrigin(_ context.Context, author, title string, ops []change.Op, origin, originTokenID string) (change.Changeset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.createErr != nil {
		return change.Changeset{}, f.createErr
	}
	f.nextID++
	id := "cs-" + string(rune('a'+f.nextID-1))
	c := change.Changeset{
		ID: id, Title: title, Author: author, Status: change.StatusDraft,
		Origin: origin, OriginTokenID: originTokenID, Ops: ops,
		CreatedAt: int64(f.nextID), UpdatedAt: int64(f.nextID),
	}
	f.changesets[id] = c
	f.order = append(f.order, id)
	return c, nil
}

func (f *fakeStager) UpdateDraft(_ context.Context, id, author string, title *string, ops []change.Op) (change.Changeset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	if f.updateErr != nil {
		return change.Changeset{}, f.updateErr
	}
	c, ok := f.changesets[id]
	if !ok {
		return change.Changeset{}, store.ErrNotFound
	}
	c.Ops = ops
	c.Author = author
	if title != nil {
		c.Title = *title
	}
	f.changesets[id] = c
	return c, nil
}

func (f *fakeStager) List(_ context.Context, status string) ([]change.Changeset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]change.Changeset, 0, len(f.order))
	for _, id := range f.order {
		c := f.changesets[id]
		if status == "" || string(c.Status) == status {
			out = append(out, c)
		}
	}
	return out, nil
}

// Apply is deliberately NOT part of gitsync.ChangesetStager. It is the
// control leg for AC1: a test calls it directly to prove applyCalls is a
// counter that moves, so "applyCalls == 0" after a sync is evidence rather
// than tautology.
func (f *fakeStager) Apply(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applyCalls++
	return nil
}

func (f *fakeStager) counts() (create, update, list, apply int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls, f.updateCalls, f.listCalls, f.applyCalls
}

// openSyncCount is AC3's assertion primitive: how many editable
// gitsync-originated changesets exist right now.
func (f *fakeStager) openSyncCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.changesets {
		if c.Origin == change.OriginGitSync && c.Editable() {
			n++
		}
	}
	return n
}

func (f *fakeStager) get(id string) change.Changeset {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.changesets[id]
}

// --- source double ---------------------------------------------------------

// fakeSource serves a scripted sequence of revisions (or errors), counting
// fetches so a retry can be asserted.
//
//nolint:govet // fieldalignment: test double; fields are grouped by role, not packed.
type fakeSource struct {
	mu      sync.Mutex
	rev     gitsync.Revision
	err     error
	fetches int
	// describe is what Describe() returns; tests that assert on
	// credential leakage set it to something credential-free on purpose.
	describe string
}

func (s *fakeSource) Describe() string {
	if s.describe == "" {
		return "https://git.example.test/org/infra (fake)"
	}
	return s.describe
}

func (s *fakeSource) Fetch(_ context.Context, _, path string) (gitsync.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetches++
	if s.err != nil {
		return gitsync.Revision{}, s.err
	}
	rev := s.rev
	rev.Path = path
	return rev, nil
}

func (s *fakeSource) set(sha string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rev = gitsync.Revision{SHA: sha, Content: content}
}

func (s *fakeSource) setSigned(sha string, content []byte, sig *gitsync.CommitSignature) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rev = gitsync.Revision{SHA: sha, Content: content, Signature: sig}
}

func (s *fakeSource) fetchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetches
}

// --- audit double ----------------------------------------------------------

//nolint:govet // fieldalignment: test double; mutex first, then what it guards.
type fakeAudit struct {
	mu      sync.Mutex
	entries []store.AuditEntry
}

func (a *fakeAudit) Append(_ context.Context, e store.AuditEntry) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return int64(len(a.entries)), nil
}

func (a *fakeAudit) all() []store.AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]store.AuditEntry(nil), a.entries...)
}

// --- inventory -------------------------------------------------------------

func buildFixtureGraph(t *testing.T, path string) *inventory.Graph {
	t.Helper()
	f, err := pvemock.LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", path, err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	graph := inventory.NewGraph()
	c, err := collect.New(collect.Config{PVE: client, Host: host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)), Graph: graph})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := c.RefreshNow(ctx, inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}
	return graph
}

// specMatchingLive renders the live cluster as a spec document: importing it
// plans to zero ops, which is the "converged" baseline every divergence test
// starts from.
func specMatchingLive(t *testing.T, g *inventory.Graph) []byte {
	t.Helper()
	b, err := spec.Marshal(spec.Export(g.Snapshot()))
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}
	return b
}

// divergentSpec renders the live cluster with the first node's first bridge's
// MTU forced to mtu — one bridge.update op, and a knob a test can turn three
// times to produce three different plans (AC3).
func divergentSpec(t *testing.T, g *inventory.Graph, mtu int) []byte {
	t.Helper()
	s := spec.Export(g.Snapshot())
	for ni := range s.Nodes {
		if len(s.Nodes[ni].Bridges) == 0 {
			continue
		}
		s.Nodes[ni].Bridges[0].MTU = mtu
		b, err := spec.Marshal(s)
		if err != nil {
			t.Fatalf("spec.Marshal: %v", err)
		}
		return b
	}
	t.Fatal("fixture has no node with a bridge to diverge")
	return nil
}

// --- misc ------------------------------------------------------------------

// captureLogger returns a logger writing into a buffer, plus a func to read
// everything logged so far. Used by AC6's log-surface assertion.
func captureLogger() (*slog.Logger, func() string) {
	var mu sync.Mutex
	var sb strings.Builder
	h := slog.NewTextHandler(&lockedWriter{mu: &mu, sb: &sb}, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), func() string {
		mu.Lock()
		defer mu.Unlock()
		return sb.String()
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	sb *strings.Builder
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sb.Write(p)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}
