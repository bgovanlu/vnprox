// SPDX-License-Identifier: Apache-2.0

package gitsync_test

// T-2703's "adopt reality" half, against the same mock git host T-2702's
// acceptance tests use.
//
// AC1 is asserted SEMANTICALLY: the test reads the document the proposal
// actually committed on the branch, re-parses it, and re-runs the planner
// against the same live snapshot. It never compares document text — a textual
// assertion would pass for a document that merely looked right and would break
// on a whitespace change that meant nothing.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
)

const adoptFinding = "spec_reconciliation|bridge:pve1:vmbr0"

// firstBridgeRef returns the ref divergentSpec diverges — the first node's
// first bridge in Export's own ordering.
func firstBridgeRef(t *testing.T, doc []byte) inventory.Ref {
	t.Helper()
	parsed, err := spec.Parse(doc)
	if err != nil {
		t.Fatalf("spec.Parse: %v", err)
	}
	for _, n := range parsed.Nodes {
		if len(n.Bridges) > 0 {
			return inventory.Ref{Kind: inventory.KindBridge, Node: n.Name, ID: n.Bridges[0].Name}
		}
	}
	t.Fatal("fixture document declares no bridge")
	return inventory.Ref{}
}

// adoptFixture is a proposeFixture whose repository holds a spec that DIVERGES
// from live: the first bridge's MTU is 1400 in the document and whatever the
// fixture actually has in the cluster. That is the situation a drift finding
// reports and adoption resolves.
func newAdoptFixture(t *testing.T, provider string) (*proposeFixture, inventory.Ref) {
	t.Helper()
	f := newProposeFixture(t, provider)
	divergent := divergentSpec(t, f.graph, 1400)
	setBaseDocument(f.host, divergent)
	ref := firstBridgeRef(t, divergent)

	if plan := planFor(t, divergent, f.graph); len(plan) == 0 {
		t.Fatal("control failed: the divergent document plans to zero ops, so there is nothing to adopt")
	}
	return f, ref
}

// setBaseDocument replaces the document on the base branch and keeps the
// mock's "the base ref resolves to this sha" answer consistent with it. The
// READ source resolves the ref first and then reads the blob at that sha, so a
// commit that left baseSHA pointing at the previous tree would make the
// document read as missing.
func setBaseDocument(host *gitHostServer, content []byte) {
	sha := host.commit(proposeRef, proposePath, "seed the base document", content)
	host.mu.Lock()
	defer host.mu.Unlock()
	host.baseSHA = sha
}

// committedPlan reads the document the proposal committed on branch and
// re-imports it against the current live snapshot.
func committedPlan(t *testing.T, f *proposeFixture, branch string) ([]string, spec.Spec) {
	t.Helper()
	content, ok := f.host.fileOn(branch, proposePath)
	if !ok {
		t.Fatalf("branch %s carries no %s", branch, proposePath)
	}
	parsed, err := spec.Parse(content)
	if err != nil {
		t.Fatalf("the committed document does not parse: %v", err)
	}
	ops, _, err := spec.Import(parsed, f.graph.Snapshot())
	if err != nil {
		t.Fatalf("Import(committed): %v", err)
	}
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, string(op.Type)+" "+op.Target.String())
	}
	return out, parsed
}

// TestAC1_AdoptRealityCommitsASpecThatReimportsToAnEmptyPlan is AC1, on both
// hosts, asserted by re-running the planner rather than by reading the diff.
func TestAC1_AdoptRealityCommitsASpecThatReimportsToAnEmptyPlan(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			f, ref := newAdoptFixture(t, provider)

			proposal, err := f.proposer.ProposeAdoption(context.Background(), gitsync.AdoptionRequest{
				FindingID: adoptFinding,
				Refs:      []inventory.Ref{ref},
				Detail:    "spec says 1400, the interfaces file and the kernel say otherwise",
				Actor:     "brian",
			})
			if err != nil {
				t.Fatalf("ProposeAdoption: %v", err)
			}
			if !proposal.Created || proposal.PullRequestURL == "" {
				t.Fatalf("proposal = %+v, want a newly created request with a URL", proposal)
			}
			if proposal.FindingID != adoptFinding || proposal.ChangesetID != "" {
				t.Errorf("proposal identity = finding %q / changeset %q, want the finding only",
					proposal.FindingID, proposal.ChangesetID)
			}

			plan, _ := committedPlan(t, f, proposal.Branch)
			if len(plan) != 0 {
				t.Fatalf("the adopted document re-imports to %v, want an empty plan — that is what adopting means", plan)
			}

			// And the request itself exists on the host, on that branch.
			prs := f.host.openPRs()
			if len(prs) != 1 || prs[0].Branch != proposal.Branch {
				t.Fatalf("open requests = %+v, want exactly one on %s", prs, proposal.Branch)
			}
		})
	}
}

// TestAC1_AdoptingAnEntityLiveNoLongerHasRemovesItFromTheSpec is the direction
// ApplyOps cannot express, end to end: the document declares a bridge the
// cluster does not have, and adopting reality deletes the declaration.
func TestAC1_AdoptingAnEntityLiveNoLongerHasRemovesItFromTheSpec(t *testing.T) {
	f := newProposeFixture(t, "github")

	base, err := spec.Parse(f.baseDoc)
	if err != nil {
		t.Fatalf("spec.Parse: %v", err)
	}
	ghost := inventory.Ref{Kind: inventory.KindBridge, Node: base.Nodes[0].Name, ID: "vmbr-ghost"}
	base.Nodes[0].Bridges = append(base.Nodes[0].Bridges, spec.BridgeSpec{Name: ghost.ID, MTU: 1500})
	withGhost, err := spec.Marshal(base)
	if err != nil {
		t.Fatalf("spec.Marshal: %v", err)
	}
	setBaseDocument(f.host, withGhost)

	if plan := planFor(t, withGhost, f.graph); len(plan) == 0 {
		t.Fatal("control failed: a document declaring an absent bridge should plan a create")
	}

	proposal, err := f.proposer.ProposeAdoption(context.Background(), gitsync.AdoptionRequest{
		FindingID: "spec_reconciliation|" + ghost.String(),
		Refs:      []inventory.Ref{ghost},
		Actor:     "brian",
	})
	if err != nil {
		t.Fatalf("ProposeAdoption: %v", err)
	}

	plan, committed := committedPlan(t, f, proposal.Branch)
	if len(plan) != 0 {
		t.Fatalf("the adopted document re-imports to %v, want an empty plan", plan)
	}
	for _, n := range committed.Nodes {
		for _, b := range n.Bridges {
			if b.Name == ghost.ID {
				t.Fatalf("the adopted document still declares %s", ghost)
			}
		}
	}
}

// TestAdopt_NothingToAdoptIsRefusedAndWritesNothing: a document that already
// describes the cluster has nothing to adopt. Opening an empty pull request
// for it is exactly the "offered but not applicable" state AC5 forbids, so the
// executing side refuses it too — and touches the host doing so.
func TestAdopt_NothingToAdoptIsRefusedAndWritesNothing(t *testing.T) {
	f := newProposeFixture(t, "github") // base doc already matches live
	ref := firstBridgeRef(t, f.baseDoc)

	_, err := f.proposer.ProposeAdoption(context.Background(), gitsync.AdoptionRequest{
		FindingID: adoptFinding, Refs: []inventory.Ref{ref}, Actor: "brian",
	})
	if !errors.Is(err, gitsync.ErrNothingToPropose) {
		t.Fatalf("adopting a converged entity = %v, want ErrNothingToPropose", err)
	}
	if branches := f.host.branchNames(); len(branches) != 1 {
		t.Errorf("the refused adoption touched the host: branches = %v, want only the base branch", branches)
	}
	if prs := f.host.openPRs(); len(prs) != 0 {
		t.Errorf("the refused adoption opened %d request(s)", len(prs))
	}

	// Control: the same host DOES get written to when there is something to
	// adopt, so the assertions above are not passing because nothing works.
	f2, divergentRef := newAdoptFixture(t, "github")
	if _, err := f2.proposer.ProposeAdoption(context.Background(), gitsync.AdoptionRequest{
		FindingID: adoptFinding, Refs: []inventory.Ref{divergentRef}, Actor: "brian",
	}); err != nil {
		t.Fatalf("control failed: a real adoption also could not be proposed: %v", err)
	}
	if branches := f2.host.branchNames(); len(branches) != 2 {
		t.Errorf("control failed: a real adoption did not create a branch (branches = %v)", branches)
	}
}

// TestAdopt_TwiceUpdatesTheSameRequest: adopting the same finding twice
// addresses the same deterministic branch, so the host holds one request.
func TestAdopt_TwiceUpdatesTheSameRequest(t *testing.T) {
	f, ref := newAdoptFixture(t, "github")
	req := gitsync.AdoptionRequest{FindingID: adoptFinding, Refs: []inventory.Ref{ref}, Actor: "brian"}

	first, err := f.proposer.ProposeAdoption(context.Background(), req)
	if err != nil {
		t.Fatalf("first ProposeAdoption: %v", err)
	}
	second, err := f.proposer.ProposeAdoption(context.Background(), req)
	if err != nil {
		t.Fatalf("second ProposeAdoption: %v", err)
	}
	if !first.Created {
		t.Errorf("the first adoption did not report Created")
	}
	if second.Created {
		t.Errorf("the second adoption opened a new request instead of updating the first")
	}
	if first.Branch != second.Branch {
		t.Errorf("branches differ between adoptions of the same finding: %q vs %q", first.Branch, second.Branch)
	}
	if prs := f.host.openPRs(); len(prs) != 1 {
		t.Errorf("open requests = %d, want 1", len(prs))
	}
	if n := f.proposals.count(); n != 1 {
		t.Errorf("recorded proposals = %d, want 1", n)
	}

	// The record round-trips as an ADOPTION, not as a changeset proposal.
	got, err := f.proposer.GetAdoption(context.Background(), adoptFinding)
	if err != nil {
		t.Fatalf("GetAdoption: %v", err)
	}
	if got.FindingID != adoptFinding || got.ChangesetID != "" {
		t.Errorf("recorded adoption = finding %q / changeset %q", got.FindingID, got.ChangesetID)
	}
	if _, err := f.proposer.GetAdoption(context.Background(), "no-such-finding"); !errors.Is(err, gitsync.ErrNoProposal) {
		t.Errorf("GetAdoption for an unproposed finding = %v, want ErrNoProposal", err)
	}
}

// TestAdopt_HostFailureLeavesNoOrphanBranch proves the adoption path really
// does share T-2702's compensating ordering rather than reimplementing it.
func TestAdopt_HostFailureLeavesNoOrphanBranch(t *testing.T) {
	f, ref := newAdoptFixture(t, "github")
	f.host.failWith(failOpenPR, 500)

	_, err := f.proposer.ProposeAdoption(context.Background(), gitsync.AdoptionRequest{
		FindingID: adoptFinding, Refs: []inventory.Ref{ref}, Actor: "brian",
	})
	if err == nil {
		t.Fatal("ProposeAdoption succeeded though the host refused to open the request")
	}
	if branches := f.host.branchNames(); len(branches) != 1 {
		t.Errorf("branches after a failed adoption = %v, want only the base branch (an orphan was left behind)", branches)
	}
	if prs := f.host.openPRs(); len(prs) != 0 {
		t.Errorf("open requests after a failed adoption = %d, want 0", len(prs))
	}

	// Control: with the failure cleared, the very same call creates exactly
	// the branch the assertion above looked for — so "no branch" was a real
	// observation, not a blind spot.
	f.host.clearFailures()
	proposal, err := f.proposer.ProposeAdoption(context.Background(), gitsync.AdoptionRequest{
		FindingID: adoptFinding, Refs: []inventory.Ref{ref}, Actor: "brian",
	})
	if err != nil {
		t.Fatalf("control failed: %v", err)
	}
	if !f.host.hasBranch(proposal.Branch) {
		t.Errorf("control failed: a successful adoption did not create %s either", proposal.Branch)
	}
}

// TestAdopt_CredentialNeverLeaks: the push token appears in no surface an
// adoption writes — branch name, commit message, request title or body.
func TestAdopt_CredentialNeverLeaks(t *testing.T) {
	f, ref := newAdoptFixture(t, "github")
	proposal, err := f.proposer.ProposeAdoption(context.Background(), gitsync.AdoptionRequest{
		FindingID: adoptFinding, Refs: []inventory.Ref{ref},
		Detail: "spec, config and live disagree", Actor: "brian",
	})
	if err != nil {
		t.Fatalf("ProposeAdoption: %v", err)
	}
	if seen := f.host.credentialSeen(); !strings.Contains(seen, pushToken) {
		t.Fatalf("control failed: the host never saw the push credential (%q), so the assertions below prove nothing", seen)
	}
	commits, branches := f.host.surfaces()
	surfaces := map[string][]string{
		"branch name":  {proposal.Branch},
		"commit":       commits,
		"branch":       branches,
		"request text": prTexts(f),
		"logs":         {f.logs()},
	}
	for name, texts := range surfaces {
		for _, text := range texts {
			if strings.Contains(text, pushToken) {
				t.Errorf("the push credential appears in the %s: %q", name, text)
			}
		}
	}
}

func prTexts(f *proposeFixture) []string {
	var out []string
	for _, pr := range f.host.openPRs() {
		out = append(out, pr.Title, pr.Body)
	}
	return out
}

// TestAdopt_NotConfiguredContactsNothing: a Proposer without a write
// credential refuses without reaching for a host.
func TestAdopt_NotConfiguredContactsNothing(t *testing.T) {
	p := gitsync.NewProposer(gitsync.ProposerConfig{})
	_, err := p.ProposeAdoption(context.Background(), gitsync.AdoptionRequest{
		FindingID: adoptFinding,
		Refs:      []inventory.Ref{{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}},
	})
	if !errors.Is(err, gitsync.ErrProposeNotConfigured) {
		t.Fatalf("ProposeAdoption on an unconfigured proposer = %v, want ErrProposeNotConfigured", err)
	}
}

// TestAdopt_RefusesAnEmptyRequest: an adoption must name a finding and at
// least one entity. Neither is ever supplied by a client — internal/drift
// looks both up by finding id — so this is a guard against a wiring mistake,
// and it must not reach the host.
func TestAdopt_RefusesAnEmptyRequest(t *testing.T) {
	f, ref := newAdoptFixture(t, "github")
	cases := []struct {
		name string
		req  gitsync.AdoptionRequest
	}{
		{name: "no finding", req: gitsync.AdoptionRequest{Refs: []inventory.Ref{ref}}},
		{name: "no refs", req: gitsync.AdoptionRequest{FindingID: adoptFinding}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.proposer.ProposeAdoption(context.Background(), tc.req); !errors.Is(err, gitsync.ErrNothingToPropose) {
				t.Errorf("ProposeAdoption(%+v) = %v, want ErrNothingToPropose", tc.req, err)
			}
		})
	}
	if branches := f.host.branchNames(); len(branches) != 1 {
		t.Errorf("a refused adoption touched the host: branches = %v", branches)
	}
}

// TestAdopt_UnadoptableKindIsRefused: a ref the document has no vocabulary for
// is refused with the ref named, before anything is written.
func TestAdopt_UnadoptableKindIsRefused(t *testing.T) {
	f, _ := newAdoptFixture(t, "github")
	nic := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}

	_, err := f.proposer.ProposeAdoption(context.Background(), gitsync.AdoptionRequest{
		FindingID: "spec_reconciliation|" + nic.String(), Refs: []inventory.Ref{nic},
	})
	if !errors.Is(err, gitsync.ErrNotExpressible) {
		t.Fatalf("adopting a physical NIC = %v, want ErrNotExpressible", err)
	}
	if !strings.Contains(err.Error(), nic.String()) {
		t.Errorf("the refusal does not name the offending ref: %v", err)
	}
	if branches := f.host.branchNames(); len(branches) != 1 {
		t.Errorf("the refused adoption touched the host: branches = %v", branches)
	}
}
