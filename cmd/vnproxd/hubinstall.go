package main

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/hubreg"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/plugin/procshim"
)

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
	var opts []hub.Option
	if len(cfg.IndexSigners) > 0 {
		opts = append(opts, hub.WithHTTPClient(hubreg.NewGate(nil, cfg.IndexSigners)))
	} else {
		logger.Warn("blueprint & plugin hub: registry index signature verification is OFF ([hub] index_signers is empty) — the catalog is unauthenticated and published revocations are not enforced (artifact signatures and the trust store still gate every install)", "url", regURL)
	}
	c, err := hub.NewClient(regURL, opts...)
	if err != nil {
		logger.Warn("blueprint & plugin hub disabled: invalid registry URL", "url", regURL, "err", err)
		return nil
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
}

// Install builds the registration and installs it. On an install failure the
// spawned subprocess (if any) is torn down so a rejected plugin never leaves an
// orphaned process behind.
func (h hubPluginInstaller) Install(ctx context.Context, actor string, m plugin.Manifest) error {
	reg, err := buildRegistration(ctx, m)
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

// buildRegistration spawns the plugin's subprocess (for grpc transport) and
// wires its procshim host adapters to exactly the extension points the manifest
// declares. It does not validate the manifest — plugin.Registry.Install does.
func buildRegistration(_ context.Context, m plugin.Manifest) (plugin.Registration, error) {
	if m.Transport != plugin.TransportGRPC {
		return plugin.Registration{}, fmt.Errorf("hub: only out-of-process (grpc) plugins can be installed from the hub, not transport %q", m.Transport)
	}
	if m.Endpoint == "" {
		return plugin.Registration{}, fmt.Errorf("hub: plugin %q declares no endpoint to launch", m.ID)
	}
	host, err := procshim.Start(exec.Command(m.Endpoint)) //nolint:gosec // endpoint is delivered by the signed+trust-gated registry artifact, not user input.
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
