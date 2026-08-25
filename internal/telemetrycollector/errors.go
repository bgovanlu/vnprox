package telemetrycollector

import "errors"

// Sentinel errors this package returns. Per docs/development.md's Go
// standards, each package keeps its own errors.go.
var (
	// ErrBodyTooLarge is returned when a submission exceeds MaxBodyBytes.
	ErrBodyTooLarge = errors.New("telemetrycollector: request body exceeds the size cap")
	// ErrEmptyBody is returned when a submission has no body at all.
	ErrEmptyBody = errors.New("telemetrycollector: request body is empty")
	// ErrRateLimited is returned when a submission is refused because its
	// install-id (or the service-wide bucket) has no tokens left.
	ErrRateLimited = errors.New("telemetrycollector: rate limit exceeded")
	// ErrInvalidInstallID is returned when a path or payload install-id is
	// not ULID-shaped.
	ErrInvalidInstallID = errors.New("telemetrycollector: install-id is not a valid ULID")
)
