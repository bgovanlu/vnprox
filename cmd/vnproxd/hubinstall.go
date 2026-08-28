// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/hubreg"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/plugin/procshim"
)

// defaultPluginInstallRoot is the vnprox-owned directory a hub-installed
// plugin's executable must live under (T-2904). A registry manifest's
// endpoint is registry-supplied data; constraining it to this root is what
// keeps "install a plugin" from meaning "run any path on the host as root".
const defaultPluginInstallRoot = "/var/lib/vnprox/plugins"

// newHubClient constructs the opt-in T-1705 Blueprint & plugin hub client from
// the [hub] section, or nil when the hub is off.
//
// Off by default: an empty registry_url returns nil, which skips mounting the
// hub routes entirely. A malformed URL is logged and the hub stays off rather
// than failing daemon startup.
//
// T-2803: when index_signers is configured, the T-2803 registry gate rides the
// client's *existing* WithHTTPClient seam — the index must verify against one
// of those signers before the client sees a single entry, and the revocations
// inside that signed index are enforced on every artifact fetch with no
// further network access. internal/hub itself is unchanged; internal/hubreg's
// gate.go explains why the seam, and not the client, is the right place for
// this. With no index_signers the pre-T-2803 behaviour is preserved and said
// out loud at startup: an unauthenticated catalog and unenforced revocations
// (artifact signatures and the trust store still gate every install either
// way, which is why this is a warning and not a refusal to start).
func newHubClient(cfg config.HubConfig, logger *slog.Logger) *hub.Client {
	regURL := cfg.RegistryURL
	if regURL == "" {
		return nil
	}
	// T-4009: registry_url may be an explicit "file://<local mirror dir>"
	// URL (`vnproxctl hub mirror`'s output) instead of a hosted http(s) one
	// — internal/hub.NewClient understands that scheme directly. Deliberately
	// NOT the same bare-directory-path convenience `vnproxctl hub pull
	// --registry` offers (internal/hub.NormalizeRegistryURL): unattended
	// daemon config that silently reinterprets a malformed URL as "must be a
	// local directory" is a config typo turning into a config typo nobody
	// notices, not a convenience — a daemon config error should fail exactly
	// as loudly as it always has (see TestNewHubClient_IndexSignersInstall
	// TheGate's "off and malformed" case: "not-a-url" must leave the hub
	// off, not guess).
	local := strings.HasPrefix(regURL, "file://")
	// T-2904: unsigned-trust is a server config decision, said out loud at
	// startup exactly like the [peer] tls_trust escape hatches — the knob is
	// named so an operator reading the log knows which key to remove.
	if cfg.TrustUnsigned {
		logger.Warn("blueprint & plugin hub: TRUSTING UNSIGNED ARTIFACTS IS ENABLED ([hub] trust_unsigned = true) — a POST /hub/install request carrying trustUnsigned: true may install an artifact that carries no signature at all. Verification of signed artifacts is unaffected and never optional. This is a configured escape hatch, not a default", "url", regURL)
	}
	var opts []hub.Option
	if len(cfg.IndexSigners) > 0 {
		// Gate's own inner doer defaults to the network — a local mirror
		// needs its inner doer to be the SAME LocalDoer NewClient would
		// install by default, or an air-gapped daemon with index_signers
		// configured (the case that actually matters for T-4009: verified
		// offline, never unverified offline) would otherwise try to dial
		// out for every artifact fetch after a perfectly good local Index().
		var inner hubreg.Doer
		if local {
			if u, perr := url.Parse(regURL); perr == nil {
				inner = hub.NewLocalDoer(u.Path)
			}
		}
		opts = append(opts, hub.WithHTTPClient(hubreg.NewGate(inner, cfg.IndexSigners)))
	} else {
		logger.Warn("blueprint & plugin hub: registry index signature verification is OFF ([hub] index_signers is empty) — the catalog is unauthenticated and published revocations are not enforced (artifact signatures and the trust store still gate every install)", "url", regURL)
	}
	c, err := hub.NewClient(regURL, opts...)
	if err != nil {
		logger.Warn("blueprint & plugin hub disabled: invalid registry URL", "url", regURL, "err", err)
		return nil
	}
	if local {
		logger.Info("blueprint & plugin hub: reading a local mirror directory ([hub] registry_url is a file:// path, T-4009) — no network access for hub browsing/install", "url", regURL)
	}
	return c
}

// hubClientOrNil returns c as an api.HubClient, or an untyped nil interface when
// c is nil — so mountHubRoutes' `client == nil` guard fires (a typed-nil
// *hub.Client stored in the interface would not be == nil, the classic Go
// nil-interface gotcha, and would panic on first use).
func hubClientOrNil(c *hub.Client) api.HubClient {
	if c == nil {
		return nil
	}
	return c
}

// hubPluginInstaller is cmd/vnproxd's concrete api.PluginInstaller: it turns a
// hub-verified plugin.Manifest into a live plugin.Registration and installs it
// through T-1702's capability-scoped registry. The api/hub layer has already
// verified the manifest's Ed25519 signature and applied the trust decision
// before this runs; the registry's Install then independently re-validates the
// capability scope and extension-point wiring — the hub never bypasses either
// gate.
//
// Only out-of-process (grpc/procshim) plugins can be installed from the hub: an
// in-process plugin is Go code linked into vnproxd at build time and cannot be
// materialized from a downloaded manifest, so that transport is refused here
// rather than pretending to install nothing.
//
// NOTE (needs hardware validation): the subprocess launch itself is exercised
// only against a test double in CI. The actual delivery of a plugin's
// executable to m.Endpoint is the registry service's responsibility (a separate
// repo, see the T-1705 report); wiring a real downloaded binary end-to-end needs
// validation on a real install.
type hubPluginInstaller struct {
	registry *plugin.Registry
	// installRoot is the directory a plugin endpoint must resolve into
	// (T-2904). Empty falls back to defaultPluginInstallRoot — the zero
	// value is constrained, never unconstrained.
	installRoot string
}

// Install builds the registration and installs it. On an install failure the
// spawned subprocess (if any) is torn down so a rejected plugin never leaves an
// orphaned process behind.
func (h hubPluginInstaller) Install(ctx context.Context, actor string, m plugin.Manifest) error {
	root := h.installRoot
	if root == "" {
		root = defaultPluginInstallRoot
	}
	reg, err := buildRegistration(ctx, root, m)
	if err != nil {
		return err
	}
	if err := h.registry.Install(ctx, actor, reg); err != nil {
		if reg.Closer != nil {
			_ = reg.Closer.Close()
		}
		return err
	}
	return nil
}

// resolvePluginEndpoint enforces T-2904's endpoint constraint: a registry
// manifest's endpoint is registry-supplied data and may only name an
// executable the operator (or packaging) placed under the vnprox-owned
// install root. It returns the fully symlink-resolved path to launch.
//
// Rejected, each with the constraint named in the error: a bare name or any
// relative path (which would otherwise resolve via $PATH or the daemon's cwd),
// an unclean path (`..` or redundant separators), an absolute path outside
// root, a symlink chain that escapes root (containment is asserted on the
// EvalSymlinks-resolved forms of BOTH root and endpoint — the lexical check
// alone would miss a symlink planted inside the root), and anything that is
// not a regular file. $PATH is never consulted: the launched path is always
// absolute.
func resolvePluginEndpoint(root, endpoint string) (string, error) {
	if !filepath.IsAbs(endpoint) {
		return "", fmt.Errorf("plugin endpoint %q is not an absolute path — a hub-installed plugin must be an absolute path inside the plugin install root %s ($PATH resolution is never used)", endpoint, root)
	}
	if endpoint != filepath.Clean(endpoint) {
		return "", fmt.Errorf("plugin endpoint %q is not a clean path (no %q segments or redundant separators) — it must name a file inside the plugin install root %s directly", endpoint, "..", root)
	}
	if endpoint == root || !strings.HasPrefix(endpoint, root+string(filepath.Separator)) {
		return "", fmt.Errorf("plugin endpoint %q is outside the plugin install root %s — only executables inside that root can be launched", endpoint, root)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolving plugin install root %s: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(endpoint)
	if err != nil {
		return "", fmt.Errorf("resolving plugin endpoint %q: %w", endpoint, err)
	}
	if resolved != resolvedRoot && !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("plugin endpoint %q resolves to %q, escaping the plugin install root %s — a symlink out of the root is refused", endpoint, resolved, root)
	}
	if resolved == resolvedRoot {
		return "", fmt.Errorf("plugin endpoint %q is the plugin install root itself, not a file inside it", endpoint)
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("examining plugin endpoint %q: %w", endpoint, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("plugin endpoint %q resolves to %q, which is not a regular file — only a regular executable file inside the plugin install root %s can be launched", endpoint, resolved, root)
	}
	return resolved, nil
}

// buildRegistration spawns the plugin's subprocess (for grpc transport) and
// wires its procshim host adapters to exactly the extension points the manifest
// declares. It does not validate the manifest — plugin.Registry.Install does —
// but it does enforce T-2904's endpoint containment before anything is
// executed: the endpoint is registry-supplied and is launched only as a
// resolved absolute path inside installRoot, never via $PATH.
func buildRegistration(_ context.Context, installRoot string, m plugin.Manifest) (plugin.Registration, error) {
	if m.Transport != plugin.TransportGRPC {
		return plugin.Registration{}, fmt.Errorf("hub: only out-of-process (grpc) plugins can be installed from the hub, not transport %q", m.Transport)
	}
	if m.Endpoint == "" {
		return plugin.Registration{}, fmt.Errorf("hub: plugin %q declares no endpoint to launch", m.ID)
	}
	path, err := resolvePluginEndpoint(installRoot, m.Endpoint)
	if err != nil {
		return plugin.Registration{}, fmt.Errorf("hub: plugin %q: %w", m.ID, err)
	}
	host, err := procshim.Start(exec.Command(path)) //nolint:gosec // resolvePluginEndpoint above pinned this to a regular file inside the vnprox-owned install root; no $PATH lookup (absolute path).
	if err != nil {
		return plugin.Registration{}, fmt.Errorf("hub: launching plugin %q subprocess: %w", m.ID, err)
	}
	reg := plugin.Registration{Manifest: m, Closer: host}
	for _, ep := range m.ExtensionPoints {
		switch ep {
		case plugin.ExtSwitchDriver:
			reg.SwitchDriver = host.SwitchDriver()
		case plugin.ExtFlowIngestor:
			reg.FlowIngestor = host.FlowIngestor()
		case plugin.ExtFindingProducer:
			reg.FindingProducer = host.FindingProducer()
		case plugin.ExtIngressDiscoverer:
			reg.IngressDiscoverer = host.IngressDiscoverer()
			// The manifest carries no separate ingress Kind; a plugin's own id
			// is its discoverer Kind (a built-in Kind is never overridden — see
			// Registry.IngressRegistry).
			reg.IngressKind = ingress.Kind(m.ID)
		case plugin.ExtDashboardTile:
			reg.DashboardTiles = host.DashboardTiles()
		}
	}
	return reg, nil
}
