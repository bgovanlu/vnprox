package guestinterior

import (
	"context"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/host"
)

// LXCClient is the subset of internal/host.Reader FetchLXC needs (T-1304):
// an lxc container has no QEMU guest agent, so its interior view is read
// directly from the host side (host.Reader.ContainerInterior/
// ContainerPing) rather than execed inside the guest.
type LXCClient interface {
	ContainerInterior(ctx context.Context, node string, vmid int) (host.ContainerInteriorRaw, error)
	ContainerPing(ctx context.Context, node string, vmid int, targetIP string) (bool, error)
}

// FetchLXC builds an lxc guest's interior View from the host side
// (host.Reader.ContainerInterior/ContainerPing) — the container
// counterpart of FetchQEMU, sharing the same route/DNS/listening-socket
// parsers since both paths ultimately run the same ip -j/cat/ss commands,
// just from different vantage points (inside the guest via the agent for
// qemu, inside the container's netns via nsenter for lxc).
func FetchLXC(ctx context.Context, client LXCClient, node string, vmid int) (View, error) {
	raw, err := client.ContainerInterior(ctx, node, vmid)
	if err != nil {
		return View{}, fmt.Errorf("guestinterior: reading container interior: %w", err)
	}

	interfaces, addresses := ParseIPAddrJSON(raw.AddrJSON)
	routes := ParseIPRouteJSON(raw.RouteJSON)
	dns := ParseResolvConf(string(raw.ResolvConf))
	sockets := ParseSS(string(raw.Sockets))

	reachable := false
	if gw, ok := defaultGateway(routes); ok {
		reachable, _ = client.ContainerPing(ctx, node, vmid, gw)
	}

	return View{
		Interfaces: interfaces, Addresses: addresses, Routes: routes, DNS: dns,
		ListeningSockets: sockets, DefaultGatewayReachable: reachable, Source: SourceLXCHost,
	}, nil
}
