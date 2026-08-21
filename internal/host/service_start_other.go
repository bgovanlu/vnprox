//go:build !linux

package host

import "context"

// StartService has no implementation outside Linux (vnprox only ships for
// Linux; this keeps `go build`/`go vet` green on a developer's other OS).
// It refuses rather than pretending to succeed — a caller that got a nil
// error here would report to an operator that a service had been started
// when nothing happened at all.
func (r *Real) StartService(_ context.Context, _ string) error {
	return ErrUnsupportedPlatform
}
