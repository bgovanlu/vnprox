package pvemock

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// scopeCtx bundles a locked *FirewallScope with the function that must be
// called to release whatever lock was taken to reach it (State.clusterFW's
// own mutex for cluster scope, a node's nodeState mutex for node/guest
// scope — guest rulesets live inside the owning node's guest map).
type scopeCtx struct {
	scope  *FirewallScope
	unlock func()
}

// scopeGetter resolves the firewall scope named by a request's URL params,
// taking a read or write lock as requested.
type scopeGetter func(r *http.Request, write bool) (*scopeCtx, error)

func (srv *Server) clusterScope(_ *http.Request, write bool) (*scopeCtx, error) {
	if write {
		srv.state.clusterFWMu.Lock()
		return &scopeCtx{scope: &srv.state.clusterFW, unlock: srv.state.clusterFWMu.Unlock}, nil
	}
	srv.state.clusterFWMu.RLock()
	return &scopeCtx{scope: &srv.state.clusterFW, unlock: srv.state.clusterFWMu.RUnlock}, nil
}

func (srv *Server) nodeScope(r *http.Request, write bool) (*scopeCtx, error) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		return nil, fmt.Errorf("%w: node %q", ErrNotFound, node)
	}
	if write {
		ns.mu.Lock()
		return &scopeCtx{scope: &ns.firewall, unlock: ns.mu.Unlock}, nil
	}
	ns.mu.RLock()
	return &scopeCtx{scope: &ns.firewall, unlock: ns.mu.RUnlock}, nil
}

// guestScope returns a scopeGetter bound to this Server for the given guest
// kind ("qemu" or "lxc"), reading {node} and {vmid} from the request's chi
// URL params.
func (srv *Server) guestScope(kind string) scopeGetter {
	return func(r *http.Request, write bool) (*scopeCtx, error) {
		node := chi.URLParam(r, "node")
		vmid := chi.URLParam(r, "vmid")
		return srv.resolveGuestScope(kind, node, vmid, write)
	}
}

func (srv *Server) resolveGuestScope(kind, node, vmid string, write bool) (*scopeCtx, error) {
	ns, ok := srv.state.node(node)
	if !ok {
		return nil, fmt.Errorf("%w: node %q", ErrNotFound, node)
	}
	if write {
		ns.mu.Lock()
	} else {
		ns.mu.RLock()
	}
	g, ok := srv.guestMap(ns, kind)[vmid]
	if !ok {
		if write {
			ns.mu.Unlock()
		} else {
			ns.mu.RUnlock()
		}
		return nil, fmt.Errorf("%w: %s %s on node %q", ErrNotFound, kind, vmid, node)
	}
	if g.Firewall == nil {
		g.Firewall = &FirewallScope{}
	}
	unlock := ns.mu.RUnlock
	if write {
		unlock = ns.mu.Unlock
	}
	return &scopeCtx{scope: g.Firewall, unlock: unlock}, nil
}

func (srv *Server) mountFirewall(api chi.Router) {
	srv.mountFirewallScope(api, "/cluster/firewall", PrivSysAudit, PrivSysModify, srv.clusterScope)
	srv.mountFirewallScope(api, "/nodes/{node}/firewall", PrivSysAudit, PrivSysModify, srv.nodeScope)
	srv.mountFirewallScope(api, "/nodes/{node}/qemu/{vmid}/firewall", PrivVMAudit, PrivVMConfigNet, srv.guestScope("qemu"))
	srv.mountFirewallScope(api, "/nodes/{node}/lxc/{vmid}/firewall", PrivVMAudit, PrivVMConfigNet, srv.guestScope("lxc"))

	// T-502's post-apply verification (docs/features/firewall.md §3) needs
	// somewhere to read "did this node's firewall compile cleanly" — see
	// handleFirewallStatus's doc comment for why this route is a mock-only
	// extension, not a real PVE API endpoint.
	api.Get("/nodes/{node}/firewall/status", srv.requirePrivilege(PrivSysAudit, srv.handleFirewallStatus))

	// Security groups are a cluster-scope concept in real PVE (reusable
	// rule bundles referenced by rules at any scope), so they are mounted
	// once under /cluster/firewall/groups rather than per-scope.
	api.Get("/cluster/firewall/groups", srv.requirePrivilege(PrivSysAudit, srv.handleFwGroupsList))
	api.Post("/cluster/firewall/groups", srv.requirePrivilege(PrivSysModify, srv.handleFwGroupCreate))
	api.Get("/cluster/firewall/groups/{group}", srv.requirePrivilege(PrivSysAudit, srv.handleFwGroupRulesList))
	api.Delete("/cluster/firewall/groups/{group}", srv.requirePrivilege(PrivSysModify, srv.handleFwGroupDelete))
	api.Post("/cluster/firewall/groups/{group}", srv.requirePrivilege(PrivSysModify, srv.handleFwGroupRuleCreate))
	api.Put("/cluster/firewall/groups/{group}/{pos}", srv.requirePrivilege(PrivSysModify, srv.handleFwGroupRuleUpdate))
	api.Delete("/cluster/firewall/groups/{group}/{pos}", srv.requirePrivilege(PrivSysModify, srv.handleFwGroupRuleDelete))
}

// mountFirewallScope wires the rules/options/aliases/ipsets CRUD common to
// all three PVE firewall scopes (cluster/node/guest) at prefix.
func (srv *Server) mountFirewallScope(api chi.Router, prefix, readPriv, writePriv string, get scopeGetter) {
	api.Get(prefix+"/rules", srv.requirePrivilege(readPriv, srv.handleFwRulesList(get)))
	api.Post(prefix+"/rules", srv.requirePrivilege(writePriv, srv.handleFwRuleCreate(get)))
	api.Get(prefix+"/rules/{pos}", srv.requirePrivilege(readPriv, srv.handleFwRuleGet(get)))
	api.Put(prefix+"/rules/{pos}", srv.requirePrivilege(writePriv, srv.handleFwRuleUpdate(get)))
	api.Delete(prefix+"/rules/{pos}", srv.requirePrivilege(writePriv, srv.handleFwRuleDelete(get)))

	api.Get(prefix+"/options", srv.requirePrivilege(readPriv, srv.handleFwOptionsGet(get)))
	api.Put(prefix+"/options", srv.requirePrivilege(writePriv, srv.handleFwOptionsPut(get)))

	api.Get(prefix+"/aliases", srv.requirePrivilege(readPriv, srv.handleFwAliasesList(get)))
	api.Post(prefix+"/aliases", srv.requirePrivilege(writePriv, srv.handleFwAliasCreate(get)))
	api.Get(prefix+"/aliases/{name}", srv.requirePrivilege(readPriv, srv.handleFwAliasGet(get)))
	api.Put(prefix+"/aliases/{name}", srv.requirePrivilege(writePriv, srv.handleFwAliasUpdate(get)))
	api.Delete(prefix+"/aliases/{name}", srv.requirePrivilege(writePriv, srv.handleFwAliasDelete(get)))

	api.Get(prefix+"/ipset", srv.requirePrivilege(readPriv, srv.handleFwIPSetsList(get)))
	api.Post(prefix+"/ipset", srv.requirePrivilege(writePriv, srv.handleFwIPSetCreate(get)))
	api.Get(prefix+"/ipset/{name}", srv.requirePrivilege(readPriv, srv.handleFwIPSetEntriesList(get)))
	api.Put(prefix+"/ipset/{name}", srv.requirePrivilege(writePriv, srv.handleFwIPSetUpdate(get)))
	api.Delete(prefix+"/ipset/{name}", srv.requirePrivilege(writePriv, srv.handleFwIPSetDelete(get)))
	api.Post(prefix+"/ipset/{name}", srv.requirePrivilege(writePriv, srv.handleFwIPSetEntryCreate(get)))
	api.Delete(prefix+"/ipset/{name}/{cidr}", srv.requirePrivilege(writePriv, srv.handleFwIPSetEntryDelete(get)))
}

// handleFirewallStatus reports node's pve-firewall compile status (T-502's
// post-apply verification, docs/features/firewall.md §3: "vnprox verifies
// post-apply that the compiled status reports no errors and surfaces
// pve-firewall status per node"). This is a mock-only extension: real PVE
// has no REST endpoint exposing pve-firewall's own compile-loop result —
// an administrator (or vnprox) checks it via `pve-firewall status`
// locally on the node. A real vnproxd implementation would read this the
// same way it reads LLDP/netlink (internal/host, root-level, proxied
// through the peer API for a non-local node), not through the PVE API;
// this mock endpoint stands in for that so the change engine's
// verification step has something to call in tests. Flagged in T-502's
// report as needing hardware validation / a real internal/host reader.
func (srv *Server) handleFirewallStatus(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	ns.mu.RLock()
	fail := ns.mock.FirewallCompileFail
	ns.mu.RUnlock()
	status := struct {
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	}{Status: "ok"}
	if fail {
		status.Status = "error"
		status.Message = "pve-firewall: syntax error in ruleset (mock-injected failure)"
	}
	writeData(w, http.StatusOK, status)
}

// --- rules -----------------------------------------------------------------

func (srv *Server) handleFwRulesList(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, false)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		writeData(w, http.StatusOK, sc.scope.Rules)
	}
}

func (srv *Server) handleFwRuleGet(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, false)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		pos := atoiOr(chi.URLParam(r, "pos"), -1)
		for _, rule := range sc.scope.Rules {
			if rule.Pos == pos {
				writeData(w, http.StatusOK, rule)
				return
			}
		}
		writeError(w, http.StatusNotFound, fmt.Sprintf("rule at pos %d not found", pos))
	}
}

func (srv *Server) handleFwRuleCreate(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var rule FwRuleSpec
		if err := decodeRequest(r, &rule); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		rule.Pos = len(sc.scope.Rules)
		sc.scope.Rules = append(sc.scope.Rules, rule)
		writeData(w, http.StatusOK, nil)
	}
}

// fwRuleUpdateBody is the PUT rules/{pos} body: the rule's full field
// content (mock's update semantics are a full replace, not a patch — see
// handleFwRuleUpdate's pre-T-502 doc history) plus real PVE's own
// "moveto" param, which relocates the rule to a new position in the same
// call (docs/features/firewall.md §2: "reorders are fw.rule.move ops" —
// T-502's op executor sends the rule's own unchanged fields alongside
// moveto for a pure move, and moveto omitted for a pure field update).
type fwRuleUpdateBody struct {
	Moveto *int `json:"moveto,omitempty"`
	FwRuleSpec
}

func (srv *Server) handleFwRuleUpdate(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body fwRuleUpdateBody
		if err := decodeRequest(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		pos := atoiOr(chi.URLParam(r, "pos"), -1)
		found := false
		for i, existing := range sc.scope.Rules {
			if existing.Pos == pos {
				updated := body.FwRuleSpec
				updated.Pos = pos
				sc.scope.Rules[i] = updated
				found = true
				break
			}
		}
		if !found {
			writeError(w, http.StatusNotFound, fmt.Sprintf("rule at pos %d not found", pos))
			return
		}
		if body.Moveto != nil {
			sc.scope.Rules = moveFwRule(sc.scope.Rules, pos, *body.Moveto)
		}
		writeData(w, http.StatusOK, nil)
	}
}

// moveFwRule relocates the rule currently at fromPos to destPos (clamped
// to the valid [0,len-1] range after removal), renumbering every
// remaining rule's Pos to stay contiguous — mirrors real pve-firewall's
// own PUT .../rules/{pos} "moveto" semantics.
func moveFwRule(rules []FwRuleSpec, fromPos, destPos int) []FwRuleSpec {
	var moved FwRuleSpec
	rest := make([]FwRuleSpec, 0, len(rules))
	for _, ru := range rules {
		if ru.Pos == fromPos {
			moved = ru
			continue
		}
		rest = append(rest, ru)
	}
	if destPos < 0 {
		destPos = 0
	}
	if destPos > len(rest) {
		destPos = len(rest)
	}
	out := make([]FwRuleSpec, 0, len(rest)+1)
	out = append(out, rest[:destPos]...)
	out = append(out, moved)
	out = append(out, rest[destPos:]...)
	for i := range out {
		out[i].Pos = i
	}
	return out
}

func (srv *Server) handleFwRuleDelete(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		pos := atoiOr(chi.URLParam(r, "pos"), -1)
		out := sc.scope.Rules[:0]
		found := false
		for _, rule := range sc.scope.Rules {
			if rule.Pos == pos {
				found = true
				continue
			}
			out = append(out, rule)
		}
		if !found {
			writeError(w, http.StatusNotFound, fmt.Sprintf("rule at pos %d not found", pos))
			return
		}
		// Renumber remaining rules to keep pos contiguous, matching PVE's
		// own behavior on delete.
		for i := range out {
			out[i].Pos = i
		}
		sc.scope.Rules = out
		writeData(w, http.StatusOK, nil)
	}
}

// --- options -----------------------------------------------------------------

type fwOptions struct {
	PolicyIn  string `json:"policy_in,omitempty"`
	PolicyOut string `json:"policy_out,omitempty"`
	Enable    bool   `json:"enable"`
}

func (srv *Server) handleFwOptionsGet(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, false)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		writeData(w, http.StatusOK, fwOptions{Enable: sc.scope.Enabled, PolicyIn: sc.scope.PolicyIn, PolicyOut: sc.scope.PolicyOut})
	}
}

func (srv *Server) handleFwOptionsPut(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := decodeRequest(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		if v, ok := body["enable"]; ok {
			sc.scope.Enabled = v == "1" || v == "true"
		}
		if v, ok := body["policy_in"]; ok {
			sc.scope.PolicyIn = v
		}
		if v, ok := body["policy_out"]; ok {
			sc.scope.PolicyOut = v
		}
		writeData(w, http.StatusOK, nil)
	}
}

// --- aliases -----------------------------------------------------------------

func (srv *Server) handleFwAliasesList(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, false)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		writeData(w, http.StatusOK, sc.scope.Aliases)
	}
}

func (srv *Server) handleFwAliasGet(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, false)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		name := chi.URLParam(r, "name")
		for _, a := range sc.scope.Aliases {
			if a.Name == name {
				writeData(w, http.StatusOK, a)
				return
			}
		}
		writeError(w, http.StatusNotFound, fmt.Sprintf("alias %q not found", name))
	}
}

func (srv *Server) handleFwAliasCreate(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var alias FwAliasSpec
		if err := decodeRequest(r, &alias); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		for _, a := range sc.scope.Aliases {
			if a.Name == alias.Name {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("alias %q already exists", alias.Name))
				return
			}
		}
		sc.scope.Aliases = append(sc.scope.Aliases, alias)
		writeData(w, http.StatusOK, nil)
	}
}

func (srv *Server) handleFwAliasUpdate(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var alias FwAliasSpec
		if err := decodeRequest(r, &alias); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		name := chi.URLParam(r, "name")
		alias.Name = name
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		for i, a := range sc.scope.Aliases {
			if a.Name == name {
				sc.scope.Aliases[i] = alias
				writeData(w, http.StatusOK, nil)
				return
			}
		}
		writeError(w, http.StatusNotFound, fmt.Sprintf("alias %q not found", name))
	}
}

func (srv *Server) handleFwAliasDelete(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		name := chi.URLParam(r, "name")
		out := sc.scope.Aliases[:0]
		found := false
		for _, a := range sc.scope.Aliases {
			if a.Name == name {
				found = true
				continue
			}
			out = append(out, a)
		}
		if !found {
			writeError(w, http.StatusNotFound, fmt.Sprintf("alias %q not found", name))
			return
		}
		sc.scope.Aliases = out
		writeData(w, http.StatusOK, nil)
	}
}

// --- ipsets ------------------------------------------------------------------

// handleFwIPSetUpdate updates an ipset's comment (real PVE's PUT
// .../ipset/{name} also supports renaming via a "rename" param; T-502's
// fw.ipset.update op never renames — Name is the op's own identity field,
// not editable, matching fw.alias.update's same convention — so this mock
// endpoint only ever touches Comment).
func (srv *Server) handleFwIPSetUpdate(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Comment string `json:"comment"`
		}
		if err := decodeRequest(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		name := chi.URLParam(r, "name")
		for i, s := range sc.scope.IPSets {
			if s.Name == name {
				sc.scope.IPSets[i].Comment = body.Comment
				writeData(w, http.StatusOK, nil)
				return
			}
		}
		writeError(w, http.StatusNotFound, fmt.Sprintf("ipset %q not found", name))
	}
}

func (srv *Server) handleFwIPSetsList(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, false)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		type ipsetSummary struct {
			Name    string `json:"name"`
			Comment string `json:"comment,omitempty"`
		}
		out := make([]ipsetSummary, 0, len(sc.scope.IPSets))
		for _, s := range sc.scope.IPSets {
			out = append(out, ipsetSummary{Name: s.Name, Comment: s.Comment})
		}
		writeData(w, http.StatusOK, out)
	}
}

func (srv *Server) handleFwIPSetCreate(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var set FwIPSetSpec
		if err := decodeRequest(r, &set); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		for _, s := range sc.scope.IPSets {
			if s.Name == set.Name {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("ipset %q already exists", set.Name))
				return
			}
		}
		sc.scope.IPSets = append(sc.scope.IPSets, set)
		writeData(w, http.StatusOK, nil)
	}
}

func (srv *Server) handleFwIPSetDelete(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		name := chi.URLParam(r, "name")
		out := sc.scope.IPSets[:0]
		found := false
		for _, s := range sc.scope.IPSets {
			if s.Name == name {
				found = true
				continue
			}
			out = append(out, s)
		}
		if !found {
			writeError(w, http.StatusNotFound, fmt.Sprintf("ipset %q not found", name))
			return
		}
		sc.scope.IPSets = out
		writeData(w, http.StatusOK, nil)
	}
}

func (srv *Server) handleFwIPSetEntriesList(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, false)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		name := chi.URLParam(r, "name")
		for _, s := range sc.scope.IPSets {
			if s.Name == name {
				writeData(w, http.StatusOK, s.Entries)
				return
			}
		}
		writeError(w, http.StatusNotFound, fmt.Sprintf("ipset %q not found", name))
	}
}

func (srv *Server) handleFwIPSetEntryCreate(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var entry FwIPSetEntry
		if err := decodeRequest(r, &entry); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		name := chi.URLParam(r, "name")
		for i, s := range sc.scope.IPSets {
			if s.Name == name {
				sc.scope.IPSets[i].Entries = append(sc.scope.IPSets[i].Entries, entry)
				writeData(w, http.StatusOK, nil)
				return
			}
		}
		writeError(w, http.StatusNotFound, fmt.Sprintf("ipset %q not found", name))
	}
}

func (srv *Server) handleFwIPSetEntryDelete(get scopeGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, err := get(r, true)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer sc.unlock()
		name := chi.URLParam(r, "name")
		cidr := chi.URLParam(r, "cidr")
		for i, s := range sc.scope.IPSets {
			if s.Name != name {
				continue
			}
			out := s.Entries[:0]
			found := false
			for _, e := range s.Entries {
				if e.CIDR == cidr {
					found = true
					continue
				}
				out = append(out, e)
			}
			if !found {
				writeError(w, http.StatusNotFound, fmt.Sprintf("entry %q not found in ipset %q", cidr, name))
				return
			}
			sc.scope.IPSets[i].Entries = out
			writeData(w, http.StatusOK, nil)
			return
		}
		writeError(w, http.StatusNotFound, fmt.Sprintf("ipset %q not found", name))
	}
}

// --- security groups (cluster-scope only) ------------------------------------

func (srv *Server) handleFwGroupsList(w http.ResponseWriter, r *http.Request) {
	srv.state.clusterFWMu.RLock()
	defer srv.state.clusterFWMu.RUnlock()
	type groupSummary struct {
		Name    string `json:"group"`
		Comment string `json:"comment,omitempty"`
	}
	out := make([]groupSummary, 0, len(srv.state.clusterFW.Groups))
	for _, g := range srv.state.clusterFW.Groups {
		out = append(out, groupSummary{Name: g.Name, Comment: g.Comment})
	}
	writeData(w, http.StatusOK, out)
}

func (srv *Server) handleFwGroupCreate(w http.ResponseWriter, r *http.Request) {
	var group FwGroupSpec
	if err := decodeRequest(r, &group); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv.state.clusterFWMu.Lock()
	defer srv.state.clusterFWMu.Unlock()
	for _, g := range srv.state.clusterFW.Groups {
		if g.Name == group.Name {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("group %q already exists", group.Name))
			return
		}
	}
	srv.state.clusterFW.Groups = append(srv.state.clusterFW.Groups, group)
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleFwGroupDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "group")
	srv.state.clusterFWMu.Lock()
	defer srv.state.clusterFWMu.Unlock()
	out := srv.state.clusterFW.Groups[:0]
	found := false
	for _, g := range srv.state.clusterFW.Groups {
		if g.Name == name {
			found = true
			continue
		}
		out = append(out, g)
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("group %q not found", name))
		return
	}
	srv.state.clusterFW.Groups = out
	writeData(w, http.StatusOK, nil)
}

func (srv *Server) handleFwGroupRulesList(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "group")
	srv.state.clusterFWMu.RLock()
	defer srv.state.clusterFWMu.RUnlock()
	for _, g := range srv.state.clusterFW.Groups {
		if g.Name == name {
			writeData(w, http.StatusOK, g.Rules)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("group %q not found", name))
}

func (srv *Server) handleFwGroupRuleCreate(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "group")
	var rule FwRuleSpec
	if err := decodeRequest(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv.state.clusterFWMu.Lock()
	defer srv.state.clusterFWMu.Unlock()
	for i, g := range srv.state.clusterFW.Groups {
		if g.Name == name {
			rule.Pos = len(g.Rules)
			srv.state.clusterFW.Groups[i].Rules = append(srv.state.clusterFW.Groups[i].Rules, rule)
			writeData(w, http.StatusOK, nil)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("group %q not found", name))
}

func (srv *Server) handleFwGroupRuleUpdate(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "group")
	pos := atoiOr(chi.URLParam(r, "pos"), -1)
	var rule FwRuleSpec
	if err := decodeRequest(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv.state.clusterFWMu.Lock()
	defer srv.state.clusterFWMu.Unlock()
	for gi, g := range srv.state.clusterFW.Groups {
		if g.Name != name {
			continue
		}
		for ri, existing := range g.Rules {
			if existing.Pos == pos {
				rule.Pos = pos
				srv.state.clusterFW.Groups[gi].Rules[ri] = rule
				writeData(w, http.StatusOK, nil)
				return
			}
		}
		writeError(w, http.StatusNotFound, fmt.Sprintf("rule at pos %d not found in group %q", pos, name))
		return
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("group %q not found", name))
}

func (srv *Server) handleFwGroupRuleDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "group")
	pos := atoiOr(chi.URLParam(r, "pos"), -1)
	srv.state.clusterFWMu.Lock()
	defer srv.state.clusterFWMu.Unlock()
	for gi, g := range srv.state.clusterFW.Groups {
		if g.Name != name {
			continue
		}
		out := g.Rules[:0]
		found := false
		for _, existing := range g.Rules {
			if existing.Pos == pos {
				found = true
				continue
			}
			out = append(out, existing)
		}
		if !found {
			writeError(w, http.StatusNotFound, fmt.Sprintf("rule at pos %d not found in group %q", pos, name))
			return
		}
		for i := range out {
			out[i].Pos = i
		}
		srv.state.clusterFW.Groups[gi].Rules = out
		writeData(w, http.StatusOK, nil)
		return
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("group %q not found", name))
}
