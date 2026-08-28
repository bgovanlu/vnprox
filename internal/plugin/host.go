// SPDX-License-Identifier: Apache-2.0

package plugin

// Host is the capability-scoped handle the registry hands to plugin code that
// needs a host-provided seam. Today it exposes exactly one seam — the stage-only
// Stager — and it exists as its own type so that any future host seam can be
// added behind the same capability gate without re-widening a plugin's
// constructor. A plugin never receives a raw *change.Service, the store, or any
// broader host object; it receives this, and this only surfaces the stage-only
// Stager scoped to the plugin's own declared capabilities.
type Host interface {
	// Stager returns the plugin's capability-scoped, stage-only change surface.
	// Every op staged through it is checked against the plugin's declared scope
	// (ErrCapabilityExceeded otherwise) and can never reach Apply/Confirm/Rollback.
	Stager() Stager
}

// HostConsumer is optionally implemented by a plugin's extension implementations
// that need host seams — e.g. a FindingProducer that stages a remediation draft
// for a human to apply. The registry injects the capability-scoped Host exactly
// once, before the plugin is dispatched; an implementation that does not need a
// host simply omits this method.
type HostConsumer interface {
	// UseHost receives the plugin's capability-scoped Host. It is called once,
	// during install/load, before any extension method is dispatched.
	UseHost(h Host)
}

// pluginHost is the concrete Host bound to one plugin's scoped Stager.
type pluginHost struct {
	stager Stager
}

func (h pluginHost) Stager() Stager { return h.stager }

// injectHost hands h to any of a registration's extension implementations that
// implement HostConsumer. Called once per plugin at load time.
func injectHost(reg Registration, h Host) {
	for _, impl := range []any{
		reg.SwitchDriver, reg.FlowIngestor, reg.FindingProducer,
		reg.IngressDiscoverer, reg.DashboardTiles,
	} {
		if c, ok := impl.(HostConsumer); ok {
			c.UseHost(h)
		}
	}
}
