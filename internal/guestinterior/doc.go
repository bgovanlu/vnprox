// Package guestinterior implements T-1304's guest network interior
// inspector: a guest's own inside view of its network — interfaces,
// addresses, routing table, DNS config, listening sockets, and
// default-gateway reachability — read two different ways depending on
// guest type:
//
//   - qemu: via the QEMU guest agent (internal/pve's AgentExec/
//     AgentExecStatus/GetGuestAgentInterfaces, T-802's existing seam),
//     source "qemu-ga".
//   - lxc: a container has no QEMU guest agent, so the same view is read
//     directly from the host side (internal/host.Reader's
//     ContainerInterior/ContainerPing, T-1304's own addition), source
//     "lxc-host".
//
// Both sources feed the same View shape and, where possible, the same
// parsers (ParseIPRouteJSON/ParseResolvConf/ParseSS): `ip -j route show`,
// `cat /etc/resolv.conf`, and `ss -H -tuln` are the identical command
// shapes both paths run (inside the guest via the agent for qemu, inside
// the container's netns via nsenter for lxc), so one parsing
// implementation covers both.
//
// This package is deliberately read-only: it has no write path at all,
// matching the task card's "strictly read-only into the guest" mandate.
// It never trusts the guest's self-report as ground truth — internal/api's
// guestinterior.go layers an IPAM cross-check annotation on top of the
// View this package returns, never folding that judgment into the package
// itself (mirrors internal/probe's own "the engine reports facts, the API
// layer adds interpretation" split).
package guestinterior
