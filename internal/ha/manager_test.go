// SPDX-License-Identifier: Apache-2.0

package ha_test

// T-1704 arbitration / fencing coverage (no change.Service needed here — see
// harness_test.go for the real-timer AC1/AC4 integration). Test <-> AC map:
//
//   AC2 -> TestSplitBrain_TransientBlip_ActiveStaysActive
//   AC3 -> TestSplitBrain_GenuineExpiry_PromotesThenOldActiveDemotes,
//          TestReceive_FencesStaleSenderTerm
//   fencing/monotonicity -> TestPromotion_StrictlyIncreasesTerm,
//          TestIsLeaderGate

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/ha"
)

const haEpoch = 1_800_000_000

var errPartitioned = errors.New("test: partitioned")

// fakeClock is a shared, manually-advanced clock both daemons read.
type fakeClock struct {
	t  time.Time
	mu sync.Mutex
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(haEpoch, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// memLeaseStore is an in-memory LeaseStore.
type memLeaseStore struct {
	l  *ha.Lease
	mu sync.Mutex
}

func (s *memLeaseStore) Get(context.Context) (ha.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.l == nil {
		return ha.Lease{}, ha.ErrNoLease
	}
	return *s.l, nil
}

func (s *memLeaseStore) Set(_ context.Context, l ha.Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := l
	s.l = &cp
	return nil
}

// fakeCoordinator records re-arm/quiesce calls.
type fakeCoordinator struct {
	mu       sync.Mutex
	rearms   int
	quiesces int
}

func (c *fakeCoordinator) ReArm(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rearms++
	return nil
}

func (c *fakeCoordinator) Quiesce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.quiesces++
}

func (c *fakeCoordinator) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rearms, c.quiesces
}

// memSource / memApplier are trivial replication endpoints (the arbitration
// tests carry no real rows — they exercise the lease heartbeat the batch
// carries).
type memSource struct{}

func (memSource) Gather(context.Context, int64) (ha.Batch, error) { return ha.Batch{}, nil }
func (memSource) AuditHighWater(context.Context) (int64, error)   { return 0, nil }

type memApplier struct {
	mu      sync.Mutex
	applied int
}

func (a *memApplier) Apply(context.Context, ha.Batch) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applied++
	return nil
}
func (a *memApplier) AuditHighWater(context.Context) (int64, error) { return 0, nil }
func (a *memApplier) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applied
}

// link is an in-memory Replicator with an injectable partition switch: Push
// delivers the batch to the peer Manager's Receive unless partitioned.
type link struct {
	peer        *ha.Manager
	mu          sync.Mutex
	partitioned bool
}

func (l *link) setPeer(p *ha.Manager) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.peer = p
}

func (l *link) partition(on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.partitioned = on
}

func (l *link) Push(ctx context.Context, batch ha.Batch) (ha.Ack, error) {
	l.mu.Lock()
	peer, part := l.peer, l.partitioned
	l.mu.Unlock()
	if part || peer == nil {
		return ha.Ack{}, errPartitioned
	}
	return peer.Receive(ctx, batch)
}

// pair builds a two-manager arbitration harness (short lease tunables so the
// tests advance a fake clock by seconds). aBootstrap makes A acquire term 1.
type pair struct {
	clock              *fakeClock
	a, b               *ha.Manager
	aCoord, bCoord     *fakeCoordinator
	aApplier           *memApplier
	bApplier           *memApplier
	linkAToB, linkBToA *link
}

func newPair(t *testing.T) *pair {
	t.Helper()
	clk := newFakeClock()
	aToB := &link{}
	bToA := &link{}
	aCoord := &fakeCoordinator{}
	bCoord := &fakeCoordinator{}
	aApplier := &memApplier{}
	bApplier := &memApplier{}

	mk := func(id string, repl *link, coord *fakeCoordinator, applier *memApplier, bootstrap bool) *ha.Manager {
		m, err := ha.NewManager(ha.Config{
			Clock: clk, InstanceID: id, Lease: &memLeaseStore{}, Coordinator: coord,
			Replicator: repl, Source: memSource{}, Applier: applier, Announcer: ha.NoopAnnouncer{},
			LeaseTTL: 6 * time.Second, RenewInterval: 2 * time.Second, FencingMargin: 6 * time.Second,
			Bootstrap: bootstrap,
		})
		if err != nil {
			t.Fatalf("NewManager(%s): %v", id, err)
		}
		return m
	}
	a := mk("node-a", aToB, aCoord, aApplier, true)
	b := mk("node-b", bToA, bCoord, bApplier, false)
	aToB.setPeer(b)
	bToA.setPeer(a)

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("a.Start: %v", err)
	}
	if err := b.Start(ctx); err != nil {
		t.Fatalf("b.Start: %v", err)
	}
	return &pair{clock: clk, a: a, b: b, aCoord: aCoord, bCoord: bCoord, aApplier: aApplier, bApplier: bApplier, linkAToB: aToB, linkBToA: bToA}
}

func TestStart_BootstrapAcquiresActiveTerm1(t *testing.T) {
	p := newPair(t)
	if p.a.Role() != ha.RoleActive {
		t.Errorf("A role = %s, want active", p.a.Role())
	}
	if p.b.Role() != ha.RoleStandby {
		t.Errorf("B role = %s, want standby", p.b.Role())
	}
	if r, _ := p.aCoord.counts(); r != 1 {
		t.Errorf("A re-arm count = %d, want 1 (became active on bootstrap)", r)
	}
	if p.a.Status().Term != 1 {
		t.Errorf("A term = %d, want 1", p.a.Status().Term)
	}
}

// TestSplitBrain_TransientBlip_ActiveStaysActive is AC2: a partition healed
// before lease expiry leaves the active active, zero promotion/double-apply.
func TestSplitBrain_TransientBlip_ActiveStaysActive(t *testing.T) {
	p := newPair(t)
	ctx := context.Background()

	p.a.Tick(ctx) // A renews + pushes; B observes A's live lease

	p.linkAToB.partition(true)
	p.clock.advance(4 * time.Second) // < observed.ExpiresAt(+6 margin): still fresh
	p.a.Tick(ctx)                    // push fails; < TTL of failures, A stays active
	p.b.Tick(ctx)                    // standby: not yet past expiry+margin, no promote

	if p.b.Role() != ha.RoleStandby {
		t.Fatalf("B promoted on a transient blip (role %s), want still standby", p.b.Role())
	}

	p.linkAToB.partition(false)
	p.clock.advance(2 * time.Second)
	p.a.Tick(ctx) // heals; B observes fresh lease again

	if p.a.Role() != ha.RoleActive || p.b.Role() != ha.RoleStandby {
		t.Fatalf("after heal: A=%s B=%s, want active/standby", p.a.Role(), p.b.Role())
	}
	if r, _ := p.bCoord.counts(); r != 0 {
		t.Errorf("B re-arm count = %d, want 0 (never promoted)", r)
	}
}

// TestSplitBrain_GenuineExpiry_PromotesThenOldActiveDemotes is AC3: a partition
// long enough makes the standby promote on genuine lease expiry; healing after
// makes the old active detect the newer term and demote — exactly one
// promotion (one re-arm), old active drives nothing.
func TestSplitBrain_GenuineExpiry_PromotesThenOldActiveDemotes(t *testing.T) {
	p := newPair(t)
	ctx := context.Background()

	p.a.Tick(ctx) // B observes A's lease (term 1, expiry epoch+6)

	// Partition, then advance well past the observed lease's expiry + fencing
	// margin so the standby genuinely promotes.
	p.linkAToB.partition(true)
	p.linkBToA.partition(true)
	p.clock.advance(20 * time.Second)

	// The isolated active keeps failing to push; after a full TTL of failures
	// it self-demotes (fail-safe).
	p.a.Tick(ctx)
	// The standby promotes on genuine expiry.
	p.b.Tick(ctx)

	if p.b.Role() != ha.RoleActive {
		t.Fatalf("B role = %s, want active (promoted on genuine expiry)", p.b.Role())
	}
	if p.b.Status().Term != 2 {
		t.Fatalf("B term = %d, want exactly 2 (one promotion)", p.b.Status().Term)
	}
	if p.a.Role() != ha.RoleStandby {
		t.Fatalf("isolated old active A role = %s, want standby (self-demoted)", p.a.Role())
	}

	// Heal: the new active (B, term 2) pushes to A; A adopts the newer term and
	// stays demoted (never drives).
	p.linkAToB.partition(false)
	p.linkBToA.partition(false)
	p.clock.advance(2 * time.Second)
	p.b.Tick(ctx)

	if p.a.Role() != ha.RoleStandby {
		t.Fatalf("healed old active A role = %s, want standby", p.a.Role())
	}
	if p.a.Status().Term < 2 {
		t.Errorf("A term = %d, want >= 2 (adopted newer term)", p.a.Status().Term)
	}
	// Exactly one promotion happened across the whole scenario.
	if r, _ := p.bCoord.counts(); r != 1 {
		t.Errorf("B re-arm count = %d, want exactly 1 (single promotion)", r)
	}
	if _, q := p.aCoord.counts(); q < 1 {
		t.Errorf("A quiesce count = %d, want >= 1 (demoted, drives nothing)", q)
	}
}

// TestReceive_FencesStaleSenderTerm: a newer-term leader rejects a stale
// sender's batch (does not apply it) and answers with its higher term so the
// stale sender demotes.
func TestReceive_FencesStaleSenderTerm(t *testing.T) {
	p := newPair(t)
	ctx := context.Background()
	p.a.Tick(ctx)

	// Drive B to active term 2 via a genuine promotion.
	p.linkAToB.partition(true)
	p.linkBToA.partition(true)
	p.clock.advance(20 * time.Second)
	p.b.Tick(ctx)
	if p.b.Status().Term != 2 {
		t.Fatalf("precondition: B term = %d, want 2", p.b.Status().Term)
	}
	before := p.bApplier.count()

	// A stale sender (term 1) pushes to B directly.
	ack, err := p.b.Receive(ctx, ha.Batch{Lease: ha.Lease{Holder: "node-a", Term: 1, ExpiresAt: haEpoch + 100}})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if ack.Term != 2 {
		t.Errorf("ack.Term = %d, want 2 (fence reports newer term)", ack.Term)
	}
	if p.bApplier.count() != before {
		t.Errorf("stale batch was applied (applied count changed), want rejected")
	}
}

// TestPromotion_StrictlyIncreasesTerm: a standby that observed a high term
// promotes strictly above it, never reusing a term.
func TestPromotion_StrictlyIncreasesTerm(t *testing.T) {
	p := newPair(t)
	ctx := context.Background()

	// B observes a high term (7) from a push, then loses the active.
	if _, err := p.b.Receive(ctx, ha.Batch{Lease: ha.Lease{Holder: "node-a", Term: 7, ExpiresAt: haEpoch + 3}}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	p.linkAToB.partition(true)
	p.linkBToA.partition(true)
	p.clock.advance(30 * time.Second)
	p.b.Tick(ctx)
	if got := p.b.Status().Term; got != 8 {
		t.Errorf("promoted term = %d, want 8 (strictly above observed 7)", got)
	}
}

// TestIsLeaderGate: only the active answers IsLeader true — the LeaderGuard
// change.Service consults.
func TestIsLeaderGate(t *testing.T) {
	p := newPair(t)
	if !p.a.IsLeader() {
		t.Error("active A IsLeader() = false, want true")
	}
	if p.b.IsLeader() {
		t.Error("standby B IsLeader() = true, want false")
	}
}
