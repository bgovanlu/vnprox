package pvemock

import (
	"io"
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

// NewCompatServer wraps NewServer(f, opts...) with profile's version-gated
// SDN Fabrics enforcement: any request at or below /cluster/sdn/fabrics
// gets a PVE-shaped 501 on a profile that predates the family, and is
// answered by this wrapper on a profile that has it — the base Server has
// no fabrics routes of its own, and modeling fabric *contents* is T-3101's
// work, not this file's. Every other request passes through to base
// unmodified.
//
// The returned handler also sets CompatVersionHeader on every response
// (including the ones it answers or rejects itself), so a test or an
// external caller can always confirm which profile actually answered.
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

// sdnFabricsAllPath is the one fabrics read this wrapper answers with a
// body. Every other path under SDNFabricsPath is a real route on PVE 9
// that this mock does not model yet; it answers those 501 on every
// profile, so a caller can never mistake "modeled as absent" for
// "supported and empty".
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

	if r.Method == http.MethodGet && r.URL.Path == sdnFabricsAllPath {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"data":`+sdnFabricsAllResponse+`}`)
		return
	}

	// A fabrics path this mock has not modeled, on a profile that does have
	// the family. Answering 501 with a message that says which case this is
	// keeps it distinguishable from the version gate above: a check that
	// starts exercising fabric CRUD should fail loudly here rather than
	// read a silence as success.
	writeError(w, http.StatusNotImplemented,
		"pvemock: PVE "+s.profile.Version+" has /cluster/sdn/fabrics, but this mock models only "+
			sdnFabricsAllPath+"; fabric CRUD is not modeled yet (T-3101)")
}
