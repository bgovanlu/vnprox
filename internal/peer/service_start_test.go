package peer

import (
	"context"
	"strings"
	"testing"
)

// T-3604 AC1. The point of these tests is that they call the peer route
// DIRECTLY, not through the coordinator: a receiving node that trusts its
// caller's validation has no allow-list, it has a convention. This is the
// check that still holds if the coordinating daemon is compromised, buggy,
// or simply a different version — so it is tested at the boundary where it
// actually runs.
//
// spyLLDPInstaller.StartService deliberately accepts anything, so nothing
// here can pass by accident because the fake was careful.
func TestHandleStartService_ReceivingNodeRefusesAUnitOutsideTheAllowList(t *testing.T) {
	for _, unit := range []string{"sshd", "pve-cluster", "", "frr.service", "DNSMASQ", "dnsmasq && reboot"} {
		t.Run(unit, func(t *testing.T) {
			installer := &spyLLDPInstaller{}
			srv := NewServer(ServerOptions{
				Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger(),
				LLDPInstaller: installer,
			})
			ts := mountedTestServer(t, srv)
			client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
			p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

			err := client.StartService(context.Background(), p, unit, true)
			if err == nil {
				t.Fatalf("unit %q was accepted", unit)
			}
			if len(installer.started) != 0 {
				t.Errorf("host writer was asked to start %v — a refused unit must never reach it", installer.started)
			}
		})
	}
}

func TestHandleStartService_AcceptsTheWatchedUnits(t *testing.T) {
	for _, unit := range []string{"dnsmasq", "frr"} {
		t.Run(unit, func(t *testing.T) {
			installer := &spyLLDPInstaller{}
			srv := NewServer(ServerOptions{
				Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger(),
				LLDPInstaller: installer,
			})
			ts := mountedTestServer(t, srv)
			client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
			p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

			if err := client.StartService(context.Background(), p, unit, true); err != nil {
				t.Fatalf("StartService(%q): %v", unit, err)
			}
			if len(installer.started) != 1 || installer.started[0] != unit {
				t.Errorf("started = %v, want [%s]", installer.started, unit)
			}
		})
	}
}

func TestHandleStartService_RequiresConfirm(t *testing.T) {
	installer := &spyLLDPInstaller{}
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger(),
		LLDPInstaller: installer,
	})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
	p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	if err := client.StartService(context.Background(), p, "dnsmasq", false); err == nil {
		t.Fatal("expected an error when confirm=false")
	}
	if len(installer.started) != 0 {
		t.Errorf("started %v without confirmation", installer.started)
	}
}

func TestHandleStartService_Unconfigured503s(t *testing.T) {
	srv := NewServer(ServerOptions{Secrets: newStaticSecretStore(testSecret), Version: "test", Logger: discardLogger()})
	ts := mountedTestServer(t, srv)
	client := NewClient(ClientOptions{Secrets: newStaticSecretStore(testSecret), Scheme: "http"})
	p := Peer{Node: "pve1", Addr: ts.Listener.Addr().String()}

	err := client.StartService(context.Background(), p, "dnsmasq", true)
	if err == nil {
		t.Fatal("expected an error when no host service manager is configured")
	}
	if !strings.Contains(err.Error(), "peer_unavailable") && !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %v, want a peer_unavailable-shaped failure", err)
	}
}
