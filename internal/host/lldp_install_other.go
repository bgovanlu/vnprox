//go:build !linux

package host

import (
	"context"
	"fmt"
)

// InstallLLDPD is a non-functional stand-in outside Linux — see
// netlink_other.go's doc comment.
func (r *Real) InstallLLDPD(_ context.Context) error {
	return fmt.Errorf("host: InstallLLDPD: %w", ErrUnsupportedPlatform)
}
