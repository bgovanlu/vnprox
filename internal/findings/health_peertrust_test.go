package findings

import (
	"strings"
	"testing"
)

type staticPeerTrust struct{ rep PeerTrustReport }

func (s staticPeerTrust) PeerTrust() PeerTrustReport { return s.rep }

func pinnedReport(peers ...PeerTrustStatus) PeerTrustReport {
	return PeerTrustReport{
		LocalNode: "pve1",
		Mode:      "cluster-ca",
		CAFile:    "/etc/pve/pve-root-ca.pem",
		Scheme:    "https",
		Pinned:    true,
		Peers:     peers,
	}
}

func byCheck(fs []Finding) map[string]Finding {
	out := map[string]Finding{}
	for _, f := range fs {
		out[f.Check] = f
	}
	return out
}

// TestPeerTrustFindings_NilProviderIsSilent — the degradation every optional
// findings seam uses.
func TestPeerTrustFindings_NilProviderIsSilent(t *testing.T) {
	if got := peerTrustFindings(nil, newDebouncer()); len(got) != 0 {
		t.Fatalf("nil provider produced %d findings, want 0", len(got))
	}
}

// TestPeerTrustFindings_HealthyClusterIsSilent — a pinned daemon whose peers
// all verify raises nothing, across several cycles.
func TestPeerTrustFindings_HealthyClusterIsSilent(t *testing.T) {
	db := newDebouncer()
	prov := staticPeerTrust{pinnedReport(
		PeerTrustStatus{Node: "pve2", Addr: "10.0.0.2:8007", State: PeerStateOK},
		PeerTrustStatus{Node: "pve3", Addr: "10.0.0.3:8007", State: PeerStateOK},
	)}
	for i := 0; i < 4; i++ {
		if got := peerTrustFindings(prov, db); len(got) != 0 {
			t.Fatalf("cycle %d: got %+v, want no findings", i, got)
		}
	}
}

// TestPeerTrustFindings_AC5_UntrustedAndUnreachableAreDifferentFindings is
// T-1906 AC5: an operator must be able to tell a network problem from an
// attack. The two peers below differ *only* in why they could not be talked
// to, and they must produce different checks, different severities, and
// different wording.
func TestPeerTrustFindings_AC5_UntrustedAndUnreachableAreDifferentFindings(t *testing.T) {
	db := newDebouncer()
	prov := staticPeerTrust{pinnedReport(
		PeerTrustStatus{Node: "pve2", Addr: "10.0.0.2:8007", State: PeerStateUntrusted, Error: "x509: certificate signed by unknown authority"},
		PeerTrustStatus{Node: "pve3", Addr: "10.0.0.3:8007", State: PeerStateUnreachable, Error: "connect: connection refused"},
	)}

	// The untrusted finding is hysteresis-exempt: it fires on the first cycle.
	first := peerTrustFindings(prov, db)
	if len(first) != 1 || first[0].Check != CheckPeerUntrusted {
		t.Fatalf("cycle 1 = %+v, want exactly the untrusted finding (a security signal is not debounced)", first)
	}
	// The unreachable finding is debounced like every other continuously
	// recomputed signal, so it appears on the second cycle.
	got := byCheck(peerTrustFindings(prov, db))
	if len(got) != 2 {
		t.Fatalf("cycle 2 produced %d distinct checks, want 2", len(got))
	}

	untrusted, ok := got[CheckPeerUntrusted]
	if !ok {
		t.Fatal("no peer_untrusted finding")
	}
	unreachable, ok := got[CheckPeerUnreachable]
	if !ok {
		t.Fatal("no peer_unreachable finding")
	}

	if untrusted.ID == unreachable.ID {
		t.Fatal("the two findings must have distinct ids")
	}
	if untrusted.ID != "peer:peer_untrusted|pve2" || unreachable.ID != "peer:peer_unreachable|pve3" {
		t.Fatalf("ids = %q / %q, want stable peer:<check>|<node> ids", untrusted.ID, unreachable.ID)
	}
	if untrusted.Source != SourcePeer || unreachable.Source != SourcePeer {
		t.Fatalf("sources = %q / %q, want %q", untrusted.Source, unreachable.Source, SourcePeer)
	}
	if untrusted.Severity != SeverityError {
		t.Errorf("untrusted severity = %q, want %q — a certificate that does not verify outranks a dead node", untrusted.Severity, SeverityError)
	}
	if unreachable.Severity != SeverityWarning {
		t.Errorf("unreachable severity = %q, want %q", unreachable.Severity, SeverityWarning)
	}
	if untrusted.Nodes[0] != "pve2" || unreachable.Nodes[0] != "pve3" {
		t.Errorf("nodes = %v / %v", untrusted.Nodes, unreachable.Nodes)
	}
	if untrusted.Fixable || unreachable.Fixable {
		t.Error("a trust problem is never auto-fixable")
	}

	// The wording has to carry the distinction too — the finding stream is
	// read by a human, and "peer X is having trouble" would be the failure
	// this AC exists to prevent.
	if !strings.Contains(untrusted.Detail, "did not verify against the pinned cluster CA") ||
		!strings.Contains(untrusted.Detail, "not a connectivity problem") {
		t.Errorf("untrusted detail does not name certificate verification: %q", untrusted.Detail)
	}
	if !strings.Contains(unreachable.Detail, "nothing responded") ||
		!strings.Contains(unreachable.Detail, "certificate was never in question") {
		t.Errorf("unreachable detail does not distinguish itself from a trust failure: %q", unreachable.Detail)
	}
}

// TestPeerTrustFindings_UnreachableDebouncesAndClears — a single missed poll
// must not raise, and recovery must clear.
func TestPeerTrustFindings_UnreachableDebouncesAndClears(t *testing.T) {
	db := newDebouncer()
	down := staticPeerTrust{pinnedReport(PeerTrustStatus{Node: "pve2", Addr: "10.0.0.2:8007", State: PeerStateUnreachable, Error: "timeout"})}
	up := staticPeerTrust{pinnedReport(PeerTrustStatus{Node: "pve2", Addr: "10.0.0.2:8007", State: PeerStateOK})}

	if got := peerTrustFindings(down, db); len(got) != 0 {
		t.Fatalf("cycle 1: got %+v, want 0 (one missed poll must not fire)", got)
	}
	if got := peerTrustFindings(down, db); len(got) != 1 {
		t.Fatalf("cycle 2: got %+v, want 1", got)
	}
	if got := peerTrustFindings(up, db); len(got) != 0 {
		t.Fatalf("recovery cycle 1: got %+v, want 0", got)
	}
	if got := peerTrustFindings(up, db); len(got) != 0 {
		t.Fatalf("recovery cycle 2: got %+v, want 0", got)
	}
}

// TestPeerTrustFindings_UntrustedNeverDebounces — the security signal fires
// immediately and keeps firing while it is true; it also clears when the peer
// recovers, so it does not become permanent noise.
func TestPeerTrustFindings_UntrustedNeverDebounces(t *testing.T) {
	db := newDebouncer()
	bad := staticPeerTrust{pinnedReport(PeerTrustStatus{Node: "pve2", State: PeerStateUntrusted, Error: "x509: unknown authority"})}
	for i := 0; i < 3; i++ {
		if got := peerTrustFindings(bad, db); len(got) != 1 || got[0].Check != CheckPeerUntrusted {
			t.Fatalf("cycle %d: got %+v, want a single peer_untrusted finding", i, got)
		}
	}
	good := staticPeerTrust{pinnedReport(PeerTrustStatus{Node: "pve2", State: PeerStateOK})}
	if got := peerTrustFindings(good, db); len(got) != 0 {
		t.Fatalf("after recovery: got %+v, want 0", got)
	}
}

// TestPeerTrustFindings_PostureIsReported covers T-1906's degradation path
// being *visible*, not only logged: every weakened posture raises its own
// finding, and the default one raises none.
func TestPeerTrustFindings_PostureIsReported(t *testing.T) {
	cases := []struct {
		name       string
		wantID     string
		wantSev    string
		wantDetail string
		rep        PeerTrustReport
	}{
		{
			name:       "system pool escape hatch",
			rep:        PeerTrustReport{LocalNode: "pve1", Mode: "system", CAFile: "/etc/pve/pve-root-ca.pem", Scheme: "https"},
			wantID:     "peer:peer_trust_degraded|unpinned",
			wantSev:    SeverityWarning,
			wantDetail: "any publicly-trusted CA is accepted as a peer",
		},
		{
			name:       "insecure escape hatch",
			rep:        PeerTrustReport{LocalNode: "pve1", Mode: "insecure", Scheme: "https"},
			wantID:     "peer:peer_trust_degraded|unverified",
			wantSev:    SeverityError,
			wantDetail: "not checked at all",
		},
		{
			name:       "caller-supplied http client",
			rep:        PeerTrustReport{LocalNode: "pve1", Mode: "external", Scheme: "https"},
			wantID:     "peer:peer_trust_degraded|unpinned",
			wantSev:    SeverityWarning,
			wantDetail: "caller-supplied HTTP client",
		},
		{
			name:       "anchor unreadable (fail closed)",
			rep:        PeerTrustReport{LocalNode: "pve1", Mode: "cluster-ca", CAFile: "/etc/pve/pve-root-ca.pem", Pinned: true, Scheme: "https", AnchorError: "open /etc/pve/pve-root-ca.pem: no such file or directory"},
			wantID:     "peer:peer_trust_degraded|anchor_unavailable",
			wantSev:    SeverityError,
			wantDetail: "never falls back to the system trust store",
		},
		{
			name:       "plaintext scheme",
			rep:        PeerTrustReport{LocalNode: "pve1", Mode: "cluster-ca", CAFile: "/etc/pve/pve-root-ca.pem", Pinned: true, Scheme: "http"},
			wantID:     "peer:peer_trust_degraded|plaintext",
			wantSev:    SeverityError,
			wantDetail: "neither encrypted nor authenticated",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := peerTrustFindings(staticPeerTrust{tc.rep}, newDebouncer())
			if len(got) != 1 {
				t.Fatalf("got %+v, want exactly one posture finding", got)
			}
			f := got[0]
			if f.ID != tc.wantID {
				t.Errorf("id = %q, want %q", f.ID, tc.wantID)
			}
			if f.Check != CheckPeerTrustDegraded {
				t.Errorf("check = %q, want %q", f.Check, CheckPeerTrustDegraded)
			}
			if f.Source != SourcePeer {
				t.Errorf("source = %q, want %q", f.Source, SourcePeer)
			}
			if f.Severity != tc.wantSev {
				t.Errorf("severity = %q, want %q", f.Severity, tc.wantSev)
			}
			if !strings.Contains(f.Detail, tc.wantDetail) {
				t.Errorf("detail %q does not contain %q", f.Detail, tc.wantDetail)
			}
			if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
				t.Errorf("nodes = %v, want [pve1]", f.Nodes)
			}
		})
	}

	if got := peerTrustFindings(staticPeerTrust{pinnedReport()}, newDebouncer()); len(got) != 0 {
		t.Fatalf("the default pinned posture must raise nothing, got %+v", got)
	}
}

// TestPeerTrustFindings_TwoPostureProblemsAreTwoFindings — the causes are
// independent, and one must not swallow the other.
func TestPeerTrustFindings_TwoPostureProblemsAreTwoFindings(t *testing.T) {
	rep := PeerTrustReport{LocalNode: "pve1", Mode: "cluster-ca", CAFile: "/etc/pve/pve-root-ca.pem", Pinned: true, Scheme: "http", AnchorError: "boom"}
	got := peerTrustFindings(staticPeerTrust{rep}, newDebouncer())
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2 (anchor_unavailable + plaintext): %+v", len(got), got)
	}
	ids := map[string]bool{got[0].ID: true, got[1].ID: true}
	if !ids["peer:peer_trust_degraded|anchor_unavailable"] || !ids["peer:peer_trust_degraded|plaintext"] {
		t.Fatalf("ids = %v", ids)
	}
}

// TestPeerTrustFindings_IDsAreStableAcrossRecomputation is the property
// Engine's transition/notification tracking depends on.
func TestPeerTrustFindings_IDsAreStableAcrossRecomputation(t *testing.T) {
	prov := staticPeerTrust{pinnedReport(PeerTrustStatus{Node: "pve2", State: PeerStateUntrusted, Error: "x509"})}
	db := newDebouncer()
	a := peerTrustFindings(prov, db)
	b := peerTrustFindings(prov, db)
	if len(a) != 1 || len(b) != 1 || a[0].ID != b[0].ID || a[0].Detail != b[0].Detail {
		t.Fatalf("re-evaluating unchanged state must reproduce identical findings: %+v vs %+v", a, b)
	}
}

// TestEngine_PeerTrustFindingsReachTheUnifiedStream wires the producer through
// the real Engine, so the seam cannot be silently unwired.
func TestEngine_PeerTrustFindingsReachTheUnifiedStream(t *testing.T) {
	e := New(Config{PeerTrust: staticPeerTrust{pinnedReport(
		PeerTrustStatus{Node: "pve2", State: PeerStateUntrusted, Error: "x509: certificate signed by unknown authority"},
	)}})
	found := false
	for _, f := range e.Findings() {
		if f.Source == SourcePeer && f.Check == CheckPeerUntrusted {
			found = true
		}
	}
	if !found {
		t.Fatal("peer_untrusted did not reach Engine.Findings()")
	}

	// And a nil provider leaves the stream untouched.
	if got := New(Config{}).Findings(); len(got) != 0 {
		t.Fatalf("an unwired engine produced %+v", got)
	}
}
