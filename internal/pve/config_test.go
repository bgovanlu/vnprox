package pve_test

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
)

func pemEncodeCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestNew_ValidatesConfig(t *testing.T) {
	// Field order here is presentation, not perf-relevant: this is a
	// test-only table, and nesting pve.Config (whose own field order is
	// already optimized for its own layout) as a value inevitably trips
	// fieldalignment's cross-struct padding heuristic.
	tests := []struct { //nolint:govet // see comment above
		cfg  pve.Config
		name string
	}{
		{name: "missing APIURL", cfg: pve.Config{Auth: pve.AuthTicket, Username: "u", Password: "p"}},
		{name: "bad APIURL", cfg: pve.Config{APIURL: "not a url", Auth: pve.AuthTicket, Username: "u", Password: "p"}},
		{name: "ticket missing username", cfg: pve.Config{APIURL: "https://localhost:8006", Auth: pve.AuthTicket, Password: "p"}},
		{name: "ticket missing password", cfg: pve.Config{APIURL: "https://localhost:8006", Auth: pve.AuthTicket, Username: "u"}},
		{name: "token missing value and file", cfg: pve.Config{APIURL: "https://localhost:8006", Auth: pve.AuthAPIToken}},
		{name: "unknown auth mode", cfg: pve.Config{APIURL: "https://localhost:8006", Auth: pve.AuthMode(99)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pve.New(tt.cfg); err == nil {
				t.Fatalf("pve.New(%+v): expected an error", tt.cfg)
			}
		})
	}
}

func TestNew_TokenFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pve-token")
	if err := os.WriteFile(path, []byte("vnprox@pve!daemon=abc123\n"), 0o600); err != nil {
		t.Fatalf("writing token file: %v", err)
	}

	var gotAuth string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer stub.Close()

	c, err := pve.New(pve.Config{
		APIURL:    stub.URL,
		Auth:      pve.AuthAPIToken,
		TokenFile: path,
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	if _, err := c.ClusterStatus(context.Background()); err != nil {
		t.Fatalf("ClusterStatus: %v", err)
	}
	if gotAuth != "PVEAPIToken=vnprox@pve!daemon=abc123" {
		t.Fatalf("Authorization header = %q, trimmed newline from file was not stripped correctly", gotAuth)
	}
}

func TestNew_TokenFileMissing(t *testing.T) {
	_, err := pve.New(pve.Config{
		APIURL:    "https://localhost:8006",
		Auth:      pve.AuthAPIToken,
		TokenFile: "/nonexistent/path/pve-token",
	})
	if err == nil {
		t.Fatalf("expected an error for a missing token file")
	}
}

func TestNew_TLSPinning(t *testing.T) {
	tsTLS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer tsTLS.Close()

	// Without pinning the test server's self-signed cert (or skipping
	// verification), the handshake must fail.
	c, err := pve.New(pve.Config{
		APIURL:     tsTLS.URL,
		Auth:       pve.AuthAPIToken,
		TokenValue: "u@pam!t=secret",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	_, err = c.ClusterStatus(context.Background())
	if err == nil {
		t.Fatalf("expected a TLS verification failure against an unpinned self-signed server")
	}
	var transportErr *pve.ErrPVETransport
	if !errors.As(err, &transportErr) {
		t.Fatalf("errors.As(err, &transportErr) failed; got %#v (%v)", err, err)
	}

	// Write the test server's certificate to a PEM file and pin to it:
	// the same request must now succeed.
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.pem")
	pemBytes := pemEncodeCert(tsTLS.Certificate().Raw)
	if writeErr := os.WriteFile(certPath, pemBytes, 0o600); writeErr != nil {
		t.Fatalf("writing cert file: %v", writeErr)
	}

	c2, err := pve.New(pve.Config{
		APIURL:     tsTLS.URL,
		Auth:       pve.AuthAPIToken,
		TokenValue: "u@pam!t=secret",
		TLS:        pve.TLSConfig{CACertFile: certPath},
	})
	if err != nil {
		t.Fatalf("pve.New (pinned): %v", err)
	}
	if _, err := c2.ClusterStatus(context.Background()); err != nil {
		t.Fatalf("ClusterStatus (pinned): %v", err)
	}
}
