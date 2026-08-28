// SPDX-License-Identifier: Apache-2.0

// recovery.go adds LoadRecoveryOnly, the config reader T-1901's
// `vnproxctl backup` / `vnproxctl restore` use.
//
// It exists for the same reason LoadStorageOnly (T-607) does: the full
// Load runs validate(), which resolves a TLS certificate/key pair and
// fails outright when neither an explicit override nor a real Proxmox
// node's /etc/pve/local/pve-ssl.pem is present. Backup and restore are
// disaster-recovery commands — the situations they exist for include "this
// node's certificate is the broken thing" and "this is a bare rescue host
// with the .deb installed and nothing else" — so they must not inherit
// that dependency.
//
// LoadStorageOnly alone is not enough: restore additionally needs the
// [server] listen address (to detect a live daemon by probing whether the
// port is bound) and the key-file paths (so `--include-keys` collects
// exactly what this node's config says it has, rather than a hardcoded
// list). Rather than widen StorageConfig's meaning, this returns its own
// struct that embeds it.

package config

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/BurntSushi/toml"
)

// RecoveryConfig is the subset of vnprox.toml the daemon-independent
// backup/restore commands need, resolved to effective values (defaults
// applied) but *not* validated — see this file's doc comment.
type RecoveryConfig struct {
	StorageConfig

	// Listen is [server] listen, verbatim from the file (defaulted to
	// DefaultListen). Restore probes it to detect a running daemon that is
	// too old to take internal/store's runtime lock.
	Listen string

	// PVETokenFile, MetricsKeyFile, BlueprintSigningKeyFile and
	// OIDCClientSecretFile are the on-disk secret files this node's config
	// declares, beyond StorageConfig.SessionKeyFile. `backup
	// --include-keys` collects each one that exists; a plain backup
	// collects none of them.
	//
	// OIDCClientSecretFile has no default (OIDC is opt-in), so it is empty
	// unless the file explicitly sets it.
	PVETokenFile            string
	MetricsKeyFile          string
	BlueprintSigningKeyFile string
	OIDCClientSecretFile    string
}

// LoadRecoveryOnly reads and parses the config file at path and returns the
// recovery-relevant subset, without running Load's full validate().
func LoadRecoveryOnly(path string, logger *slog.Logger) (RecoveryConfig, error) {
	if logger == nil {
		logger = slog.Default()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return RecoveryConfig{}, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var raw rawConfig
	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		return RecoveryConfig{}, fmt.Errorf("%w: parsing config file %s: %v", ErrInvalidConfig, path, err)
	}
	for _, key := range meta.Undecoded() {
		logger.Warn("config: unrecognized key, ignoring", "key", key.String(), "file", path)
	}

	cfg := RecoveryConfig{
		StorageConfig: StorageConfig{
			DBPath:         firstNonEmpty(raw.Storage.DBPath, DefaultDBPath),
			SessionKeyFile: firstNonEmpty(raw.Storage.SessionKeyFile, DefaultSessionKeyFile),
		},
		Listen:                  firstNonEmpty(raw.Server.Listen, DefaultListen),
		PVETokenFile:            firstNonEmpty(raw.PVE.TokenFile, DefaultPVETokenFile),
		MetricsKeyFile:          firstNonEmpty(raw.Metrics.KeyFile, DefaultMetricsKeyFile),
		BlueprintSigningKeyFile: firstNonEmpty(raw.Blueprint.SigningKeyFile, DefaultBlueprintSigningKeyFile),
	}
	cfg.OIDCClientSecretFile = raw.OIDC.ClientSecretFile
	return cfg, nil
}
