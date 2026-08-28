package plugintemplate

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/findings"
)

// Producer is a minimal plugin.FindingProducer (internal/plugin/
// interfaces.go): strictly read-only, contributing a "finding pack" of
// findings.Finding values alongside the built-in producers in
// internal/findings. It cannot apply a remediation itself — a Fixable
// finding is still staged through the ordinary change-engine flow by a
// human, never by a plugin (docs/plugin-development.md's stage-only
// boundary; this producer holds no plugin.Stager and cannot construct one).
//
// This is the whole extension point: one method, Produce, called by
// plugin.Registry.PluginFindings on every findings refresh. Replace
// produceFindings' body with real detection logic — the interface shape
// (Produce(ctx) ([]findings.Finding, error)) is frozen at
// plugin.APIVersion == "v1" and documented at internal/plugin/interfaces.go,
// which this file deliberately does not restate.
type Producer struct{}

// NewProducer constructs the example FindingProducer. A real plugin's
// constructor is a natural place to take read-only dependencies it needs at
// Produce time (an HTTP client for a vendor API, a parsed config) — never a
// database handle or a change-engine handle; neither is ever offered to a
// FindingProducer.
func NewProducer() *Producer { return &Producer{} }

// Produce implements plugin.FindingProducer. An error here degrades only
// this plugin's pack (its findings are omitted from the aggregate response);
// it never fails GET /findings for every other producer — the same
// graceful-degradation contract a dead out-of-process plugin gets.
func (p *Producer) Produce(_ context.Context) ([]findings.Finding, error) {
	return produceFindings(), nil
}

// produceFindings is split out from Produce so a test can assert on its
// output without a context in scope. Replace this with whatever this
// plugin's real check computes.
func produceFindings() []findings.Finding {
	return []findings.Finding{
		{
			ID:       "plugin.plugintemplate.example",
			Source:   findings.Source("plugin"),
			Check:    "plugintemplate-example-check",
			Severity: "info",
			Detail:   "example finding produced by the plugintemplate scaffold — replace producer.go's produceFindings with real detection logic",
			Fixable:  false,
		},
	}
}
