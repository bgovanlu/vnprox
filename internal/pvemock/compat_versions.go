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

	// SDNFabrics: PVE 9.0 added SDN "Fabrics" — an underlay-routing feature
	// reachable at its own API family, /cluster/sdn/fabrics, with `fabric`
	// and `node` sub-collections and an `all` read. PVE 8.2 has no such
	// path. This is the one concrete, checkable divergence this matrix
	// enforces at the mock layer (SupportsSDNFabricsAPI, compat_server.go).
	//
	// This field replaced SDNFabricZones on 2026-08-16, because that model
	// was wrong and its check could never have failed for the right reason.
	// SDNFabricZones asserted that PVE 9 added "openfabric"/"ospf" to the
	// SDN *zone type* enumeration — modeled from Proxmox's 9.0 release
	// notes, never captured from hardware. The first PVE 9 node this
	// project ever had access to (pvecube, 9.2.4) says otherwise, and the
	// capture is checked in at planning/reports/evidence/
	// pve-9.2.4-sdn-schema.txt:
	//
	//   - the real 9.2 zone type enum is
	//     <evpn | faucet | qinq | simple | vlan | vxlan>. "openfabric" and
	//     "ospf" are not zone types on PVE 9 at all, so the old gate
	//     asserted a divergence that does not exist in either direction:
	//     real 8.2 and real 9.2 both reject an "openfabric" zone.
	//   - fabrics are a separate object family, created with
	//     POST /cluster/sdn/fabrics/fabric --id <string> --protocol
	//     <bgp | openfabric | ospf | wireguard>. openfabric/ospf are two of
	//     four fabric *protocols*, not zone types — and the other two
	//     (bgp, wireguard) appear in no vnprox document.
	//
	// The lesson is worth keeping next to the field: a compatibility check
	// derived from release notes tests the release notes. This one passed
	// on every commit since T-2103 while describing a PVE that does not
	// exist. Any future field here states the surface it was captured from.
	SDNFabrics bool
}

// CompatVersionHeader is set by NewCompatServer on every response, naming
// which PVEVersionProfile answered — the compat-matrix analogue of
// Server's own MockIdentityHeader (server.go), and useful for the same
// reason: a caller (or a test) should never have to infer which version was
// actually behind a response.
const CompatVersionHeader = "X-Pvemock-Compat-Version"

// SDNFabricsPath is the API family PVE 9.0 added and PVE 8.2 does not
// serve, spelled as it appears under this mock's shared "/api2/json"
// prefix. compat_server.go gates every request at or below it.
const SDNFabricsPath = "/api2/json/cluster/sdn/fabrics"

// sdnFabricsAllResponse (removed by T-3101) used to be the hardcoded body
// compatServer self-answered for GET /cluster/sdn/fabrics/all, copied
// verbatim from what pvecube (PVE 9.2.4) returned with no fabrics
// configured — planning/reports/evidence/pve-9.2.4-sdn-schema.txt. The base
// *Server now has a real handler for that route (sdn_fabric.go's
// handleSDNFabricsAll, serving live fixture-backed content) and
// compatServer.ServeHTTP falls through to it on every supporting profile,
// so this literal has no reader left. Modeling fabric contents was T-3101's
// job; it's done — see internal/pvemock/sdn_fabric.go.

// CompatProfiles is the fixed set of PVE version profiles the matrix
// (internal/apicontract/compat) currently runs: the two lines
// docs/roadmap.md's Compatibility policy names explicitly (8.2, and 9.x
// represented by 9.0 and 9.2 — "whatever is current").
var CompatProfiles = []PVEVersionProfile{
	{Version: "8.2", Major: 8, Minor: 2, SDNFabrics: false},
	{Version: "9.0", Major: 9, Minor: 0, SDNFabrics: true},
	{Version: "9.2", Major: 9, Minor: 2, SDNFabrics: true},
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

// SupportsSDNFabricsAPI reports whether this profile serves the
// /cluster/sdn/fabrics family at all. Note what this deliberately does not
// do: it does not validate zone types. pvemock has never enumerated an
// exhaustive zone-type allowlist (SDNZoneSpec.Type is an unvalidated
// string, sdn.go) and it still does not — partly because the previous
// attempt to gate one was wrong (see SDNFabrics), and partly because the
// real 9.2 enum contains a type vnprox itself rejects: "faucet" is
// accepted by real PVE zone create and is absent from
// internal/change.validSdnZoneTypes. That gap is vnprox's, not this
// mock's, and it is carded rather than papered over here.
func (p PVEVersionProfile) SupportsSDNFabricsAPI() bool { return p.SDNFabrics }

// ErrSDNFabricsUnsupported is returned (and surfaced as the mock HTTP
// server's PVE-shaped 501 response body) when the /cluster/sdn/fabrics
// family is requested from a profile that predates it. 501 rather than 404
// because that is what real PVE answers for an API path its running
// version does not implement.
var ErrSDNFabricsUnsupported = errors.New("pvemock: sdn fabrics api unsupported on this PVE version")

// fabricsUnsupportedError renders ErrSDNFabricsUnsupported with the
// requesting profile named, so a failing check never has to infer which
// version answered.
func (p PVEVersionProfile) fabricsUnsupportedError() error {
	return fmt.Errorf("%w: /cluster/sdn/fabrics needs PVE 9.0 or later; this mock server is modeling PVE %s",
		ErrSDNFabricsUnsupported, p.Version)
}
