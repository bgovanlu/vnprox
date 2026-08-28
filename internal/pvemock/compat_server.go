// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"net/http"
	"strings"
)

// compat_server.go (T-2103) wraps a normal Server with the one
// version-gated behavior compat_versions.go currently models: whether the
// /cluster/sdn/fabrics API family exists. It is a new file, not an edit to
// server.go/sdn.go: every existing pvemock test and consumer keeps talking
// to the version-independent NewServer exactly as before, and this wrapper
// only engages for callers that explicitly ask for a version profile.
//
// Until 2026-08-16 this wrapper gated SDN *zone types* instead
// ("openfabric"/"ospf" rejected below PVE 9.0). Hardware disproved that
// model — see PVEVersionProfile.SDNFabrics for the capture and the
// reasoning. The gate now sits on the surface the feature actually has.
//
// Until T-3101, this wrapper also self-answered GET .../fabrics/all with a
// hardcoded literal and 501'd every other fabrics path, because the base
// *Server had no fabric routes of its own to fall through to — "modeling
// fabric contents is T-3101's job, not this file's" (the comment this file
// used to carry). T-3101 gave the base Server real fabric routes
// (sdn_fabric.go: GET/POST /cluster/sdn/fabrics/fabric, GET/PUT/DELETE
// .../fabric/{id}, GET .../all, GET .../node), so that job is done, and
// this wrapper's only remaining responsibility is the version gate itself:
// on a supporting profile it falls straight through to the base Server for
// every fabrics path; on a profile that predates the family it still
// answers 501 for all of them, so PVE 8.2 continues to look like it has no
// fabrics family at all.

// NewCompatServer wraps NewServer(f, opts...) with profile's version-gated
// SDN Fabrics enforcement: any request at or below /cluster/sdn/fabrics is
// rejected with a PVE-shaped 501 on a profile that predates the family, and
// falls through unmodified to the base Server (which now has real fabric
// routes, T-3101) on a profile that has it. Every other request passes
// through to base unmodified regardless of profile.
//
// The returned handler also sets CompatVersionHeader on every response
// (including the ones it rejects itself), so a test or an external caller
// can always confirm which profile actually answered.
func NewCompatServer(f *Fixture, profile PVEVersionProfile, opts ...Option) http.Handler {
	base := NewServer(f, opts...)
	return &compatServer{base: base, profile: profile}
}

type compatServer struct {
	base    *Server
	profile PVEVersionProfile
}

// sdnZonesPath matches server.go's own route registration (buildRouter:
// api.Post("/cluster/sdn/zones", ...)) under the shared "/api2/json"
// prefix. It is no longer gated — it is kept because the package's tests
// post ordinary zones through it to prove this wrapper stays additive.
const sdnZonesPath = "/api2/json/cluster/sdn/zones"

// sdnFabricsAllPath names GET .../fabrics/all specifically — kept as a
// named constant purely because several tests in this package address it
// directly; it carries no special handling of its own any more (every
// fabrics path, this one included, is either 501-gated or passed straight
// through to the base Server's own real route, sdn_fabric.go's
// handleSDNFabricsAll).
const sdnFabricsAllPath = SDNFabricsPath + "/all"

func (s *compatServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(CompatVersionHeader, s.profile.Version)

	if r.URL.Path != SDNFabricsPath && !strings.HasPrefix(r.URL.Path, SDNFabricsPath+"/") {
		s.base.ServeHTTP(w, r)
		return
	}

	if !s.profile.SupportsSDNFabricsAPI() {
		writeError(w, http.StatusNotImplemented, s.profile.fabricsUnsupportedError().Error())
		return
	}

	// A supporting profile: the base Server now has real fabric routes
	// (T-3101), so every fabrics path — not just .../all — falls straight
	// through to them.
	s.base.ServeHTTP(w, r)
}
