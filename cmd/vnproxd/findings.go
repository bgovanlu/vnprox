package main

import (
	"encoding/json"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/metrics"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// topicFindings is the WS subscribe topic name for T-602's
// `findings.changed` event — the unified-stream counterpart of
// cmd/vnproxd/drift.go's topicDrift, added rather than replacing it (any
// existing subscriber to "drift" keeps working unchanged).
const topicFindings = "findings"

// findingsChangedEvent is docs/api.md's documented `findings.changed
// {count}` WS event payload, the same envelope shape driftChangedEvent uses.
type findingsChangedEvent struct {
	Event string `json:"event"`
	Count int    `json:"count"`
}

// findingsBroadcaster is the same one-method WS seam driftBroadcaster uses.
type findingsBroadcaster interface {
	Broadcast(topic string, payload []byte)
}

// setupFindings builds T-602's *findings.Engine composing driftSvc (T-305,
// unchanged), topoSvc's LLDP VLAN cross-check (T-302, unchanged), and this
// package's own health checks over the same live graph metricsSampler
// already reads. IPAM is left nil: T-405 (internal/ipam) is still a stub
// package as of this task (see internal/findings/adapt_ipam.go's doc
// comment) — wiring it in later is a one-line addition to this Config
// literal once T-405 lands.
//
// notifier is nil-safe (findings.Engine.Config.Notifier accepts nil,
// disabling the notification hook entirely — the P1 half of this task's
// deliverable is present but harmless to omit if, say, the PVE client
// failed to construct).
func setupFindings(graph *inventory.Graph, driftSvc findings.DriftProvider, topoSvc *topology.Service, metricsSampler *metrics.Sampler, notifier findings.Notifier, ws findingsBroadcaster, logger *slog.Logger) *findings.Engine {
	return findings.New(findings.Config{
		Graph:    graph,
		Drift:    driftSvc,
		LLDP:     topoSvc,
		Metrics:  metricsSampler,
		Logger:   logger,
		Notifier: notifier,
		OnChange: func(count int) {
			data, err := json.Marshal(findingsChangedEvent{Event: "findings.changed", Count: count})
			if err != nil {
				logger.Error("findings: marshaling findings.changed event", "error", err)
				return
			}
			ws.Broadcast(topicFindings, data)
		},
	})
}

// setupFindingsNotifier builds T-602's P1 notification hook: PVE's
// notification-target system (webhook/email/gotify), driven through
// internal/pve.Client — nil when pveClient itself is nil (collectors failed
// to initialize; see setupCollect's doc comment on why that's tolerated
// rather than fatal), mirroring every other "no PVE client -> that feature
// quietly doesn't exist" degradation in server.go.
func setupFindingsNotifier(pveClient *pve.Client, logger *slog.Logger) findings.Notifier {
	if pveClient == nil {
		return nil
	}
	return findings.NewPVENotifier(pveClient, logger)
}
