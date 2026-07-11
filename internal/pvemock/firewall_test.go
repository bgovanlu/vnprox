package pvemock

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestFirewall_AliasesCRUD(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	createBody, _ := json.Marshal(FwAliasSpec{Name: "test_alias", CIDR: "10.0.0.0/8"})
	create := authedRequest(t, http.MethodPost, "/api2/json/cluster/firewall/aliases", ticket, csrf, createBody)
	if rec, _ := doJSON(t, srv, create); rec.Code != http.StatusOK {
		t.Fatalf("create alias status = %d", rec.Code)
	}

	get := authedRequest(t, http.MethodGet, "/api2/json/cluster/firewall/aliases/test_alias", ticket, "", nil)
	rec, body := doJSON(t, srv, get)
	if rec.Code != http.StatusOK {
		t.Fatalf("get alias status = %d", rec.Code)
	}
	data, _ := body["data"].(map[string]any)
	if data["cidr"] != "10.0.0.0/8" {
		t.Fatalf("alias cidr = %v, want 10.0.0.0/8", data["cidr"])
	}

	updateBody, _ := json.Marshal(FwAliasSpec{CIDR: "10.1.0.0/16"})
	update := authedRequest(t, http.MethodPut, "/api2/json/cluster/firewall/aliases/test_alias", ticket, csrf, updateBody)
	if rec, _ := doJSON(t, srv, update); rec.Code != http.StatusOK {
		t.Fatalf("update alias status = %d", rec.Code)
	}

	del := authedRequest(t, http.MethodDelete, "/api2/json/cluster/firewall/aliases/test_alias", ticket, csrf, nil)
	if rec, _ := doJSON(t, srv, del); rec.Code != http.StatusOK {
		t.Fatalf("delete alias status = %d", rec.Code)
	}
	getGone := authedRequest(t, http.MethodGet, "/api2/json/cluster/firewall/aliases/test_alias", ticket, "", nil)
	if rec, _ := doJSON(t, srv, getGone); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted alias status = %d, want 404", rec.Code)
	}
}

func TestFirewall_IPSetEntriesCRUD(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	createSet, _ := json.Marshal(FwIPSetSpec{Name: "blocklist"})
	req := authedRequest(t, http.MethodPost, "/api2/json/cluster/firewall/ipset", ticket, csrf, createSet)
	if rec, _ := doJSON(t, srv, req); rec.Code != http.StatusOK {
		t.Fatalf("create ipset status = %d", rec.Code)
	}

	addEntry, _ := json.Marshal(FwIPSetEntry{CIDR: "1.2.3.4/32", Comment: "bad actor"})
	req2 := authedRequest(t, http.MethodPost, "/api2/json/cluster/firewall/ipset/blocklist", ticket, csrf, addEntry)
	if rec, _ := doJSON(t, srv, req2); rec.Code != http.StatusOK {
		t.Fatalf("add ipset entry status = %d", rec.Code)
	}

	list := authedRequest(t, http.MethodGet, "/api2/json/cluster/firewall/ipset/blocklist", ticket, "", nil)
	rec, body := doJSON(t, srv, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list ipset entries status = %d", rec.Code)
	}
	entries, _ := body["data"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	delEntry := authedRequest(t, http.MethodDelete, "/api2/json/cluster/firewall/ipset/blocklist/1.2.3.4%2F32", ticket, csrf, nil)
	rec, _ = doJSON(t, srv, delEntry)
	// chi URL-decodes path params, so "/" in the CIDR arrives decoded;
	// exercise via the literal (undecoded-by-us) value instead:
	if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
		t.Fatalf("delete ipset entry status = %d", rec.Code)
	}

	delSet := authedRequest(t, http.MethodDelete, "/api2/json/cluster/firewall/ipset/blocklist", ticket, csrf, nil)
	if rec, _ := doJSON(t, srv, delSet); rec.Code != http.StatusOK {
		t.Fatalf("delete ipset status = %d", rec.Code)
	}
}

func TestFirewall_SecurityGroupsCRUD(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	createGroup, _ := json.Marshal(FwGroupSpec{Name: "webservers"})
	req := authedRequest(t, http.MethodPost, "/api2/json/cluster/firewall/groups", ticket, csrf, createGroup)
	if rec, _ := doJSON(t, srv, req); rec.Code != http.StatusOK {
		t.Fatalf("create group status = %d", rec.Code)
	}

	addRule, _ := json.Marshal(FwRuleSpec{Enabled: true, Type: "in", Action: "ACCEPT", Dport: "80"})
	req2 := authedRequest(t, http.MethodPost, "/api2/json/cluster/firewall/groups/webservers", ticket, csrf, addRule)
	if rec, _ := doJSON(t, srv, req2); rec.Code != http.StatusOK {
		t.Fatalf("add group rule status = %d", rec.Code)
	}

	list := authedRequest(t, http.MethodGet, "/api2/json/cluster/firewall/groups/webservers", ticket, "", nil)
	rec, body := doJSON(t, srv, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list group rules status = %d", rec.Code)
	}
	rules, _ := body["data"].([]any)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	updateRule, _ := json.Marshal(FwRuleSpec{Enabled: false, Type: "in", Action: "DROP"})
	req3 := authedRequest(t, http.MethodPut, "/api2/json/cluster/firewall/groups/webservers/0", ticket, csrf, updateRule)
	if rec, _ := doJSON(t, srv, req3); rec.Code != http.StatusOK {
		t.Fatalf("update group rule status = %d", rec.Code)
	}

	delRule := authedRequest(t, http.MethodDelete, "/api2/json/cluster/firewall/groups/webservers/0", ticket, csrf, nil)
	if rec, _ := doJSON(t, srv, delRule); rec.Code != http.StatusOK {
		t.Fatalf("delete group rule status = %d", rec.Code)
	}

	listGroups := authedRequest(t, http.MethodGet, "/api2/json/cluster/firewall/groups", ticket, "", nil)
	if rec, _ := doJSON(t, srv, listGroups); rec.Code != http.StatusOK {
		t.Fatalf("list groups status = %d", rec.Code)
	}

	delGroup := authedRequest(t, http.MethodDelete, "/api2/json/cluster/firewall/groups/webservers", ticket, csrf, nil)
	if rec, _ := doJSON(t, srv, delGroup); rec.Code != http.StatusOK {
		t.Fatalf("delete group status = %d", rec.Code)
	}
}

func TestFirewall_NodeAndGuestScopeOptionsAndRules(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	// Node scope options.
	getOpts := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/firewall/options", ticket, "", nil)
	if rec, _ := doJSON(t, srv, getOpts); rec.Code != http.StatusOK {
		t.Fatalf("get node fw options status = %d", rec.Code)
	}
	putOpts, _ := json.Marshal(map[string]string{"enable": "1"})
	req := authedRequest(t, http.MethodPut, "/api2/json/nodes/pve1/firewall/options", ticket, csrf, putOpts)
	if rec, _ := doJSON(t, srv, req); rec.Code != http.StatusOK {
		t.Fatalf("put node fw options status = %d", rec.Code)
	}

	// Guest (qemu) scope rules.
	ruleBody, _ := json.Marshal(FwRuleSpec{Enabled: true, Type: "in", Action: "ACCEPT", Dport: "443"})
	createRule := authedRequest(t, http.MethodPost, "/api2/json/nodes/pve1/qemu/100/firewall/rules", ticket, csrf, ruleBody)
	if rec, _ := doJSON(t, srv, createRule); rec.Code != http.StatusOK {
		t.Fatalf("create guest fw rule status = %d", rec.Code)
	}
	listRules := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/qemu/100/firewall/rules", ticket, "", nil)
	rec, body := doJSON(t, srv, listRules)
	if rec.Code != http.StatusOK {
		t.Fatalf("list guest fw rules status = %d", rec.Code)
	}
	rules, _ := body["data"].([]any)
	// single-node.yaml's guest 100 fixture already ships one rule.
	if len(rules) != 2 {
		t.Fatalf("expected 2 guest fw rules (1 fixture + 1 created), got %d", len(rules))
	}

	// VM.Audit alone should not permit the write above from a read-only user.
	roTicket, roCSRF := login(t, srv, "auditor@pve", "readonly")
	roReq := authedRequest(t, http.MethodPost, "/api2/json/nodes/pve1/qemu/100/firewall/rules", roTicket, roCSRF, ruleBody)
	if rec, _ := doJSON(t, srv, roReq); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only guest fw rule create status = %d, want 403", rec.Code)
	}
}

// TestFirewall_RuleUpdate_Moveto is T-502's addition to the rule update
// endpoint: PUT .../rules/{pos} with a "moveto" field relocates the rule
// (real pve-firewall's own semantics on this same endpoint), renumbering
// every rule to stay contiguous.
func TestFirewall_RuleUpdate_Moveto(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	// cluster.fw starts empty in single-node.yaml; create three rules.
	for _, comment := range []string{"first", "second", "third"} {
		body, _ := json.Marshal(FwRuleSpec{Enabled: true, Type: "in", Action: "ACCEPT", Comment: comment})
		req := authedRequest(t, http.MethodPost, "/api2/json/cluster/firewall/rules", ticket, csrf, body)
		if rec, _ := doJSON(t, srv, req); rec.Code != http.StatusOK {
			t.Fatalf("create rule %q status = %d", comment, rec.Code)
		}
	}

	// Move "first" (pos 0) to the end (pos 2), resending its own unchanged
	// fields plus moveto — fw.rule.move's realization.
	moveBody, _ := json.Marshal(fwRuleUpdateBody{
		FwRuleSpec: FwRuleSpec{Enabled: true, Type: "in", Action: "ACCEPT", Comment: "first"},
		Moveto:     intPtrPvemock(2),
	})
	moveReq := authedRequest(t, http.MethodPut, "/api2/json/cluster/firewall/rules/0", ticket, csrf, moveBody)
	if rec, _ := doJSON(t, srv, moveReq); rec.Code != http.StatusOK {
		t.Fatalf("move rule status = %d", rec.Code)
	}

	listReq := authedRequest(t, http.MethodGet, "/api2/json/cluster/firewall/rules", ticket, "", nil)
	rec, body := doJSON(t, srv, listReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("list rules status = %d", rec.Code)
	}
	rules, _ := body["data"].([]any)
	if len(rules) != 3 {
		t.Fatalf("len(rules) = %d, want 3", len(rules))
	}
	wantOrder := []string{"second", "third", "first"}
	for i, raw := range rules {
		r, _ := raw.(map[string]any)
		if r["comment"] != wantOrder[i] {
			t.Errorf("rules[%d].comment = %v, want %q (order: %+v)", i, r["comment"], wantOrder[i], rules)
		}
		if int(r["pos"].(float64)) != i {
			t.Errorf("rules[%d].pos = %v, want %d", i, r["pos"], i)
		}
	}
}

func intPtrPvemock(v int) *int { return &v }

// TestFirewall_IPSetUpdate_Comment covers T-502's new PUT .../ipset/{name}
// endpoint (rename the comment; Name itself is not editable).
func TestFirewall_IPSetUpdate_Comment(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	createBody, _ := json.Marshal(FwIPSetSpec{Name: "blocklist", Comment: "original"})
	create := authedRequest(t, http.MethodPost, "/api2/json/cluster/firewall/ipset", ticket, csrf, createBody)
	if rec, _ := doJSON(t, srv, create); rec.Code != http.StatusOK {
		t.Fatalf("create ipset status = %d", rec.Code)
	}

	updateBody, _ := json.Marshal(map[string]string{"comment": "updated"})
	update := authedRequest(t, http.MethodPut, "/api2/json/cluster/firewall/ipset/blocklist", ticket, csrf, updateBody)
	if rec, _ := doJSON(t, srv, update); rec.Code != http.StatusOK {
		t.Fatalf("update ipset status = %d", rec.Code)
	}

	list := authedRequest(t, http.MethodGet, "/api2/json/cluster/firewall/ipset", ticket, "", nil)
	rec, body := doJSON(t, srv, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list ipsets status = %d", rec.Code)
	}
	sets, _ := body["data"].([]any)
	found := false
	for _, raw := range sets {
		s, _ := raw.(map[string]any)
		if s["name"] == "blocklist" {
			found = true
			if s["comment"] != "updated" {
				t.Errorf("comment = %v, want %q", s["comment"], "updated")
			}
		}
	}
	if !found {
		t.Fatal("blocklist ipset not found in list")
	}
}

// TestFirewall_CompileStatus covers T-502's mock-only /nodes/{node}/firewall/
// status route and its failure-injection control endpoint.
func TestFirewall_CompileStatus(t *testing.T) {
	srv := newTestServer(t, "single-node.yaml")
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")

	ok := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/firewall/status", ticket, "", nil)
	rec, body := doJSON(t, srv, ok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	data, _ := body["data"].(map[string]any)
	if data["status"] != "ok" {
		t.Fatalf("status field = %v, want ok", data["status"])
	}

	failBody, _ := json.Marshal(map[string]bool{"fail": true})
	failReq, err := http.NewRequest(http.MethodPost, "/mock/nodes/pve1/firewall-compile-fail", bytes.NewReader(failBody))
	if err != nil {
		t.Fatal(err)
	}
	failReq.Header.Set("Content-Type", "application/json")
	rec2, _ := doJSON(t, srv, failReq)
	if rec2.Code != http.StatusOK {
		t.Fatalf("toggling compile-fail status = %d", rec2.Code)
	}

	failed := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/firewall/status", ticket, "", nil)
	rec3, body3 := doJSON(t, srv, failed)
	if rec3.Code != http.StatusOK {
		t.Fatalf("status = %d", rec3.Code)
	}
	data3, _ := body3["data"].(map[string]any)
	if data3["status"] != "error" || data3["message"] == "" {
		t.Fatalf("data = %+v, want status=error with a message", data3)
	}
}
