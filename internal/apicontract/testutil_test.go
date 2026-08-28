// SPDX-License-Identifier: Apache-2.0

package apicontract

import (
	"io"
	"io/fs"
	"log/slog"
	"testing/fstest"
)

// testDistFS/testLogger mirror internal/api/router_test.go's own helpers of
// the same name (unexported there, so reimplemented here) — a minimal
// embedded-SPA stand-in and a discard logger, neither of which is part of
// what this suite is testing.
func testDistFS() fs.FS {
	return fstest.MapFS{
		"index.html": {Data: []byte("<html>vnprox contract test shell</html>")},
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
