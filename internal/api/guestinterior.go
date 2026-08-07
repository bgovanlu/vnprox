// guestinterior.go implements T-1304's guest network interior inspector:
// an opt-in, read-only view of a guest's own inside network state
// (interfaces, addresses, routes, DNS, listening sockets, default-gateway
// reachability), sourced via the QEMU guest agent for qemu guests
// (internal/guestinterior.FetchQEMU, reusing T-802's AgentExec seam) or
// directly from the host side for lxc guests (internal/guestinterior.
// FetchLXC, over internal/host.Reader's ContainerInterior/ContainerPing —
// local when the guest's node is this daemon's own, peer-routed
// otherwise, per docs/architecture.md §1/§5's cluster-awareness
// invariant), plus an IPAM cross-check annotation (never a write to IPAM).
//
//   - GET /guests/{ref}/interior-toggle — current opt-in state
//   - PUT /guests/{ref}/interior-toggle {enabled} — flip it
//   - GET /guests/{ref}/interior — the interior view (404/explicit
//     not-enabled response when the toggle is off — never silently
//     reaching into the guest)
//
// All three are netRead-gated, no CSRF requirement — the toggle is a
// personal/team UI preference (app-owned data, docs/api.md's Saved views &
// annotations section's own precedent), not a network-config mutation;
// the interior read itself reaches into a guest (or a peer node) but,
// like T-802/T-806's live probe, is a diagnostic read, never a mutation —
// it is however audited (guest.interior_read), matching probe.verify's
// own "reaches into a guest, so it's audited" precedent, so GET
// /guests/{ref}/interior is only mounted when AuthService also implements
// UsernameLookup (the same requirement mountSimulateRoutes' POST
// /simulate/verify already has, for the same reason: there is no username
// to stamp on the audit row otherwise).

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/guestinterior"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// maxGuestInteriorToggleBodyBytes bounds PUT /guests/{ref}/interior-toggle's
// request body — a single {enabled: bool} field needs only a tiny cap,
// mirroring maxAnnotationBodyBytes' reasoning for a similarly small body.
const maxGuestInteriorToggleBodyBytes = 4 << 10

// Sentinel errors fetchQEMUInterior/fetchLXCInterior return for the
// several distinct "this read could not even be attempted" conditions —
// handleGetGuestInterior reports all of them as
// guestInteriorErrUnavailable/503 (the request itself was valid; the
// dependency needed to answer it isn't wired/reachable right now), but
// keeps them distinct sentinels so a future caller (or a test) can tell
// them apart.
var (
	errGuestInteriorNoProbeClients = errors.New("guestinterior: no live PVE client available for a qemu guest-agent read")
	errGuestInteriorNoPVESession   = errors.New("guestinterior: no active PVE session for this request")
	errGuestInteriorNoHostReader   = errors.New("guestinterior: no local host reader configured for an lxc guest on this node")
	errGuestInteriorNoPeerRoute    = errors.New("guestinterior: guest's node is not this daemon's own and no reachable peer route exists for it")
)

// GuestInteriorToggleStore is the per-guest opt-in preference seam.
// *store.GuestInteriorToggleRepo satisfies this directly.
type GuestInteriorToggleStore interface {
	Get(ctx context.Context, ref string) (bool, error)
	Set(ctx context.Context, ref string, enabled bool, updatedBy string, at int64) error
}

// GuestInteriorGraph resolves a guest ref to its inventory entity —
// typically the same live *inventory.Graph Simulator/Firewall above
// already wire in (its Snapshot method satisfies this directly).
type GuestInteriorGraph interface {
	Snapshot() inventory.Snapshot
}

// GuestInteriorHostReader is the lxc-path local-node dependency: the
// subset of internal/host.Reader FetchLXC needs. host.NewReal() (or a
// host.FixtureReader) satisfies this directly.
type GuestInteriorHostReader interface {
	ContainerInterior(ctx context.Context, node string, vmid int) (host.ContainerInteriorRaw, error)
	ContainerPing(ctx context.Context, node string, vmid int, targetIP string) (bool, error)
}

// PeerContainerSource is the lxc-path peer-node dependency (T-1304): peer
// discovery plus the peer-routed counterpart of GuestInteriorHostReader's
// two methods, for a guest whose node is not this daemon's own —
// docs/architecture.md's cluster-awareness invariant applied to the lxc
// host-side read path (the qemu path needs no equivalent: PVE's own REST
// API, which internal/pve.Client.AgentExec/GetGuestAgentInterfaces call,
// is already cluster-transparent). *peer.Client satisfies this directly.
type PeerContainerSource interface {
	ClusterPeers
	ContainerInterior(ctx context.Context, p peer.Peer, node string, vmid int) (host.ContainerInteriorRaw, error)
	ContainerPing(ctx context.Context, p peer.Peer, node string, vmid int, targetIP string) (bool, error)
}

// GuestInteriorIPAMSource backs the IPAM cross-check annotation:
// *ipam.Service's existing AllAllocations method (already exported for
// T-406's DHCP-range-overlap check) satisfies this directly. nil simply
// omits the ipamDiff annotation (an empty array) rather than failing the
// whole response — the same degraded-mode tolerance every other optional
// dependency in this package gets.
type GuestInteriorIPAMSource interface {
	AllAllocations(ctx context.Context) (map[string][]ipam.Allocation, error)
}

// mountGuestInteriorRoutes registers the three routes documented in this
// file's own doc comment. toggles/graph/auth are required (nil skips
// mounting the whole family, matching Layouts/Annotations' degraded-mode
// convention); GET /guests/{ref}/interior and PUT .../interior-toggle
// additionally need auth to implement UsernameLookup (audit/toggle-author
// stamping) — GET .../interior-toggle alone is mounted without it.
// probeClients/hostReader/peers/ipamSvc are each independently optional —
// their absence degrades only the read paths that need them.
func mountGuestInteriorRoutes(r chi.Router, toggles GuestInteriorToggleStore, graph GuestInteriorGraph, probeClients ProbeClientProvider, hostReader GuestInteriorHostReader, peers PeerContainerSource, ipamSvc GuestInteriorIPAMSource, localNode func() string, audit simulateVerifyAuditor, auth AuthService) {
	if toggles == nil || graph == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/guests/{ref}/interior-toggle", handleGetGuestInteriorToggle(toggles))
		if lookup, ok := auth.(UsernameLookup); ok {
			r.Put("/guests/{ref}/interior-toggle", handlePutGuestInteriorToggle(toggles, lookup))
			r.Get("/guests/{ref}/interior", handleGetGuestInterior(toggles, graph, probeClients, hostReader, peers, ipamSvc, localNode, audit, lookup))
		}
	})
}

// guestInteriorToggleResponse is GET/PUT .../interior-toggle's body.
type guestInteriorToggleResponse struct {
	Ref     string `json:"ref"`
	Enabled bool   `json:"enabled"`
}

func handleGetGuestInteriorToggle(toggles GuestInteriorToggleStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref, errMsg := parseGuestRef(chi.URLParam(r, "ref"))
		if errMsg != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", errMsg)
			return
		}
		enabled, err := toggles.Get(r.Context(), ref.String())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read interior toggle state")
			return
		}
		writeJSON(w, http.StatusOK, guestInteriorToggleResponse{Ref: ref.String(), Enabled: enabled})
	}
}

type guestInteriorTogglePutRequest struct {
	Enabled bool `json:"enabled"`
}

func handlePutGuestInteriorToggle(toggles GuestInteriorToggleStore, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		ref, errMsg := parseGuestRef(chi.URLParam(r, "ref"))
		if errMsg != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", errMsg)
			return
		}
		var req guestInteriorTogglePutRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxGuestInteriorToggleBodyBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "request body must be {\"enabled\": bool}")
			return
		}
		if err := toggles.Set(r.Context(), ref.String(), req.Enabled, username, time.Now().Unix()); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save interior toggle state")
			return
		}
		writeJSON(w, http.StatusOK, guestInteriorToggleResponse{Ref: ref.String(), Enabled: req.Enabled})
	}
}

// parseGuestRef validates refStr as a "guest:<node>:<vmid>" Ref.
func parseGuestRef(refStr string) (inventory.Ref, string) {
	// chi routes on r.URL.RawPath when it is non-empty, so URLParam hands back
	// the still-percent-encoded segment: a browser calling
	// encodeURIComponent("guest:pve1:200") arrives here as
	// "guest%3Apve1%3A200", which ParseRef rejects. Every other ref-taking
	// route in this package already unescapes (topology.go's entity lookup,
	// ipam.go's CIDR params); these three did not, which made the entire guest
	// interior feature unreachable from the SPA — both the GET and the PUT
	// returned 400 for every real request the frontend has ever sent. Found by
	// the e2e suite once T-2108 stopped an earlier failure from masking it.
	//
	// On a bad escape sequence, fall through to the raw form so an unescaped
	// ref (curl, a script) keeps working exactly as before.
	if unescaped, uerr := url.PathUnescape(refStr); uerr == nil {
		refStr = unescaped
	}
	ref, err := inventory.ParseRef(refStr)
	if err != nil || ref.Kind != inventory.KindGuest {
		return inventory.Ref{}, "ref must be a valid guest ref (guest:node:vmid)"
	}
	return ref, ""
}

// guestInteriorResponse is GET /guests/{ref}/interior's body
// (docs/api.md's Guest interior section).
type guestInteriorResponse struct {
	Source                  string                          `json:"source"`
	DNS                     guestinterior.DNSConfig         `json:"dns"`
	Interfaces              []guestinterior.Interface       `json:"interfaces"`
	Addresses               []guestinterior.Address         `json:"addresses"`
	Routes                  []guestinterior.Route           `json:"routes"`
	ListeningSockets        []guestinterior.ListeningSocket `json:"listeningSockets"`
	IPAMDiff                []ipamDiffEntry                 `json:"ipamDiff"`
	DefaultGatewayReachable bool                            `json:"defaultGatewayReachable"`
}

// ipamDiffEntry is one guest-claimed address's IPAM cross-check
// annotation (docs/api.md's Guest interior section: "ipamDiff: {claimed,
// allocated, matches}" per address) — Claimed is always true (this list
// only ever holds addresses the guest itself claimed); Allocated reports
// whether IPAM has a matching allocation record for that exact address;
// Matches is Claimed && Allocated, spelled out explicitly rather than left
// implicit so a client never has to re-derive it.
type ipamDiffEntry struct {
	IP        string `json:"ip"`
	Claimed   bool   `json:"claimed"`
	Allocated bool   `json:"allocated"`
	Matches   bool   `json:"matches"`
}

// Machine-readable error codes for GET /guests/{ref}/interior.
const (
	guestInteriorErrNotEnabled    = "interior_not_enabled"
	guestInteriorErrGuestNotFound = "not_found"
	guestInteriorErrUnavailable   = "interior_unavailable"
)

func handleGetGuestInterior(toggles GuestInteriorToggleStore, graph GuestInteriorGraph, probeClients ProbeClientProvider, hostReader GuestInteriorHostReader, peers PeerContainerSource, ipamSvc GuestInteriorIPAMSource, localNode func() string, audit simulateVerifyAuditor, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		ref, errMsg := parseGuestRef(chi.URLParam(r, "ref"))
		if errMsg != "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", errMsg)
			return
		}

		enabled, err := toggles.Get(r.Context(), ref.String())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read interior toggle state")
			return
		}
		if !enabled {
			writeJSONError(w, http.StatusNotFound, guestInteriorErrNotEnabled,
				"the guest-interior inspector is not enabled for this guest — opt in via PUT .../interior-toggle first")
			return
		}

		ent, ok := graph.Snapshot().Get(ref)
		if !ok {
			writeJSONError(w, http.StatusNotFound, guestInteriorErrGuestNotFound, "guest not found in inventory")
			return
		}
		guest, ok := ent.(*inventory.Guest)
		if !ok {
			writeJSONError(w, http.StatusNotFound, guestInteriorErrGuestNotFound, "ref does not name a guest")
			return
		}

		var view guestinterior.View
		switch guest.Type {
		case "qemu":
			view, err = fetchQEMUInterior(r.Context(), probeClients, guest)
		case "lxc":
			view, err = fetchLXCInterior(r.Context(), hostReader, peers, localNode, guest)
		default:
			writeJSONError(w, http.StatusNotFound, guestInteriorErrGuestNotFound,
				"guest type is neither qemu nor lxc — no interior read path exists for it")
			return
		}
		if err != nil {
			auditGuestInteriorRead(r.Context(), audit, username, ref.String(), "error")
			writeJSONError(w, http.StatusServiceUnavailable, guestInteriorErrUnavailable, err.Error())
			return
		}

		auditGuestInteriorRead(r.Context(), audit, username, ref.String(), "ok")
		writeJSON(w, http.StatusOK, toGuestInteriorResponse(view, computeIPAMDiff(r.Context(), ipamSvc, view.Addresses)))
	}
}

// fetchQEMUInterior resolves a live PVE-client and runs
// internal/guestinterior.FetchQEMU. A nil/unavailable ProbeClients
// provider (no live PVE session support wired, or the client doesn't
// satisfy guestinterior.QEMUClient) is reported the same honest way any
// other "could not attempt this read" condition is.
func fetchQEMUInterior(ctx context.Context, probeClients ProbeClientProvider, guest *inventory.Guest) (guestinterior.View, error) {
	if probeClients == nil {
		return guestinterior.View{}, errGuestInteriorNoProbeClients
	}
	client, ok := probeClients.ProbeClientFor(ctx)
	if !ok {
		return guestinterior.View{}, errGuestInteriorNoPVESession
	}
	qc, ok := client.(guestinterior.QEMUClient)
	if !ok {
		return guestinterior.View{}, errGuestInteriorNoProbeClients
	}
	return guestinterior.FetchQEMU(ctx, qc, guest.Node, guest.VMID)
}

// fetchLXCInterior resolves the right host.Reader-shaped client for
// guest's node — local (hostReader) when it's this daemon's own node, a
// specific peer (via peers) otherwise — and runs
// internal/guestinterior.FetchLXC. Cluster-aware per docs/architecture.md
// §1/§5: an lxc guest on a peer node is inspected exactly the way one on
// this node is, not silently unsupported.
func fetchLXCInterior(ctx context.Context, hostReader GuestInteriorHostReader, peers PeerContainerSource, localNode func() string, guest *inventory.Guest) (guestinterior.View, error) {
	local := localNode == nil || guest.Node == "" || guest.Node == localNode()
	if local {
		if hostReader == nil {
			return guestinterior.View{}, errGuestInteriorNoHostReader
		}
		return guestinterior.FetchLXC(ctx, hostReaderLXCClient{hostReader}, guest.Node, guest.VMID)
	}
	if peers == nil {
		return guestinterior.View{}, errGuestInteriorNoPeerRoute
	}
	list, err := peers.Peers(ctx)
	if err != nil {
		return guestinterior.View{}, errGuestInteriorNoPeerRoute
	}
	for _, p := range list {
		if p.Node == guest.Node {
			return guestinterior.FetchLXC(ctx, peerLXCClient{source: peers, peer: p}, guest.Node, guest.VMID)
		}
	}
	return guestinterior.View{}, errGuestInteriorNoPeerRoute
}

// hostReaderLXCClient adapts a GuestInteriorHostReader into
// guestinterior.LXCClient — both already have identical method sets, but
// the concrete adapter makes the interface satisfaction explicit across
// the package boundary.
type hostReaderLXCClient struct{ r GuestInteriorHostReader }

func (a hostReaderLXCClient) ContainerInterior(ctx context.Context, node string, vmid int) (host.ContainerInteriorRaw, error) {
	return a.r.ContainerInterior(ctx, node, vmid)
}

func (a hostReaderLXCClient) ContainerPing(ctx context.Context, node string, vmid int, targetIP string) (bool, error) {
	return a.r.ContainerPing(ctx, node, vmid, targetIP)
}

// peerLXCClient adapts a PeerContainerSource + one resolved peer.Peer into
// guestinterior.LXCClient — mirrors internal/neighbor's own
// peerNeighborReader adapter pattern exactly.
type peerLXCClient struct {
	source PeerContainerSource
	peer   peer.Peer
}

func (a peerLXCClient) ContainerInterior(ctx context.Context, node string, vmid int) (host.ContainerInteriorRaw, error) {
	return a.source.ContainerInterior(ctx, a.peer, node, vmid)
}

func (a peerLXCClient) ContainerPing(ctx context.Context, node string, vmid int, targetIP string) (bool, error) {
	return a.source.ContainerPing(ctx, a.peer, node, vmid, targetIP)
}

func toGuestInteriorResponse(v guestinterior.View, diff []ipamDiffEntry) guestInteriorResponse {
	resp := guestInteriorResponse{
		Interfaces: v.Interfaces, Addresses: v.Addresses, Routes: v.Routes, DNS: v.DNS,
		ListeningSockets: v.ListeningSockets, DefaultGatewayReachable: v.DefaultGatewayReachable,
		Source: string(v.Source), IPAMDiff: diff,
	}
	if resp.Interfaces == nil {
		resp.Interfaces = []guestinterior.Interface{}
	}
	if resp.Addresses == nil {
		resp.Addresses = []guestinterior.Address{}
	}
	if resp.Routes == nil {
		resp.Routes = []guestinterior.Route{}
	}
	if resp.ListeningSockets == nil {
		resp.ListeningSockets = []guestinterior.ListeningSocket{}
	}
	if resp.IPAMDiff == nil {
		resp.IPAMDiff = []ipamDiffEntry{}
	}
	return resp
}

// computeIPAMDiff cross-checks every guest-claimed address against
// ipamSvc's resolved allocation set (docs/features/ipam.md §1's
// "observed, never authoritative" confidence labeling applied here: a
// guest's own self-report is the thing being checked, not the source of
// truth) — never a write to IPAM. ipamSvc == nil (no live PVE session
// wired) simply omits the annotation, the same degraded-mode treatment
// every other optional dependency in this file gets.
func computeIPAMDiff(ctx context.Context, ipamSvc GuestInteriorIPAMSource, addresses []guestinterior.Address) []ipamDiffEntry {
	if ipamSvc == nil || len(addresses) == 0 {
		return nil
	}
	byCIDR, err := ipamSvc.AllAllocations(ctx)
	if err != nil {
		return nil
	}
	allocatedIPs := map[string]bool{}
	for _, allocs := range byCIDR {
		for _, a := range allocs {
			allocatedIPs[a.IP] = true
		}
	}
	out := make([]ipamDiffEntry, 0, len(addresses))
	seen := map[string]bool{}
	for _, addr := range addresses {
		if addr.IP == "" || seen[addr.IP] {
			continue
		}
		seen[addr.IP] = true
		allocated := allocatedIPs[addr.IP]
		out = append(out, ipamDiffEntry{IP: addr.IP, Claimed: true, Allocated: allocated, Matches: allocated})
	}
	return out
}

// auditGuestInteriorRead appends one guest.interior_read audit_log row per
// attempted interior read (docs/api.md's audit conventions) — mirrors
// auditSimulateVerify's exact nil-safety pattern: reaching into a guest
// (or a peer node, for lxc) is a diagnostic action worth an audit trail,
// same as T-802's probe.verify.
func auditGuestInteriorRead(ctx context.Context, audit simulateVerifyAuditor, username, ref, result string) {
	if audit == nil {
		return
	}
	entry := store.AuditEntry{At: time.Now().Unix(), Username: username, Action: "guest.interior_read", Result: result}
	entry.Target.String, entry.Target.Valid = ref, true
	_, _ = audit.Append(ctx, entry)
}
