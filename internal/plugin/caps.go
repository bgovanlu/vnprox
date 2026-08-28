// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/change"
)

// ExtensionPoint names one of the SDK's five v1 extension points. The set is
// fixed and enumerable (AllExtensionPoints) — a plugin attaches to one or more
// of these and to nothing else; there is no "extend an arbitrary route" seam.
type ExtensionPoint string

const (
	// ExtSwitchDriver is T-1205's physical-switch driver seam (write-adjacent,
	// dark-by-default, bounded by the switch.port op family's netWrite gate).
	ExtSwitchDriver ExtensionPoint = "switchDriver"
	// ExtFlowIngestor is T-1002's flow/telemetry decode seam (read-only).
	ExtFlowIngestor ExtensionPoint = "flowIngestor"
	// ExtFindingProducer is the finding-pack seam (read-only).
	ExtFindingProducer ExtensionPoint = "findingProducer"
	// ExtIngressDiscoverer is T-1406's reverse-proxy discovery seam (read-only).
	ExtIngressDiscoverer ExtensionPoint = "ingressDiscoverer"
	// ExtDashboardTile is T-904's dashboard-tile seam (read-only).
	ExtDashboardTile ExtensionPoint = "dashboardTile"
)

// AllExtensionPoints is the fixed, enumerable extension-point vocabulary. The
// registry validates a plugin's declared extension points against exactly this
// set on install; an unrecognized point is refused.
var AllExtensionPoints = []ExtensionPoint{
	ExtSwitchDriver, ExtFlowIngestor, ExtFindingProducer,
	ExtIngressDiscoverer, ExtDashboardTile,
}

// extensionPointMinCap is the minimum capability a plugin must declare to attach
// to an extension point — the point's "entry ceiling". The three read-only seams
// require only netRead; the switch-driver seam is write-adjacent (it configures
// physical ports within the change engine's bounded switch.port op) and so
// requires netWrite, exactly the cap that op family already gates. A plugin whose
// declared scope does not include an attached point's minimum capability is
// refused at install (ValidateScope), before it can ever be dispatched.
var extensionPointMinCap = map[ExtensionPoint]auth.Cap{
	ExtSwitchDriver:      auth.CapNetWrite,
	ExtFlowIngestor:      auth.CapNetRead,
	ExtFindingProducer:   auth.CapNetRead,
	ExtIngressDiscoverer: auth.CapNetRead,
	ExtDashboardTile:     auth.CapNetRead,
}

// validExtensionPoint reports whether ep is a recognized extension point.
func validExtensionPoint(ep ExtensionPoint) bool {
	for _, known := range AllExtensionPoints {
		if ep == known {
			return true
		}
	}
	return false
}

// Scope is a plugin's declared capability set — the ceiling on which seams it may
// touch and which change-engine op classes it may stage. It is drawn entirely
// from internal/auth's existing AllCaps vocabulary: this SDK introduces no new
// privilege of its own (docs/security.md's plugin capability-scope model). A
// plugin can never widen its own Scope at runtime; the Scope recorded on the
// plugins row at install time is authoritative and enforced host-side.
type Scope struct {
	caps map[auth.Cap]bool
}

// NewScope builds a Scope from a list of capability names, rejecting any name
// outside internal/auth's AllCaps vocabulary so a typo or a fabricated
// capability can never silently widen (or narrow) the ceiling. Order and
// duplicates are irrelevant.
func NewScope(names []string) (Scope, error) {
	set := make(map[auth.Cap]bool, len(names))
	for _, raw := range names {
		c := auth.Cap(raw)
		if !knownCap(c) {
			return Scope{}, fmt.Errorf("plugin: unknown capability %q (not in internal/auth AllCaps)", raw)
		}
		set[c] = true
	}
	return Scope{caps: set}, nil
}

// Has reports whether the scope grants capability c.
func (s Scope) Has(c auth.Cap) bool { return s.caps[c] }

// Names returns the scope's capabilities as sorted strings, for persistence and
// audit. Always non-nil (empty slice for the empty scope) so it round-trips
// through JSON as "[]".
func (s Scope) Names() []string {
	out := make([]string, 0, len(s.caps))
	for c := range s.caps {
		out = append(out, string(c))
	}
	sort.Strings(out)
	return out
}

// knownCap reports whether c is part of internal/auth's capability vocabulary.
func knownCap(c auth.Cap) bool {
	for _, known := range auth.AllCaps {
		if c == known {
			return true
		}
	}
	return false
}

// RequiredCap maps a change-engine op type to the capability that op class
// already requires — the single mapping the Stager consults before letting a
// plugin stage an op. It is deliberately fail-safe: any op type not matched by a
// more specific prefix defaults to netWrite (the strongest config-write cap), so
// a newly-added op the SDK has not yet special-cased can never be staged by a
// plugin holding only a weaker capability.
//
//   - fw.*             -> fwWrite
//   - sdn.*            -> sdnWrite   (zones/vnets/subnets/dns/apply)
//   - guest.nic.update -> guestNet
//   - everything else  -> netWrite   (iface/bond/bridge/vlan/ipam/nat/qos/
//     route/switch/vf/wg, and any future op)
func RequiredCap(op change.OpType) auth.Cap {
	s := string(op)
	switch {
	case strings.HasPrefix(s, "fw."):
		return auth.CapFWWrite
	case strings.HasPrefix(s, "sdn."):
		return auth.CapSDNWrite
	case s == string(change.OpGuestNicUpdate):
		return auth.CapGuestNet
	default:
		return auth.CapNetWrite
	}
}

// ValidateScope checks a plugin's declared extension points and capability scope
// are internally consistent before install: every extension point is recognized,
// and the scope covers each point's minimum capability. It returns a descriptive
// error naming the first violation — the install is refused, not silently
// clamped, so an operator sees exactly why a plugin was rejected.
func ValidateScope(points []ExtensionPoint, scope Scope) error {
	if len(points) == 0 {
		return fmt.Errorf("plugin: declares no extension points")
	}
	for _, ep := range points {
		if !validExtensionPoint(ep) {
			return fmt.Errorf("plugin: unknown extension point %q", ep)
		}
		need := extensionPointMinCap[ep]
		if !scope.Has(need) {
			return fmt.Errorf("plugin: extension point %q requires capability %q, which the plugin's declared scope does not include", ep, need)
		}
	}
	return nil
}
