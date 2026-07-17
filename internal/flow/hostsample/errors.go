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
)
