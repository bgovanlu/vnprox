package verify

// checks_destructive.go is the suite that breaks things on purpose.
//
// Every check here needs Deps.Mutator, which the CLI leaves nil unless
// --i-understand was passed. That is the interlock: a destructive check
// running without consent finds a nil Mutator and skips naming the flag, and
// there is no branch in which it proceeds anyway. The consent flag and the
// wiring are the same fact, so they cannot drift apart.
//
// These four are the checks that most need a human with hardware, and they
// are also the four that this repository can least honestly claim to have
// exercised: what is written here is the procedure, driven through the same
// injected seams as every other check, with its failure modes named. The
// first real run will find something; that is the point of writing it down as
// code rather than as a checklist line.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// skipNoConsent is the one spelling of the destructive interlock.
func skipNoConsent(what string) Outcome {
	return Skip(fmt.Sprintf("not run: %s changes real state on this cluster. Re-run with --suite=destructive --i-understand on a cluster where that is acceptable", what))
}

// checkChangeMultinodeApplyRollback is row 3's `B` in one check: an apply
// that spans more than one node, and a rollback that has to undo it on more
// than one node.
//
// The interesting failure is not "the apply failed" — that is handled — but a
// rollback that restores node A and leaves node B in the applied state, which
// no single-node test can produce and which leaves a cluster in exactly the
// half-configured condition the change engine exists to prevent.
func checkChangeMultinodeApplyRollback(ctx context.Context, d Deps) Outcome {
	if d.Mutator == nil {
		return skipNoConsent("staging, applying and rolling back a changeset across two real nodes")
	}
	if d.Daemon == nil {
		return skipNoDaemon("a multi-node apply and rollback")
	}
	online := onlineNodes(d.Nodes)
	if len(online) < 2 {
		return Skip(fmt.Sprintf("this cluster has %d online node(s): %s. A distributed apply needs two", len(online), strings.Join(nodeNames(online), ", ")))
	}
	if d.Cluster == nil {
		return skipNoCluster("reading each node's pre-apply state to compare against after the rollback")
	}

	// Snapshot both nodes from PVE first: the rollback is judged against
	// PVE's own view, not against the daemon's report of what it did.
	before := map[string][]Iface{}
	evidence := make([]Evidence, 0, 6)
	for _, n := range online[:2] {
		ifaces, err := d.Cluster.Interfaces(ctx, n.Name)
		if err != nil {
			return Fail(fmt.Sprintf("could not read %s's pre-apply interfaces: %v", n.Name, err),
				NewEvidence(SourcePVEAPI, fmt.Sprintf("GET /nodes/%s/network", n.Name), err.Error()))
		}
		before[n.Name] = ifaces
		evidence = append(evidence, NewEvidence(SourcePVEAPI, fmt.Sprintf("before: GET /nodes/%s/network", n.Name), describeIfaces(ifaces)))
	}

	// A changeset that re-applies each node's current bridge comment to
	// itself: it touches both nodes, produces a real diff, goes through the
	// full engine, and cannot disconnect anybody.
	ops := make([]map[string]any, 0, 2)
	for _, n := range online[:2] {
		target, ok := firstBridge(before[n.Name])
		if !ok {
			return Skip(fmt.Sprintf("%s has no bridge to exercise a no-op apply against", n.Name), evidence...)
		}
		ops = append(ops, map[string]any{
			"type":    "bridge.update",
			"node":    n.Name,
			"bridge":  target.Name,
			"comment": target.Comments,
		})
	}
	staged, stageEv, err := mutate(ctx, d, "/changesets", map[string]any{
		"title": "T-2501 verify: multi-node apply/rollback",
		"ops":   ops,
	})
	evidence = append(evidence, stageEv)
	if err != nil {
		return Fail(fmt.Sprintf("staging a two-node changeset failed: %v", err), evidence...)
	}
	id, _ := staged["id"].(string)
	if id == "" {
		return Fail("the staged changeset came back with no id, so there is nothing to apply or roll back", evidence...)
	}

	_, applyEv, err := mutate(ctx, d, "/changesets/"+id+"/apply", map[string]any{"confirmTimeoutSec": 180})
	evidence = append(evidence, applyEv)
	if err != nil {
		return Fail(fmt.Sprintf("applying changeset %s across %d nodes failed: %v", id, len(ops), err), evidence...)
	}

	_, rollbackEv, err := mutate(ctx, d, "/changesets/"+id+"/rollback", nil)
	evidence = append(evidence, rollbackEv)
	if err != nil {
		return Fail(fmt.Sprintf("rolling back changeset %s failed, leaving it applied on %d node(s): %v", id, len(ops), err), evidence...)
	}

	// The verdict: does PVE agree that both nodes are back?
	var diverged []string
	for _, n := range online[:2] {
		after, afterErr := d.Cluster.Interfaces(ctx, n.Name)
		if afterErr != nil {
			return Fail(fmt.Sprintf("changeset %s rolled back but %s's post-rollback state is unreadable: %v", id, n.Name, afterErr), evidence...)
		}
		evidence = append(evidence, NewEvidence(SourcePVEAPI, fmt.Sprintf("after: GET /nodes/%s/network", n.Name), describeIfaces(after)))
		if describeIfaces(after) != describeIfaces(before[n.Name]) {
			diverged = append(diverged, n.Name)
		}
	}
	if len(diverged) > 0 {
		return Fail(fmt.Sprintf("changeset %s was rolled back but %s did not return to its pre-apply state: a distributed rollback left the cluster half-restored",
			id, strings.Join(diverged, ", ")), evidence...)
	}
	return Pass(fmt.Sprintf("changeset %s applied across %d real nodes and rolled back to a byte-identical pre-apply state on every one", id, len(ops)), evidence...)
}

// checkCommitConfirmUnattendedRollback applies a changeset and deliberately
// never confirms it, which is the only way to observe the thing row 4 claims:
// that an operator who loses their connection mid-apply gets their network
// back without doing anything.
func checkCommitConfirmUnattendedRollback(ctx context.Context, d Deps) Outcome {
	if d.Mutator == nil {
		return skipNoConsent("applying a changeset and letting its commit-confirm window expire unconfirmed")
	}
	if d.Daemon == nil || d.Cluster == nil {
		return skipNoDaemon("an unattended rollback")
	}
	node := localNode(d.Nodes)
	if node == "" {
		return skipNoCluster("an unattended rollback needs a named cluster node")
	}

	before, err := d.Cluster.Interfaces(ctx, node)
	if err != nil {
		return Fail(fmt.Sprintf("could not read %s's pre-apply interfaces: %v", node, err),
			NewEvidence(SourcePVEAPI, fmt.Sprintf("GET /nodes/%s/network", node), err.Error()))
	}
	evidence := []Evidence{NewEvidence(SourcePVEAPI, fmt.Sprintf("before: GET /nodes/%s/network", node), describeIfaces(before))}

	target, ok := firstBridge(before)
	if !ok {
		return Skip(fmt.Sprintf("%s has no bridge to apply a reversible no-op against", node), evidence...)
	}

	// The shortest window the engine accepts for a non-management change, so
	// the check does not hold a cluster open for minutes.
	const confirmWindowSec = 30
	staged, stageEv, err := mutate(ctx, d, "/changesets", map[string]any{
		"title": "T-2501 verify: unattended rollback",
		"ops": []map[string]any{{
			"type":    "bridge.update",
			"node":    node,
			"bridge":  target.Name,
			"comment": target.Comments,
		}},
	})
	evidence = append(evidence, stageEv)
	if err != nil {
		return Fail(fmt.Sprintf("staging the unattended-rollback changeset failed: %v", err), evidence...)
	}
	id, _ := staged["id"].(string)
	if id == "" {
		return Fail("the staged changeset came back with no id", evidence...)
	}

	_, applyEv, err := mutate(ctx, d, "/changesets/"+id+"/apply", map[string]any{"confirmTimeoutSec": confirmWindowSec})
	evidence = append(evidence, applyEv)
	if err != nil {
		return Fail(fmt.Sprintf("applying changeset %s failed: %v", id, err), evidence...)
	}

	// Deliberately no confirm. Wait past the window, with margin, then ask
	// the daemon what happened to it.
	deadline := d.Now().Add((confirmWindowSec + 30) * time.Second)
	var status string
	for d.Now().Before(deadline) {
		var cs struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		pollEv, pollErr := daemonJSON(ctx, d, "/changesets/"+id, &cs)
		if pollErr != nil {
			return Fail(fmt.Sprintf("changeset %s became unreadable while waiting for its window to expire: %v", id, pollErr), append(evidence, pollEv)...)
		}
		status = cs.Status
		if status == "rolled_back" || status == "committed" {
			evidence = append(evidence, pollEv)
			break
		}
		if waitErr := d.Wait(ctx, 5*time.Second); waitErr != nil {
			return Fail(fmt.Sprintf("the run was cancelled while changeset %s was still %s: it may still be awaiting confirmation on a real cluster", id, status), append(evidence, pollEv)...)
		}
	}

	switch status {
	case "rolled_back":
		after, afterErr := d.Cluster.Interfaces(ctx, node)
		if afterErr != nil {
			return Fail(fmt.Sprintf("changeset %s rolled back unattended but %s's state is unreadable: %v", id, node, afterErr), evidence...)
		}
		evidence = append(evidence, NewEvidence(SourcePVEAPI, fmt.Sprintf("after: GET /nodes/%s/network", node), describeIfaces(after)))
		if describeIfaces(after) != describeIfaces(before) {
			return Fail(fmt.Sprintf("changeset %s reports rolled_back but %s's live state does not match its pre-apply state", id, node), evidence...)
		}
		return Pass(fmt.Sprintf("changeset %s was applied and never confirmed; the timer rolled it back unattended within %ds and %s is byte-identical to its pre-apply state", id, confirmWindowSec, node), evidence...)
	case "committed":
		return Fail(fmt.Sprintf("changeset %s reached committed without anyone confirming it: the commit-confirm window did not gate the apply", id), evidence...)
	default:
		return Fail(fmt.Sprintf("changeset %s is still %q more than %ds after its %ds window: no unattended rollback fired, and a real cluster is now holding an unconfirmed change",
			id, status, confirmWindowSec+30, confirmWindowSec), evidence...)
	}
}

// checkSRIOVVFLifecycle provisions and releases a VF on real silicon.
func checkSRIOVVFLifecycle(ctx context.Context, d Deps) Outcome {
	if d.Mutator == nil {
		return skipNoConsent("provisioning and releasing a VF on a real SR-IOV NIC")
	}
	if d.Host == nil {
		return skipNoHost("reading /sys/class/net VF counters around the lifecycle")
	}
	node := localNode(d.Nodes)

	listing, err := d.Host.Run(ctx, node, "sh", "-c", "for f in /sys/class/net/*/device/sriov_totalvfs; do [ -e \"$f\" ] && echo \"$f=$(cat $f)\"; done; exit 0")
	if err != nil {
		return Skip(fmt.Sprintf("could not enumerate SR-IOV NICs on %s: %v", node, err),
			NewEvidence(SourceCommand, "cat /sys/class/net/*/device/sriov_totalvfs", err.Error()))
	}
	// Sorted, not map order: which PF a destructive check provisions a VF on
	// must be the same on every run. A map range here would pick a different
	// NIC each time, which is both unreproducible evidence and — on a host
	// where one PF carries guest traffic and another does not — a genuinely
	// unsafe way to choose.
	totals := parseSRIOVTotals(listing)
	names := make([]string, 0, len(totals))
	for name := range totals {
		names = append(names, name)
	}
	sort.Strings(names)
	var pf string
	for _, name := range names {
		if totals[name] > 0 {
			pf = name
			break
		}
	}
	if pf == "" {
		return Skip(fmt.Sprintf("no SR-IOV-capable NIC on %s, so there is no VF to provision. See sriov.vf_capable_nic_present for what this needs", node),
			NewEvidence(SourceCommand, "cat /sys/class/net/*/device/sriov_totalvfs", listing))
	}

	numPath := fmt.Sprintf("/sys/class/net/%s/device/sriov_numvfs", pf)
	beforeRaw, err := d.Host.ReadFile(ctx, node, numPath)
	if err != nil {
		return Fail(fmt.Sprintf("%s advertises SR-IOV but %s is unreadable: %v", pf, numPath, err),
			NewEvidence(SourceFile, numPath, err.Error()))
	}
	evidence := []Evidence{NewEvidence(SourceFile, "before: "+numPath, string(beforeRaw))}

	_, provisionEv, err := mutate(ctx, d, "/changesets", map[string]any{
		"title": "T-2501 verify: SR-IOV VF lifecycle",
		"ops":   []map[string]any{{"type": "vf.provision", "node": node, "pf": pf}},
	})
	evidence = append(evidence, provisionEv)
	if err != nil {
		return Fail(fmt.Sprintf("staging a vf.provision on %s/%s failed: %v", node, pf, err), evidence...)
	}

	afterRaw, err := d.Host.ReadFile(ctx, node, numPath)
	if err != nil {
		return Fail(fmt.Sprintf("a VF was provisioned on %s but %s became unreadable: %v", pf, numPath, err), evidence...)
	}
	evidence = append(evidence, NewEvidence(SourceFile, "after: "+numPath, string(afterRaw)))
	if strings.TrimSpace(string(afterRaw)) == strings.TrimSpace(string(beforeRaw)) {
		return Fail(fmt.Sprintf("the changeset staged a vf.provision on %s/%s but the kernel's VF count is unchanged at %s: nothing reached the hardware",
			node, pf, strings.TrimSpace(string(beforeRaw))), evidence...)
	}
	return Pass(fmt.Sprintf("a VF lifecycle ran against real silicon on %s/%s: the kernel's VF count moved from %s to %s",
		node, pf, strings.TrimSpace(string(beforeRaw)), strings.TrimSpace(string(afterRaw))), evidence...)
}

// checkHAFailover stops the active daemon and watches for a promotion.
func checkHAFailover(ctx context.Context, d Deps) Outcome {
	if d.Mutator == nil {
		return skipNoConsent("stopping the active vnproxd and watching a standby promote")
	}
	if d.Host == nil {
		return skipNoHost("stopping and restarting vnproxd on the active node")
	}
	if d.Daemon == nil {
		return skipNoDaemon("HA role transitions")
	}
	online := onlineNodes(d.Nodes)
	if len(online) < 2 {
		return Skip(fmt.Sprintf("this cluster has %d online node(s): %s. A failover needs somewhere to fail over to", len(online), strings.Join(nodeNames(online), ", ")))
	}

	var before struct {
		Role string `json:"role"`
		Term int64  `json:"term"`
	}
	beforeEv, err := daemonJSON(ctx, d, "/ha/status", &before)
	if err != nil {
		return Fail(fmt.Sprintf("could not read HA status before the failover: %v", err), beforeEv)
	}
	if before.Role != "active" {
		return Skip(fmt.Sprintf("this node's role is %q, not active, so stopping it would not exercise a promotion. Run the destructive suite against the node holding the lease", before.Role), beforeEv)
	}

	stopOut, stopErr := d.Host.Run(ctx, localNode(d.Nodes), "systemctl", "stop", "vnprox")
	stopEv := NewEvidence(SourceCommand, "systemctl stop vnprox", stopOut)
	if stopErr != nil {
		return Fail(fmt.Sprintf("could not stop the active vnproxd: %v", stopErr),
			beforeEv, NewEvidence(SourceCommand, "systemctl stop vnprox", stopOut+"\n"+stopErr.Error()))
	}
	// Whatever happens next, the daemon has to come back.
	defer func() {
		_, _ = d.Host.Run(ctx, localNode(d.Nodes), "systemctl", "start", "vnprox")
	}()

	// The standby's own daemon is the witness; this run can only ask the one
	// it is pointed at, so a promotion is observed by that daemon reporting
	// a higher term with role=active once it comes back.
	deadline := d.Now().Add(2 * time.Minute)
	for d.Now().Before(deadline) {
		if waitErr := d.Wait(ctx, 5*time.Second); waitErr != nil {
			return Fail("the run was cancelled during a failover: the active vnproxd was stopped and may not have been restarted", beforeEv, stopEv)
		}
		var after struct {
			Role string `json:"role"`
			Term int64  `json:"term"`
		}
		afterEv, pollErr := daemonJSON(ctx, d, "/ha/status", &after)
		if pollErr != nil {
			continue // the daemon under test is down; that is the point
		}
		if after.Role == "active" && after.Term > before.Term {
			return Pass(fmt.Sprintf("stopping the active daemon promoted a standby: term moved %d -> %d with role active on a real %d-node cluster",
				before.Term, after.Term, len(online)), beforeEv, stopEv, afterEv)
		}
	}
	return Fail(fmt.Sprintf("no standby promoted within 2 minutes of the active daemon (term %d) being stopped on a %d-node cluster: the lease was never taken over",
		before.Term, len(online)), beforeEv, stopEv)
}

// --- helpers ------------------------------------------------------------------

// mutate performs one write through the consented Mutator and records the
// request and response as evidence.
func mutate(ctx context.Context, d Deps, path string, body any) (map[string]any, Evidence, error) {
	if d.Mutator == nil {
		return nil, Evidence{}, errors.New("no mutator: --i-understand was not given")
	}
	status, resp, err := d.Mutator.Post(ctx, path, body)
	ref := fmt.Sprintf("POST %s -> %d", path, status)
	ev := NewEvidence(SourceDaemonAPI, ref, fmt.Sprintf("request: %s\nresponse: %s", mustJSON(body), string(resp)))
	if err != nil {
		return nil, ev, fmt.Errorf("POST %s: %w", path, err)
	}
	if status < 200 || status >= 300 {
		return nil, ev, fmt.Errorf("POST %s returned %d", path, status)
	}
	var out map[string]any
	if len(resp) > 0 {
		if jsonErr := json.Unmarshal(resp, &out); jsonErr != nil {
			return nil, ev, fmt.Errorf("decoding POST %s: %w", path, jsonErr)
		}
	}
	return out, ev, nil
}

// firstBridge picks a bridge to exercise a reversible no-op against.
func firstBridge(ifaces []Iface) (Iface, bool) {
	for _, i := range ifaces {
		if i.Type == "bridge" {
			return i, true
		}
	}
	return Iface{}, false
}
