// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/store"
)

// setupMetricsExporterToken loads — generating on first run if absent, the
// same "belt and suspenders" convention setupAuth's session key follows —
// T-1001's Prometheus scrape token (docs/security.md's Authentication
// section: "generated at install alongside the session key"). Returns nil
// when cfg.Metrics.Enabled is false: cmd/vnproxd never touches the key file
// at all in that case, and server.go's api.MetricsExporterConfig.Token
// stays empty, which mountMetricsExporterRoutes treats as "don't mount GET
// /metrics" — the same nil/empty-skips-mounting convention every other
// optional dependency in internal/api.Options follows.
func setupMetricsExporterToken(cfg *config.Config, logger *slog.Logger) ([]byte, error) {
	if !cfg.Metrics.Enabled {
		return nil, nil
	}

	if _, statErr := os.Stat(cfg.Metrics.KeyFile); errors.Is(statErr, os.ErrNotExist) {
		logger.Info("metrics: generating scrape token", "path", cfg.Metrics.KeyFile)
		if genErr := store.GenerateHexTokenFile(cfg.Metrics.KeyFile); genErr != nil {
			return nil, fmt.Errorf("generating metrics scrape token: %w", genErr)
		}
	} else if statErr != nil {
		return nil, fmt.Errorf("checking metrics scrape token file %s: %w", cfg.Metrics.KeyFile, statErr)
	}

	token, err := store.LoadHexTokenFile(cfg.Metrics.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading metrics scrape token: %w", err)
	}
	return []byte(token), nil
}
