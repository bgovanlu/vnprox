// SPDX-License-Identifier: Apache-2.0

package collect_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/sim"
)

// TestSim_FirewallDifferential is T-503 acceptance criterion 2: the
// engine's firewall-only verdicts must agree with an INDEPENDENT
// interpretation of the T-501 resolved view (fw.Resolve) on every fixture
// guest pair. It is script-generated (an exhaustive sweep over every ordered
// guest pair × a probe set) rather than hand-written cases, run against the
// three-node fixture the card names plus the firewall-scenarios fixture
// (whose guests carry real per-guest rulesets, giving the sweep teeth).
//
// The reference evaluator (refFirewallVerdict) re-walks fw.Resolve with a
// separately-coded matcher, so a bug in the engine's ordering / direction
// filtering / default-policy handling would surface as a disagreement.
func TestSim_FirewallDifferential(t *testing.T) {
	for _, fixture := range []string{fixtureThreeNode, fixtureFirewallScenarios} {
		fixture := fixture
		t.Run(shortName(fixture), func(t *testing.T) {
			snap := convergedSnapshot(t, fixture)
			fwSnap := fw.BuildSnapshot(snap.All())
			engine := sim.NewEngine(sim.Input{Inventory: snap})

			nics := guestNics(snap)
			if len(nics) < 2 {
				t.Skipf("fixture %s has %d guest NICs; need >= 2 for a pair sweep", fixture, len(nics))
			}

			probes := []struct {
				proto string
				port  int
			}{
				{"tcp", 22}, {"tcp", 80}, {"tcp", 443}, {"tcp", 8080},
				{"udp", 53}, {"tcp", 3306}, {"tcp", 1234}, {"icmp", 0},
			}

			pairs, checks := 0, 0
			for _, a := range nics {
				for _, b := range nics {
					if a.GetRef() == b.GetRef() {
						continue
					}
					pairs++
					for _, p := range probes {
						res := engine.Simulate(sim.Request{
							Src:   sim.Endpoint{Kind: sim.EndpointGuestNic, NicRef: a.GetRef()},
							Dst:   sim.Endpoint{Kind: sim.EndpointGuestNic, NicRef: b.GetRef()},
							Proto: p.proto, Port: p.port,
						})
						if res.Verdict == sim.VerdictUnreachable {
							continue // reachability, not a firewall-only question
						}
						want := refFirewallVerdict(fwSnap, a, b, p.proto, p.port)
						got := res.Verdict
						checks++
						if got != want {
							t.Errorf("pair %s -> %s %s/%d: engine=%s ref=%s\n  blocking=%+v",
								a.GetRef(), b.GetRef(), p.proto, p.port, got, want, res.BlockingRule)
						}
					}
				}
			}
			t.Logf("%s: swept %d ordered guest pairs, %d firewall-only checks", shortName(fixture), pairs, checks)
			if checks == 0 {
				t.Fatal("no firewall-only checks executed")
			}
		})
	}
}

// --- reference evaluator (independent of internal/sim's own matcher) -------

func refFirewallVerdict(snap fw.Snapshot, src, dst *inventory.GuestNic, proto string, port int) sim.Verdict {
	if out := refDir(snap, src, "out", proto, port); out != sim.VerdictAllow {
		return out
	}
	return refDir(snap, dst, "in", proto, port)
}

func refDir(snap fw.Snapshot, nic *inventory.GuestNic, dir, proto string, port int) sim.Verdict {
	if !nic.Firewall {
		return sim.VerdictAllow // per-NIC firewall off => passthrough
	}
	view, err := fw.Resolve(snap, nic.Guest)
	if err != nil {
		return sim.VerdictIndeterminate
	}
	if !view.Active {
		return sim.VerdictAllow // scope disabled => passthrough
	}
	for _, rr := range view.Rules {
		r := rr.Rule
		if r.Direction == "group" || !r.Enabled || r.Direction != dir {
			continue
		}
		switch refMatch(r, proto, port) {
		case matchNo:
			continue
		case matchUnknown:
			return sim.VerdictIndeterminate
		case matchYes:
			return actionToVerdict(r.Action)
		}
	}
	def := view.DefaultIn
	if dir == "out" {
		def = view.DefaultOut
	}
	return actionToVerdict(def.Policy)
}

type refMatchState int

const (
	matchNo refMatchState = iota
	matchYes
	matchUnknown
)

// refMatch mirrors the engine's evaluation ORDER (proto/port first, then
// address) but is coded independently: any address/interface constraint on a
// port-matching rule is treated as undecidable (the sweep supplies no guest
// IPs), exactly as the engine does.
func refMatch(r inventory.FwRule, proto string, port int) refMatchState {
	pp := refProtoPort(r, proto, port)
	if pp != matchYes {
		return pp
	}
	if r.Source != "" || r.Dest != "" || r.Iface != "" {
		return matchUnknown
	}
	return matchYes
}

func refProtoPort(r inventory.FwRule, proto string, port int) refMatchState {
	if r.Macro != "" {
		m, ok := fw.MacroExpansion(r.Macro)
		if !ok {
			return matchUnknown
		}
		unknown := false
		for _, mp := range m.Ports {
			if mp.Proto != "" && !strings.EqualFold(mp.Proto, proto) && proto != "" {
				continue
			}
			switch refPort(mp.Dport, port) {
			case matchYes:
				return matchYes
			case matchUnknown:
				unknown = true
			}
		}
		if unknown {
			return matchUnknown
		}
		return matchNo
	}
	if r.Proto != "" && proto != "" && !strings.EqualFold(r.Proto, proto) {
		return matchNo
	}
	return refPort(r.Dport, port)
}

func refPort(spec string, port int) refMatchState {
	if spec == "" {
		return matchYes
	}
	if port == 0 {
		return matchUnknown
	}
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		lo, hi, ok := refPortToken(tok)
		if !ok {
			return matchUnknown
		}
		if port >= lo && port <= hi {
			return matchYes
		}
	}
	return matchNo
}

func refPortToken(tok string) (int, int, bool) {
	if i := strings.IndexAny(tok, ":-"); i >= 0 {
		a := atoiSafe(tok[:i])
		b := atoiSafe(tok[i+1:])
		if a < 0 || b < 0 {
			return 0, 0, false
		}
		if a > b {
			a, b = b, a
		}
		return a, b, true
	}
	n := atoiSafe(tok)
	if n < 0 {
		return 0, 0, false
	}
	return n, n, true
}

func atoiSafe(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return -1
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func actionToVerdict(action string) sim.Verdict {
	switch action {
	case "ACCEPT":
		return sim.VerdictAllow
	case "DROP", "REJECT":
		return sim.VerdictDeny
	default:
		return sim.VerdictIndeterminate
	}
}

// --- helpers ---------------------------------------------------------------

func convergedSnapshot(t testing.TB, fixture string) inventory.Snapshot {
	t.Helper()
	srv := loadFixtureServer(t, fixture)
	c, graph, _ := newTestCollector(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.RunPVELoop(ctx) }()
	waitFor(t, 3*time.Second, "guests + firewall to converge", func() bool {
		s := graph.Snapshot()
		return len(guestNics(s)) > 0 && fw.BuildSnapshot(s.All()).Cluster != nil
	})
	return graph.Snapshot()
}

func guestNics(snap inventory.Snapshot) []*inventory.GuestNic {
	var out []*inventory.GuestNic
	for _, e := range snap.All() {
		if n, ok := e.(*inventory.GuestNic); ok {
			out = append(out, n)
		}
	}
	return out
}

func shortName(path string) string {
	i := strings.LastIndex(path, "/")
	return strings.TrimSuffix(path[i+1:], ".yaml")
}
