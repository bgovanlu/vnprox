package fwlog

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func guestRef100() inventory.Ref {
	return inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}
}

func rule(pos int, origin fw.Origin, group string, r inventory.FwRule) fw.ResolvedRule {
	return fw.ResolvedRule{Origin: origin, GroupName: group, Rule: r, Pos: pos}
}

func TestCorrelate_UnknownChain(t *testing.T) {
	e := Entry{Guest: false, Chain: "PVEFW-HOST-IN", Direction: "in", Action: "DROP"}
	c := Correlate(e, fw.ResolvedView{})
	if c.Status != StatusUnknownChain {
		t.Fatalf("Status = %s, want %s", c.Status, StatusUnknownChain)
	}
	if c.Reason == "" {
		t.Fatal("Reason must be set for a non-rule status")
	}
	if c.Rule != nil {
		t.Fatal("Rule must be nil for StatusUnknownChain")
	}
}

func TestCorrelate_DefaultPolicy(t *testing.T) {
	e := Entry{Guest: true, PolicyFallthrough: true, Direction: "in", Action: "DROP"}
	c := Correlate(e, fw.ResolvedView{Guest: guestRef100()})
	if c.Status != StatusDefaultPolicy {
		t.Fatalf("Status = %s, want %s", c.Status, StatusDefaultPolicy)
	}
	if c.Rule != nil {
		t.Fatal("Rule must be nil for a default-policy hit")
	}
}

func TestCorrelate_NoActionDeterminable(t *testing.T) {
	e := Entry{Guest: true, Direction: "in", Action: ""}
	c := Correlate(e, fw.ResolvedView{Guest: guestRef100()})
	if c.Status != StatusUnmatched {
		t.Fatalf("Status = %s, want %s", c.Status, StatusUnmatched)
	}
}

func TestCorrelate_UniqueMatch(t *testing.T) {
	resolved := fw.ResolvedView{
		Guest: guestRef100(),
		Rules: []fw.ResolvedRule{
			rule(0, fw.OriginCluster, "", inventory.FwRule{Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22"}),
			rule(1, fw.OriginGuest, "", inventory.FwRule{Enabled: true, Direction: "in", Action: "DROP"}),
		},
	}
	e := Entry{Guest: true, Direction: "in", Action: "DROP"}
	c := Correlate(e, resolved)
	if c.Status != StatusRule {
		t.Fatalf("Status = %s, want %s (reason: %s)", c.Status, StatusRule, c.Reason)
	}
	if c.Rule == nil || c.Rule.Pos != 1 || c.Rule.Origin != string(fw.OriginGuest) || c.Rule.GuestRef != guestRef100().String() {
		t.Fatalf("Rule = %+v, want pos=1 origin=guest guestRef=%s", c.Rule, guestRef100())
	}
}

func TestCorrelate_GroupOrigin(t *testing.T) {
	resolved := fw.ResolvedView{
		Guest: guestRef100(),
		Rules: []fw.ResolvedRule{
			rule(0, fw.OriginGroup, "webservers", inventory.FwRule{Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "80"}),
		},
	}
	e := Entry{Guest: true, Direction: "in", Action: "ACCEPT", Proto: "TCP", Dport: "80"}
	c := Correlate(e, resolved)
	if c.Status != StatusRule || c.Rule == nil || c.Rule.GroupName != "webservers" || c.Rule.Origin != string(fw.OriginGroup) {
		t.Fatalf("Correlate group-origin rule: status=%s rule=%+v", c.Status, c.Rule)
	}
}

func TestCorrelate_DisabledRuleIsNotACandidate(t *testing.T) {
	resolved := fw.ResolvedView{
		Guest: guestRef100(),
		Rules: []fw.ResolvedRule{
			rule(0, fw.OriginGuest, "", inventory.FwRule{Enabled: false, Direction: "in", Action: "DROP"}),
		},
	}
	e := Entry{Guest: true, Direction: "in", Action: "DROP"}
	c := Correlate(e, resolved)
	if c.Status != StatusUnmatched {
		t.Fatalf("Status = %s, want %s (a disabled rule must never be offered as a match)", c.Status, StatusUnmatched)
	}
}

func TestCorrelate_AmbiguousWhenUnnarrowable(t *testing.T) {
	resolved := fw.ResolvedView{
		Guest: guestRef100(),
		Rules: []fw.ResolvedRule{
			rule(0, fw.OriginCluster, "", inventory.FwRule{Enabled: true, Direction: "in", Action: "DROP"}),
			rule(1, fw.OriginGuest, "", inventory.FwRule{Enabled: true, Direction: "in", Action: "DROP"}),
		},
	}
	e := Entry{Guest: true, Direction: "in", Action: "DROP"} // no proto/port to disambiguate
	c := Correlate(e, resolved)
	if c.Status != StatusAmbiguous {
		t.Fatalf("Status = %s, want %s", c.Status, StatusAmbiguous)
	}
	if len(c.CandidatePositions) != 2 {
		t.Fatalf("CandidatePositions = %v, want 2 entries", c.CandidatePositions)
	}
	if c.Rule != nil {
		t.Fatal("Rule must be nil when ambiguous — never guess")
	}
	if c.Reason == "" {
		t.Fatal("Reason must explain the ambiguity")
	}
}

func TestCorrelate_NarrowedByProto(t *testing.T) {
	resolved := fw.ResolvedView{
		Guest: guestRef100(),
		Rules: []fw.ResolvedRule{
			rule(0, fw.OriginCluster, "", inventory.FwRule{Enabled: true, Direction: "in", Action: "DROP", Proto: "udp"}),
			rule(1, fw.OriginGuest, "", inventory.FwRule{Enabled: true, Direction: "in", Action: "DROP", Proto: "tcp"}),
		},
	}
	e := Entry{Guest: true, Direction: "in", Action: "DROP", Proto: "TCP"}
	c := Correlate(e, resolved)
	if c.Status != StatusRule || c.Rule == nil || c.Rule.Pos != 1 {
		t.Fatalf("Status/Rule = %s/%+v, want StatusRule at pos 1 (proto=tcp match)", c.Status, c.Rule)
	}
}

func TestCorrelate_NarrowedByDport(t *testing.T) {
	resolved := fw.ResolvedView{
		Guest: guestRef100(),
		Rules: []fw.ResolvedRule{
			rule(0, fw.OriginCluster, "", inventory.FwRule{Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "80,443"}),
			rule(1, fw.OriginGuest, "", inventory.FwRule{Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "8000:8100"}),
		},
	}
	e := Entry{Guest: true, Direction: "in", Action: "ACCEPT", Proto: "TCP", Dport: "8080"}
	c := Correlate(e, resolved)
	if c.Status != StatusRule || c.Rule == nil || c.Rule.Pos != 1 {
		t.Fatalf("Status/Rule = %s/%+v, want StatusRule at pos 1 (dport range 8000:8100 contains 8080)", c.Status, c.Rule)
	}
}

func TestCorrelate_WildcardRuleNeverEliminated(t *testing.T) {
	// A rule with no Proto/Dport set at all must remain a candidate even
	// after narrowing (it could be the actual match — see
	// narrowByProtoPort's doc comment) — so two such wildcard rules with
	// the same direction/action stay ambiguous, not falsely resolved.
	resolved := fw.ResolvedView{
		Guest: guestRef100(),
		Rules: []fw.ResolvedRule{
			rule(0, fw.OriginCluster, "", inventory.FwRule{Enabled: true, Direction: "in", Action: "ACCEPT"}),
			rule(1, fw.OriginGuest, "", inventory.FwRule{Enabled: true, Direction: "in", Action: "ACCEPT"}),
		},
	}
	e := Entry{Guest: true, Direction: "in", Action: "ACCEPT", Proto: "TCP", Dport: "22"}
	c := Correlate(e, resolved)
	if c.Status != StatusAmbiguous {
		t.Fatalf("Status = %s, want %s", c.Status, StatusAmbiguous)
	}
}

func TestDportMatches(t *testing.T) {
	tests := []struct {
		configured, logged string
		want               bool
	}{
		{"80", "80", true},
		{"80", "81", false},
		{"80,443", "443", true},
		{"8000:8100", "8050", true},
		{"8000:8100", "9000", false},
		{"http", "80", false}, // service-name aliases are not resolved here — conservative "no narrowing"
	}
	for _, tt := range tests {
		if got := dportMatches(tt.configured, tt.logged); got != tt.want {
			t.Errorf("dportMatches(%q, %q) = %v, want %v", tt.configured, tt.logged, got, tt.want)
		}
	}
}
