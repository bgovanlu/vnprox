package metrics

import (
	"io"
	"log/slog"
)

// testLogger is a discard-output logger for tests, matching the
// io.Discard-based testLogger helper other packages (internal/api,
// internal/change) define identically for their own tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
