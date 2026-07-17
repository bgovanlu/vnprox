//go:build !linux

package host

import (
	"context"
	"fmt"
)

// Neighbors implements Reader for the non-Linux Real stub (see
// netlink_other.go's doc comment on why this stub exists at all: vnprox
// only ships for Linux, but `go build`/`go vet` must still succeed on a
// contributor's non-Linux machine).
func (r *Real) Neighbors(_ context.Context, _ string) ([]Neighbor, error) {
	return nil, fmt.Errorf("host: Neighbors: %w", ErrUnsupportedPlatform)
}
