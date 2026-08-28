// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/ingress"
	"github.com/bgovanlu/vnprox/internal/store"
)

// Audit action names for plugin lifecycle events (T-1702 AC5). A human reviewing
// GET /audit can always see which plugin was installed/enabled/disabled/
// uninstalled, by whom, and with which capability scope.
const (
	AuditInstall   = "plugin.install"
	AuditEnable    = "plugin.enable"
	AuditDisable   = "plugin.disable"
	AuditUninstall = "plugin.uninstall"
)

// ErrAlreadyInstalled is returned when Install is called for a plugin id that is
// already installed. Re-scoping a plugin goes through Uninstall + Install so the
// audit trail records both the removal and the re-install with the new scope.
var ErrAlreadyInstalled = errors.New("plugin: already installed")

// ErrNotInstalled is returned by Enable/Disable/Uninstall for an unknown id.
var ErrNotInstalled = errors.New("plugin: not installed")

// pluginRepo is the persistence seam the registry needs — satisfied by
// *store.PluginRepo. Declared as an interface so tests can substitute a fake and
// the registry never depends on the concrete store type.
type pluginRepo interface {
	Upsert(ctx context.Context, p store.PluginRow) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (store.PluginRow, error)
	List(ctx context.Context) ([]store.PluginRow, error)
}

// auditSink is the audit seam — satisfied by *store.AuditRepo.
type auditSink interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

// Config configures a Registry. Repo and Change are required to install
// anything; Audit, Logger, and Now default sensibly when nil/zero (a nil Audit
// disables audit writes — used only in narrow unit tests — mirroring the
// nil-seam convention internal/change.Config already uses).
type Config struct {
	Repo   pluginRepo
	Change changeCreator
	Audit  auditSink
	Logger *slog.Logger
	Now    func() time.Time
}

// loaded is one live, installed plugin: its registration (manifest + concrete
// implementations), its parsed capability scope, and its enabled state.
type loaded struct {
	scope   Scope
	reg     Registration
	enabled bool
}

// Registry installs, enables, disables, uninstalls, and dispatches plugins. It
// is the one place a plugin's capability scope is bound and enforced: an install
// validates the manifest and scope, records the row (with the scope, for audit),
// and — for any implementation that needs to stage changesets — hands it a
// capability-scoped Stager and nothing broader.
type Registry struct {
	cfg       Config
	log       *slog.Logger
	now       func() time.Time
	installed map[string]*loaded
	mu        sync.RWMutex
}

// NewRegistry constructs a Registry.
func NewRegistry(cfg Config) *Registry {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Registry{
		cfg:       cfg,
		log:       log,
		now:       now,
		installed: make(map[string]*loaded),
	}
}

// Install validates and installs a plugin: it checks the manifest wiring
// (Registration.validate), parses and checks the declared capability scope
// against the extension points it attaches to (ValidateScope), persists the row
// with its scope, hands any host-consuming implementation a capability-scoped
// Stager, loads it live (enabled), and audits plugin.install with the recorded
// capabilities. actor is the identity performing the install (for the audit
// trail).
func (r *Registry) Install(ctx context.Context, actor string, reg Registration) error {
	if err := reg.validate(); err != nil {
		return err
	}
	scope, err := NewScope(reg.Manifest.Capabilities)
	if err != nil {
		return fmt.Errorf("plugin %q: %w", reg.Manifest.ID, err)
	}
	if err := ValidateScope(reg.Manifest.ExtensionPoints, scope); err != nil {
		return err
	}

	id := reg.Manifest.ID
	r.mu.Lock()
	if _, dup := r.installed[id]; dup {
		r.mu.Unlock()
		return fmt.Errorf("plugin %q: %w", id, ErrAlreadyInstalled)
	}

	// Hand the plugin a capability-scoped, stage-only Host before it is live.
	if r.cfg.Change != nil {
		host := pluginHost{stager: newScopedStager(r.cfg.Change, "plugin:"+id, scope)}
		injectHost(reg, host)
	}
	r.installed[id] = &loaded{reg: reg, scope: scope, enabled: true}
	r.mu.Unlock()

	if err := r.persist(ctx, reg, scope, actor, true); err != nil {
		// Roll the live registration back so the in-memory and persisted views
		// never diverge on a write failure.
		r.mu.Lock()
		delete(r.installed, id)
		r.mu.Unlock()
		return err
	}
	r.audit(ctx, AuditInstall, actor, id, scope.Names())
	return nil
}

// Enable re-enables a disabled plugin's extension points.
func (r *Registry) Enable(ctx context.Context, actor, id string) error {
	return r.setEnabled(ctx, actor, id, true, AuditEnable)
}

// Disable stops dispatching a plugin's extension points without uninstalling it.
// An out-of-process plugin's subprocess is left running (uninstall tears it
// down); a disabled plugin is simply skipped by every dispatch accessor.
func (r *Registry) Disable(ctx context.Context, actor, id string) error {
	return r.setEnabled(ctx, actor, id, false, AuditDisable)
}

func (r *Registry) setEnabled(ctx context.Context, actor, id string, enabled bool, action string) error {
	r.mu.Lock()
	p, ok := r.installed[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("plugin %q: %w", id, ErrNotInstalled)
	}
	p.enabled = enabled
	scopeNames := p.scope.Names()
	r.mu.Unlock()

	if err := r.cfg.Repo.SetEnabled(ctx, id, enabled); err != nil {
		return err
	}
	r.audit(ctx, action, actor, id, scopeNames)
	return nil
}

// Uninstall removes a plugin: it stops dispatching it, tears down any
// out-of-process subprocess (Registration.Closer), deletes the row, and audits
// plugin.uninstall.
func (r *Registry) Uninstall(ctx context.Context, actor, id string) error {
	r.mu.Lock()
	p, ok := r.installed[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("plugin %q: %w", id, ErrNotInstalled)
	}
	closer := p.reg.Closer
	scopeNames := p.scope.Names()
	delete(r.installed, id)
	r.mu.Unlock()

	if closer != nil {
		if cerr := closer.Close(); cerr != nil {
			r.log.Warn("plugin subprocess teardown returned error", "plugin", id, "err", cerr)
		}
	}
	if err := r.cfg.Repo.Delete(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	r.audit(ctx, AuditUninstall, actor, id, scopeNames)
	return nil
}

// List returns every installed plugin as a persisted row, newest-first, for GET
// /plugins. It reads the store (the durable record), not the in-memory map.
func (r *Registry) List(ctx context.Context) ([]store.PluginRow, error) {
	return r.cfg.Repo.List(ctx)
}

// SwitchDriver returns the enabled switch-driver plugin registered under id, or
// (nil, false). The change engine's switch gateway resolves a driver this way
// only when T-1205's dark-by-default feature guard is on.
func (r *Registry) SwitchDriver(id string) (SwitchDriver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.installed[id]
	if !ok || !p.enabled || p.reg.SwitchDriver == nil {
		return nil, false
	}
	return p.reg.SwitchDriver, true
}

// FlowIngestors returns every enabled flow-ingestor plugin, id-sorted for
// deterministic dispatch order.
func (r *Registry) FlowIngestors() []FlowIngestor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []FlowIngestor
	for _, id := range r.sortedIDsLocked() {
		p := r.installed[id]
		if p.enabled && p.reg.FlowIngestor != nil {
			out = append(out, p.reg.FlowIngestor)
		}
	}
	return out
}

// IngressRegistry merges every enabled ingress-discoverer plugin into base by
// Kind, returning a new Registry (base is not mutated). A plugin Kind never
// overrides a built-in Kind already in base — a built-in vendor is authoritative
// over a plugin claiming the same Kind, so a plugin cannot shadow a shipped
// discoverer.
func (r *Registry) IngressRegistry(base ingress.Registry) ingress.Registry {
	merged := make(ingress.Registry, len(base))
	for k, d := range base {
		merged[k] = d
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, id := range r.sortedIDsLocked() {
		p := r.installed[id]
		if !p.enabled || p.reg.IngressDiscoverer == nil {
			continue
		}
		if _, taken := merged[p.reg.IngressKind]; taken {
			continue
		}
		merged[p.reg.IngressKind] = p.reg.IngressDiscoverer
	}
	return merged
}

// PluginFindings aggregates the findings of every enabled finding-producer
// plugin. A producer that errors (including a dead out-of-process plugin) is
// logged and skipped — its pack is omitted, never allowed to fail the whole
// findings response (T-1702 AC5's graceful-degradation contract).
func (r *Registry) PluginFindings(ctx context.Context) []findingsResult {
	r.mu.RLock()
	producers := make([]idProducer, 0)
	for _, id := range r.sortedIDsLocked() {
		p := r.installed[id]
		if p.enabled && p.reg.FindingProducer != nil {
			producers = append(producers, idProducer{id: id, prod: p.reg.FindingProducer})
		}
	}
	r.mu.RUnlock()

	var out []findingsResult
	for _, ip := range producers {
		fs, err := ip.prod.Produce(ctx)
		if err != nil {
			r.log.Warn("plugin finding producer degraded", "plugin", ip.id, "err", err)
			continue
		}
		out = append(out, findingsResult{PluginID: ip.id, Findings: fs})
	}
	return out
}

// DashboardTiles aggregates the tiles of every enabled dashboard-tile plugin,
// with the same graceful-degradation contract as PluginFindings: a provider that
// errors is logged and skipped, never crashing the dashboard.
func (r *Registry) DashboardTiles(ctx context.Context) []Tile {
	r.mu.RLock()
	providers := make([]idTileProvider, 0)
	for _, id := range r.sortedIDsLocked() {
		p := r.installed[id]
		if p.enabled && p.reg.DashboardTiles != nil {
			providers = append(providers, idTileProvider{id: id, prov: p.reg.DashboardTiles})
		}
	}
	r.mu.RUnlock()

	var out []Tile
	for _, ip := range providers {
		tiles, err := ip.prov.Tiles(ctx)
		if err != nil {
			r.log.Warn("plugin dashboard tile provider degraded", "plugin", ip.id, "err", err)
			continue
		}
		out = append(out, tiles...)
	}
	return out
}

// findingsResult is one plugin's contributed findings.
type findingsResult struct {
	PluginID string
	Findings []findings.Finding
}

// idProducer / idTileProvider pair a plugin id with its implementation for
// logging a degraded provider by name.
type idProducer struct {
	prod FindingProducer
	id   string
}

type idTileProvider struct {
	prov DashboardTileProvider
	id   string
}

// sortedIDsLocked returns installed plugin ids in sorted order. Caller holds
// r.mu (read or write).
func (r *Registry) sortedIDsLocked() []string {
	ids := make([]string, 0, len(r.installed))
	for id := range r.installed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// persist writes the plugin's row (with its scope recorded) to the store.
func (r *Registry) persist(ctx context.Context, reg Registration, scope Scope, actor string, enabled bool) error {
	points := make([]string, 0, len(reg.Manifest.ExtensionPoints))
	for _, ep := range reg.Manifest.ExtensionPoints {
		points = append(points, string(ep))
	}
	row := store.PluginRow{
		ID:              reg.Manifest.ID,
		Name:            reg.Manifest.Name,
		Version:         reg.Manifest.Version,
		APIVersion:      reg.Manifest.APIVersion,
		Transport:       string(reg.Manifest.Transport),
		Endpoint:        reg.Manifest.Endpoint,
		InstalledBy:     actor,
		ExtensionPoints: points,
		Capabilities:    scope.Names(),
		InstalledAt:     r.now().Unix(),
		Enabled:         enabled,
	}
	return r.cfg.Repo.Upsert(ctx, row)
}

// audit writes one plugin-lifecycle audit row with the capability scope recorded
// in its detail, so GET /audit always shows what a plugin could touch. A nil
// Audit sink (narrow unit tests only) is a silent no-op.
func (r *Registry) audit(ctx context.Context, action, actor, id string, capabilities []string) {
	if r.cfg.Audit == nil {
		return
	}
	detail, err := json.Marshal(map[string]any{
		"plugin":       id,
		"capabilities": nonNil(capabilities),
	})
	if err != nil {
		r.log.Warn("marshaling plugin audit detail", "plugin", id, "err", err)
		return
	}
	if _, err := r.cfg.Audit.Append(ctx, store.AuditEntry{
		Username:   actor,
		Action:     action,
		Result:     "success",
		Target:     sql.NullString{String: id, Valid: true},
		DetailJSON: sql.NullString{String: string(detail), Valid: true},
		At:         r.now().Unix(),
	}); err != nil {
		r.log.Warn("appending plugin audit row", "plugin", id, "action", action, "err", err)
	}
}

// nonNil normalizes a nil string slice to empty for stable JSON.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
