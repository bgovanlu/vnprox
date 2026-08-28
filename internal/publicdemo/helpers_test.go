// SPDX-License-Identifier: Apache-2.0

package publicdemo_test

import (
	"io"
	"log/slog"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
