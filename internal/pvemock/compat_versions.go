package pvemock

import (
	"errors"
	"fmt"
)

// compat_versions.go (T-2103) is the mock-validated half of vnprox's PVE
// compatibility matrix. Its counterpart, `vnproxctl telemetry` (T-2503),
// aggregates `vnproxctl verify` results from real clusters in the field —
// hardware-validated, but only for whatever an operator's cluster and
// checks happen to cover. This half runs the opposite direction: it can
// cover every PVE line docs/roadmap.md's Compatibility policy promises
// support for, on every commit, with no cluster required — at the cost of
// only ever proving "the mock, and whatever it was told about a version's
// API shape, agree with each other." docs/compatibility.md states this
// distinction and neither half is allowed to blur into the other (T-2103's
// card, AC3).
//
// A PVEVersionProfile is this package's model of one such told-about
// difference: a place where real Proxmox VE's API surface genuinely
// changed between minor releases, encoded so a running mock server can
// enforce it (compat_server.go) rather than merely documenting it in prose
// nobody re-checks. It intentionally does not attempt to model every
// difference between PVE releases — see the field doc comments below for
// what is modeled and the specific, checkable source for each choice.

// PVEVersionProfile names one PVE release line this matrix tracks and the
// version-gated API-shape differences internal/pvemock knows how to enforce
// for it.
type PVEVersionProfile struct {
	// Version is the matrix column label (e.g. "8.2", "9.0", "9.2") and the
	// value NewCompatServer stamps onto the CompatVersionHeader.
	Version string
	Major   int
	Minor   int

	// SDNFabricZones: PVE 9.0 added SDN "Fabrics" (OSPF/OpenFabric
	// underlay-routing zone types, e.g. "openfabric") to the SDN zone type
	// enumeration — documented in Proxmox's 9.0 release notes and SDN
	// documentation. PVE 8.2 has no such zone type. This is the one
	// concrete, checkable divergence this matrix currently enforces at the
	// mock layer (ValidateSDNZoneType, compat_server.go) — modeled from
	// Proxmox's published feature list, NOT captured from real hardware
	// (this repo has no PVE 8.2 or 9.0 cluster to capture from; see
	// docs/compatibility.md and CLAUDE.md's "no live Proxmox cluster"
	// note). Treat it as a documented-shape approximation, same standing as
	// every other entry in planning/reports/needs-hardware-validation.md.
	SDNFabricZones bool
}

// CompatVersionHeader is set by NewCompatServer on every response, naming
// which PVEVersionProfile answered — the compat-matrix analogue of
// Server's own MockIdentityHeader (server.go), and useful for the same
// reason: a caller (or a test) should never have to infer which version was
// actually behind a response.
const CompatVersionHeader = "X-Pvemock-Compat-Version"

// fabricZoneTypes are the SDN zone `type` values gated by
// PVEVersionProfile.SDNFabricZones.
var fabricZoneTypes = map[string]bool{
	"openfabric": true,
	"ospf":       true,
}

// CompatProfiles is the fixed set of PVE version profiles the matrix
// (internal/apicontract/compat) currently runs: the two lines
// docs/roadmap.md's Compatibility policy names explicitly (8.2, and 9.x
// represented by 9.0 and 9.2 — "whatever is current").
var CompatProfiles = []PVEVersionProfile{
	{Version: "8.2", Major: 8, Minor: 2, SDNFabricZones: false},
	{Version: "9.0", Major: 9, Minor: 0, SDNFabricZones: true},
	{Version: "9.2", Major: 9, Minor: 2, SDNFabricZones: true},
}

// ProfileByVersion looks up a profile in CompatProfiles by its Version
// label. The second return is false if no such profile is registered.
func ProfileByVersion(version string) (PVEVersionProfile, bool) {
	for _, p := range CompatProfiles {
		if p.Version == version {
			return p, true
		}
	}
	return PVEVersionProfile{}, false
}

// ValidateSDNZoneType reports whether zoneType is a valid SDN zone `type`
// value under this profile. It returns nil for every zone type this mock
// already supports version-independently (vlan, qinq, vxlan, evpn, simple,
// ...) — pvemock never enumerated an exhaustive zone-type allowlist before
// T-2103 (SDNZoneSpec.Type is an unvalidated string, sdn.go), and this
// function deliberately does not start: it only ever rejects the specific,
// documented, version-gated types this package models (currently the SDN
// Fabric types), never an arbitrary/unknown one.
func (p PVEVersionProfile) ValidateSDNZoneType(zoneType string) error {
	if fabricZoneTypes[zoneType] && !p.SDNFabricZones {
		return fmt.Errorf("%w: zone type %q needs PVE 9.0 or later (SDN Fabrics); this mock server is modeling PVE %s",
			ErrSDNZoneTypeUnsupported, zoneType, p.Version)
	}
	return nil
}

// ErrSDNZoneTypeUnsupported is returned by ValidateSDNZoneType (and
// surfaced as the mock HTTP server's 400 response body) when a zone type
// this profile does not support is requested.
var ErrSDNZoneTypeUnsupported = errors.New("pvemock: sdn zone type unsupported on this PVE version")
