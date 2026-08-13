package pvemock

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
)

// SDN DNS management mock (T-1204). Real PVE's SDN DNS plugin keeps zone
// (plugin) config in /etc/pve/sdn/dns.cfg and writes each record straight
// into the backing PowerDNS server. This mock models both: the zone config
// CRUD under /cluster/sdn/dns, and a PowerDNS-shaped per-record CRUD +
// live-resolve read under /cluster/sdn/dns/{zone}/... . Malformed records
// are rejected with PVE/PowerDNS-style 400s where the shape is known; the
// exact real wording/semantics are unconfirmed against live hardware
// (planning/reports/needs-hardware-validation.md).

// dnsRecordTypes is the record-type set this mock accepts. Real PowerDNS
// supports many more; these are the ones vnprox's DNS management surface
// exposes (A/AAAA/PTR forward+reverse host records, CNAME aliases, TXT).
var dnsRecordTypes = map[string]bool{
	"A": true, "AAAA": true, "PTR": true, "CNAME": true, "TXT": true,
}

// dnsNamePattern is the accepted record-name (hostname label / FQDN) charset:
// letters, digits, hyphen, dot, underscore, and a trailing dot; a leading
// "@" (zone apex) or "*" (wildcard) is allowed as the first label. This is a
// deliberately permissive superset flagged for hardware validation, not a
// strict RFC-1035 check.
var dnsNamePattern = regexp.MustCompile(`^(\*|@|[A-Za-z0-9_])[A-Za-z0-9_.-]*$`)

func (srv *Server) mountSDNDNS(api chi.Router) {
	api.Get("/cluster/sdn/dns", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNDnsZonesList))
	api.Post("/cluster/sdn/dns", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNDnsZoneCreate))
	api.Get("/cluster/sdn/dns/{zone}", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNDnsZoneGet))
	api.Put("/cluster/sdn/dns/{zone}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNDnsZoneUpdate))
	api.Delete("/cluster/sdn/dns/{zone}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNDnsZoneDelete))

	api.Get("/cluster/sdn/dns/{zone}/records", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNDnsRecordsList))
	api.Post("/cluster/sdn/dns/{zone}/records", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNDnsRecordCreate))
	api.Put("/cluster/sdn/dns/{zone}/records/{name}/{type}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNDnsRecordUpdate))
	api.Delete("/cluster/sdn/dns/{zone}/records/{name}/{type}", srv.requirePrivilege(PrivSDNAllocate, srv.handleSDNDnsRecordDelete))

	api.Get("/cluster/sdn/dns/{zone}/resolve", srv.requirePrivilege(PrivSDNAudit, srv.handleSDNDnsResolve))
}

// dnsRecordValueError returns a PVE/PowerDNS-style rejection string if r is
// malformed, or "" if acceptable.
func dnsRecordValueError(r SDNDnsRecordSpec) string {
	if strings.TrimSpace(r.Name) == "" {
		return "Parameter verification failed. - name: record name is required"
	}
	if !dnsNamePattern.MatchString(r.Name) {
		return fmt.Sprintf("Parameter verification failed. - name: value '%s' is not a valid record name", r.Name)
	}
	if !dnsRecordTypes[r.Type] {
		return fmt.Sprintf("Parameter verification failed. - type: unknown record type '%s'", r.Type)
	}
	if strings.TrimSpace(r.Value) == "" {
		return "Parameter verification failed. - value: record value is required"
	}
	switch r.Type {
	case "A":
		if ip := net.ParseIP(r.Value); ip == nil || ip.To4() == nil {
			return fmt.Sprintf("Parameter verification failed. - value: '%s' is not a valid IPv4 address for an A record", r.Value)
		}
	case "AAAA":
		if ip := net.ParseIP(r.Value); ip == nil || ip.To4() != nil {
			return fmt.Sprintf("Parameter verification failed. - value: '%s' is not a valid IPv6 address for an AAAA record", r.Value)
		}
	case "PTR":
		if ip := net.ParseIP(r.Value); ip == nil {
			return fmt.Sprintf("Parameter verification failed. - value: '%s' is not a valid IP address for a PTR record", r.Value)
		}
	}
	if r.TTL < 0 {
		return "Parameter verification failed. - ttl: ttl must not be negative"
	}
	return ""
}

func (srv *Server) handleSDNDnsZonesList(w http.ResponseWriter, r *http.Request) {
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	// T-2502-followup-01: dnsZones is a map, whose iteration order is
	// randomized; sort by ID (the map's own key) so the response is
	// deterministic.
	out := make([]SDNDnsZoneSpec, 0, len(srv.state.sdn.dnsZones))
	for _, id := range sortedKeys(srv.state.sdn.dnsZones) {
		out = append(out, srv.state.sdn.dnsZones[id])
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleSDNDnsZoneGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	z, ok := srv.state.sdn.dnsZones[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dns zone %q not found", id))
		return
	}
	writeData(w, http.StatusOK, z)
}

func (srv *Server) handleSDNDnsZoneCreate(w http.ResponseWriter, r *http.Request) {
	var z SDNDnsZoneSpec
	if err := decodeRequest(r, &z); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if z.ID == "" {
		writeError(w, http.StatusBadRequest, "dns zone id (\"zone\") is required")
		return
	}
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, exists := srv.state.sdn.dnsZones[z.ID]; exists {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("dns zone %q already exists", z.ID))
		return
	}
	srv.state.sdn.dnsZones[z.ID] = z
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNDnsZoneUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	var z SDNDnsZoneSpec
	if err := decodeRequest(r, &z); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	existing, ok := srv.state.sdn.dnsZones[id]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dns zone %q not found", id))
		return
	}
	// Preserve the record set — a zone (plugin) config update doesn't touch
	// the records already registered in PowerDNS.
	z.ID = id
	z.Records = existing.Records
	srv.state.sdn.dnsZones[id] = z
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNDnsZoneDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "zone")
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	if _, ok := srv.state.sdn.dnsZones[id]; !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dns zone %q not found", id))
		return
	}
	delete(srv.state.sdn.dnsZones, id)
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNDnsRecordsList(w http.ResponseWriter, r *http.Request) {
	zone := chi.URLParam(r, "zone")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	z, ok := srv.state.sdn.dnsZones[zone]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dns zone %q not found", zone))
		return
	}
	writeData(w, http.StatusOK, append([]SDNDnsRecordSpec(nil), z.Records...))
}

func (srv *Server) handleSDNDnsResolve(w http.ResponseWriter, r *http.Request) {
	zone := chi.URLParam(r, "zone")
	srv.state.sdn.mu.RLock()
	defer srv.state.sdn.mu.RUnlock()
	z, ok := srv.state.sdn.dnsZones[zone]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dns zone %q not found", zone))
		return
	}
	if z.Unreachable {
		// The PowerDNS server backing this zone is unreachable — the live
		// resolve read fails even though config-truth still knows the zone.
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("powerdns server for zone %q is unreachable", zone))
		return
	}
	writeData(w, http.StatusOK, append([]SDNDnsRecordSpec(nil), z.Records...))
}

func (srv *Server) handleSDNDnsRecordCreate(w http.ResponseWriter, r *http.Request) {
	zone := chi.URLParam(r, "zone")
	var rec SDNDnsRecordSpec
	if err := decodeRequest(r, &rec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if msg := dnsRecordValueError(rec); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	z, ok := srv.state.sdn.dnsZones[zone]
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("dns zone %q does not exist", zone))
		return
	}
	for _, existing := range z.Records {
		if existing.Name == rec.Name && existing.Type == rec.Type {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("record %s/%s already exists in zone %q", rec.Name, rec.Type, zone))
			return
		}
	}
	z.Records = append(append([]SDNDnsRecordSpec(nil), z.Records...), rec)
	srv.state.sdn.dnsZones[zone] = z
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNDnsRecordUpdate(w http.ResponseWriter, r *http.Request) {
	zone := chi.URLParam(r, "zone")
	name := chi.URLParam(r, "name")
	typ := chi.URLParam(r, "type")
	var rec SDNDnsRecordSpec
	if err := decodeRequest(r, &rec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rec.Name, rec.Type = name, typ
	if msg := dnsRecordValueError(rec); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	z, ok := srv.state.sdn.dnsZones[zone]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dns zone %q not found", zone))
		return
	}
	recs := append([]SDNDnsRecordSpec(nil), z.Records...)
	found := false
	for i := range recs {
		if recs[i].Name == name && recs[i].Type == typ {
			recs[i] = rec
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("record %s/%s not found in zone %q", name, typ, zone))
		return
	}
	z.Records = recs
	srv.state.sdn.dnsZones[zone] = z
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleSDNDnsRecordDelete(w http.ResponseWriter, r *http.Request) {
	zone := chi.URLParam(r, "zone")
	name := chi.URLParam(r, "name")
	typ := chi.URLParam(r, "type")
	srv.state.sdn.mu.Lock()
	defer srv.state.sdn.mu.Unlock()
	z, ok := srv.state.sdn.dnsZones[zone]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("dns zone %q not found", zone))
		return
	}
	kept := make([]SDNDnsRecordSpec, 0, len(z.Records))
	found := false
	for _, rec := range z.Records {
		if rec.Name == name && rec.Type == typ {
			found = true
			continue
		}
		kept = append(kept, rec)
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("record %s/%s not found in zone %q", name, typ, zone))
		return
	}
	z.Records = kept
	srv.state.sdn.dnsZones[zone] = z
	writeData(w, http.StatusOK, nil)
}
