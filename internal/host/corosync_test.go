package host

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleCorosyncConf = `
totem {
    version: 2
    cluster_name: testcluster
    transport: knet
    interface {
        linknumber: 0
    }
}

nodelist {
    node {
        name: pve1
        nodeid: 1
        quorum_votes: 1
        ring0_addr: 10.10.0.1
        ring1_addr: 10.10.1.1
    }
    node {
        name: pve2
        nodeid: 2
        quorum_votes: 1
        ring0_addr: 10.10.0.2
    }
}

quorum {
    provider: corosync_votequorum
}

logging {
    debug: off
}
`

func TestParseCorosyncConf(t *testing.T) {
	cfg, err := ParseCorosyncConf([]byte(sampleCorosyncConf))
	if err != nil {
		t.Fatalf("ParseCorosyncConf: %v", err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2: %+v", len(cfg.Nodes), cfg.Nodes)
	}

	n1, ok := cfg.NodeByName("pve1")
	if !ok {
		t.Fatalf("pve1 not found in %+v", cfg.Nodes)
	}
	if n1.NodeID != 1 {
		t.Errorf("pve1 nodeid = %d, want 1", n1.NodeID)
	}
	if got, want := n1.RingAddrs, []string{"10.10.0.1", "10.10.1.1"}; !stringSlicesEqual(got, want) {
		t.Errorf("pve1 ring addrs = %v, want %v", got, want)
	}

	n2, ok := cfg.NodeByName("pve2")
	if !ok {
		t.Fatalf("pve2 not found in %+v", cfg.Nodes)
	}
	if got, want := n2.RingAddrs, []string{"10.10.0.2"}; !stringSlicesEqual(got, want) {
		t.Errorf("pve2 ring addrs = %v, want %v", got, want)
	}

	if _, ok := cfg.NodeByName("nonexistent"); ok {
		t.Error("NodeByName(nonexistent) = ok, want not found")
	}
}

func TestParseCorosyncConf_IgnoresOtherSections(t *testing.T) {
	cfg, err := ParseCorosyncConf([]byte(`
totem {
    version: 2
    ring0_addr: should-not-be-captured
}
nodelist {
    node {
        name: solo
        ring0_addr: 192.168.1.1
    }
}
`))
	if err != nil {
		t.Fatalf("ParseCorosyncConf: %v", err)
	}
	if len(cfg.Nodes) != 1 {
		t.Fatalf("got %d nodes, want 1 (totem's ring0_addr must not leak in): %+v", len(cfg.Nodes), cfg.Nodes)
	}
	if cfg.Nodes[0].RingAddrs[0] != "192.168.1.1" {
		t.Errorf("ring addr = %q, want 192.168.1.1", cfg.Nodes[0].RingAddrs[0])
	}
}

func TestNodeByName_NilConfig(t *testing.T) {
	var cfg *CorosyncConfig
	if _, ok := cfg.NodeByName("anything"); ok {
		t.Error("NodeByName on nil *CorosyncConfig = ok, want not found")
	}
}

func TestReadCorosyncConf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corosync.conf")
	if err := os.WriteFile(path, []byte(sampleCorosyncConf), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := ReadCorosyncConf(path)
	if err != nil {
		t.Fatalf("ReadCorosyncConf: %v", err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(cfg.Nodes))
	}
}

func TestReadCorosyncConf_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadCorosyncConf(filepath.Join(dir, "does-not-exist.conf"))
	if err == nil {
		t.Fatal("ReadCorosyncConf on a missing file: got nil error, want a wrapped os.PathError")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadCorosyncConf error = %v, want errors.Is(err, os.ErrNotExist)", err)
	}
}

const sampleCorosyncStatusHealthy = `Printing ring status.
Local node ID 1
RING ID 0
	id	= 10.10.0.1
	status	= ring 0 active with no faults
RING ID 1
	id	= 10.10.1.1
	status	= ring 1 active with no faults
`

const sampleCorosyncStatusFaulty = `Printing ring status.
Local node ID 1
RING ID 0
	id	= 10.10.0.1
	status	= ring 0 active with no faults
RING ID 1
	id	= 10.10.1.1
	status	= Marking ringid 1 interface 10.10.1.1 FAULTY - administrative intervention required.
`

// TestParseCorosyncStatus_Healthy: every ring reporting "no faults" parses
// with Faulty=false (T-803).
func TestParseCorosyncStatus_Healthy(t *testing.T) {
	rings, err := ParseCorosyncStatus([]byte(sampleCorosyncStatusHealthy))
	if err != nil {
		t.Fatalf("ParseCorosyncStatus: %v", err)
	}
	if len(rings) != 2 {
		t.Fatalf("got %d rings, want 2: %+v", len(rings), rings)
	}
	for _, r := range rings {
		if r.Faulty {
			t.Errorf("ring %d (%s) reported Faulty=true, want false: %q", r.RingID, r.Addr, r.StatusText)
		}
	}
	if rings[0].RingID != 0 || rings[0].Addr != "10.10.0.1" {
		t.Errorf("ring[0] = %+v, want {RingID:0 Addr:10.10.0.1 ...}", rings[0])
	}
	if rings[1].RingID != 1 || rings[1].Addr != "10.10.1.1" {
		t.Errorf("ring[1] = %+v, want {RingID:1 Addr:10.10.1.1 ...}", rings[1])
	}
}

// TestParseCorosyncStatus_Faulty: a ring whose status text does not contain
// "no faults" parses with Faulty=true, and its raw status text is preserved
// verbatim for the health finding's detail text.
func TestParseCorosyncStatus_Faulty(t *testing.T) {
	rings, err := ParseCorosyncStatus([]byte(sampleCorosyncStatusFaulty))
	if err != nil {
		t.Fatalf("ParseCorosyncStatus: %v", err)
	}
	if len(rings) != 2 {
		t.Fatalf("got %d rings, want 2: %+v", len(rings), rings)
	}
	if rings[0].Faulty {
		t.Errorf("ring 0 reported Faulty=true, want false: %q", rings[0].StatusText)
	}
	if !rings[1].Faulty {
		t.Errorf("ring 1 reported Faulty=false, want true: %q", rings[1].StatusText)
	}
	if !strings.Contains(rings[1].StatusText, "FAULTY") {
		t.Errorf("ring 1 StatusText = %q, want it to preserve the raw FAULTY wording", rings[1].StatusText)
	}
}

// TestParseCorosyncStatus_Empty: empty input is a clean (nil, nil), not an
// error — matching ParseBGPSummary/ParseEVPNVNI's own "no output is not
// itself an error" convention.
func TestParseCorosyncStatus_Empty(t *testing.T) {
	rings, err := ParseCorosyncStatus(nil)
	if err != nil {
		t.Fatalf("ParseCorosyncStatus(nil): %v", err)
	}
	if rings != nil {
		t.Errorf("ParseCorosyncStatus(nil) = %+v, want nil", rings)
	}
}

// TestParseCorosyncStatus_MalformedNeverPanics: garbage input never panics,
// and unrecognized lines are silently skipped rather than failing the parse
// (mirrors ParseCorosyncConf's own tolerant-parser convention).
func TestParseCorosyncStatus_MalformedNeverPanics(t *testing.T) {
	inputs := []string{
		"garbage\nRING ID not-a-number\nmore garbage",
		"RING ID 0\nno equals sign here\n",
		"= = = =\n",
		"RING ID 0\n\tid\t=\n\tstatus\t=\n",
	}
	for _, in := range inputs {
		if _, err := ParseCorosyncStatus([]byte(in)); err != nil {
			t.Errorf("ParseCorosyncStatus(%q) returned an error, want tolerant parsing: %v", in, err)
		}
	}
}

// realKnetCorosyncStatusHealthy is `corosync-cfgtool -s`'s real, verbatim
// output, captured against both nodes of a real, healthy, quorate 2/2 PVE
// 9.2.10 cluster (planning/reports/blocked-validation.md §1.4/§2.1) —
// knet's actual shape, not an invented one. The old parser found zero rings
// against this text at all (a silent (nil, nil), not an error) because it
// only recognized a "RING ID n" header; knet says "LINK ID n <transport>"
// and reports per-peer connection state on nested "nodeid: N: <state>"
// lines instead of one flat "status = ..." line.
const realKnetCorosyncStatusHealthy = "Local node ID 1, transport knet\n" +
	"LINK ID 0 udp\n" +
	"\taddr\t= 192.168.1.9\n" +
	"\tstatus:\n" +
	"\t\tnodeid:          1:\tlocalhost\n" +
	"\t\tnodeid:          2:\tconnected\n"

// TestParseCorosyncStatus_RealKnetOutput_Healthy proves the new parser
// returns one correctly-populated, non-faulty RingStatus for the real
// captured knet output above — the direct fix for §2.1's "cannot parse a
// real, modern (knet) cluster at all" finding. Before this fix, this
// assertion failed with rings == nil (zero rings parsed, no error).
func TestParseCorosyncStatus_RealKnetOutput_Healthy(t *testing.T) {
	rings, err := ParseCorosyncStatus([]byte(realKnetCorosyncStatusHealthy))
	if err != nil {
		t.Fatalf("ParseCorosyncStatus: %v", err)
	}
	if len(rings) != 1 {
		t.Fatalf("got %d rings, want 1 (one LINK ID 0 block): %+v", len(rings), rings)
	}
	r := rings[0]
	if r.RingID != 0 {
		t.Errorf("RingID = %d, want 0 (from LINK ID 0)", r.RingID)
	}
	if r.Addr != "192.168.1.9" {
		t.Errorf("Addr = %q, want 192.168.1.9 (from addr\\t=)", r.Addr)
	}
	if r.Faulty {
		t.Errorf("Faulty = true, want false: every nodeid line reports localhost/connected (%q)", r.StatusText)
	}
	if !strings.Contains(r.StatusText, "nodeid 1: localhost") || !strings.Contains(r.StatusText, "nodeid 2: connected") {
		t.Errorf("StatusText = %q, want it to summarize both nodeid lines verbatim", r.StatusText)
	}
}

// TestParseCorosyncStatus_KnetFaultyNodeState covers a knet link where one
// peer's reported connection state is neither "localhost" nor "connected".
// The real wording knet uses for a genuinely disconnected/faulty peer was
// NOT captured on hardware (only the healthy 2/2 case was observed —
// planning/reports/blocked-validation.md §2.1/§3, RingStatus.Faulty's own
// doc comment) — "disconnected" here is a plausible but NOT
// hardware-confirmed stand-in, chosen only to exercise the permissive
// "anything other than localhost/connected is faulty" default. Do not treat
// this exact wording as proven real knet output.
func TestParseCorosyncStatus_KnetFaultyNodeState(t *testing.T) {
	const in = "Local node ID 1, transport knet\n" +
		"LINK ID 0 udp\n" +
		"\taddr\t= 192.168.1.9\n" +
		"\tstatus:\n" +
		"\t\tnodeid:          1:\tlocalhost\n" +
		"\t\tnodeid:          2:\tdisconnected\n"

	rings, err := ParseCorosyncStatus([]byte(in))
	if err != nil {
		t.Fatalf("ParseCorosyncStatus: %v", err)
	}
	if len(rings) != 1 {
		t.Fatalf("got %d rings, want 1: %+v", len(rings), rings)
	}
	if !rings[0].Faulty {
		t.Errorf("Faulty = false, want true: nodeid 2 reports a state that is neither localhost nor connected (%q)", rings[0].StatusText)
	}
	if !strings.Contains(rings[0].StatusText, "nodeid 2: disconnected") {
		t.Errorf("StatusText = %q, want the raw peer state preserved verbatim", rings[0].StatusText)
	}
}

// TestParseCorosyncStatus_KnetMultiLink covers more than one "LINK ID"
// block in one cfgtool run (a cluster with redundant rings configured),
// proving each link is parsed into its own independent RingStatus.
func TestParseCorosyncStatus_KnetMultiLink(t *testing.T) {
	const in = "Local node ID 1, transport knet\n" +
		"LINK ID 0 udp\n" +
		"\taddr\t= 192.168.1.9\n" +
		"\tstatus:\n" +
		"\t\tnodeid:          1:\tlocalhost\n" +
		"\t\tnodeid:          2:\tconnected\n" +
		"LINK ID 1 udp\n" +
		"\taddr\t= 10.10.1.9\n" +
		"\tstatus:\n" +
		"\t\tnodeid:          1:\tlocalhost\n" +
		"\t\tnodeid:          2:\tconnected\n"

	rings, err := ParseCorosyncStatus([]byte(in))
	if err != nil {
		t.Fatalf("ParseCorosyncStatus: %v", err)
	}
	if len(rings) != 2 {
		t.Fatalf("got %d rings, want 2: %+v", len(rings), rings)
	}
	if rings[0].RingID != 0 || rings[0].Addr != "192.168.1.9" || rings[0].Faulty {
		t.Errorf("rings[0] = %+v, want {RingID:0 Addr:192.168.1.9 Faulty:false ...}", rings[0])
	}
	if rings[1].RingID != 1 || rings[1].Addr != "10.10.1.9" || rings[1].Faulty {
		t.Errorf("rings[1] = %+v, want {RingID:1 Addr:10.10.1.9 Faulty:false ...}", rings[1])
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
