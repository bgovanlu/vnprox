// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/config"
)

// setupBlueprintSigningKey loads — generating on first run if absent, the
// same "belt and suspenders" convention setupMetricsExporterToken's scrape
// token follows — T-1107's bundle-signing Ed25519 identity
// (docs/features/blueprints.md §5: "generated at first use ...
// /etc/vnprox/keys/blueprint-signing.key, root:root 0600").
func setupBlueprintSigningKey(cfg *config.Config, logger *slog.Logger) (ed25519.PrivateKey, error) {
	path := cfg.Blueprint.SigningKeyFile
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		logger.Info("blueprint: generating bundle-signing key", "path", path)
		if genErr := blueprint.GenerateSigningKeyFile(path); genErr != nil {
			return nil, fmt.Errorf("generating blueprint signing key: %w", genErr)
		}
	} else if statErr != nil {
		return nil, fmt.Errorf("checking blueprint signing key file %s: %w", path, statErr)
	}

	priv, err := blueprint.LoadSigningKeyFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading blueprint signing key: %w", err)
	}
	return priv, nil
}
