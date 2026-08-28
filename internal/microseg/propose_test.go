// SPDX-License-Identifier: Apache-2.0

package microseg

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestProposeNASGuest_CoverageAndMinimality is AC1: against a clean 14-day NAS
// corpus, Propose returns a policy covering >=99.5% of observed-good bytes with
// a rule count materially smaller than the raw distinct-flow-tuple count.
func TestProposeNASGuest_CoverageAndMinimality(t *testing.T) {
	corpus := nasCorpus()
	subj := nasSubject()
	profile := baseline.Learn(corpus, subj.GuestRef.String(), baseline.Window{Start: baseEpoch, End: baseEpoch + 14*daySeconds})

	// The corpus is spike/anomaly-free: its baseline flags nothing on itself.
	if got := baseline.Detect(profile, corpus, DefaultConfig().detectConfig()); len(got) != 0 {
		t.Fatalf("clean NAS corpus must raise zero baseline anomalies, got %d: %v", len(got), got)
	}

	prop := Propose(subj, corpus, profile, Existing{}, DefaultConfig())

	if prop.CoveragePct < 99.5 {
		t.Errorf("coverage = %.3f%%, want >= 99.5%%", prop.CoveragePct)
	}
	if prop.CoveragePct != 100 {
		t.Errorf("clean NAS corpus should cover 100%%, got %.3f%%", prop.CoveragePct)
	}
	if prop.UncoveredFlowCount != 0 {
		t.Errorf("UncoveredFlowCount = %d, want 0 on a fully-covered corpus", prop.UncoveredFlowCount)
	}

	// 4 ACCEPT (in 445, in 2049, out 53, out 443) + 2 trailing deny (in, out).
	if len(prop.Rules) != 6 {
		t.Fatalf("rule count = %d, want 6; rules: %v", len(prop.Rules), prop.Rules)
	}
	rawTuples := distinctRawTuples(corpus, subj.GuestRef.String())
	if len(prop.Rules) >= rawTuples {
		t.Errorf("rule count %d not materially smaller than raw distinct-tuple count %d", len(prop.Rules), rawTuples)
	}
	if rawTuples < 20 {
		t.Fatalf("fixture sanity: expected many raw tuples to collapse, got %d", rawTuples)
	}

	wantSigs := map[string]bool{
		"in ACCEPT tcp dport=445 src=10.0.0.0/24 dst=":  true,
		"in ACCEPT tcp dport=2049 src=10.0.0.0/24 dst=": true,
		"out ACCEPT udp dport=53 src= dst=10.0.0.0/24":  true,
		"out ACCEPT tcp dport=443 src= dst=10.0.0.0/24": true,
		"in DROP  dport= src= dst=":                     true,
		"out DROP  dport= src= dst=":                    true,
	}
	for _, r := range prop.Rules {
		sig := ruleSig(r)
		if !wantSigs[sig] {
			t.Errorf("unexpected rule signature %q", sig)
		}
		delete(wantSigs, sig)
	}
	for sig := range wantSigs {
		t.Errorf("missing expected rule %q", sig)
	}

	// The two trailing deny rules come after every ACCEPT of their direction.
	assertDenyIsLast(t, prop.Rules)
}

// distinctRawTuples counts the (direction, proto, port, peerIP) tuples a
// per-flow (non-grouped) policy would have to enumerate — the "raw
// distinct-flow-tuple count" AC1 compares the rule count against.
func distinctRawTuples(corpus []flow.Record, target string) int {
	type k struct {
		dir  string
		peer string
		pr   int
		port int
	}
	set := map[k]bool{}
	for _, r := range corpus {
		switch {
		case r.SrcRef == target:
			set[k{"out", r.DstIP, r.Proto, r.DstPort}] = true
		case r.DstRef == target:
			set[k{"in", r.SrcIP, r.Proto, r.DstPort}] = true
		}
	}
	return len(set)
}

func assertDenyIsLast(t *testing.T, rules []inventory.FwRule) {
	t.Helper()
	lastAccept := map[string]int{}
	firstDeny := map[string]int{}
	for i, r := range rules {
		if r.Action == "ACCEPT" {
			lastAccept[r.Direction] = i
		} else if _, ok := firstDeny[r.Direction]; !ok {
			firstDeny[r.Direction] = i
		}
	}
	for dir, di := range firstDeny {
		if ai, ok := lastAccept[dir]; ok && di < ai {
			t.Errorf("direction %s: deny at pos %d precedes an ACCEPT at %d", dir, di, ai)
		}
	}
}

// TestPropose_ExcludesAnomalousFlows is the load-bearing anomaly-exclusion
// proof (AC4): a flow flagged by T-1601's detector is never treated as
// observed-good, so a transient compromise inside the training window can never
// legitimize itself into an allow rule. Table over all three T-1601 anomaly
// classes, reusing T-1601's clean_injected corpus verbatim.
//
// The exclusion property (the flagged flow is removed from observed-good) is
// asserted for all three classes. The stronger "no proposed rule allows this
// specific flow" is asserted for new_port and new_subnet, where the anomaly is
// a NEW connection the attacker opened — exactly the flow the planner must not
// legitimize. For volume_spike the anomaly is excess *volume* on an
// already-permitted service (a firewall rule governs connectivity, not rate);
// the safe, provable property there is that excluding the spike changes the
// proposal by nothing at all — the burst legitimizes no rule. The
// complementary case of a spike to a service seen ONLY during the burst — which
// DOES lose its rule — is proven by TestDryRun_VolumeSpikeBurst_NeverAllowed.
func TestPropose_ExcludesAnomalousFlows(t *testing.T) {
	c := loadT1601Corpus(t, "clean_injected_corpus.json")
	profile := baseline.Learn(c.Records, c.Ref, c.Window)
	subj := Subject{GuestRef: inventory.MustParseRef(c.Ref)}

	baselinePolicy := Propose(subj, c.Records, profile, Existing{}, DefaultConfig())

	for _, class := range []string{"new_port", "new_subnet", "volume_spike"} {
		t.Run(class, func(t *testing.T) {
			anomalous := recentByClass(t, c, class)
			// Baseline learned WITHOUT the anomalous flow; corpus WITH it.
			corpus := append(append([]flow.Record(nil), c.Records...), anomalous)

			// The detector must actually flag it under the expected class.
			if !detectFlagsClass(profile, corpus, class) {
				t.Fatalf("precondition: baseline.Detect did not flag injected %s flow", class)
			}

			// Proposing with the clean baseline must exclude it from observed-good.
			prop := Propose(subj, corpus, profile, Existing{}, DefaultConfig())
			if prop.ExcludedAnomalyFlows < 1 {
				t.Fatalf("ExcludedAnomalyFlows = %d, want >= 1 (the injected %s flow)", prop.ExcludedAnomalyFlows, class)
			}

			switch class {
			case "new_port", "new_subnet":
				if ruleAllows(prop, anomalous) {
					t.Errorf("a proposed rule allows the excluded %s flow", class)
				}
			case "volume_spike":
				// Excluding the spike-hour flows must legitimize nothing: the
				// proposed rule set is identical to the spike-free baseline.
				if !sameRules(baselinePolicy.Rules, prop.Rules) {
					t.Errorf("the volume_spike burst changed the proposed policy; it must legitimize no new rule\nbaseline: %v\nwith spike: %v", baselinePolicy.Rules, prop.Rules)
				}
			}
		})
	}
}

func sameRules(a, b []inventory.FwRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if ruleSig(a[i]) != ruleSig(b[i]) {
			return false
		}
	}
	return true
}

func detectFlagsClass(profile baseline.Profile, corpus []flow.Record, class string) bool {
	for _, a := range baseline.Detect(profile, corpus, DefaultConfig().detectConfig()) {
		if string(a.Class) == class {
			return true
		}
	}
	return false
}

// ruleAllows dry-runs the single flow against the proposal and reports whether
// it would be allowed — the direct "no rule allows this flow" check.
func ruleAllows(prop Proposal, rec flow.Record) bool {
	rep := DryRun(prop, []flow.Record{rec}, DefaultConfig())
	return len(rep.WouldAllow) > 0
}

// TestPropose_CoverageThresholdLeavesTail proves the covering set states its
// coverage honestly and never rounds up: a heavy service plus a rare long-tail
// service (below the 0.5% tail) yields <100% coverage and a nonzero uncovered
// count, with no rule for the tail.
func TestPropose_CoverageThresholdLeavesTail(t *testing.T) {
	const guest = "guest:pve1:200"
	subj := Subject{GuestRef: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"}}
	var corpus []flow.Record
	// Heavy: 1000 flows of 1000 bytes to 443 (10.0.0.0/24). Total 1_000_000.
	for i := 0; i < 1000; i++ {
		corpus = append(corpus, flow.Record{SrcRef: guest, SrcIP: "10.0.0.5", DstIP: "10.0.0.10", At: baseEpoch + int64(i)*3600, Bytes: 1000, DstPort: 443, Proto: 6})
	}
	// Tail: one 100-byte flow to a rare port (0.01% of bytes) — below 0.5%.
	tail := flow.Record{SrcRef: guest, SrcIP: "10.0.0.5", DstIP: "10.7.7.7", At: baseEpoch + 5*3600, Bytes: 100, DstPort: 9, Proto: 17}
	corpus = append(corpus, tail)

	prop := Propose(subj, corpus, baseline.Profile{}, Existing{}, DefaultConfig())

	if prop.CoveragePct >= 100 {
		t.Errorf("coverage = %.4f%%, want < 100%% (a tail is left uncovered)", prop.CoveragePct)
	}
	if prop.CoveragePct < 99.5 {
		t.Errorf("coverage = %.4f%%, want >= 99.5%% (the heavy service is covered)", prop.CoveragePct)
	}
	if prop.UncoveredFlowCount != 1 {
		t.Errorf("UncoveredFlowCount = %d, want 1 (the tail flow)", prop.UncoveredFlowCount)
	}
	// The tail must NOT dry-run as allowed.
	rep := DryRun(prop, []flow.Record{tail}, DefaultConfig())
	if len(rep.WouldAllow) != 0 {
		t.Errorf("tail flow should not be allowed; got %+v", rep.WouldAllow)
	}
	if len(rep.WouldBlock) != 1 {
		t.Errorf("tail flow should be would-block; report=%+v", rep)
	}
}

// TestPropose_SuppressesRuleExistingPolicyAlreadyHas proves the planner does
// not propose a rule PVE effectively already has: a group the guest's current
// resolved view already ACCEPTs is suppressed (its direction stays governed),
// evaluated through the shared sim evaluator.
func TestPropose_SuppressesRuleExistingPolicyAlreadyHas(t *testing.T) {
	corpus := nasCorpus()
	subj := nasSubject()
	profile := baseline.Learn(corpus, subj.GuestRef.String(), baseline.Window{Start: baseEpoch, End: baseEpoch + 14*daySeconds})

	existing := &fw.ResolvedView{
		Guest:      subj.GuestRef,
		Active:     true,
		DefaultIn:  fw.DefaultPolicy{Direction: "in", Policy: "DROP", Origin: fw.OriginDefault},
		DefaultOut: fw.DefaultPolicy{Direction: "out", Policy: "DROP", Origin: fw.OriginDefault},
		Rules: []fw.ResolvedRule{{
			Origin: fw.OriginGuest, Pos: 0,
			Rule: inventory.FwRule{Direction: "out", Action: "ACCEPT", Proto: "tcp", Dport: "443", Dest: "10.0.0.0/24", Enabled: true},
		}},
	}

	prop := Propose(subj, corpus, profile, Existing{View: existing}, DefaultConfig())
	if prop.AlreadyCoveredGroups != 1 {
		t.Errorf("AlreadyCoveredGroups = %d, want 1", prop.AlreadyCoveredGroups)
	}
	for _, r := range prop.Rules {
		if r.Action == "ACCEPT" && r.Direction == "out" && r.Dport == "443" {
			t.Errorf("proposed a rule the existing policy already has: %q", ruleSig(r))
		}
	}
	// The out direction is still governed (still gets a trailing deny), and the
	// other outbound service (53) is still proposed.
	if !containsRuleSig(prop.Rules, "out ACCEPT udp dport=53 src= dst=10.0.0.0/24") {
		t.Errorf("suppressing 443 must not drop the unrelated 53 rule; rules=%v", prop.Rules)
	}
	if !containsRuleSig(prop.Rules, "out DROP  dport= src= dst=") {
		t.Errorf("out direction must stay governed by a trailing deny")
	}
}

func containsRuleSig(rules []inventory.FwRule, sig string) bool {
	for _, r := range rules {
		if ruleSig(r) == sig {
			return true
		}
	}
	return false
}

// TestPropose_NoiseCorpusReuse exercises T-1601's pure-noise corpus verbatim:
// it has no injected anomalies, so every flow is observed-good and the policy
// covers all of it (a sanity check that the reused fixture flows through the
// planner cleanly).
func TestPropose_NoiseCorpusReuse(t *testing.T) {
	c := loadT1601Corpus(t, "noise_corpus.json")
	profile := baseline.Learn(c.Records, c.Ref, c.Window)
	subj := Subject{GuestRef: inventory.MustParseRef(c.Ref)}
	prop := Propose(subj, c.Records, profile, Existing{}, DefaultConfig())
	if prop.ExcludedAnomalyFlows != 0 {
		t.Errorf("pure-noise corpus should exclude nothing, excluded %d", prop.ExcludedAnomalyFlows)
	}
	if prop.CoveragePct != 100 {
		t.Errorf("pure-noise corpus should be fully covered, got %.3f%%", prop.CoveragePct)
	}
	rep := DryRun(prop, c.Records, DefaultConfig())
	if len(rep.WouldBlock) != 0 || len(rep.CannotDetermine) != 0 {
		t.Errorf("self-dry-run of noise corpus must be clean; block=%d undecidable=%d", len(rep.WouldBlock), len(rep.CannotDetermine))
	}
}
