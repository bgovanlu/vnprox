//go:build !linux

// SPDX-License-Identifier: Apache-2.0

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

func (r *Real) DHCPLeases(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: DHCPLeases: %w", ErrUnsupportedPlatform)
}

func (r *Real) CorosyncStatus(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: CorosyncStatus: %w", ErrUnsupportedPlatform)
}

// RouteTableV4/RouteTableV6/RouteRulesV4/RouteRulesV6/FRRRIBV4/FRRRIBV6
// (T-3903's route explorer, internal/route.Fetcher) are not part of the
// Reader interface (see netlink_linux.go's doc comment on the same
// methods) but keep this stub structurally complete for internal/route's
// own seam on non-Linux dev machines.
func (r *Real) RouteTableV4(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: RouteTableV4: %w", ErrUnsupportedPlatform)
}

func (r *Real) RouteTableV6(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: RouteTableV6: %w", ErrUnsupportedPlatform)
}

func (r *Real) RouteRulesV4(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: RouteRulesV4: %w", ErrUnsupportedPlatform)
}

func (r *Real) RouteRulesV6(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: RouteRulesV6: %w", ErrUnsupportedPlatform)
}

func (r *Real) FRRRIBV4(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: FRRRIBV4: %w", ErrUnsupportedPlatform)
}

func (r *Real) FRRRIBV6(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: FRRRIBV6: %w", ErrUnsupportedPlatform)
}

var _ OVSReader = (*Real)(nil)

func (r *Real) OVSStatus(_ context.Context, _ string) ([]OVSBridgeStatus, error) {
	return nil, fmt.Errorf("host: OVSStatus: %w", ErrUnsupportedPlatform)
}

func (r *Real) Services(_ context.Context, _ string) (map[string]bool, error) {
	return nil, fmt.Errorf("host: Services: %w", ErrUnsupportedPlatform)
}

func (r *Real) ContainerInterior(_ context.Context, _ string, _ int) (ContainerInteriorRaw, error) {
	return ContainerInteriorRaw{}, fmt.Errorf("host: ContainerInterior: %w", ErrUnsupportedPlatform)
}

func (r *Real) ContainerPing(_ context.Context, _ string, _ int, _ string) (bool, error) {
	return false, fmt.Errorf("host: ContainerPing: %w", ErrUnsupportedPlatform)
}

func (r *Real) Conntrack(_ context.Context, _ string) ([]ConntrackEntry, error) {
	return nil, fmt.Errorf("host: Conntrack: %w", ErrUnsupportedPlatform)
}

func (r *Real) IPv6RA(_ context.Context, _ string) ([]IPv6RAObservation, error) {
	return nil, fmt.Errorf("host: IPv6RA: %w", ErrUnsupportedPlatform)
}

func (r *Real) MDB(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: MDB: %w", ErrUnsupportedPlatform)
}

func (r *Real) NftRuleset(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("host: NftRuleset: %w", ErrUnsupportedPlatform)
}
