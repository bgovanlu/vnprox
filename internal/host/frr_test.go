// SPDX-License-Identifier: Apache-2.0

package host

import (
	"os"
	"path/filepath"
	"testing"
)

func readTestdata(t *testing.T, parts ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("reading testdata %v: %v", parts, err)
	}
	return data
}

func TestParseBGPSummary_NestedAFI(t *testing.T) {
	data := readTestdata(t, "testdata", "frr", "bgp", "v8_nested_established.json")
	summary, err := ParseBGPSummary(data)
	if err != nil {
		t.Fatalf("ParseBGPSummary: %v", err)
	}
	if summary.RouterID != "10.20.0.11" {
		t.Errorf("RouterID = %q, want 10.20.0.11", summary.RouterID)
	}
	if summary.ASN != 65001 {
		t.Errorf("ASN = %d, want 65001", summary.ASN)
	}
	// Two AFI blocks (ipv4Unicast, l2VpnEvpn), two peers each = 4 entries.
	if len(summary.Peers) != 4 {
		t.Fatalf("len(Peers) = %d, want 4", len(summary.Peers))
	}
	var evpnPeer *BGPPeer
	for i := range summary.Peers {
		p := &summary.Peers[i]
		if p.AddressFamily == "l2VpnEvpn" && p.Addr == "10.20.0.12" {
			evpnPeer = p
		}
	}
	if evpnPeer == nil {
		t.Fatal("expected an l2VpnEvpn peer for 10.20.0.12")
	}
	if evpnPeer.Hostname != "pve2" {
		t.Errorf("Hostname = %q, want pve2", evpnPeer.Hostname)
	}
	if evpnPeer.State != "Established" {
		t.Errorf("State = %q, want Established", evpnPeer.State)
	}
	if evpnPeer.StateReason != "" {
		t.Errorf("StateReason = %q, want empty", evpnPeer.StateReason)
	}
	if evpnPeer.PfxRcd != 6 {
		t.Errorf("PfxRcd = %d, want 6", evpnPeer.PfxRcd)
	}
	if evpnPeer.RemoteAS != 65001 {
		t.Errorf("RemoteAS = %d, want 65001", evpnPeer.RemoteAS)
	}
	if evpnPeer.UptimeSecs != 5025000/1000 {
		t.Errorf("UptimeSecs = %d, want %d", evpnPeer.UptimeSecs, 5025000/1000)
	}
}

func TestParseBGPSummary_V9StringNumbersAndMixedStates(t *testing.T) {
	data := readTestdata(t, "testdata", "frr", "bgp", "v9_nested_mixed_states.json")
	summary, err := ParseBGPSummary(data)
	if err != nil {
		t.Fatalf("ParseBGPSummary: %v", err)
	}
	if summary.ASN != 65001 {
		t.Errorf("ASN = %d, want 65001 (string-encoded 'as' must still parse)", summary.ASN)
	}
	var active, idleAdmin *BGPPeer
	for i := range summary.Peers {
		p := &summary.Peers[i]
		switch {
		case p.AddressFamily == "ipv4Unicast" && p.Addr == "10.20.0.13":
			active = p
		case p.AddressFamily == "l2VpnEvpn" && p.Addr == "10.20.0.13":
			idleAdmin = p
		}
	}
	if active == nil {
		t.Fatal("expected an ipv4Unicast peer for 10.20.0.13")
	}
	if active.State != "Active" {
		t.Errorf("State = %q, want Active", active.State)
	}
	if idleAdmin == nil {
		t.Fatal("expected an l2VpnEvpn peer for 10.20.0.13")
	}
	if idleAdmin.State != "Idle" || idleAdmin.StateReason != "Admin" {
		t.Errorf("State/StateReason = %q/%q, want Idle/Admin", idleAdmin.State, idleAdmin.StateReason)
	}
	// String-encoded remoteAs/pfxRcd must still parse via flexInt.
	var established *BGPPeer
	for i := range summary.Peers {
		p := &summary.Peers[i]
		if p.AddressFamily == "l2VpnEvpn" && p.Addr == "10.20.0.12" {
			established = p
		}
	}
	if established == nil {
		t.Fatal("expected an l2VpnEvpn peer for 10.20.0.12")
	}
	if established.RemoteAS != 65001 {
		t.Errorf("RemoteAS = %d, want 65001 (string-encoded)", established.RemoteAS)
	}
	if established.PfxRcd != 6 {
		t.Errorf("PfxRcd = %d, want 6 (string-encoded)", established.PfxRcd)
	}
}

func TestParseBGPSummary_FlatLegacyShape(t *testing.T) {
	data := readTestdata(t, "testdata", "frr", "bgp", "flat_legacy_single_afi.json")
	summary, err := ParseBGPSummary(data)
	if err != nil {
		t.Fatalf("ParseBGPSummary: %v", err)
	}
	if summary.RouterID != "10.30.0.1" || summary.ASN != 65010 {
		t.Errorf("RouterID/ASN = %q/%d, want 10.30.0.1/65010", summary.RouterID, summary.ASN)
	}
	if len(summary.Peers) != 1 {
		t.Fatalf("len(Peers) = %d, want 1", len(summary.Peers))
	}
	p := summary.Peers[0]
	if p.AddressFamily != "" {
		t.Errorf("AddressFamily = %q, want empty (flat shape)", p.AddressFamily)
	}
	if p.State != "Established" {
		t.Errorf("State = %q, want Established", p.State)
	}
	// "3d02h14m" -> 3*86400 + 2*3600 + 14*60
	want := int64(3*86400 + 2*3600 + 14*60)
	if p.UptimeSecs != want {
		t.Errorf("UptimeSecs = %d, want %d", p.UptimeSecs, want)
	}
}

func TestParseBGPSummary_IdleWithReason(t *testing.T) {
	data := readTestdata(t, "testdata", "frr", "bgp", "idle_with_reason.json")
	summary, err := ParseBGPSummary(data)
	if err != nil {
		t.Fatalf("ParseBGPSummary: %v", err)
	}
	got := map[string]BGPPeer{}
	for _, p := range summary.Peers {
		got[p.Addr] = p
	}
	if p, ok := got["10.20.0.11"]; !ok || p.State != "Idle" || p.StateReason != "Admin" {
		t.Errorf("10.20.0.11: got %+v, want Idle/Admin", p)
	}
	if p, ok := got["10.20.0.12"]; !ok || p.State != "Idle" || p.StateReason != "PfxCt" {
		t.Errorf("10.20.0.12: got %+v, want Idle/PfxCt", p)
	}
}

func TestParseBGPSummary_EmptyPeers(t *testing.T) {
	data := readTestdata(t, "testdata", "frr", "bgp", "empty_peers.json")
	summary, err := ParseBGPSummary(data)
	if err != nil {
		t.Fatalf("ParseBGPSummary: %v", err)
	}
	if len(summary.Peers) != 0 {
		t.Errorf("len(Peers) = %d, want 0", len(summary.Peers))
	}
	if summary.RouterID != "10.40.0.1" {
		t.Errorf("RouterID = %q, want 10.40.0.1", summary.RouterID)
	}
}

func TestParseBGPSummary_NoL2VpnAFI(t *testing.T) {
	data := readTestdata(t, "testdata", "frr", "bgp", "no_l2vpn_afi.json")
	summary, err := ParseBGPSummary(data)
	if err != nil {
		t.Fatalf("ParseBGPSummary: %v", err)
	}
	if len(summary.Peers) != 1 {
		t.Fatalf("len(Peers) = %d, want 1", len(summary.Peers))
	}
	if summary.Peers[0].AddressFamily != "ipv4Unicast" {
		t.Errorf("AddressFamily = %q, want ipv4Unicast", summary.Peers[0].AddressFamily)
	}
}

func TestParseBGPSummary_EmptyInput(t *testing.T) {
	summary, err := ParseBGPSummary(nil)
	if err != nil {
		t.Fatalf("ParseBGPSummary(nil): %v", err)
	}
	if len(summary.Peers) != 0 || summary.RouterID != "" {
		t.Errorf("expected zero-value BGPSummary, got %+v", summary)
	}
}

func TestParseBGPSummary_AdversarialNeverErrors(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "frr", "bgp", "adversarial"))
	if err != nil {
		t.Fatalf("reading adversarial corpus: %v", err)
	}
	for _, e := range entries {
		data := readTestdata(t, "testdata", "frr", "bgp", "adversarial", e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			// The only invariant: never panics. Errors, empty results, or
			// partial peer lists are all acceptable for adversarial input.
			_, _ = ParseBGPSummary(data)
		})
	}
}

func TestSplitBGPState(t *testing.T) {
	tests := []struct {
		raw, wantState, wantReason string
	}{
		{"Established", "Established", ""},
		{"Idle (Admin)", "Idle", "Admin"},
		{"Idle (PfxCt)", "Idle", "PfxCt"},
		{"", "", ""},
		{"Idle ()", "Idle", ""},
		{"(Admin)", "(Admin)", ""}, // no state before '(' -> treated as opaque
	}
	for _, tt := range tests {
		state, reason := splitBGPState(tt.raw)
		if state != tt.wantState || reason != tt.wantReason {
			t.Errorf("splitBGPState(%q) = (%q,%q), want (%q,%q)", tt.raw, state, reason, tt.wantState, tt.wantReason)
		}
	}
}

func TestParseFRRUptime(t *testing.T) {
	tests := []struct {
		s    string
		msec int64
		want int64
	}{
		{"never", 0, 0},
		{"", 0, 0},
		{"00:05:00", 0, 300},
		{"01:23:45", 0, 3600 + 23*60 + 45},
		{"3d02h14m", 0, 3*86400 + 2*3600 + 14*60},
		{"2w1d05h", 0, 2*7*86400 + 86400 + 5*3600},
		{"01:23:45", 5025000, 5025}, // msec wins when present
		{"garbage", 0, 0},
		{"1x2y", 0, 0}, // unrecognized unit letters
	}
	for _, tt := range tests {
		got := parseFRRUptime(tt.s, tt.msec)
		if got != tt.want {
			t.Errorf("parseFRRUptime(%q, %d) = %d, want %d", tt.s, tt.msec, got, tt.want)
		}
	}
}

func TestParseEVPNVNI_Object(t *testing.T) {
	data := readTestdata(t, "testdata", "frr", "evpn", "object_l2_l3.json")
	vnis, err := ParseEVPNVNI(data)
	if err != nil {
		t.Fatalf("ParseEVPNVNI: %v", err)
	}
	if len(vnis) != 2 {
		t.Fatalf("len(vnis) = %d, want 2", len(vnis))
	}
	byVNI := map[int]EVPNVni{}
	for _, v := range vnis {
		byVNI[v.VNI] = v
	}
	l2, ok := byVNI[10001]
	if !ok {
		t.Fatal("expected VNI 10001")
	}
	if l2.Type != "L2" || l2.VxlanIf != "vxlan10001" || l2.NumMacs != 12 || l2.NumArpND != 4 {
		t.Errorf("VNI 10001 = %+v, unexpected fields", l2)
	}
	l3, ok := byVNI[10000]
	if !ok {
		t.Fatal("expected VNI 10000")
	}
	if l3.Type != "L3" || l3.TenantVRF != "vrf10000" {
		t.Errorf("VNI 10000 = %+v, unexpected fields", l3)
	}
}

func TestParseEVPNVNI_Array(t *testing.T) {
	data := readTestdata(t, "testdata", "frr", "evpn", "array_form.json")
	vnis, err := ParseEVPNVNI(data)
	if err != nil {
		t.Fatalf("ParseEVPNVNI: %v", err)
	}
	if len(vnis) != 2 {
		t.Fatalf("len(vnis) = %d, want 2", len(vnis))
	}
	if vnis[0].VNI != 20001 || vnis[0].NumMacs != 8 {
		t.Errorf("vnis[0] = %+v, unexpected", vnis[0])
	}
}

func TestParseEVPNVNI_Empty(t *testing.T) {
	data := readTestdata(t, "testdata", "frr", "evpn", "empty.json")
	vnis, err := ParseEVPNVNI(data)
	if err != nil {
		t.Fatalf("ParseEVPNVNI: %v", err)
	}
	if len(vnis) != 0 {
		t.Errorf("len(vnis) = %d, want 0", len(vnis))
	}
}

func TestParseEVPNVNI_EmptyInput(t *testing.T) {
	vnis, err := ParseEVPNVNI(nil)
	if err != nil || vnis != nil {
		t.Errorf("ParseEVPNVNI(nil) = (%v, %v), want (nil, nil)", vnis, err)
	}
}

func TestParseEVPNVNI_AdversarialNeverErrors(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "frr", "evpn", "adversarial"))
	if err != nil {
		t.Fatalf("reading adversarial corpus: %v", err)
	}
	for _, e := range entries {
		data := readTestdata(t, "testdata", "frr", "evpn", "adversarial", e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			_, _ = ParseEVPNVNI(data)
		})
	}
}
