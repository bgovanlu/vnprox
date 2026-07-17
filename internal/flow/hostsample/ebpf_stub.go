//go:build !ebpf

// This file is the default build (no `-tags ebpf`): the eBPF build tag is
// deliberately excluded from `go test ./...`/`make build`/`make check`'s
// default matrix (see the Makefile's test target comment and this
// package's doc comment) — no CI environment is assumed to support real
// eBPF attachment. EBPFSampler here presents the exact same API as the
// real, build-tagged implementation in ebpf.go so cmd/vnproxd's wiring
// compiles identically either way; Probe always fails with a clear
// "not compiled into this binary" reason, exercising AC3's negative path
// deterministically and portably in every default test run.

package hostsample

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// EBPFSampler is the non-tagged stand-in for ebpf.go's real sampler. See
// this file's header comment and this package's doc comment.
type EBPFSampler struct {
	Logger *slog.Logger
	Node   string
}

// NewEBPFSampler builds a stub EBPFSampler for node.
func NewEBPFSampler(node string) *EBPFSampler {
	return &EBPFSampler{Node: node}
}

// Probe always fails: this binary was built without -tags ebpf, so no
// kernel-feature check ever ran and no BPF program can ever be attached.
func (s *EBPFSampler) Probe() error {
	return fmt.Errorf("%w: daemon binary built without the \"ebpf\" build tag", ErrEBPFUnavailable)
}

// Run logs Probe's (always-failing) result and returns nil — the daemon
// continues on conntrack-only (or fully disabled) sampling, never a fatal
// error (AC3).
func (s *EBPFSampler) Run(_ context.Context, _ func(ctx context.Context, records []flow.Record)) error {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("hostsample: eBPF probe failed; falling back to conntrack-only sampling", "error", s.Probe())
	return nil
}
