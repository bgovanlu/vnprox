// SPDX-License-Identifier: Apache-2.0

package hostsample

import "errors"

// Sentinel errors, per docs/development.md's Go standards ("sentinel errors
// in each package's errors.go").
var (
	// ErrEBPFUnavailable wraps every reason the eBPF sampler's kernel-
	// feature probe can fail: not compiled into this binary (no -tags
	// ebpf), wrong OS, kernel too old, missing CAP_BPF/CAP_PERFMON, or no
	// BTF (CO-RE) support. Callers (cmd/vnproxd's wiring) log the wrapped
	// detail and fall back to conntrack-only (or fully disabled) sampling
	// — never fatal.
	ErrEBPFUnavailable = errors.New("hostsample: eBPF sampling unavailable")

	// ErrConntrackUnavailable indicates the netlink conntrack interface
	// itself is not usable on this node: refused for lack of CAP_NET_ADMIN
	// (see ErrConntrackPermissionDenied, which additionally wraps this for
	// that specific cause) or the kernel has no nf_conntrack netlink
	// support at all (module not loaded, or built without conntrack
	// netlink support). NewNetlinkConntrackReader
	// (conntrack_netlink_linux.go, T-3711) wraps this so
	// ConntrackSampler.Run can report it once and stop polling rather than
	// retrying forever, and so cmd/vnproxd's wiring can log a clear
	// startup-time reason. Independent from internal/host's own
	// same-named sentinel — this package and internal/host each read
	// conntrack for a different purpose via a different reader, per
	// docs/development.md's "sentinel errors in each package's errors.go".
	ErrConntrackUnavailable = errors.New("hostsample: conntrack netlink interface unavailable")

	// ErrConntrackPermissionDenied indicates the netlink conntrack dump
	// was refused specifically for lack of CAP_NET_ADMIN (T-3711) —
	// distinguished from ErrConntrackUnavailable's other causes because
	// the operator fix differs: grant the capability
	// (packaging/systemd/vnprox.service already does for a packaged
	// install). Always wrapped together with ErrConntrackUnavailable,
	// never alone.
	ErrConntrackPermissionDenied = errors.New("hostsample: conntrack netlink read requires CAP_NET_ADMIN")
)
