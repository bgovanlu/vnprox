// SPDX-License-Identifier: Apache-2.0

package reconcile_test

// T-2703 AC3: neither reconciliation action fires without an explicit request,
// asserted with a drift finding at every severity and a transport that fails
// the test if it is called.
//
// Every negative leg here is paired with a CONTROL leg that reuses the very
// same spy type and asserts it records the call it was supposed to record. A
// "nothing was called" assertion against a spy that cannot observe a call is
// not an assertion at all.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/reconcile"
	"github.com/bgovanlu/vnprox/internal/spec"
)

// --- the transport spies ----------------------------------------------------

// spy records every call made through it and, when failOnCall is set, fails
// the test the moment one arrives. Both the negative and the control legs use
// this same type: the control leg is what proves a call IS recorded, so the
// negative leg's "no calls" means something.
//
//nolint:govet // fieldalignment: test double; mutex first, then what it guards.
type spy struct {
	t          *testing.T
	failOnCall bool

	mu    sync.Mutex
	calls []string
}

func newSpy(t *testing.T, failOnCall bool) *spy {
	t.Helper()
	return &spy{t: t, failOnCall: failOnCall}
}

func (s *spy) note(name string) {
	s.mu.Lock()
	s.calls = append(s.calls, name)
	s.mu.Unlock()
	if s.failOnCall {
		s.t.Errorf("a reconciliation transport was reached without an explicit request: %s", name)
	}
}

func (s *spy) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// stagerSpy is the change-engine transport. Creating a changeset is the ONLY
// thing it can do, which is the interface's own guarantee; the spy exists to
// prove it is not even reached.
type stagerSpy struct{ *spy }

var _ reconcile.Stager = (*stagerSpy)(nil)

func (s stagerSpy) Create(_ context.Context, author, title string, ops []change.Op) (change.Changeset, error) {
	s.note("Stager.Create")
	return change.Changeset{ID: "cs-staged", Author: author, Title: title, Ops: ops, Status: change.StatusDraft}, nil
}

// adopterSpy is the git transport.
type adopterSpy struct {
	*spy
	enabled bool
}

var _ reconcile.Adopter = (*adopterSpy)(nil)

func (a adopterSpy) Enabled() bool { return a.enabled }

func (a adopterSpy) ProposeAdoption(_ context.Context, req gitsync.AdoptionRequest) (gitsync.Proposal, error) {
	a.note("Adopter.ProposeAdoption")
	return gitsync.Proposal{FindingID: req.FindingID, PullRequestURL: "https://example.invalid/pr/1", Created: true}, nil
}

func (a adopterSpy) GetAdoption(_ context.Context, findingID string) (gitsync.Proposal, error) {
	a.note("Adopter.GetAdoption")
	return gitsync.Proposal{FindingID: findingID}, nil
}

// --- the finding corpus -----------------------------------------------------

// staticPin is a drift.PinProvider over one fixed document.
type staticPin string

func (p staticPin) Pin() (string, bool) { return string(p), true }

// everySeverityGraph builds one node carrying three bridges, chosen so the
// drift service produces a spec_reconciliation finding at each of the three
// severities in docs/api.md's vocabulary — error, warning, info. There is no
// fourth: "critical" is not in this codebase's severity vocabulary.
//
//	vmbr-err   spec 9000 / config 1500 / live 1400 — all three differ
//	vmbr-warn  spec 9000 / config 1500 / live 1500 — the spec is the odd one out
//	vmbr-info  spec 1500 / config 1500 / live 1400 — the runtime is the odd one out
func everySeverityGraph(t *testing.T) (*inventory.Graph, drift.PinProvider) {
	t.Helper()
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online"},
	})

	type bridge struct {
		name      string
		specMTU   int
		configMTU int
		liveMTU   int
	}
	bridges := []bridge{
		{name: "vmbr-err", specMTU: 9000, configMTU: 1500, liveMTU: 1400},
		{name: "vmbr-warn", specMTU: 9000, configMTU: 1500, liveMTU: 1500},
		{name: "vmbr-info", specMTU: 1500, configMTU: 1500, liveMTU: 1400},
	}

	// One ApplyPoll per source, carrying every bridge: a poll REPLACES the
	// whole (source, scope) set, so applying them one at a time would leave
	// only the last.
	doc := spec.Spec{SpecVersion: 1, Nodes: []spec.NodeSpec{{Name: "pve1"}}}
	var declared, running []inventory.Entity
	for _, b := range bridges {
		ref := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: b.name}
		declared = append(declared, &inventory.Bridge{
			Ref: ref, Name: b.name, Virt: inventory.BridgeLinux, MTUDeclared: b.configMTU,
		})
		running = append(running, &inventory.Bridge{
			Ref: ref, Name: b.name, Virt: inventory.BridgeLinux, MTU: b.liveMTU,
		})
		doc.Nodes[0].Bridges = append(doc.Nodes[0].Bridges, spec.BridgeSpec{Name: b.name, MTU: b.specMTU})
	}
	scope := inventory.Scope{Node: "pve1", Kinds: []inventory.Kind{inventory.KindBridge}}
	g.ApplyPoll(inventory.SourcePVENetwork, scope, declared)
	g.ApplyPoll(inventory.SourceHostNetlink, scope, running)

	content, err := spec.Marshal(doc)
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}
	return g, staticPin(content)
}

// findingsBySeverity indexes the spec_reconciliation findings by severity, and
// fails if any of the three is missing — AC3 asks for a finding at EVERY
// severity, so a corpus short of one would weaken the assertion silently.
func findingsBySeverity(t *testing.T, svc *drift.Service) map[string]drift.Finding {
	t.Helper()
	out := map[string]drift.Finding{}
	for _, f := range svc.Findings() {
		if f.Check == drift.CheckSpecReconciliation {
			out[f.Severity] = f
		}
	}
	for _, sev := range []string{drift.SeverityError, drift.SeverityWarning, drift.SeverityInfo} {
		if _, ok := out[sev]; !ok {
			t.Fatalf("the corpus has no %s spec_reconciliation finding; AC3 needs one at every severity", sev)
		}
	}
	return out
}

// --- AC3 --------------------------------------------------------------------

// TestAC3_NoActionFiresWithoutAnExplicitRequest is the negative leg: findings
// at every severity are computed, the drift cycle is driven repeatedly, and
// both transports are wired to fail the test the instant they are called.
func TestAC3_NoActionFiresWithoutAnExplicitRequest(t *testing.T) {
	g, pin := everySeverityGraph(t)
	stager := stagerSpy{newSpy(t, true)}
	adopter := adopterSpy{spy: newSpy(t, true), enabled: true}

	driftSvc := drift.New(drift.Config{Graph: g, Pins: pin})
	// Constructing the reconcile service must not reach a transport either.
	reconcile.New(reconcile.Config{Findings: driftSvc, Stager: stager, Adopter: adopter})

	bySeverity := findingsBySeverity(t, driftSvc)

	// Every severity is present, and at least one finding offers each action —
	// so "nothing fired" is not because there was nothing to fire.
	offersAdopt, offersRestore := 0, 0
	for sev, f := range bySeverity {
		if f.Reconcile == nil {
			t.Fatalf("the %s finding carries no reconciliation report", sev)
		}
		if f.Reconcile.Actions.AdoptReality {
			offersAdopt++
		}
		if f.Reconcile.Actions.RestoreIntent {
			offersRestore++
		}
	}
	if offersAdopt == 0 || offersRestore == 0 {
		t.Fatalf("the corpus offers adopt=%d restore=%d; with nothing on offer this test asserts nothing", offersAdopt, offersRestore)
	}

	// Drive the periodic cycle the way the daemon does — several times over,
	// including the RunLoop that fires on startup and on every tick.
	ctx, cancel := context.WithCancel(context.Background())
	loop := drift.New(drift.Config{Graph: g, Pins: pin, Interval: time.Millisecond})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = loop.RunLoop(ctx)
	}()
	for i := 0; i < 5; i++ {
		driftSvc.Findings()
		loop.Findings()
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	if calls := stager.recorded(); len(calls) != 0 {
		t.Errorf("the change engine was reached %d time(s) with no request: %v", len(calls), calls)
	}
	if calls := adopter.recorded(); len(calls) != 0 {
		t.Errorf("the git host was reached %d time(s) with no request: %v", len(calls), calls)
	}
}

// TestAC3_ControlLeg_TheSpiesFireWhenAskedExplicitly is the control AC3's
// negative assertion is worthless without: the same spy types, wired the same
// way, DO record a call the moment an operator asks — at every severity that
// offers the action.
func TestAC3_ControlLeg_TheSpiesFireWhenAskedExplicitly(t *testing.T) {
	g, pin := everySeverityGraph(t)
	driftSvc := drift.New(drift.Config{Graph: g, Pins: pin})
	bySeverity := findingsBySeverity(t, driftSvc)

	for _, sev := range []string{drift.SeverityError, drift.SeverityWarning, drift.SeverityInfo} {
		f := bySeverity[sev]
		t.Run(sev, func(t *testing.T) {
			stager := stagerSpy{newSpy(t, false)}
			adopter := adopterSpy{spy: newSpy(t, false), enabled: true}
			svc := reconcile.New(reconcile.Config{Findings: driftSvc, Stager: stager, Adopter: adopter})

			if f.Reconcile.Actions.RestoreIntent {
				cs, err := svc.RestoreIntent(context.Background(), f.ID, "brian")
				if err != nil {
					t.Fatalf("RestoreIntent: %v", err)
				}
				if cs.Status != change.StatusDraft {
					t.Errorf("restoring intent produced a %s changeset, want a draft", cs.Status)
				}
				if got := stager.recorded(); !reflect.DeepEqual(got, []string{"Stager.Create"}) {
					t.Errorf("stager calls = %v, want exactly one Create", got)
				}
			} else if _, err := svc.RestoreIntent(context.Background(), f.ID, "brian"); !errors.Is(err, reconcile.ErrNotOffered) {
				t.Errorf("RestoreIntent on a finding that does not offer it = %v, want ErrNotOffered", err)
			}

			if f.Reconcile.Actions.AdoptReality {
				proposal, err := svc.AdoptReality(context.Background(), f.ID, "brian")
				if err != nil {
					t.Fatalf("AdoptReality: %v", err)
				}
				if proposal.PullRequestURL == "" {
					t.Errorf("adopting reality produced no pull request URL")
				}
				if got := adopter.recorded(); !reflect.DeepEqual(got, []string{"Adopter.ProposeAdoption"}) {
					t.Errorf("adopter calls = %v, want exactly one ProposeAdoption", got)
				}
			} else if _, err := svc.AdoptReality(context.Background(), f.ID, "brian"); !errors.Is(err, reconcile.ErrNotOffered) {
				t.Errorf("AdoptReality on a finding that does not offer it = %v, want ErrNotOffered", err)
			}
		})
	}
}

// TestAC3_AnUnknownFindingReachesNoTransport: a caller naming a finding that
// does not exist is refused before either transport is touched. This is the
// half of "explicit request" that matters for an API surface — an explicit
// request for something that is not there is still not permission to act.
func TestAC3_AnUnknownFindingReachesNoTransport(t *testing.T) {
	g, pin := everySeverityGraph(t)
	stager := stagerSpy{newSpy(t, true)}
	adopter := adopterSpy{spy: newSpy(t, true), enabled: true}
	svc := reconcile.New(reconcile.Config{
		Findings: drift.New(drift.Config{Graph: g, Pins: pin}), Stager: stager, Adopter: adopter,
	})

	if _, err := svc.RestoreIntent(context.Background(), "no-such-finding", "brian"); !errors.Is(err, reconcile.ErrNotOffered) {
		t.Errorf("RestoreIntent(unknown) = %v, want ErrNotOffered", err)
	}
	if _, err := svc.AdoptReality(context.Background(), "no-such-finding", "brian"); !errors.Is(err, reconcile.ErrNotOffered) {
		t.Errorf("AdoptReality(unknown) = %v, want ErrNotOffered", err)
	}
	if len(stager.recorded())+len(adopter.recorded()) != 0 {
		t.Errorf("a request for an unknown finding still reached a transport")
	}
}

// TestAdoptRealityWithoutAConfiguredRepository refuses without contacting
// anything, rather than reporting a finding-level problem.
func TestAdoptRealityWithoutAConfiguredRepository(t *testing.T) {
	g, pin := everySeverityGraph(t)
	driftSvc := drift.New(drift.Config{Graph: g, Pins: pin})
	adopter := adopterSpy{spy: newSpy(t, true), enabled: false}
	svc := reconcile.New(reconcile.Config{Findings: driftSvc, Stager: stagerSpy{newSpy(t, true)}, Adopter: adopter})

	f := findingsBySeverity(t, driftSvc)[drift.SeverityError]
	if _, err := svc.AdoptReality(context.Background(), f.ID, "brian"); !errors.Is(err, reconcile.ErrAdoptNotConfigured) {
		t.Fatalf("AdoptReality with no repository = %v, want ErrAdoptNotConfigured", err)
	}
	if svc.AdoptEnabled() {
		t.Errorf("AdoptEnabled reported true with a disabled adopter")
	}
	if len(adopter.recorded()) != 0 {
		t.Errorf("an unconfigured adopter was still asked to propose")
	}
}

// --- structural guarantees --------------------------------------------------

// TestStagerCannotApply is AC3's structural half: the change-engine interface
// this package holds has no verb that changes the cluster. A reconcile path
// that applied could not be written without editing this interface, and this
// test makes that edit fail the build rather than merely being noticed.
func TestStagerCannotApply(t *testing.T) {
	typ := reflect.TypeOf((*reconcile.Stager)(nil)).Elem()
	if typ.NumMethod() != 1 {
		t.Errorf("reconcile.Stager has %d methods; staging a draft is one method's worth of authority", typ.NumMethod())
	}
	forbidden := []string{"apply", "confirm", "approve", "validate", "rollback", "discard", "delete", "commit"}
	for i := 0; i < typ.NumMethod(); i++ {
		name := strings.ToLower(typ.Method(i).Name)
		for _, verb := range forbidden {
			if strings.Contains(name, verb) {
				t.Errorf("reconcile.Stager exposes %s: this package must not be able to %s", typ.Method(i).Name, verb)
			}
		}
	}
}

// TestAdopterCannotMerge: opening a pull request is the end of vnprox's
// involvement. Nothing here merges, approves or polls for approval.
func TestAdopterCannotMerge(t *testing.T) {
	typ := reflect.TypeOf((*reconcile.Adopter)(nil)).Elem()
	forbidden := []string{"merge", "approve", "poll", "wait", "close"}
	for i := 0; i < typ.NumMethod(); i++ {
		name := strings.ToLower(typ.Method(i).Name)
		for _, verb := range forbidden {
			if strings.Contains(name, verb) {
				t.Errorf("reconcile.Adopter exposes %s: vnprox opens a request and stops", typ.Method(i).Name)
			}
		}
	}
}

// TestFindingsSeamTakesOnlyAnID: the lookup seam accepts a finding id and
// nothing else. If it ever accepted an op list or a ref list from its caller,
// an API client could widen an adoption past the entity its finding is about.
func TestFindingsSeamTakesOnlyAnID(t *testing.T) {
	typ := reflect.TypeOf((*reconcile.Findings)(nil)).Elem()
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if m.Type.NumIn() != 1 || m.Type.In(0).Kind() != reflect.String {
			t.Errorf("reconcile.Findings.%s takes %v; it must take a finding id and nothing else",
				m.Name, m.Type)
		}
	}
}
