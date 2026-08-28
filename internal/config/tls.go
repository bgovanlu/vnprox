// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// CertProvider serves the current TLS certificate for the HTTPS listener
// and reloads it from disk without a daemon restart.
//
// Reload is triggered two ways:
//   - immediately, on SIGHUP delivered to the sighup channel passed to
//     Watch (the explicit, operator-driven path: "I just renewed the
//     cert, pick it up now");
//   - by polling the certificate/key file modification times, as a
//     fallback that requires no operator action.
//
// Design note (fsnotify vs. polling): we poll rather than use
// github.com/fsnotify/fsnotify. Certificate files change on the order of
// days to months (PVE cert renewal, or an admin swapping an override), so
// sub-second inotify latency has no practical value here, and polling
// avoids adding a dependency plus its inotify-instance/watch-descriptor
// lifecycle (which also needs care across editors that replace files via
// rename, a case naive fsnotify watches can miss without rewatching
// anyway). SIGHUP remains the fast path for the common "reload now" case.
type CertProvider struct {
	certMod  time.Time
	keyMod   time.Time
	logger   *slog.Logger
	cert     *tls.Certificate
	certPath string
	keyPath  string
	mu       sync.RWMutex
}

// NewCertProvider loads the keypair at certPath/keyPath and returns a
// CertProvider serving it. Both files must exist and form a valid keypair;
// resolving *which* paths to use (PVE default vs. explicit override) is
// Config's job (see resolveTLSPaths), not this constructor's.
func NewCertProvider(certPath, keyPath string, logger *slog.Logger) (*CertProvider, error) {
	if logger == nil {
		logger = slog.Default()
	}
	cp := &CertProvider{certPath: certPath, keyPath: keyPath, logger: logger}
	if err := cp.reload(); err != nil {
		return nil, err
	}
	return cp, nil
}

func (cp *CertProvider) reload() error {
	cert, err := tls.LoadX509KeyPair(cp.certPath, cp.keyPath)
	if err != nil {
		return fmt.Errorf("loading TLS keypair (cert=%s key=%s): %w", cp.certPath, cp.keyPath, err)
	}
	certMod, keyMod := statModTime(cp.certPath), statModTime(cp.keyPath)

	cp.mu.Lock()
	cp.cert = &cert
	cp.certMod = certMod
	cp.keyMod = keyMod
	cp.mu.Unlock()
	return nil
}

// GetCertificate implements the tls.Config.GetCertificate hook, so callers
// wire it in with &tls.Config{GetCertificate: cp.GetCertificate}.
func (cp *CertProvider) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return cp.cert, nil
}

func (cp *CertProvider) changedOnDisk() bool {
	cp.mu.RLock()
	certMod, keyMod := cp.certMod, cp.keyMod
	cp.mu.RUnlock()
	return !statModTime(cp.certPath).Equal(certMod) || !statModTime(cp.keyPath).Equal(keyMod)
}

func statModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// Watch runs the reload loop until ctx is cancelled, at which point it
// returns nil. sighup should be a channel registered (via signal.Notify)
// for syscall.SIGHUP by the caller; pollInterval controls the mtime-polling
// fallback (docs/deployment.md-adjacent operational default: 30s is a
// reasonable balance of promptness vs. stat() overhead for a rarely-firing
// event).
func (cp *CertProvider) Watch(ctx context.Context, sighup <-chan os.Signal, pollInterval time.Duration) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sighup:
			cp.logger.Info("tls: reload requested via SIGHUP", "cert", cp.certPath)
			if err := cp.reload(); err != nil {
				cp.logger.Error("tls: reload failed", "error", err)
			} else {
				cp.logger.Info("tls: certificate reloaded")
			}
		case <-ticker.C:
			if cp.changedOnDisk() {
				cp.logger.Info("tls: certificate file changed on disk, reloading", "cert", cp.certPath)
				if err := cp.reload(); err != nil {
					cp.logger.Error("tls: reload failed", "error", err)
				}
			}
		}
	}
}
