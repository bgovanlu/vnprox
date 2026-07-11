package sim

import "github.com/bgovanlu/vnprox/internal/inventory"

// fwBuild is the workhorse for firewall-category cases: two guests on one
// bridge/node (firewall on, IPs known), with the given cluster defaults +
// rules and per-guest rules. opts pass cluster aliases/ipsets/groups.
func fwBuild(cIn, cOut string, cRules, g100r, g101r []inventory.FwRule, opts ...func(*inventory.FwRuleset)) func() Input {
	return func() Input {
		w := fwWorld()
		w.clusterFw(true, cIn, cOut, cRules, opts...)
		w.guestFw("pve1", "100", true, "", "", g100r)
		w.guestFw("pve1", "101", true, "", "", g101r)
		return w.build()
	}
}

func rules(rs ...inventory.FwRule) []inventory.FwRule { return rs }

// --- category: firewall enforcement points --------------------------------

func firewallEnforcementCases() []simCase {
	cat := "fw-enforcement"
	r22 := req(guestEP(g100), guestEP(g101), "tcp", 22)
	return []simCase{
		{name: "cluster-in-drop", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in", blockingOrigin: "cluster", blockingAction: "DROP",
			wantCaveats: []string{CodeFwClusterHostGuest},
			build:       fwBuild("ACCEPT", "ACCEPT", rules(rule(0, "in", "DROP")), nil, nil), req: r22},
		{name: "cluster-out-drop", category: cat, want: VerdictDeny,
			blockingPoint: "source-guest-out", blockingOrigin: "cluster", blockingAction: "DROP",
			build: fwBuild("ACCEPT", "ACCEPT", rules(rule(0, "out", "DROP")), nil, nil), req: r22},
		{name: "guest-in-drop", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in", blockingOrigin: "guest", blockingAction: "DROP",
			build: fwBuild("ACCEPT", "ACCEPT", nil, nil, rules(rule(0, "in", "DROP"))), req: r22},
		{name: "guest-out-drop", category: cat, want: VerdictDeny,
			blockingPoint: "source-guest-out", blockingOrigin: "guest", blockingAction: "DROP",
			build: fwBuild("ACCEPT", "ACCEPT", nil, rules(rule(0, "out", "DROP")), nil), req: r22},
		{name: "group-in-drop", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in", blockingOrigin: "group",
			build: fwBuild("ACCEPT", "ACCEPT", nil, nil, rules(groupRef("blockin")),
				withGroups(inventory.FwGroup{Name: "blockin", Rules: rules(rule(0, "in", "DROP"))})), req: r22},
		{name: "group-out-drop", category: cat, want: VerdictDeny,
			blockingPoint: "source-guest-out", blockingOrigin: "group",
			build: fwBuild("ACCEPT", "ACCEPT", nil, rules(groupRef("blockout")), nil,
				withGroups(inventory.FwGroup{Name: "blockout", Rules: rules(rule(0, "out", "DROP"))})), req: r22},
		{name: "reject-action", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in", blockingAction: "REJECT",
			build: fwBuild("ACCEPT", "ACCEPT", nil, nil, rules(rule(0, "in", "REJECT"))), req: r22},
		{name: "accept-rule-matches-port", category: cat, want: VerdictAllow,
			build: fwBuild("DROP", "ACCEPT", nil, nil, rules(rule(0, "in", "ACCEPT", proto("tcp"), dport("22")))), req: r22},
		{name: "accept-rule-wrong-port-falls-to-default-drop", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in",
			build:         fwBuild("DROP", "ACCEPT", nil, nil, rules(rule(0, "in", "ACCEPT", proto("tcp"), dport("80")))), req: r22},
		{name: "earlier-accept-shadows-later-drop", category: cat, want: VerdictAllow,
			build: fwBuild("DROP", "ACCEPT", nil, nil,
				rules(rule(0, "in", "ACCEPT", proto("tcp"), dport("22")), rule(1, "in", "DROP", proto("tcp"), dport("22")))), req: r22},
		{name: "disabled-rule-skipped", category: cat, want: VerdictAllow,
			build: fwBuild("DROP", "ACCEPT", nil, nil,
				rules(rule(0, "in", "DROP", disabled(), proto("tcp"), dport("22")), rule(1, "in", "ACCEPT", proto("tcp"), dport("22")))), req: r22},
		{name: "source-out-passes-then-dest-in-drops", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in",
			build: fwBuild("ACCEPT", "ACCEPT", nil,
				rules(rule(0, "out", "ACCEPT")), rules(rule(0, "in", "DROP"))), req: r22},
		{name: "wrong-direction-rule-ignored", category: cat, want: VerdictAllow,
			// an OUT drop on the dest guest must not block inbound traffic.
			build: fwBuild("ACCEPT", "ACCEPT", nil, nil, rules(rule(0, "out", "DROP"))), req: r22},
	}
}

// --- category: macros ------------------------------------------------------

func macroCases() []simCase {
	cat := "macro"
	acceptMacro := func(m string) func() Input {
		return fwBuild("DROP", "ACCEPT", nil, nil, rules(rule(0, "in", "ACCEPT", macro(m))))
	}
	return []simCase{
		{name: "http-accept-tcp80", category: cat, want: VerdictAllow,
			build: acceptMacro("HTTP"), req: req(guestEP(g100), guestEP(g101), "tcp", 80)},
		{name: "http-not-tcp443", category: cat, want: VerdictDeny, blockingPoint: "dest-guest-in",
			build: acceptMacro("HTTP"), req: req(guestEP(g100), guestEP(g101), "tcp", 443)},
		{name: "https-accept-tcp443", category: cat, want: VerdictAllow,
			build: acceptMacro("HTTPS"), req: req(guestEP(g100), guestEP(g101), "tcp", 443)},
		{name: "ssh-accept-tcp22", category: cat, want: VerdictAllow,
			build: acceptMacro("SSH"), req: req(guestEP(g100), guestEP(g101), "tcp", 22)},
		{name: "dns-accept-udp53", category: cat, want: VerdictAllow,
			build: acceptMacro("DNS"), req: req(guestEP(g100), guestEP(g101), "udp", 53)},
		{name: "dns-accept-tcp53", category: cat, want: VerdictAllow,
			build: acceptMacro("DNS"), req: req(guestEP(g100), guestEP(g101), "tcp", 53)},
		{name: "vnc-range-accept-5901", category: cat, want: VerdictAllow,
			build: acceptMacro("VNC"), req: req(guestEP(g100), guestEP(g101), "tcp", 5901)},
		{name: "drop-macro-http-tcp80", category: cat, want: VerdictDeny, blockingPoint: "dest-guest-in",
			build: fwBuild("ACCEPT", "ACCEPT", nil, nil, rules(rule(0, "in", "DROP", macro("HTTP")))),
			req:   req(guestEP(g100), guestEP(g101), "tcp", 80)},
		{name: "unknown-macro-indeterminate", category: cat, want: VerdictIndeterminate,
			wantCaveats: []string{CodeNotEvaluated},
			build:       fwBuild("ACCEPT", "ACCEPT", nil, nil, rules(rule(0, "in", "DROP", macro("NoSuchMacro")))),
			req:         req(guestEP(g100), guestEP(g101), "tcp", 80)},
	}
}

// --- category: alias / ipset objects --------------------------------------

func objectCases() []simCase {
	cat := "objects"
	mgmtAlias := withAliases(inventory.FwAlias{Name: "mgmt_net", CIDR: "10.0.0.0/24"})
	otherAlias := withAliases(inventory.FwAlias{Name: "other_net", CIDR: "10.9.9.0/24"})
	blocklist := withIPSets(inventory.FwIPSet{Name: "blocklist", Entries: []inventory.FwIPSetEntry{{CIDR: "10.0.0.10/32"}}})
	trusted := withIPSets(inventory.FwIPSet{Name: "trusted", Entries: []inventory.FwIPSetEntry{
		{CIDR: "10.0.0.0/24"}, {CIDR: "10.0.0.10/32", NoMatch: true}}})

	return []simCase{
		{name: "alias-source-match-allow", category: cat, want: VerdictAllow,
			build: fwBuild("DROP", "ACCEPT", nil, nil, rules(rule(0, "in", "ACCEPT", source("mgmt_net"))), mgmtAlias),
			req:   req(guestEP(g100), guestEP(g101), "tcp", 22)},
		{name: "alias-source-nomatch-default-drop", category: cat, want: VerdictDeny, blockingPoint: "dest-guest-in",
			build: fwBuild("DROP", "ACCEPT", nil, nil, rules(rule(0, "in", "ACCEPT", source("other_net"))), otherAlias),
			req:   req(guestEP(g100), guestEP(g101), "tcp", 22)},
		{name: "ipset-drop-member", category: cat, want: VerdictDeny, blockingPoint: "dest-guest-in",
			// src is guest100 (10.0.0.10) which is in +blocklist.
			build: fwBuild("ACCEPT", "ACCEPT", nil, nil, rules(rule(0, "in", "DROP", source("+blocklist"))), blocklist),
			req:   req(guestEP(g100), guestEP(g101), "tcp", 22)},
		{name: "ipset-drop-nonmember-allow", category: cat, want: VerdictAllow,
			// src is guest101 (10.0.0.11), not in +blocklist; rule lives on the
			// dest guest (100); default ACCEPT.
			build: fwBuild("ACCEPT", "ACCEPT", nil, rules(rule(0, "in", "DROP", source("+blocklist"))), nil, blocklist),
			req:   req(guestEP(g101), guestEP(g100), "tcp", 22)},
		{name: "ipset-nomatch-excludes-member", category: cat, want: VerdictDeny, blockingPoint: "dest-guest-in",
			// guest100 (10.0.0.10) is excluded from +trusted by the nomatch
			// entry, so the ACCEPT does not fire and default DROP wins.
			build: fwBuild("DROP", "ACCEPT", nil, nil, rules(rule(0, "in", "ACCEPT", source("+trusted"))), trusted),
			req:   req(guestEP(g100), guestEP(g101), "tcp", 22)},
		{name: "ipset-trusted-included-member-allow", category: cat, want: VerdictAllow,
			// guest101 (10.0.0.11) is in 10.0.0.0/24 and not excluded; rule on
			// the dest guest (100).
			build: fwBuild("DROP", "ACCEPT", nil, rules(rule(0, "in", "ACCEPT", source("+trusted"))), nil, trusted),
			req:   req(guestEP(g101), guestEP(g100), "tcp", 22)},
		{name: "unknown-alias-indeterminate", category: cat, want: VerdictIndeterminate,
			wantCaveats: []string{CodeNotEvaluated},
			build:       fwBuild("ACCEPT", "ACCEPT", nil, nil, rules(rule(0, "in", "DROP", source("ghost_alias")))),
			req:         req(guestEP(g100), guestEP(g101), "tcp", 22)},
		{name: "unknown-ipset-indeterminate", category: cat, want: VerdictIndeterminate,
			wantCaveats: []string{CodeNotEvaluated},
			build:       fwBuild("ACCEPT", "ACCEPT", nil, nil, rules(rule(0, "in", "DROP", source("+ghostset")))),
			req:         req(guestEP(g100), guestEP(g101), "tcp", 22)},
		{name: "alias-in-dest-field-match", category: cat, want: VerdictAllow,
			build: fwBuild("DROP", "ACCEPT", nil, nil, rules(rule(0, "in", "ACCEPT", dest("mgmt_net"))), mgmtAlias),
			req:   req(guestEP(g100), guestEP(g101), "tcp", 22)},
	}
}

// --- category: default policy fallthrough ---------------------------------

func defaultPolicyCases() []simCase {
	cat := "default-policy"
	r := req(guestEP(g100), guestEP(g101), "tcp", 22)
	// buildWithGuestPolicies gives guests 100/101 enabled rulesets, using an
	// explicit [in,out] default policy where supplied and empty (cluster
	// fallback) otherwise.
	buildWithGuestPolicies := func(cIn, cOut string, explicit map[string][2]string) func() Input {
		return func() Input {
			w := fwWorld()
			w.clusterFw(true, cIn, cOut, nil)
			for _, vmid := range []string{"100", "101"} {
				p := explicit[vmid]
				w.guestFw("pve1", vmid, true, p[0], p[1], nil)
			}
			return w.build()
		}
	}
	return []simCase{
		{name: "cluster-default-in-drop-denies", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in", blockingAction: "DROP", blockingOrigin: "cluster",
			build: fwBuild("DROP", "ACCEPT", nil, nil, nil), req: r},
		{name: "cluster-default-in-accept-allows", category: cat, want: VerdictAllow,
			build: fwBuild("ACCEPT", "ACCEPT", nil, nil, nil), req: r},
		{name: "cluster-default-out-drop-denies-at-source", category: cat, want: VerdictDeny,
			blockingPoint: "source-guest-out", blockingAction: "DROP", blockingOrigin: "cluster",
			build: fwBuild("ACCEPT", "DROP", nil, nil, nil), req: r},
		{name: "cluster-default-in-reject", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in", blockingAction: "REJECT",
			build: fwBuild("REJECT", "ACCEPT", nil, nil, nil), req: r},
		{name: "hard-default-drop-in-when-unset", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in", blockingAction: "DROP", blockingOrigin: "default",
			build: fwBuild("", "ACCEPT", nil, nil, nil), req: r},
		{name: "guest-default-in-drop-overrides-cluster-accept", category: cat, want: VerdictDeny,
			blockingPoint: "dest-guest-in", blockingAction: "DROP", blockingOrigin: "guest",
			build: buildWithGuestPolicies("ACCEPT", "ACCEPT", map[string][2]string{"101": {"DROP", ""}}), req: r},
		{name: "guest-default-in-accept-overrides-cluster-drop", category: cat, want: VerdictAllow,
			build: buildWithGuestPolicies("DROP", "ACCEPT", map[string][2]string{"101": {"ACCEPT", ""}}), req: r},
		{name: "hard-default-out-accept-allows-egress", category: cat, want: VerdictAllow,
			// nothing set anywhere; hard default out is ACCEPT, in ACCEPT set to isolate egress.
			build: fwBuild("ACCEPT", "", nil, nil, nil), req: r},
	}
}
