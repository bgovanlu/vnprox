package peer

import (
	"context"
	"errors"
	"testing"
)

// spyLLDPInstaller records InstallLLDPD calls and can be made to fail.
type spyLLDPInstaller struct {
	failErr error
	calls   int
}

func (s *spyLLDPInstaller) InstallLLDPD(_ context.Context) error {
	s.calls++
	return s.failErr
}

// TestHandleInstallLLDPD_RequiresConfirm checks the peer route's
// "changeset-like confirmation" gate (docs/features/lldp-discovery.md §1):
// a request without confirm:true is rejected before the installer runs.
func TestHandleInstallLLDPD_RequiresConfirm(t *testing.T) {
	installer := &spyLLDPInstaller{}
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger(),
		LLDPInstaller: installer,
	})
	ts := mountedTestServer(t, srv)

	client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
	peer := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	if err := client.InstallLLDPD(context.Background(), peer, false); err == nil {
		t.Fatal("expected an error when confirm=false")
	}
	if installer.calls != 0 {
		t.Errorf("installer called %d times, want 0 (unconfirmed request must not reach it)", installer.calls)
	}

	if err := client.InstallLLDPD(context.Background(), peer, true); err != nil {
		t.Fatalf("InstallLLDPD(confirm=true): %v", err)
	}
	if installer.calls != 1 {
		t.Errorf("installer called %d times, want 1", installer.calls)
	}
}

// TestHandleInstallLLDPD_Unconfigured503s checks the nil-safety documented
// on ServerOptions.LLDPInstaller.
func TestHandleInstallLLDPD_Unconfigured503s(t *testing.T) {
	srv := NewServer(ServerOptions{Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger()})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
	peer := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	err := client.InstallLLDPD(context.Background(), peer, true)
	if err == nil {
		t.Fatal("expected an error when no LLDPInstaller is configured")
	}
	var rerr *ResponseError
	if !errors.As(err, &rerr) || rerr.StatusCode != 503 {
		t.Errorf("error = %v, want a 503 ResponseError", err)
	}
}

// TestHandleInstallLLDPD_InstallerError checks the installer's own error
// surfaces as a 500 host_write_failed, matching the other host-write routes.
func TestHandleInstallLLDPD_InstallerError(t *testing.T) {
	installer := &spyLLDPInstaller{failErr: errors.New("apt-get exit status 100")}
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger(),
		LLDPInstaller: installer,
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
	peer := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	err := client.InstallLLDPD(context.Background(), peer, true)
	if err == nil {
		t.Fatal("expected the installer's error to surface")
	}
	var rerr *ResponseError
	if !errors.As(err, &rerr) || rerr.Code != "host_write_failed" {
		t.Errorf("error = %v, want code host_write_failed", err)
	}
}
