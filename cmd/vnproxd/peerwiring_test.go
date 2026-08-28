// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/peer"
)

// TestRunDaemon_PeerAPIRequiresValidSignature is T-301's daemon-level wiring
// check: a real runDaemon (config load, TLS, the generate-if-absent cluster
// secret, and internal/api's router with PeerServer mounted) must serve
// GET /api/peer/version for a request signed with the daemon's own
// generated secret, and reject the same request with no signature at all —
// end-to-end proof that cmd/vnproxd's wiring (not just internal/peer's own
// unit tests) rejects unsigned peer requests and accepts correctly-signed
// ones.
func TestRunDaemon_PeerAPIRequiresValidSignature(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	dir := t.TempDir()
	cfgPath := rewriteDevConfig(t, repoRoot, dir, port)
	secretPath := filepath.Join(dir, "cluster.secret")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	daemonDone := make(chan error, 1)
	go func() { daemonDone <- runDaemon(ctx, daemonOptions{ConfigPath: cfgPath}, testLogger()) }()

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for the throwaway dev cert
		},
	}
	versionURL := fmt.Sprintf("https://127.0.0.1:%d/api/peer/version", port)

	// Wait for the daemon (and its generated secret file) to come up,
	// polling the unsigned request until it stops connection-refusing —
	// it should always answer 401, never connect-refuse once serving.
	deadline := time.Now().Add(10 * time.Second)
	var unsignedStatus int
	for {
		select {
		case daemonErr := <-daemonDone:
			t.Fatalf("daemon exited before serving: %v", daemonErr)
		default:
		}
		req, _ := http.NewRequest(http.MethodGet, versionURL, nil)
		resp, reqErr := client.Do(req)
		if reqErr == nil {
			unsignedStatus = resp.StatusCode
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never served %s: last error: %v", versionURL, reqErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if unsignedStatus != http.StatusUnauthorized {
		t.Errorf("unsigned GET /api/peer/version status = %d, want 401", unsignedStatus)
	}

	secretStore, err := peer.LoadOrGenerateSecret(secretPath, testLogger())
	if err != nil {
		t.Fatalf("loading the daemon's generated secret: %v", err)
	}

	peerClient := peer.NewClient(peer.ClientOptions{
		Secrets: secretStore,
		Scheme:  "https",
		HTTPClient: &http.Client{
			Timeout: 2 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for the throwaway dev cert
			},
		},
		Logger: testLogger(),
	})
	target := peer.Peer{Node: "self", Addr: fmt.Sprintf("127.0.0.1:%d", port)}

	v, err := peerClient.Version(ctx, target)
	if err != nil {
		t.Fatalf("signed Version request: %v", err)
	}
	if v.ProtocolVersion != peer.ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, want %d", v.ProtocolVersion, peer.ProtocolVersion)
	}

	cancel()
	select {
	case shutdownErr := <-daemonDone:
		if shutdownErr != nil {
			t.Fatalf("runDaemon returned error on shutdown: %v", shutdownErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of context cancellation")
	}
}
