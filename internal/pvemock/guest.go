package pvemock

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// probeExecRequest is the request body of `POST .../agent/exec`: PVE's
// guest-agent exec accepts a `command` field, either a single string or an
// argv array — internal/pve.Client.AgentExec always sends the array form
// (see that method's doc comment), which is all this mock parses.
type probeExecRequest struct {
	Command []string `json:"command"`
}

// parseProbeCommand recovers (proto,dstIP,port) from an argv this mock
// received at `POST .../agent/exec`. It intentionally understands only the
// exact two shapes internal/probe's buildCommand emits (see that function's
// doc comment for why a general-purpose command emulator is out of scope):
//
//	["ping", "-c", "1", "-W", "<secs>", "<ip>"]
//	["nc", "-z", "-w", "<secs>", "<ip>", "<port>"]
//
// Any other argv (a real command a hand-written client might send) is
// reported unrecognized rather than guessed at.
func parseProbeCommand(cmd []string) (proto, dstIP string, port int, ok bool) {
	if len(cmd) < 2 {
		return "", "", 0, false
	}
	switch cmd[0] {
	case "ping":
		return "icmp", cmd[len(cmd)-1], 0, true
	case "nc":
		if len(cmd) < 3 {
			return "", "", 0, false
		}
		p, err := strconv.Atoi(cmd[len(cmd)-1])
		if err != nil {
			return "", "", 0, false
		}
		return "tcp", cmd[len(cmd)-2], p, true
	default:
		return "", "", 0, false
	}
}

// matchAgentExecOutcome finds g's scripted outcome for (proto,dstIP,port),
// or the honest "no scripted outcome" error fallback documented on
// GuestSpec.AgentExecOutcomes.
func (g *GuestSpec) matchAgentExecOutcome(proto, dstIP string, port int) (outcome, detail string) {
	for _, o := range g.AgentExecOutcomes {
		if o.Proto == proto && o.DstIP == dstIP && o.Port == port {
			return o.Outcome, o.Detail
		}
	}
	return "error", fmt.Sprintf("pvemock: no agent_exec_outcomes entry declared for proto=%s dst=%s port=%d", proto, dstIP, port)
}

// outcomeToExecResult synthesizes the exec-status result classify (T-802's
// internal/probe.classify) needs to reclassify back to outcome — this mock
// never runs a real command, so it must speak the same exit-code/output
// contract in reverse. exit code 0 = reachable (matches ping/nc's own
// success code), 1 = unreachable (matches ping's "no reply"/nc's generic
// failure code — "refused" is additionally sniffed from output text, hence
// detail is passed through verbatim for exit code 1), anything else =
// error. "timeout" never exits at all — see execResult's doc comment.
func outcomeToExecResult(outcome, detail string) execResult {
	switch outcome {
	case "reachable":
		return execResult{exited: true, exitCode: 0, outData: detail}
	case "unreachable":
		return execResult{exited: true, exitCode: 1, outData: detail, errData: detail}
	case "timeout":
		return execResult{exited: false}
	default: // "error" or any unrecognized value
		return execResult{exited: true, exitCode: 2, errData: detail}
	}
}

// guestConfigNumericFields lists guest-config keys hardware validation
// (T-608) found real PVE returns as JSON numbers rather than strings —
// mirroring internal/pve's stringifyConfigValue, the client-side half of
// this same fix. This is a best-effort subset, not an authoritative or
// exhaustive contract: real PVE's per-field typing is genuinely
// inconsistent (e.g. "memory" was observed as a string for qemu guests but
// an int for lxc containers on the very same real node) and isn't
// documented anywhere. The point is exercising the client's type-agnostic
// conversion via tests, not replicating PVE's exact rules field-for-field.
var guestConfigNumericFields = map[string]bool{
	"cores": true, "sockets": true, "numa": true, "swap": true,
	"onboot": true, "unprivileged": true, "vcpus": true,
	"cpulimit": true, "cpuunits": true, "balloon": true,
}

// marshalGuestConfig converts cfg's fixture-declared strings into the mixed
// string/number shape guestConfigNumericFields documents, so the JSON this
// endpoint serves isn't uniformly all-strings (see that var's doc comment).
func marshalGuestConfig(cfg map[string]string) map[string]any {
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if guestConfigNumericFields[k] {
			if n, err := strconv.Atoi(v); err == nil {
				out[k] = n
				continue
			}
		}
		out[k] = v
	}
	return out
}

func (srv *Server) guestMap(ns *nodeState, kind string) map[string]*GuestSpec {
	if kind == "lxc" {
		return ns.lxc
	}
	return ns.qemu
}

// handleGuestConfigGet returns a closure implementing
// `GET /nodes/{node}/{kind}/{vmid}/config`, where kind is "qemu" or "lxc".
func (srv *Server) handleGuestConfigGet(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := chi.URLParam(r, "node")
		vmid := chi.URLParam(r, "vmid")
		ns, ok := srv.state.node(node)
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
			return
		}
		ns.mu.RLock()
		defer ns.mu.RUnlock()
		g, ok := srv.guestMap(ns, kind)[vmid]
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("%s %s not found on node %q", kind, vmid, node))
			return
		}
		writeData(w, http.StatusOK, marshalGuestConfig(g.Config))
	}
}

// handleGuestConfigPut returns a closure implementing
// `PUT /nodes/{node}/{kind}/{vmid}/config`. Matching real PVE semantics: the
// body's keys are merged into the existing config (not a full replace), and
// a `delete` field/query param (comma-separated key list) removes keys.
func (srv *Server) handleGuestConfigPut(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := chi.URLParam(r, "node")
		vmid := chi.URLParam(r, "vmid")
		ns, ok := srv.state.node(node)
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
			return
		}

		var body map[string]string
		if err := decodeRequest(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		ns.mu.Lock()
		defer ns.mu.Unlock()
		g, ok := srv.guestMap(ns, kind)[vmid]
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("%s %s not found on node %q", kind, vmid, node))
			return
		}
		if g.Config == nil {
			g.Config = map[string]string{}
		}
		var deleteKeys string
		for k, v := range body {
			if k == "delete" {
				deleteKeys = v
				continue
			}
			g.Config[k] = v
		}
		for _, k := range strings.Split(deleteKeys, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				delete(g.Config, k)
			}
		}
		writeData(w, http.StatusOK, nil)
	}
}

// agentInterfacesResult wraps AgentIfaceSpec rows in real PVE's
// {"result": [...]} envelope: the qemu guest agent's
// network-get-interfaces QMP command shape, one level inside the usual
// {"data": ...} PVE API envelope (T-405's guest-agent-reported-IP
// enrichment source, docs/features/ipam.md §1).
type agentInterfacesResult struct {
	Result []AgentIfaceSpec `json:"result"`
}

// handleGuestAgentInterfaces implements
// `GET /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces`. Only qemu
// guests carry a QEMU guest agent (real PVE has no lxc equivalent of this
// route — a container's interfaces are read directly from its netns), so
// this is mounted for "qemu" only.
func (srv *Server) handleGuestAgentInterfaces(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	vmid := chi.URLParam(r, "vmid")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	g, ok := ns.qemu[vmid]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("qemu %s not found on node %q", vmid, node))
		return
	}
	writeData(w, http.StatusOK, agentInterfacesResult{Result: g.AgentInterfaces})
}

// handleGuestAgentExec implements `POST /nodes/{node}/qemu/{vmid}/agent/exec`
// (T-802): starts a scripted probe run against the guest's
// AgentExecOutcomes fixture table (or, if AgentUnreachable is set, fails the
// same way real PVE does for a guest whose QEMU guest agent isn't running —
// docs/features/firewall.md §5's "verify live" honesty contract needs this
// exact failure mode distinguishable from a normal reachable/unreachable
// result). This mock never runs a real command — see this file's
// parseProbeCommand/outcomeToExecResult doc comments. qemu-only, same
// precedent as handleGuestAgentInterfaces above.
func (srv *Server) handleGuestAgentExec(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	vmid := chi.URLParam(r, "vmid")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	ns.mu.RLock()
	g, ok := ns.qemu[vmid]
	ns.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("qemu %s not found on node %q", vmid, node))
		return
	}
	if g.AgentUnreachable {
		writeError(w, http.StatusInternalServerError, "QEMU guest agent is not running")
		return
	}

	var body probeExecRequest
	if err := decodeRequest(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	proto, dstIP, port, ok := parseProbeCommand(body.Command)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("pvemock: unrecognized probe command %v (see internal/probe.buildCommand)", body.Command))
		return
	}

	outcome, detail := g.matchAgentExecOutcome(proto, dstIP, port)
	pid := srv.state.nextExecPID()
	srv.state.storeExecResult(pid, outcomeToExecResult(outcome, detail))
	writeData(w, http.StatusOK, map[string]int{"pid": pid})
}

// handleGuestAgentPing implements `POST /nodes/{node}/qemu/{vmid}/agent/ping`
// (T-806): the QEMU guest agent's transport-level "guest-ping" liveness
// check, backing the "Verify live" button's eligibility gate
// (GET /simulate/verify/eligibility). Fails the same way
// handleGuestAgentExec does when AgentUnreachable is set; otherwise always
// succeeds — deliberately independent of AgentInterfaces/AgentExecOutcomes
// (a guest can have a perfectly reachable agent with no declared
// interfaces, e.g. sim-lab's vm-a, so this must not reuse
// handleGuestAgentInterfaces' "empty result = no agent" convention, which
// answers a different question). qemu-only, same precedent as
// handleGuestAgentInterfaces/handleGuestAgentExec above.
func (srv *Server) handleGuestAgentPing(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	vmid := chi.URLParam(r, "vmid")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	ns.mu.RLock()
	g, ok := ns.qemu[vmid]
	ns.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("qemu %s not found on node %q", vmid, node))
		return
	}
	if g.AgentUnreachable {
		writeError(w, http.StatusInternalServerError, "QEMU guest agent is not running")
		return
	}
	writeData(w, http.StatusOK, map[string]any{})
}

// handleGuestAgentExecStatus implements
// `GET /nodes/{node}/qemu/{vmid}/agent/exec-status?pid=`: reads back the
// synthesized result handleGuestAgentExec stored for pid. The node/vmid
// path segments are validated (so a bogus ref 404s cleanly) but the exec
// table itself is keyed by pid alone — see State.execs' doc comment.
func (srv *Server) handleGuestAgentExecStatus(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	vmid := chi.URLParam(r, "vmid")
	ns, ok := srv.state.node(node)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("node %q not found", node))
		return
	}
	ns.mu.RLock()
	_, ok = ns.qemu[vmid]
	ns.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("qemu %s not found on node %q", vmid, node))
		return
	}

	pid := atoiOr(r.URL.Query().Get("pid"), -1)
	res, ok := srv.state.execResult(pid)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no exec run with pid %d", pid))
		return
	}
	writeData(w, http.StatusOK, map[string]any{
		"exited":   boolToInt(res.exited),
		"exitcode": res.exitCode,
		"out-data": res.outData,
		"err-data": res.errData,
	})
}
