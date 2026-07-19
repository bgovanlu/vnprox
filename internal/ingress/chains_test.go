package ingress

import "testing"

// fixtureGuestLookup resolves the fixed set of IPs this file's tests use to
// guest refs, mirroring internal/edge's own GuestLookup contract: an
// unrecognized IP resolves to nothing rather than a guess.
func fixtureGuestLookup(ip string) (string, bool, bool) {
	switch ip {
	case "10.0.0.5":
		return "guest/pve1/101", false, true
	case "10.0.0.6":
		return "guest/pve1/102", true, true // powered off
	default:
		return "", false, false
	}
}

// TestProjectChains_GoldenFullChain is T-1406 AC3's golden projection
// test: a port-forward whose IntIP matches a configured ingress_targets
// entry's own address renders as one connected WAN -> port-forward ->
// proxy guest -> backend guest chain, with every discovered backend
// resolved to its known guest ref.
func TestProjectChains_GoldenFullChain(t *testing.T) {
	portForwards := []PortForwardRef{
		{ID: "pf1", Node: "pve1", Proto: "tcp", IntIP: "10.0.0.20", ExtPort: 443, IntPort: 443, TargetGuestRef: "guest/pve1/100"},
	}
	targets := []TargetChainInput{
		{
			TargetID: "ing1", Address: "http://10.0.0.20:8404",
			State: ProxyState{Kind: KindHAProxy, Reachable: true, Backends: []Backend{
				{Address: "10.0.0.5:8080", Healthy: true},
				{Address: "10.0.0.6:8080", Healthy: false},
			}},
		},
	}

	chains := ProjectChains(portForwards, targets, fixtureGuestLookup)
	if len(chains) != 1 {
		t.Fatalf("expected exactly one chain, got %d: %+v", len(chains), chains)
	}
	c := chains[0]
	if c.PortForwardID != "pf1" || c.ProxyGuestRef != "guest/pve1/100" || c.TargetID != "ing1" || c.TargetKind != KindHAProxy {
		t.Fatalf("chain head = %+v, unexpected", c)
	}
	if len(c.Backends) != 2 {
		t.Fatalf("expected 2 backends in chain, got %+v", c.Backends)
	}
	if c.Backends[0].GuestRef != "guest/pve1/101" || !c.Backends[0].Healthy {
		t.Errorf("backend[0] = %+v, want guest/pve1/101 healthy", c.Backends[0])
	}
	if c.Backends[1].GuestRef != "guest/pve1/102" || c.Backends[1].Healthy {
		t.Errorf("backend[1] = %+v, want guest/pve1/102 unhealthy", c.Backends[1])
	}
}

// TestProjectChains_NoMatchProducesNoChain covers T-1406 AC3's negative
// case explicitly: a port-forward with no ingress_targets row pointed at
// its own IntIP never synthesizes a chain.
func TestProjectChains_NoMatchProducesNoChain(t *testing.T) {
	portForwards := []PortForwardRef{{ID: "pf1", IntIP: "10.0.0.99"}}
	targets := []TargetChainInput{{TargetID: "ing1", Address: "http://10.0.0.20:8404"}}
	chains := ProjectChains(portForwards, targets, fixtureGuestLookup)
	if len(chains) != 0 {
		t.Fatalf("expected no chains, got %+v", chains)
	}
}

// TestProjectChains_BackendCorrelatesToKnownGuestRef is T-1406 AC2's
// golden test: GET /ingress/status correlates a discovered backend address
// to a known guest ref.
func TestProjectChains_BackendCorrelatesToKnownGuestRef(t *testing.T) {
	portForwards := []PortForwardRef{{ID: "pf1", IntIP: "10.0.0.20"}}
	targets := []TargetChainInput{{
		TargetID: "ing1", Address: "10.0.0.20:8404",
		State: ProxyState{Backends: []Backend{{Address: "10.0.0.5:8080"}}},
	}}
	chains := ProjectChains(portForwards, targets, fixtureGuestLookup)
	if len(chains) != 1 || len(chains[0].Backends) != 1 {
		t.Fatalf("unexpected chains: %+v", chains)
	}
	if got := chains[0].Backends[0].GuestRef; got != "guest/pve1/101" {
		t.Fatalf("backend guest ref = %q, want guest/pve1/101", got)
	}
}

// TestProjectChains_NilLookupLeavesBackendsUnresolved covers the optional-
// dependency degrade-gracefully contract (GuestLookup nil-safe).
func TestProjectChains_NilLookupLeavesBackendsUnresolved(t *testing.T) {
	portForwards := []PortForwardRef{{ID: "pf1", IntIP: "10.0.0.20"}}
	targets := []TargetChainInput{{
		TargetID: "ing1", Address: "10.0.0.20:8404",
		State: ProxyState{Backends: []Backend{{Address: "10.0.0.5:8080"}}},
	}}
	chains := ProjectChains(portForwards, targets, nil)
	if len(chains) != 1 || chains[0].Backends[0].GuestRef != "" {
		t.Fatalf("expected unresolved guest ref with nil lookup, got %+v", chains)
	}
}

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"http://10.0.0.5:8404":  "10.0.0.5",
		"https://10.0.0.5":      "10.0.0.5",
		"10.0.0.6:8080":         "10.0.0.6",
		"10.0.0.7":              "10.0.0.7",
		"http://10.0.0.8:8404/": "10.0.0.8",
	}
	for in, want := range cases {
		if got := HostOnly(in); got != want {
			t.Errorf("HostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
