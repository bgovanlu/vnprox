// SPDX-License-Identifier: Apache-2.0

package fwlog_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/fwlog"
)

// TestDefaultLogPath_MatchesConfigPackage mirrors internal/change's
// TestDefaultProtectedPath_MatchesConfigPackage: internal/config
// duplicates fwlog.DefaultLogPath as its own DefaultFirewallLogPath
// constant (it cannot import internal/fwlog without an import-cycle risk
// — config is a low-level package many others, including this one's own
// cmd/vnproxd wiring, import). This test keeps the two string literals
// from silently drifting apart.
func TestDefaultLogPath_MatchesConfigPackage(t *testing.T) {
	if config.DefaultFirewallLogPath != fwlog.DefaultLogPath {
		t.Errorf("config.DefaultFirewallLogPath = %q, fwlog.DefaultLogPath = %q — keep them in sync",
			config.DefaultFirewallLogPath, fwlog.DefaultLogPath)
	}
}
