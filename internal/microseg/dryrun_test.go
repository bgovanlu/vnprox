package microseg

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestDryRun_SelfConsistency is AC2: dry-running a proposal against the exact
// corpus it was derived from reports zero would-block among observed-good flows
// — the training-corpus soundness proof.
func TestDryRun_SelfConsistency(t *testing.T) {
	corpus := nasCorpus()
	subj := nasSubject()
	profile := baseline.Learn(corpus, subj.GuestRef.String(), baseline.Window{Start: baseEpoch, End: baseEpoch + 14*daySeconds})
	prop := Propose(subj, corpus, profile, Existing{}, DefaultConfig())

	rep := DryRun(prop, corpus, DefaultConfig())
	if len(rep.WouldBlock) != 0 {
		t.Errorf("self-consistency: wouldBlock = %d, want 0: %+v", len(rep.WouldBlock), rep.WouldBlock)
	}
	if len(rep.CannotDetermine) != 0 {
		t.Errorf("self-consistency: cannotDetermine = %d, want 0: %+v", len(rep.CannotDetermine), rep.CannotDetermine)
	}
	if len(rep.WouldAllow) != len(corpus) {
		t.Errorf("wouldAllow = %d, want every observed-good flow (%d)", len(rep.WouldAllow), len(corpus))
	}
}

// TestDryRun_SelfConsistency_T1601Clean repeats the self-consistency proof on
// T-1601's clean corpus, reused verbatim, as a second independent data point.
func TestDryRun_SelfConsistency_T1601Clean(t *testing.T) {
	c := loadT1601Corpus(t, "clean_injected_corpus.json")
	profile := baseline.Learn(c.Records, c.Ref, c.Window)
	subj := Subject{GuestRef: inventory.MustParseRef(c.Ref)}
	prop := Propose(subj, c.Records, profile, Existing{}, DefaultConfig())

	rep := DryRun(prop, c.Records, DefaultConfig())
	if len(rep.WouldBlock) != 0 || len(rep.CannotDetermine) != 0 {
		t.Errorf("clean corpus self-consistency: block=%d undecidable=%d, want 0/0", len(rep.WouldBlock), len(rep.CannotDetermine))
	}
}

// TestDryRun_HeldOut is AC3: dry-running a training-derived policy against a
// held-out day reports a bounded, explicitly-stated would-block count, every
// entry traceable to the uncovered tail (a service never seen in training), not
// a synthesis bug.
func TestDryRun_HeldOut(t *testing.T) {
	train := nasCorpus()
	subj := nasSubject()
	profile := baseline.Learn(train, subj.GuestRef.String(), baseline.Window{Start: baseEpoch, End: baseEpoch + 14*daySeconds})
	prop := Propose(subj, train, profile, Existing{}, DefaultConfig())

	rep := DryRun(prop, nasHeldout(), DefaultConfig())

	// Exactly the two never-observed services block; the four legitimate ones
	// still pass.
	if len(rep.WouldBlock) != 2 {
		t.Fatalf("held-out wouldBlock = %d, want 2: %+v", len(rep.WouldBlock), rep.WouldBlock)
	}
	if len(rep.CannotDetermine) != 0 {
		t.Errorf("held-out cannotDetermine = %d, want 0", len(rep.CannotDetermine))
	}
	if len(rep.WouldAllow) != 4 {
		t.Errorf("held-out wouldAllow = %d, want 4 legitimate flows", len(rep.WouldAllow))
	}
	// Every would-block traces to a peer subnet no proposed ACCEPT covers.
	covered := coveredSubnets(prop)
	for _, fr := range rep.WouldBlock {
		if covered[fr.PeerSubnet] {
			t.Errorf("would-block flow %+v maps to a COVERED subnet — synthesis bug, not a tail flow", fr)
		}
	}
	wantBlocked := map[int]bool{3306: true, 8080: true}
	for _, fr := range rep.WouldBlock {
		if !wantBlocked[fr.Port] {
			t.Errorf("unexpected would-block port %d", fr.Port)
		}
	}
}

func coveredSubnets(prop Proposal) map[string]bool {
	out := map[string]bool{}
	for _, r := range prop.Rules {
		if r.Action != "ACCEPT" {
			continue
		}
		if r.Source != "" {
			out[r.Source] = true
		}
		if r.Dest != "" {
			out[r.Dest] = true
		}
	}
	return out
}

// TestDryRun_VolumeSpikeBurst_NeverAllowed completes AC4's third class: a burst
// of new traffic in one hour (the classic exfiltration signature) trips
// baseline's volume_spike detector; the planner excludes the spiking hour, so
// the burst's service — seen ONLY during the spike — gets no rule and dry-runs
// wouldBlock.
func TestDryRun_VolumeSpikeBurst_NeverAllowed(t *testing.T) {
	const guest = "guest:pve1:300"
	subj := Subject{GuestRef: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "300"}}

	// Baseline: 5 days of modest udp/53 at hour 3, learned WITHOUT the spike.
	var base []flow.Record
	for d := int64(0); d < 5; d++ {
		base = append(base, flow.Record{SrcRef: guest, SrcIP: "10.0.0.5", DstIP: "10.0.0.20", At: baseEpoch + d*daySeconds + 3*3600, Bytes: 1000, DstPort: 53, Proto: 17})
	}
	profile := baseline.Learn(base, guest, baseline.Window{Start: baseEpoch, End: baseEpoch + 5*daySeconds})

	// Day 5, hour 3: normal 53 continues AND a 60k-byte burst to a NEW port.
	spikeAt := baseEpoch + 5*daySeconds + 3*3600
	burst := flow.Record{SrcRef: guest, SrcIP: "10.0.0.5", DstIP: "10.0.0.20", At: spikeAt + 1, Bytes: 60000, DstPort: 9999, Proto: 6}
	corpus := append(append([]flow.Record(nil), base...),
		flow.Record{SrcRef: guest, SrcIP: "10.0.0.5", DstIP: "10.0.0.20", At: spikeAt, Bytes: 1000, DstPort: 53, Proto: 17},
		burst,
	)

	// The burst is genuinely a volume_spike scenario per T-1601's own detector.
	if !detectFlagsClass(profile, corpus, "volume_spike") {
		t.Fatalf("precondition: baseline.Detect did not flag a volume_spike for the burst hour")
	}

	prop := Propose(subj, corpus, profile, Existing{}, DefaultConfig())
	rep := DryRun(prop, []flow.Record{burst}, DefaultConfig())
	if len(rep.WouldAllow) != 0 {
		t.Errorf("the spiking burst must never be allowed; got %+v", rep.WouldAllow)
	}
	if len(rep.WouldBlock) != 1 {
		t.Errorf("the spiking burst must be would-block; report=%+v", rep)
	}
}

// TestDryRun_CannotDetermine proves the honesty contract's third bucket: a flow
// the evaluator cannot decide (an address-restricting rule against an
// unparseable peer IP) is reported CannotDetermine, never folded into
// wouldAllow.
func TestDryRun_CannotDetermine(t *testing.T) {
	corpus := nasCorpus()
	subj := nasSubject()
	profile := baseline.Learn(corpus, subj.GuestRef.String(), baseline.Window{Start: baseEpoch, End: baseEpoch + 14*daySeconds})
	prop := Propose(subj, corpus, profile, Existing{}, DefaultConfig())

	undecidable := flow.Record{SrcRef: subj.GuestRef.String(), SrcIP: "10.0.0.5", DstIP: "not-an-ip", At: baseEpoch, Bytes: 500, DstPort: 443, Proto: 6}
	rep := DryRun(prop, []flow.Record{undecidable}, DefaultConfig())

	if len(rep.WouldAllow) != 0 {
		t.Errorf("an undecidable flow must never be counted as allowed; got %+v", rep.WouldAllow)
	}
	if len(rep.CannotDetermine) != 1 {
		t.Fatalf("cannotDetermine = %d, want 1: %+v", len(rep.CannotDetermine), rep)
	}
	if rep.CannotDetermine[0].Reason == "" {
		t.Errorf("cannot-determine entry must name why evaluation was undecidable")
	}
}

// TestDryRun_UngovernedDirection proves a flow in a direction the proposal does
// not govern is reported Ungoverned, not silently blocked — the policy changes
// nothing for it, so classifying it as blocked would be a lie.
func TestDryRun_UngovernedDirection(t *testing.T) {
	c := loadT1601Corpus(t, "clean_injected_corpus.json") // outbound-only corpus
	profile := baseline.Learn(c.Records, c.Ref, c.Window)
	subj := Subject{GuestRef: inventory.MustParseRef(c.Ref)}
	prop := Propose(subj, c.Records, profile, Existing{}, DefaultConfig())

	// Sanity: the outbound-only corpus governs only "out".
	if got := prop.Directions; len(got) != 1 || got[0] != "out" {
		t.Fatalf("expected only 'out' governed, got %v", got)
	}
	inbound := flow.Record{DstRef: c.Ref, SrcIP: "10.0.0.99", DstIP: "10.0.0.5", At: c.Window.Start, Bytes: 500, DstPort: 22, Proto: 6}
	rep := DryRun(prop, []flow.Record{inbound}, DefaultConfig())
	if len(rep.Ungoverned) != 1 {
		t.Fatalf("ungoverned = %d, want 1: %+v", len(rep.Ungoverned), rep)
	}
	if len(rep.WouldBlock) != 0 {
		t.Errorf("an ungoverned-direction flow must not be reported would-block")
	}
}
