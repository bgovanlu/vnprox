package peer

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// spyCaptureAgent is an in-memory CaptureAgent stand-in for the peer capture
// route tests, recording what it was asked to run.
type spyCaptureAgent struct {
	err      error
	lastStop string
	lastStat string
	startRes CaptureResult
	lastSpec CaptureSpec
}

func (s *spyCaptureAgent) StartLocal(_ context.Context, spec CaptureSpec) (CaptureResult, error) {
	s.lastSpec = spec
	return s.startRes, s.err
}
func (s *spyCaptureAgent) StopLocal(_ context.Context, id string) (CaptureResult, error) {
	s.lastStop = id
	return CaptureResult{Status: "stopped", Packets: 7, Bytes: 700}, s.err
}
func (s *spyCaptureAgent) StatusLocal(_ context.Context, id string) (CaptureResult, error) {
	s.lastStat = id
	return CaptureResult{Status: "running", Packets: 3, Bytes: 300}, s.err
}

func TestClient_Capture_RoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	agent := &spyCaptureAgent{startRes: CaptureResult{Status: "running", Packets: 0, Bytes: 24}}
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Capture: agent, Version: "test",
		Logger: discardLogger(), Now: func() time.Time { return now },
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret), Scheme: "http", Logger: discardLogger(),
		Now: func() time.Time { return now },
	})
	p := Peer{Node: "pve2", Addr: ts.Listener.Addr().String()}

	spec := CaptureSpec{
		SessionID: "s1", GroupID: "g1", TargetRef: "bridge:pve2:vmbr0", Node: "pve2",
		Iface: "vmbr0", Filter: "tcp port 443",
		Caps:      CaptureCaps{MaxDurationSec: 60, MaxBytes: 4096, MaxPackets: 100, RetentionHours: 24},
		StartedBy: "root@pam", StartedAt: 100,
		Nodes:     []string{"pve1", "pve2"},
	}
	res, err := client.CaptureStart(t.Context(), p, spec)
	if err != nil {
		t.Fatalf("CaptureStart: %v", err)
	}
	if res.Status != "running" || res.Bytes != 24 {
		t.Errorf("start result = %+v", res)
	}
	if agent.lastSpec.SessionID != "s1" || agent.lastSpec.Filter != "tcp port 443" {
		t.Errorf("server-observed spec = %+v", agent.lastSpec)
	}
	if agent.lastSpec.Caps.MaxPackets != 100 {
		t.Errorf("caps did not round-trip over the wire: %+v", agent.lastSpec.Caps)
	}

	stop, err := client.CaptureStop(t.Context(), p, "s1")
	if err != nil {
		t.Fatalf("CaptureStop: %v", err)
	}
	if stop.Status != "stopped" || agent.lastStop != "s1" {
		t.Errorf("stop = %+v, lastStop=%q", stop, agent.lastStop)
	}

	stat, err := client.CaptureStatus(t.Context(), p, "s1")
	if err != nil {
		t.Fatalf("CaptureStatus: %v", err)
	}
	if stat.Packets != 3 || agent.lastStat != "s1" {
		t.Errorf("status = %+v, lastStat=%q", stat, agent.lastStat)
	}
}

func TestServer_UnconfiguredCapture503s(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Version: "test",
		Logger: discardLogger(), Now: func() time.Time { return now },
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(testSecret), Scheme: "http", Logger: discardLogger(),
		Now: func() time.Time { return now },
	})
	p := Peer{Node: "solo", Addr: ts.Listener.Addr().String()}

	if _, err := client.CaptureStart(t.Context(), p, CaptureSpec{SessionID: "s1"}); err == nil {
		t.Fatal("expected an error with no CaptureAgent configured")
	}
}

// TestServer_CaptureRequiresHMAC confirms the capture routes inherit the peer
// server's cluster-secret HMAC gate like every other /api/peer/* route: a
// client signing with the wrong secret is rejected before reaching the agent.
func TestServer_CaptureRequiresHMAC(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	agent := &spyCaptureAgent{}
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Capture: agent, Version: "test",
		Logger: discardLogger(), Now: func() time.Time { return now },
	})
	ts := mountedTestServer(t, srv)
	badClient := NewClient(ClientOptions{
		Secrets: newStaticSecretStore(bytes.Repeat([]byte{0x99}, secretLen)), Scheme: "http",
		Logger: discardLogger(), Now: func() time.Time { return now },
	})
	p := Peer{Node: "pve2", Addr: ts.Listener.Addr().String()}

	if _, err := badClient.CaptureStart(t.Context(), p, CaptureSpec{SessionID: "s1"}); err == nil {
		t.Fatal("expected HMAC rejection with a wrong cluster secret")
	}
	if agent.lastSpec.SessionID != "" {
		t.Errorf("capture agent was reached despite bad HMAC: %+v", agent.lastSpec)
	}
}
