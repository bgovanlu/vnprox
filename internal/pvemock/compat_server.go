package pvemock

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// compat_server.go (T-2103) wraps a normal Server with the one
// version-gated behavior compat_versions.go currently models (SDN Fabric
// zone types). It is a new file, not an edit to server.go/sdn.go: every
// existing pvemock test and consumer keeps talking to the
// version-independent NewServer exactly as before, and this wrapper only
// engages for callers that explicitly ask for a version profile.

// zoneTypeBody is the subset of an SDN zone create/update request body this
// wrapper needs to read. SDNZoneSpec.Type carries json:"type"
// (types.go); the zone id itself is irrelevant here.
type zoneTypeBody struct {
	Type string `json:"type"`
}

// NewCompatServer wraps NewServer(f, opts...) with profile's version-gated
// SDN zone-type enforcement: a POST to create a zone, or a PUT to update
// one, naming a zone `type` that profile.ValidateSDNZoneType rejects gets a
// PVE-shaped 400 before it ever reaches the underlying Server — mirroring
// how real PVE would reject a zone type its own running version doesn't
// know about, at the same request. Every other request passes through to
// base unmodified.
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

// sdnZonesPath and sdnZonePathPrefix match server.go's own route
// registrations (buildRouter: api.Post("/cluster/sdn/zones", ...),
// api.Put("/cluster/sdn/zones/{zone}", ...)) under the shared "/api2/json"
// prefix.
const (
	sdnZonesPath      = "/api2/json/cluster/sdn/zones"
	sdnZonePathPrefix = sdnZonesPath + "/"
)

func (s *compatServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(CompatVersionHeader, s.profile.Version)

	gatesZoneType := (r.Method == http.MethodPost && r.URL.Path == sdnZonesPath) ||
		(r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, sdnZonePathPrefix))
	if !gatesZoneType {
		s.base.ServeHTTP(w, r)
		return
	}

	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		writeError(w, http.StatusBadRequest, "reading request body: "+err.Error())
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))

	var body zoneTypeBody
	// A zone update (PUT) legitimately omits `type` (PVE's PUT merges,
	// same as every other PUT this mock serves — see README.md's curl
	// walkthrough step 3); an empty/absent type is not this wrapper's
	// concern, and json.Unmarshal leaves body.Type == "" for it, which
	// ValidateSDNZoneType always accepts (fabricZoneTypes has no ""
	// entry). A malformed body is left for the underlying handler's own
	// decodeRequest to reject with its normal error, not this wrapper's.
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	if body.Type != "" {
		if verr := s.profile.ValidateSDNZoneType(body.Type); verr != nil {
			writeError(w, http.StatusBadRequest, verr.Error())
			return
		}
	}
	s.base.ServeHTTP(w, r)
}
