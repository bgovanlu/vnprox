package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestMainRun_VersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := mainRun([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "vnproxd") {
		t.Errorf("stdout = %q, want it to mention vnproxd", stdout.String())
	}
}

func TestMainRun_UnknownFlagFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := mainRun([]string{"--not-a-real-flag"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected a non-zero exit code for an unknown flag")
	}
}

func TestMainRun_BadConfigFailsCleanlyWithoutPanicking(t *testing.T) {
	dir := t.TempDir()
	badConfig := filepath.Join(dir, "bad.toml")
	// invalid listen address: acceptance criterion 4.
	if err := os.WriteFile(badConfig, []byte("[server]\nlisten = \"not-an-address\"\n"), 0o600); err != nil {
		t.Fatalf("writing bad config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := mainRun([]string{"--config", badConfig}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid config") {
		t.Errorf("stderr = %q, want it to mention the invalid config error", stderr.String())
	}
}

func TestMainRun_MissingCertPathFailsCleanly(t *testing.T) {
	dir := t.TempDir()
	badConfig := filepath.Join(dir, "bad.toml")
	toml := "[server]\nlisten = \"127.0.0.1:8007\"\ntls_cert = \"/no/such/cert.pem\"\ntls_key = \"/no/such/key.pem\"\n"
	if err := os.WriteFile(badConfig, []byte(toml), 0o600); err != nil {
		t.Fatalf("writing bad config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := mainRun([]string{"--config", badConfig}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "tls certificate not found") {
		t.Errorf("stderr = %q, want it to mention the missing certificate", stderr.String())
	}
}

// generateSelfSignedCert returns an in-memory self-signed TLS certificate
// for tests that need to stand up a real TLS listener without depending on
// files on disk.
func generateSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "vnprox-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}

// TestGracefulShutdown_SIGTERMDrainsSlowRequestWithinBudget is the
// acceptance-criterion-3 test: a real SIGTERM, delivered to this process
// while a deliberately slow request is in flight, must let that request
// finish (not abort it) and the whole run group must return within 3s.
func TestGracefulShutdown_SIGTERMDrainsSlowRequestWithinBudget(t *testing.T) {
	const handlerDelay = 500 * time.Millisecond

	handlerStarted := make(chan struct{})
	r := chi.NewRouter()
	r.Get("/slow", func(w http.ResponseWriter, req *http.Request) {
		close(handlerStarted)
		time.Sleep(handlerDelay)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
	})

	cert := generateSelfSignedCert(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	srv := &http.Server{
		Handler: r,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	// Mirrors main()'s own SIGTERM wiring exactly, so this test exercises
	// the real signal-handling path, not a simulation of it.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	logger := testLogger()
	var g runGroup
	g.add(func(ctx context.Context) error {
		return serveHTTPS(ctx, srv, ln, logger)
	})

	runDone := make(chan error, 1)
	go func() { runDone <- g.run(ctx) }()

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for a throwaway cert
	}}
	addr := ln.Addr().String()

	type reqResult struct {
		err    error
		body   string
		status int
	}
	reqDone := make(chan reqResult, 1)
	go func() {
		resp, err := client.Get(fmt.Sprintf("https://%s/slow", addr))
		if err != nil {
			reqDone <- reqResult{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		reqDone <- reqResult{status: resp.StatusCode, body: string(body)}
	}()

	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow handler never started")
	}

	sigSentAt := time.Now()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM to self: %v", err)
	}

	var runErr error
	select {
	case runErr = <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("run group did not exit within 3s of SIGTERM")
	}
	elapsed := time.Since(sigSentAt)
	if elapsed > 3*time.Second {
		t.Fatalf("shutdown took %s, want <= 3s", elapsed)
	}
	if runErr != nil {
		t.Fatalf("run group returned error: %v", runErr)
	}

	select {
	case res := <-reqDone:
		if res.err != nil {
			t.Fatalf("in-flight request was aborted instead of drained: %v", res.err)
		}
		if res.status != http.StatusOK || res.body != "done" {
			t.Fatalf("in-flight request result = (%d, %q), want (200, \"done\")", res.status, res.body)
		}
	default:
		t.Fatal("in-flight request result not available after shutdown completed")
	}
}
