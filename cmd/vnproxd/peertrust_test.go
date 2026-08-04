package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/peer"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestNewPeerTrust_DefaultsToThePinnedClusterCA — the composition root must
// not be the place the pin gets lost.
func TestNewPeerTrust_DefaultsToThePinnedClusterCA(t *testing.T) {
	cfg := &config.Config{Peer: config.PeerConfig{CAFile: peer.DefaultClusterCAPath}}
	trust, err := newPeerTrust(cfg, quietLogger())
	if err != nil {
		t.Fatalf("newPeerTrust: %v", err)
	}
	if !trust.Mode().Pinned() {
		t.Fatalf("mode = %q, want a pinned mode", trust.Mode())
	}
	if trust.CAFile() != peer.DefaultClusterCAPath {
		t.Fatalf("ca file = %q, want %q", trust.CAFile(), peer.DefaultClusterCAPath)
	}
}

// TestNewPeerTrust_RefusesAnUnacknowledgedEscapeHatch — a config that got past
// config.Load somehow (a hand-built Config, a future caller) still cannot
// produce an unpinned daemon: the library is the enforcement point.
func TestNewPeerTrust_RefusesAnUnacknowledgedEscapeHatch(t *testing.T) {
	cfg := &config.Config{Peer: config.PeerConfig{TLSTrust: peer.TrustInsecure}}
	if _, err := newPeerTrust(cfg, quietLogger()); err == nil {
		t.Fatal("newPeerTrust accepted an unacknowledged escape hatch")
	}
}

// TestNewPeerTrust_LogsTheFailClosedConditionAtStartup: the reason newPeerTrust
// forces the first anchor evaluation instead of leaving it lazy — a missing
// `/etc/pve/pve-root-ca.pem` must be visible in the startup log, not only in a
// peer request an hour later.
func TestNewPeerTrust_LogsTheFailClosedConditionAtStartup(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := &config.Config{Peer: config.PeerConfig{CAFile: filepath.Join(t.TempDir(), "absent.pem")}}
	if _, err := newPeerTrust(cfg, logger); err != nil {
		t.Fatalf("newPeerTrust: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "absent.pem") {
		t.Fatalf("startup log did not report the unusable anchor at ERROR:\n%s", out)
	}
	if !strings.Contains(out, "no fallback to the system trust store") {
		t.Fatalf("startup log must state that there is no fallback:\n%s", out)
	}
}

// TestNewPeerTrust_LogsTheEscapeHatchWarningEveryStartup — AC3, at the layer
// an operator's journal actually sees.
func TestNewPeerTrust_LogsTheEscapeHatchWarningEveryStartup(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cfg := &config.Config{Peer: config.PeerConfig{TLSTrust: peer.TrustSystem, TLSTrustAck: peer.AckSystem}}
	for i := 0; i < 3; i++ {
		if _, err := newPeerTrust(cfg, logger); err != nil {
			t.Fatalf("newPeerTrust: %v", err)
		}
	}
	if got := strings.Count(buf.String(), "level=WARN"); got != 3 {
		t.Fatalf("%d WARN lines across 3 startups, want 3:\n%s", got, buf.String())
	}
}

// TestPeerTrustAdapter_UnwiredIsSilent / _ReportsThePeerClient cover the
// lazily-set findings seam both before and after set().
func TestPeerTrustAdapter_UnwiredIsSilent(t *testing.T) {
	a := &peerTrustAdapter{}
	if got := (a.PeerTrust()); got.Mode != "" || len(got.Peers) != 0 {
		t.Fatalf("unwired adapter report = %+v, want empty", got)
	}
	// And an empty report raises nothing in the real engine.
	if got := findings.New(findings.Config{PeerTrust: a}).Findings(); len(got) != 0 {
		t.Fatalf("unwired adapter produced findings: %+v", got)
	}
}

func TestPeerTrustAdapter_ReportsThePeerClientPosture(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "absent.pem")
	trust, err := peer.NewTrust(peer.TrustOptions{CAFile: caPath, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("NewTrust: %v", err)
	}
	secretPath := filepath.Join(dir, "cluster.secret")
	if writeErr := os.WriteFile(secretPath, []byte(strings.Repeat("a", 64)), 0o600); writeErr != nil {
		t.Fatalf("write secret: %v", writeErr)
	}
	secrets, err := peer.LoadOrGenerateSecret(secretPath, quietLogger())
	if err != nil {
		t.Fatalf("LoadOrGenerateSecret: %v", err)
	}
	client := peer.NewClient(peer.ClientOptions{Secrets: secrets, Trust: trust, Logger: quietLogger()})

	a := &peerTrustAdapter{}
	a.set(client, func() string { return "pve1" })
	rep := a.PeerTrust()
	if rep.LocalNode != "pve1" || rep.Mode != string(peer.TrustClusterCA) || !rep.Pinned || rep.CAFile != caPath {
		t.Fatalf("report = %+v", rep)
	}
	if rep.AnchorError == "" {
		t.Fatal("a missing anchor must surface as AnchorError so the findings stream can raise it")
	}
	if rep.Scheme != "https" {
		t.Fatalf("scheme = %q, want https", rep.Scheme)
	}

	// End to end through the real engine: the fail-closed condition becomes a
	// visible finding, not just a log line.
	got := findings.New(findings.Config{PeerTrust: a}).Findings()
	if len(got) != 1 || got[0].Check != findings.CheckPeerTrustDegraded || got[0].Severity != findings.SeverityError {
		t.Fatalf("findings = %+v, want one peer_trust_degraded error", got)
	}
}
