//go:build ebpf

// This file is only compiled with `-tags ebpf` (excluded from the default
// `go test ./...`/`make build`/`make check` matrix — see the Makefile's
// test target comment and this package's doc comment). Without the tag,
// ebpf_stub.go provides the same EBPFSampler API with a Probe that always
// fails "not compiled into this binary".

package hostsample

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// minEBPFKernelMajor/Minor is the earliest kernel version this probe
// accepts: CAP_PERFMON (the finer-grained perf-event capability this
// sampler needs instead of the broad CAP_SYS_ADMIN older kernels required
// for BPF program loading) was introduced in Linux 5.8.
const (
	minEBPFKernelMajor = 5
	minEBPFKernelMinor = 8

	// capPERFMON/capBPF are the Linux capability bit numbers (bit index
	// into /proc/self/status's CapEff bitmask) this sampler needs —
	// linux/capability.h: CAP_PERFMON = 38, CAP_BPF = 39 (both added in
	// the same Linux 5.8 capability split referenced above).
	capPERFMON = 38
	capBPF     = 39

	// btfPath is where CO-RE (Compile Once – Run Everywhere) BTF type
	// info for the running kernel lives when the kernel was built with
	// CONFIG_DEBUG_INFO_BTF=y — required for a single compiled BPF
	// program to attach across the range of kernel versions
	// docs/architecture.md D9 targets (PVE 8.2+/9.x) without a per-kernel
	// rebuild.
	btfPath = "/sys/kernel/btf/vmlinux"
)

// EBPFSampler is the per-bridge eBPF-based flow sampler's build-tagged
// entry point (see this package's doc comment for the full requirements
// list and ebpf_stub.go for the non-tagged fallback). Probe performs a real
// kernel-feature/capability check; actual per-packet BPF program
// attachment is NOT implemented in this codebase (no third-party eBPF
// loader dependency has been added — flagged in
// planning/reports/T-1004.md as a deliberate scope limit, not an
// oversight), so Run always falls back to conntrack-only sampling after
// logging Probe's outcome, whether Probe passes or fails.
type EBPFSampler struct {
	Node   string
	Logger *slog.Logger
}

// NewEBPFSampler builds an EBPFSampler for node.
func NewEBPFSampler(node string) *EBPFSampler {
	return &EBPFSampler{Node: node}
}

// Probe checks this host's runtime eBPF feature support: Linux only, a
// kernel version at or above minEBPFKernelMajor.minEBPFKernelMinor, this
// process's effective CAP_BPF and CAP_PERFMON capabilities (read from
// /proc/self/status's CapEff bitmask — the same field `capsh --print`/
// `getpcaps` read), and BTF (CO-RE) support at btfPath. Returns a wrapped
// ErrEBPFUnavailable naming the first failing check; nil means every check
// passed (attachment itself is still not implemented — see this type's doc
// comment).
func (s *EBPFSampler) Probe() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("%w: requires Linux (running on %s)", ErrEBPFUnavailable, runtime.GOOS)
	}

	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return fmt.Errorf("%w: reading kernel version: %v", ErrEBPFUnavailable, err)
	}
	release := charsToString(uts.Release[:])
	major, minor, ok := parseKernelVersion(release)
	if !ok {
		return fmt.Errorf("%w: could not parse kernel release %q", ErrEBPFUnavailable, release)
	}
	if major < minEBPFKernelMajor || (major == minEBPFKernelMajor && minor < minEBPFKernelMinor) {
		return fmt.Errorf("%w: kernel %s is older than the required %d.%d (CAP_PERFMON/CAP_BPF split)", ErrEBPFUnavailable, release, minEBPFKernelMajor, minEBPFKernelMinor)
	}

	capEff, err := effectiveCapabilities()
	if err != nil {
		return fmt.Errorf("%w: reading process capabilities: %v", ErrEBPFUnavailable, err)
	}
	if capEff&(1<<capBPF) == 0 {
		return fmt.Errorf("%w: missing CAP_BPF", ErrEBPFUnavailable)
	}
	if capEff&(1<<capPERFMON) == 0 {
		return fmt.Errorf("%w: missing CAP_PERFMON", ErrEBPFUnavailable)
	}

	if _, err := os.Stat(btfPath); err != nil {
		return fmt.Errorf("%w: no BTF (CO-RE) support at %s: %v", ErrEBPFUnavailable, btfPath, err)
	}

	return nil
}

// Run probes for eBPF support and logs the outcome. Real per-bridge BPF
// program attachment is not implemented in this codebase (see this type's
// doc comment), so Run always returns nil (a no-op; the daemon continues
// on conntrack-only or fully-disabled sampling) after logging — never a
// fatal error (AC3).
func (s *EBPFSampler) Run(_ context.Context, _ func(ctx context.Context, records []flow.Record)) error {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if err := s.Probe(); err != nil {
		logger.Warn("hostsample: eBPF probe failed; falling back to conntrack-only sampling", "error", err)
		return nil
	}
	logger.Warn("hostsample: eBPF probe passed but per-bridge BPF program attachment is not implemented in this build; falling back to conntrack-only sampling")
	return nil
}

// parseKernelVersion extracts the leading "MAJOR.MINOR" of a
// uname-reported release string (e.g. "6.8.0-40-generic" -> 6, 8),
// tolerating any non-numeric suffix on the minor field.
func parseKernelVersion(release string) (major, minor int, ok bool) {
	parts := strings.SplitN(release, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	minorField := parts[1]
	for i, r := range minorField {
		if r < '0' || r > '9' {
			minorField = minorField[:i]
			break
		}
	}
	maj, err1 := strconv.Atoi(parts[0])
	minv, err2 := strconv.Atoi(minorField)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, minv, true
}

// charsToString converts a NUL-terminated fixed-size byte array (uname(2)'s
// C-string fields, as unix.Utsname exposes them) to a Go string.
func charsToString(b []byte) string {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

// effectiveCapabilities reads this process's CapEff bitmask from
// /proc/self/status — the same field `capsh --print`/`getpcaps` read,
// avoiding a capget(2) syscall wrapper this codebase has no other need for.
func effectiveCapabilities() (uint64, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if rest, ok := strings.CutPrefix(line, "CapEff:"); ok {
			return strconv.ParseUint(strings.TrimSpace(rest), 16, 64)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("CapEff field not found in /proc/self/status")
}
