package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/demo"
	"github.com/bgovanlu/vnprox/internal/host"
)

// T-2801: `vnproxd --demo`.
//
// Two shapes, one code path:
//
//	vnproxd --demo                 zero configuration. Writes a demo config
//	                               and a throwaway TLS keypair into a demo
//	                               data directory on first run, then loads
//	                               it. This is the shape someone who just
//	                               ran the install script gets.
//	vnproxd --demo --config X      loads X, which must not configure a PVE
//	                               endpoint. This is the shape CI and the
//	                               e2e suite use, because they need to pick
//	                               the listen port and the store path.
//
// Both go through config.LoadDemo, so both are subject to AC3's refusal.

// demoRuntime is a demo daemon's synthetic world, or nil in a normal
// daemon. Every method is nil-safe, so the wiring in server.go reads
// `demoRT.httpClient()` without a branch at each call site — the branch is
// here, once.
type demoRuntime struct {
	mode *demo.Mode
}

// httpClient is the transport every PVE client in a demo daemon is built
// with: in-process, no dialer, no socket (internal/demo/transport.go). nil
// for a normal daemon, which is exactly what pve.Config.HTTPClient's zero
// value means ("build a real one").
func (d *demoRuntime) httpClient() *http.Client {
	if d == nil {
		return nil
	}
	return d.mode.HTTPClient()
}

// hostReader is the fixture-backed host.Reader a demo daemon's collectors
// use. A normal daemon gets host.NewReal(); a demo daemon must never read
// the machine it happens to be running on, because the demo user's own
// laptop is not part of the story being told.
func (d *demoRuntime) hostReader() host.Reader {
	if d == nil {
		return host.NewReal()
	}
	return d.mode.HostReader()
}

func (d *demoRuntime) enabled() bool { return d != nil }

// demoPVEConfig is the [pve] section a demo daemon runs with. It is
// assembled here and never read from a file — config.LoadDemo blanks the
// section and refuses a config that set it.
func demoPVEConfig() config.PVEConfig {
	return config.PVEConfig{
		APIURL:         demo.APIURL,
		TicketUsername: demo.TicketUsername,
		TicketPassword: demo.TicketPassword,
	}
}

// setupDemo loads the demo dataset and resolves the demo daemon's config.
//
// configPath is "" for the zero-argument form, in which case a config and a
// TLS keypair are materialized under dataDir first.
func setupDemo(configPath, dataDir string, logger *slog.Logger) (*config.Config, *demoRuntime, error) {
	mode, err := demo.New(logger)
	if err != nil {
		return nil, nil, fmt.Errorf("loading the demo dataset: %w", err)
	}

	if configPath == "" {
		configPath, err = materializeDemoConfig(dataDir, logger)
		if err != nil {
			return nil, nil, err
		}
	}

	cfg, err := config.LoadDemo(configPath, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("loading demo config %s: %w", configPath, err)
	}
	cfg.PVE = demoPVEConfig()

	logger.Warn("DEMO MODE: this daemon is running against an embedded synthetic cluster. "+
		"No Proxmox VE endpoint is configured, nothing is sent to any network, and every mutating API answers "+
		"with what it would have done instead of doing it.",
		"cluster", mode.ClusterName(), "config", configPath, "listen", cfg.Server.Listen)

	return cfg, &demoRuntime{mode: mode}, nil
}

// DefaultDemoDirName is the demo data directory's name under the user's
// state directory.
const DefaultDemoDirName = "vnprox-demo"

// defaultDemoDir picks where a zero-argument demo run keeps its store,
// config and throwaway certificate.
//
// $XDG_STATE_HOME (or ~/.local/state) rather than /var/lib/vnprox, because
// the whole point of demo mode is that it runs for someone who has not
// installed anything and may not be root. Falling back to ./vnprox-demo
// keeps it working in a container with no HOME at all.
func defaultDemoDir() string {
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		return filepath.Join(state, DefaultDemoDirName)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", DefaultDemoDirName)
	}
	return DefaultDemoDirName
}

// materializeDemoConfig writes (once) a demo config and a throwaway
// self-signed TLS keypair under dir, and returns the config's path.
//
// Existing files are never overwritten. That makes a second `vnproxd --demo`
// resume the first one's store and keep the same certificate — a demo that
// regenerated its TLS identity on every start would train its user to click
// through a certificate warning, which is not a habit this product should
// be teaching anyone.
func materializeDemoConfig(dir string, logger *slog.Logger) (string, error) {
	if dir == "" {
		dir = defaultDemoDir()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving demo data directory %s: %w", dir, err)
	}
	if mkErr := os.MkdirAll(abs, 0o750); mkErr != nil {
		return "", fmt.Errorf("creating demo data directory %s: %w", abs, mkErr)
	}

	certPath := filepath.Join(abs, "demo-cert.pem")
	keyPath := filepath.Join(abs, "demo-key.pem")
	if genErr := ensureDemoCert(certPath, keyPath, logger); genErr != nil {
		return "", genErr
	}

	cfgPath := filepath.Join(abs, "demo.toml")
	if _, statErr := os.Stat(cfgPath); statErr == nil {
		return cfgPath, nil
	}
	body := demoConfigTOML(abs, certPath, keyPath)
	if writeErr := os.WriteFile(cfgPath, []byte(body), 0o600); writeErr != nil {
		return "", fmt.Errorf("writing demo config %s: %w", cfgPath, writeErr)
	}
	logger.Info("demo: wrote a demo configuration", "path", cfgPath)
	return cfgPath, nil
}

// demoConfigTOML is the generated config's content. It has NO [pve]
// section, which is not an omission — config.LoadDemo would refuse the
// daemon's own generated config if it had one, and that is the check
// working.
func demoConfigTOML(dir, certPath, keyPath string) string {
	return fmt.Sprintf(`# Generated by 'vnproxd --demo'. Safe to edit; safe to delete (it is
# regenerated on the next demo start, and so is everything else in this
# directory).
#
# There is deliberately no [pve] section. A demo daemon runs against the
# synthetic cluster embedded in the binary; adding a PVE endpoint here does
# not make the demo talk to your cluster, it makes 'vnproxd --demo' refuse
# to start (internal/config.ErrDemoRealEndpoint). To manage a real cluster,
# start vnproxd without --demo.

[server]
listen = %q
tls_cert = %q
tls_key = %q
confirm_timeout_default = 120

[storage]
db_path = %q
session_key_file = %q

[peer]
secret_path = %q

[metrics]
key_file = %q

[blueprint]
signing_key_file = %q
trusted_signers_dir = %q

[capture]
root = %q

[safety]
protected_path = %q
# The change engine never reaches a real interfaces file in demo mode (the
# API refuses every mutation before it gets there), but the host writer is
# sandboxed here too, belt and braces: a demo must not be one bug away from
# rewriting /etc/network/interfaces on the machine it is being shown on.
dev_interfaces_dir = %q

[collect]
pve_interval = "10s"
host_interval = "5s"
lldp_interval = "30s"
`,
		"127.0.0.1:8007", certPath, keyPath,
		filepath.Join(dir, "vnprox.db"),
		filepath.Join(dir, "session.key"),
		filepath.Join(dir, "cluster.secret"),
		filepath.Join(dir, "metrics.key"),
		filepath.Join(dir, "blueprint-signing.key"),
		filepath.Join(dir, "trusted-signers"),
		filepath.Join(dir, "captures"),
		filepath.Join(dir, "protected.json"),
		filepath.Join(dir, "host"),
	)
}

// ensureDemoCert generates a throwaway self-signed keypair at certPath/
// keyPath if either is missing.
//
// Self-signed and browser-untrusted, like every other vnprox listener
// before an operator installs a real certificate — this is not a new
// security posture, it is the existing one applied to a daemon that has no
// PVE node certificate to borrow.
func ensureDemoCert(certPath, keyPath string, logger *slog.Logger) error {
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	if certErr == nil && keyErr == nil {
		return nil
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generating the demo TLS key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generating the demo certificate serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "vnprox demo (self-signed)"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("creating the demo certificate: %w", err)
	}
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return fmt.Errorf("writing the demo certificate %s: %w", certPath, err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshalling the demo TLS key: %w", err)
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return fmt.Errorf("writing the demo TLS key %s: %w", keyPath, err)
	}
	logger.Info("demo: generated a throwaway self-signed TLS keypair", "cert", certPath)
	return nil
}
