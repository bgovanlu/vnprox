// SPDX-License-Identifier: Apache-2.0

package guestinterior

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
)

// fakeLXCClient is a scripted LXCClient test double.
type fakeLXCClient struct {
	interiorErr   error
	pingErr       error
	raw           host.ContainerInteriorRaw
	pingCalls     []string
	pingReachable bool
}

func (f *fakeLXCClient) ContainerInterior(context.Context, string, int) (host.ContainerInteriorRaw, error) {
	return f.raw, f.interiorErr
}

func (f *fakeLXCClient) ContainerPing(_ context.Context, _ string, _ int, targetIP string) (bool, error) {
	f.pingCalls = append(f.pingCalls, targetIP)
	return f.pingReachable, f.pingErr
}

// TestFetchLXC_NotFound covers the "no such container" propagation path.
func TestFetchLXC_NotFound(t *testing.T) {
	client := &fakeLXCClient{interiorErr: host.ErrNotFound}
	if _, err := FetchLXC(context.Background(), client, "pve2", 201); !errors.Is(err, host.ErrNotFound) {
		t.Fatalf("FetchLXC() error = %v, want wrapping host.ErrNotFound", err)
	}
}

// TestFetchLXC_SameResponseShapeAsQEMU is AC2: an lxc fixture-shaped read
// produces the same View shape the qemu path does (interfaces/addresses/
// routes/dns/listeningSockets/defaultGatewayReachable), with
// source: "lxc-host".
func TestFetchLXC_SameResponseShapeAsQEMU(t *testing.T) {
	client := &fakeLXCClient{
		raw: host.ContainerInteriorRaw{
			AddrJSON:   []byte(`[{"ifname":"eth0","address":"bc:24:11:aa:01:01","flags":["BROADCAST","UP"],"addr_info":[{"family":"inet","local":"10.10.0.201","prefixlen":24}]}]`),
			RouteJSON:  []byte(`[{"dst":"default","gateway":"10.10.0.1","dev":"eth0"},{"dst":"10.10.0.0/24","dev":"eth0"}]`),
			ResolvConf: []byte("nameserver 1.1.1.1\nsearch example.com\n"),
			Sockets:    []byte("tcp   LISTEN 0      128          0.0.0.0:80          0.0.0.0:*\n"),
		},
		pingReachable: true,
	}

	view, err := FetchLXC(context.Background(), client, "pve2", 201)
	if err != nil {
		t.Fatalf("FetchLXC: %v", err)
	}
	if view.Source != SourceLXCHost {
		t.Errorf("Source = %q, want %q", view.Source, SourceLXCHost)
	}
	if len(view.Interfaces) != 1 || view.Interfaces[0].Name != "eth0" || !view.Interfaces[0].Up {
		t.Errorf("Interfaces = %+v, want one up eth0", view.Interfaces)
	}
	if len(view.Addresses) != 1 || view.Addresses[0].IP != "10.10.0.201" {
		t.Errorf("Addresses = %+v, want 10.10.0.201", view.Addresses)
	}
	if len(view.Routes) != 2 {
		t.Errorf("Routes = %+v, want 2 entries", view.Routes)
	}
	if len(view.DNS.Nameservers) != 1 || view.DNS.Nameservers[0] != "1.1.1.1" {
		t.Errorf("DNS = %+v, want nameserver 1.1.1.1", view.DNS)
	}
	if len(view.ListeningSockets) != 1 || view.ListeningSockets[0].LocalPort != 80 {
		t.Errorf("ListeningSockets = %+v, want one entry on port 80", view.ListeningSockets)
	}
	if !view.DefaultGatewayReachable {
		t.Errorf("DefaultGatewayReachable = false, want true")
	}
	if len(client.pingCalls) != 1 || client.pingCalls[0] != "10.10.0.1" {
		t.Errorf("pingCalls = %v, want exactly one ping to the default gateway 10.10.0.1", client.pingCalls)
	}
}

// TestFetchLXC_NoDefaultRoute: no default route in the parsed routing
// table skips the ping entirely rather than probing a zero-value gateway.
func TestFetchLXC_NoDefaultRoute(t *testing.T) {
	client := &fakeLXCClient{
		raw: host.ContainerInteriorRaw{RouteJSON: []byte(`[{"dst":"10.10.0.0/24","dev":"eth0"}]`)},
	}
	view, err := FetchLXC(context.Background(), client, "pve2", 201)
	if err != nil {
		t.Fatalf("FetchLXC: %v", err)
	}
	if view.DefaultGatewayReachable {
		t.Errorf("DefaultGatewayReachable = true, want false (no default route)")
	}
	if len(client.pingCalls) != 0 {
		t.Errorf("pingCalls = %v, want none (no default route to probe)", client.pingCalls)
	}
}
