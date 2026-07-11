//go:build !linux

package host

import (
	"context"
	"fmt"
)

// Real is a non-functional stand-in outside Linux. vnprox only ships for
// Linux; this stub exists purely so `go build ./...`/`go vet ./...` keep
// working on a contributor's non-Linux development machine instead of
// failing to compile the whole module.
type Real struct{}

// NewReal constructs a Real reader. Every method returns
// ErrUnsupportedPlatform outside Linux.
func NewReal() *Real { return &Real{} }

var _ Reader = (*Real)(nil)

func (r *Real) InterfacesFile(_ context.Context, _ string, _ bool) (string, error) {
	return "", fmt.Errorf("host: InterfacesFile: %w", ErrUnsupportedPlatform)
}

func (r *Real) Links(_ context.Context, _ string) ([]LinkState, error) {
	return nil, fmt.Errorf("host: Links: %w", ErrUnsupportedPlatform)
}

func (r *Real) LLDP(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: LLDP: %w", ErrUnsupportedPlatform)
}

func (r *Real) Stats(_ context.Context, _ string) (map[string]IfaceStats, error) {
	return nil, fmt.Errorf("host: Stats: %w", ErrUnsupportedPlatform)
}

func (r *Real) FRRBGPSummary(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: FRRBGPSummary: %w", ErrUnsupportedPlatform)
}

func (r *Real) FRREVPNVNI(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: FRREVPNVNI: %w", ErrUnsupportedPlatform)
}
