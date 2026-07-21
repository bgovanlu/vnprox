package baseline

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// TestLearn_Summary verifies Learn's statistical summary: the observed
// service-port set (destination ports only — ephemeral source ports excluded),
// the observed peer-subnet set (/24-aggregated), top talkers by bytes, and the
// per-hour-of-day byte-volume mean/stddev.
func TestLearn_Summary(t *testing.T) {
	const ref = "guest:pve1:100"
	window := Window{Start: 0, End: 100 * 3600}
	recs := []flow.Record{
		// outbound to a server on tcp/443, ephemeral src port 51000 (must NOT
		// appear as an observed port).
		{SrcRef: ref, SrcIP: "10.0.0.5", DstIP: "10.0.0.10", At: 0, Bytes: 1000, SrcPort: 51000, DstPort: 443, Proto: protoTCP},
		// inbound from a client to this ref's own udp/53 listener.
		{DstRef: ref, SrcIP: "10.0.1.9", DstIP: "10.0.0.5", At: 3600, Bytes: 500, SrcPort: 40000, DstPort: 53, Proto: protoUDP},
		// second hour-0 sample the next day, same hour-of-day bucket.
		{SrcRef: ref, SrcIP: "10.0.0.5", DstIP: "10.0.0.10", At: 86400, Bytes: 3000, DstPort: 443, Proto: protoTCP},
	}

	prof := Learn(recs, ref, window)
	if prof.Empty() {
		t.Fatal("Learn produced an empty profile")
	}

	// Observed service ports: tcp/443 and udp/53 — never the ephemeral 51000.
	wantPorts := map[string]bool{"tcp/443": true, "udp/53": true}
	if len(prof.Ports) != len(wantPorts) {
		t.Fatalf("ports = %v, want %v", prof.Ports, wantPorts)
	}
	for _, p := range prof.Ports {
		if !wantPorts[p.String()] {
			t.Errorf("unexpected observed port %s (ephemeral source ports must be excluded)", p)
		}
	}

	// Observed subnets: the /24 of each peer.
	wantSubnets := map[string]bool{"10.0.0.0/24": true, "10.0.1.0/24": true}
	for _, s := range prof.Subnets {
		if !wantSubnets[s] {
			t.Errorf("unexpected observed subnet %s", s)
		}
	}
	if len(prof.Subnets) != len(wantSubnets) {
		t.Errorf("subnets = %v, want %v", prof.Subnets, wantSubnets)
	}

	// Talkers: peer identity -> bytes (peer ref when known, else IP).
	if got := prof.Talkers["10.0.0.10"]; got != 4000 {
		t.Errorf("talkers[10.0.0.10] = %d, want 4000", got)
	}
	if got := prof.Talkers["10.0.1.9"]; got != 500 {
		t.Errorf("talkers[10.0.1.9] = %d, want 500", got)
	}

	// Hour-of-day 0 saw two wall-clock hours (At 0 and At 86400) totalling 1000
	// and 3000 bytes: mean 2000, population stddev 1000.
	h0 := prof.Hours[0]
	if h0.Count != 2 {
		t.Errorf("hour-0 count = %d, want 2", h0.Count)
	}
	if h0.Mean != 2000 {
		t.Errorf("hour-0 mean = %v, want 2000", h0.Mean)
	}
	if h0.Stddev != 1000 {
		t.Errorf("hour-0 stddev = %v, want 1000", h0.Stddev)
	}
}

// TestProfile_MarshalRoundTrip verifies a learned Profile survives the JSON
// serialization used for baseline_profiles.profile_json.
func TestProfile_MarshalRoundTrip(t *testing.T) {
	c := loadCorpus(t, cleanCorpusFile)
	prof := Learn(c.Records, c.Ref, c.Window)

	js, err := Marshal(prof)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := Unmarshal(js)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Ref != prof.Ref || got.Window != prof.Window {
		t.Errorf("round-trip ref/window mismatch: got %s/%+v, want %s/%+v", got.Ref, got.Window, prof.Ref, prof.Window)
	}
	if len(got.Ports) != len(prof.Ports) || len(got.Subnets) != len(prof.Subnets) {
		t.Errorf("round-trip ports/subnets length mismatch")
	}
	if got.Hours != prof.Hours {
		t.Errorf("round-trip hours mismatch")
	}
}
