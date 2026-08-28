// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"io"

	"github.com/bgovanlu/vnprox/internal/ingress"
)

// Transport is how a plugin's code is reached: in the daemon's own process, or
// out-of-process as a supervised subprocess speaking the procshim wire protocol.
type Transport string

const (
	// TransportInProcess is a plugin linked into vnproxd (a built-in, or a Go
	// plugin compiled in). Its extension implementations are ordinary Go values.
	TransportInProcess Transport = "in-process"
	// TransportGRPC is an out-of-process plugin: a subprocess vnproxd spawns and
	// supervises, reached over the procshim length-delimited JSON wire protocol.
	// It is never given direct DB or file access.
	TransportGRPC Transport = "grpc"
)

// validTransport reports whether t is a recognized transport.
func validTransport(t Transport) bool {
	return t == TransportInProcess || t == TransportGRPC
}

// Manifest is a plugin's self-description: its identity, the SDK interface
// version it was built against, the extension points it attaches to, the
// capability scope it requests, and its transport. Everything here is validated
// on install against fixed vocabularies before a plugins row is written — the
// capability scope against internal/auth's AllCaps, the extension points against
// AllExtensionPoints, the api version against APIVersion.
type Manifest struct {
	ID              string
	Name            string
	Version         string
	APIVersion      string
	Transport       Transport
	Endpoint        string
	ExtensionPoints []ExtensionPoint
	Capabilities    []string
}

// Registration is one plugin's manifest plus its concrete extension
// implementations. A plugin supplies an implementation for each extension point
// it declares and leaves the rest nil; the registry cross-checks that a declared
// point has a non-nil implementation before it will install the plugin. For an
// out-of-process plugin these implementations are procshim host adapters; for an
// in-process plugin they are ordinary Go values.
type Registration struct {
	SwitchDriver      SwitchDriver
	FlowIngestor      FlowIngestor
	FindingProducer   FindingProducer
	IngressDiscoverer IngressDiscoverer
	DashboardTiles    DashboardTileProvider
	Closer            io.Closer
	IngressKind       ingress.Kind
	Manifest          Manifest
}

// validate checks the registration is internally consistent: manifest identity
// present, api version understood, transport recognized, and — for each declared
// extension point — a matching non-nil implementation (and, for ingress, a Kind).
// It does NOT check the capability scope; ValidateScope does that separately so a
// scope error and a wiring error report distinctly.
func (r Registration) validate() error {
	m := r.Manifest
	if m.ID == "" {
		return fmt.Errorf("plugin: manifest has no id")
	}
	if m.Name == "" {
		return fmt.Errorf("plugin %q: manifest has no name", m.ID)
	}
	if m.APIVersion != APIVersion {
		return fmt.Errorf("plugin %q: built against api version %q, this build supports %q", m.ID, m.APIVersion, APIVersion)
	}
	if !validTransport(m.Transport) {
		return fmt.Errorf("plugin %q: unknown transport %q", m.ID, m.Transport)
	}
	for _, ep := range m.ExtensionPoints {
		if err := r.checkImpl(ep); err != nil {
			return err
		}
	}
	return nil
}

// checkImpl verifies the registration actually carries an implementation for a
// declared extension point — a plugin that declares switchDriver but supplies no
// SwitchDriver is a wiring bug, refused rather than installed as a dead point.
func (r Registration) checkImpl(ep ExtensionPoint) error {
	id := r.Manifest.ID
	switch ep {
	case ExtSwitchDriver:
		if r.SwitchDriver == nil {
			return fmt.Errorf("plugin %q declares %q but supplies no SwitchDriver", id, ep)
		}
	case ExtFlowIngestor:
		if r.FlowIngestor == nil {
			return fmt.Errorf("plugin %q declares %q but supplies no FlowIngestor", id, ep)
		}
	case ExtFindingProducer:
		if r.FindingProducer == nil {
			return fmt.Errorf("plugin %q declares %q but supplies no FindingProducer", id, ep)
		}
	case ExtIngressDiscoverer:
		if r.IngressDiscoverer == nil {
			return fmt.Errorf("plugin %q declares %q but supplies no IngressDiscoverer", id, ep)
		}
		if r.IngressKind == "" {
			return fmt.Errorf("plugin %q declares %q but names no IngressKind", id, ep)
		}
	case ExtDashboardTile:
		if r.DashboardTiles == nil {
			return fmt.Errorf("plugin %q declares %q but supplies no DashboardTileProvider", id, ep)
		}
	default:
		return fmt.Errorf("plugin %q declares unknown extension point %q", id, ep)
	}
	return nil
}
