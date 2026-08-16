package compat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// checks.go drives real HTTP requests at a running
// pvemock.NewCompatServer instance — the same PVE-API wire layer
// internal/pve.Client itself talks to, exercised directly (over HTTP,
// ticket-authenticated) rather than through vnprox's own production stack.
// That scope is deliberate: this matrix's whole question is "does the PVE
// API surface this mock models differ across versions", which is fully
// answerable at this boundary, and internal/apicontract's own suite already
// runs vnprox's production handlers against the (version-independent)
// standard fixtures on every commit — this package does not duplicate that.
//
// Every fixture Cells names declares exactly one user, root@pam/vnprox-mock
// (see testdata/clusters/compat/pve-8.2.yaml's header comment), and one
// node, "pve1" — hardcoded below rather than threaded through Cell because
// every compat fixture is deliberately built to that same minimal shape.

const (
	compatUsername = "root@pam"
	compatPassword = "vnprox-mock"
	compatNode     = "pve1"
)

// runChecks starts an httptest server wrapping f with profile's version
// gating, runs every check against it, and returns their results in a
// fixed, stable order. It never panics or aborts partway: a failed
// prerequisite (e.g. login itself failing) is recorded as a failed check
// and short-circuits only the checks that depend on it, not the ones that
// don't.
func runChecks(f *pvemock.Fixture, profile pvemock.PVEVersionProfile) []CheckResult {
	srv := httptest.NewServer(pvemock.NewCompatServer(f, profile))
	defer srv.Close()
	client := srv.Client()

	var results []CheckResult

	ticket, csrf, err := compatLogin(client, srv.URL)
	if err != nil {
		results = append(results, CheckResult{Name: "auth_ticket", Pass: false, Detail: err.Error()})
		// Every other check needs a ticket; recording them as failed
		// (rather than silently omitting them) keeps the cell's check
		// list shape stable across every cell, which matters for a
		// reader comparing rows in the published matrix.
		for _, name := range []string{"network_read", "sdn_zone_baseline", "sdn_fabrics_api_gate"} {
			results = append(results, CheckResult{Name: name, Pass: false, Detail: "skipped: auth_ticket failed"})
		}
		return results
	}
	results = append(results, CheckResult{Name: "auth_ticket", Pass: true,
		Detail: "POST /access/ticket issued a ticket and CSRF token for " + compatUsername})

	results = append(results, checkNetworkRead(client, srv.URL, ticket))
	results = append(results, checkSDNZoneCreate(client, srv.URL, ticket, csrf, "sdn_zone_baseline", "cbaseline", "vlan", true, profile))
	results = append(results, checkSDNFabricsAPI(client, srv.URL, ticket, profile))

	return results
}

func compatLogin(client *http.Client, baseURL string) (ticket, csrf string, err error) {
	form := strings.NewReader(fmt.Sprintf("username=%s&password=%s", compatUsername, compatPassword))
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api2/json/access/ticket", form)
	if err != nil {
		return "", "", fmt.Errorf("building login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("POST /access/ticket: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("POST /access/ticket: status %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Ticket string `json:"ticket"`
			CSRF   string `json:"CSRFPreventionToken"`
		} `json:"data"`
	}
	if decErr := json.NewDecoder(resp.Body).Decode(&envelope); decErr != nil {
		return "", "", fmt.Errorf("decoding login response: %w", decErr)
	}
	if envelope.Data.Ticket == "" {
		return "", "", fmt.Errorf("login response carried no ticket")
	}
	return envelope.Data.Ticket, envelope.Data.CSRF, nil
}

func checkNetworkRead(client *http.Client, baseURL, ticket string) CheckResult {
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/api2/json/nodes/"+compatNode+"/network", nil)
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{Name: "network_read", Pass: false, Detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return CheckResult{Name: "network_read", Pass: false, Detail: fmt.Sprintf("GET .../network: status %d", resp.StatusCode)}
	}
	return CheckResult{Name: "network_read", Pass: true, Detail: "GET /nodes/" + compatNode + "/network: 200"}
}

// checkSDNZoneCreate POSTs an SDN zone of zoneType and records whether the
// mock's behavior matched wantAccepted. wantAccepted is derived from the
// profile by the caller (runChecks) rather than hardcoded here.
//
// It has one caller now (the always-accepted `vlan` baseline). Its second
// caller used to be `sdn_fabric_zone_gate`, which posted an "openfabric"
// zone and expected a version-dependent answer. That check was removed on
// 2026-08-16: real PVE 9.2.4's zone type enum is
// <evpn | faucet | qinq | simple | vlan | vxlan>, so 8.2 and 9.2 both
// reject an "openfabric" zone and the gate tested a difference that does
// not exist. See checkSDNFabricsAPI for the divergence that does, and
// pvemock.PVEVersionProfile.SDNFabrics for the capture behind it.
func checkSDNZoneCreate(client *http.Client, baseURL, ticket, csrf, checkName, zoneID, zoneType string, wantAccepted bool, profile pvemock.PVEVersionProfile) CheckResult {
	body, err := json.Marshal(pvemock.SDNZoneSpec{ID: zoneID, Type: zoneType})
	if err != nil {
		return CheckResult{Name: checkName, Pass: false, Detail: "marshaling zone body: " + err.Error()}
	}
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api2/json/cluster/sdn/zones", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
	req.Header.Set("CSRFPreventionToken", csrf)
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{Name: checkName, Pass: false, Detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	accepted := resp.StatusCode == http.StatusOK
	if accepted == wantAccepted {
		return CheckResult{Name: checkName, Pass: true, Detail: fmt.Sprintf(
			"POST zone type=%s on PVE %s: status %d, accepted=%v as expected", zoneType, profile.Version, resp.StatusCode, accepted)}
	}
	return CheckResult{Name: checkName, Pass: false, Detail: fmt.Sprintf(
		"POST zone type=%s on PVE %s: status %d, accepted=%v, want accepted=%v", zoneType, profile.Version, resp.StatusCode, accepted, wantAccepted)}
}

// checkSDNFabricsAPI is the matrix's one genuine version divergence: PVE
// 9.0 added the /cluster/sdn/fabrics API family and PVE 8.2 does not serve
// it. On an 8.2 cell the check PASSES exactly when the mock answers 501; on
// a 9.x cell it passes only on a 200 that carries both keys real hardware
// returns ("fabrics" and "nodes"), so a wrapper that answered 200 with an
// empty body would fail rather than score a pass.
//
// This replaced sdn_fabric_zone_gate, whose premise hardware disproved.
// Unlike its predecessor, this check's expectation was read off a running
// PVE 9.2.4 node — planning/reports/evidence/pve-9.2.4-sdn-schema.txt.
func checkSDNFabricsAPI(client *http.Client, baseURL, ticket string, profile pvemock.PVEVersionProfile) CheckResult {
	const name = "sdn_fabrics_api_gate"
	req, _ := http.NewRequest(http.MethodGet, baseURL+pvemock.SDNFabricsPath+"/all", nil)
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{Name: name, Pass: false, Detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	var envelope struct {
		Data struct {
			Fabrics []json.RawMessage `json:"fabrics"`
			Nodes   []json.RawMessage `json:"nodes"`
		} `json:"data"`
	}
	// A decode failure is not fatal on its own: the 501 branch below only
	// needs the status, and the 200 branch reports the missing keys itself.
	_ = json.NewDecoder(resp.Body).Decode(&envelope)

	if !profile.SupportsSDNFabricsAPI() {
		if resp.StatusCode == http.StatusNotImplemented {
			return CheckResult{Name: name, Pass: true, Detail: fmt.Sprintf(
				"GET /cluster/sdn/fabrics/all on PVE %s: status 501, absent as expected", profile.Version)}
		}
		return CheckResult{Name: name, Pass: false, Detail: fmt.Sprintf(
			"GET /cluster/sdn/fabrics/all on PVE %s: status %d, want 501 (this version predates SDN Fabrics)",
			profile.Version, resp.StatusCode)}
	}

	if resp.StatusCode != http.StatusOK {
		return CheckResult{Name: name, Pass: false, Detail: fmt.Sprintf(
			"GET /cluster/sdn/fabrics/all on PVE %s: status %d, want 200", profile.Version, resp.StatusCode)}
	}
	if envelope.Data.Fabrics == nil || envelope.Data.Nodes == nil {
		return CheckResult{Name: name, Pass: false, Detail: fmt.Sprintf(
			"GET /cluster/sdn/fabrics/all on PVE %s: 200 without both \"fabrics\" and \"nodes\" keys",
			profile.Version)}
	}
	return CheckResult{Name: name, Pass: true, Detail: fmt.Sprintf(
		"GET /cluster/sdn/fabrics/all on PVE %s: status 200 with fabrics+nodes keys", profile.Version)}
}
