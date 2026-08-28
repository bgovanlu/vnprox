// SPDX-License-Identifier: Apache-2.0

package posture

import (
	"github.com/bgovanlu/vnprox/internal/failsim"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/microseg"
)

// world is a terse inventory-snapshot builder for posture tests, applying
// entities through a real inventory.Graph so the firewall resolver links guest
// rulesets exactly as production does (mirrors internal/failsim's own
// world_test helper).
type world struct {
	nodes  []inventory.Entity
	guests []inventory.Entity
	fw     []inventory.Entity
}

func newWorld() *world { return &world{} }

func (w *world) node(name string) *world {
	w.nodes = append(w.nodes, &inventory.Node{
		Ref:  inventory.Ref{Kind: inventory.KindNode, Node: name, ID: name},
		Name: name, Status: "online", Quorate: true,
	})
	return w
}

func (w *world) guest(node, vmid string) *world {
	w.guests = append(w.guests, &inventory.Guest{
		Ref:  inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: vmid},
		VMID: atoiSafe(vmid), Name: "vm" + vmid, Type: "qemu", Node: node, Status: "running",
	})
	return w
}

// clusterFw adds an enabled, empty cluster-scope ruleset so guest views resolve
// Active (no datacenter-off gate) without contributing any rules of its own.
func (w *world) clusterFw() *world {
	w.fw = append(w.fw, &inventory.FwRuleset{
		Ref:     inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"},
		Scope:   inventory.FwScopeCluster,
		Enabled: true,
	})
	return w
}

// guestFw adds an enabled guest-scope ruleset (id "guest/qemu/<vmid>", the
// collector's convention fw.BuildSnapshot parses) with the given rules.
func (w *world) guestFw(node, vmid string, rules ...inventory.FwRule) *world {
	w.fw = append(w.fw, &inventory.FwRuleset{
		Ref:     inventory.Ref{Kind: inventory.KindFwRuleset, Node: node, ID: "guest/qemu/" + vmid},
		Scope:   inventory.FwScopeGuest,
		Enabled: true,
		Rules:   rules,
	})
	return w
}

func (w *world) build() inventory.Snapshot {
	g := inventory.NewGraph()
	if len(w.nodes) > 0 {
		g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, w.nodes)
	}
	if len(w.guests) > 0 {
		g.ApplyPoll(inventory.SourcePVEGuest,
			inventory.Scope{Kinds: []inventory.Kind{inventory.KindGuest, inventory.KindGuestNic}}, w.guests)
	}
	if len(w.fw) > 0 {
		g.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{}, w.fw)
	}
	return g.Snapshot()
}

// microsegRule builds an ACCEPT inbound rule bearing microseg's marker comment
// — the trace an applied microsegmentation policy leaves in the inventory.
func microsegRule(pos int, source, dport string) inventory.FwRule {
	return inventory.FwRule{
		Direction: "in", Action: "ACCEPT", Proto: "tcp",
		Source: source, Dport: dport, Enabled: true, Pos: pos,
		Comment: microseg.RuleCommentPrefix + "observed tcp to " + source,
	}
}

// anyInAccept builds an inbound ACCEPT from any source on dport — an exposed
// port.
func anyInAccept(pos int, dport string) inventory.FwRule {
	return inventory.FwRule{
		Direction: "in", Action: "ACCEPT", Proto: "tcp",
		Source: "0.0.0.0/0", Dport: dport, Enabled: true, Pos: pos,
	}
}

// spofEntries builds len(sevs) SPOF entries with the given severities, enough
// to drive failsim.Score deterministically (Score reads only Impact.Severity).
func spofEntries(sevs ...string) []failsim.SPOFEntry {
	out := make([]failsim.SPOFEntry, 0, len(sevs))
	for i, s := range sevs {
		out = append(out, failsim.SPOFEntry{
			Ref:    inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond" + itoa(i)},
			Impact: failsim.Impact{Severity: s},
		})
	}
	return out
}

func factorByName(p Posture, name string) (Factor, bool) {
	for _, f := range p.Factors {
		if f.Name == name {
			return f, true
		}
	}
	return Factor{}, false
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
