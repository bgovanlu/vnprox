//go:build !linux

// SPDX-License-Identifier: Apache-2.0

// conntrack_netlink_other.go is the non-Linux stand-in for
// conntrack_netlink_linux.go — see that file's header comment and
// internal/host/netlink_other.go for the same convention. vnprox only
// ships for Linux; this exists purely so `go build ./...`/`go vet ./...`
// keep working on a contributor's non-Linux development machine.

package hostsample

import (
	"context"
	"fmt"
)

type netlinkConntrackReaderStub struct{}

// NewNetlinkConntrackReader outside Linux always yields a reader whose
// ReadEntries fails wrapping ErrConntrackUnavailable — netlink conntrack
// is a Linux-only interface.
func NewNetlinkConntrackReader() ConntrackReader {
	return netlinkConntrackReaderStub{}
}

func (netlinkConntrackReaderStub) ReadEntries(_ context.Context) ([]ConntrackEntry, int, error) {
	return nil, 0, fmt.Errorf("hostsample: %w: not running on Linux", ErrConntrackUnavailable)
}
