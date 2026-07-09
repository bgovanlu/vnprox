package config

import (
	"bytes"
	"context"
	"crypto/tls"
	"os"
	"testing"
	"time"
)

func TestCertProvider_LoadsInitialKeypair(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())

	cp, err := NewCertProvider(certPath, keyPath, discardLogger())
	if err != nil {
		t.Fatalf("NewCertProvider: %v", err)
	}
	cert, err := cp.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert == nil {
		t.Fatal("GetCertificate returned nil cert")
	}
}

func TestNewCertProvider_MissingFilesErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := NewCertProvider(dir+"/missing-cert.pem", dir+"/missing-key.pem", discardLogger())
	if err == nil {
		t.Fatal("expected an error for a missing keypair, got nil")
	}
}

func TestCertProvider_Watch_ReloadsOnSIGHUP(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCert(t, dir)

	cp, err := NewCertProvider(certPath, keyPath, discardLogger())
	if err != nil {
		t.Fatalf("NewCertProvider: %v", err)
	}
	original, _ := cp.GetCertificate(nil)

	// Replace the keypair on disk with a different one.
	newCertPath, newKeyPath := writeTestCert(t, t.TempDir())
	mustCopy(t, newCertPath, certPath)
	mustCopy(t, newKeyPath, keyPath)

	sighup := make(chan os.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cp.Watch(ctx, sighup, time.Hour) }()

	sighup <- os.Interrupt // stand-in signal value; Watch treats any receive as "reload"

	waitForCondition(t, time.Second, func() bool {
		cur, _ := cp.GetCertificate(nil)
		return cur != nil && !certEqual(cur, original)
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Watch did not return after ctx cancellation")
	}
}

func TestCertProvider_Watch_ReloadsOnPoll(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCert(t, dir)

	cp, err := NewCertProvider(certPath, keyPath, discardLogger())
	if err != nil {
		t.Fatalf("NewCertProvider: %v", err)
	}
	original, _ := cp.GetCertificate(nil)

	newCertPath, newKeyPath := writeTestCert(t, t.TempDir())
	mustCopy(t, newCertPath, certPath)
	mustCopy(t, newKeyPath, keyPath)
	// Ensure the mtime actually advances on filesystems with coarse
	// resolution.
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(certPath, future, future); err != nil {
		t.Fatalf("Chtimes cert: %v", err)
	}
	if err := os.Chtimes(keyPath, future, future); err != nil {
		t.Fatalf("Chtimes key: %v", err)
	}

	sighup := make(chan os.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cp.Watch(ctx, sighup, 20*time.Millisecond) }()

	waitForCondition(t, 2*time.Second, func() bool {
		cur, _ := cp.GetCertificate(nil)
		return cur != nil && !certEqual(cur, original)
	})

	cancel()
	<-done
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func mustCopy(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", dst, err)
	}
}

func certEqual(a, b *tls.Certificate) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Certificate) != len(b.Certificate) {
		return false
	}
	for i := range a.Certificate {
		if !bytes.Equal(a.Certificate[i], b.Certificate[i]) {
			return false
		}
	}
	return true
}
