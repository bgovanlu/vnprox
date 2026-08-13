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
		for _, name := range []string{"network_read", "sdn_zone_baseline", "sdn_fabric_zone_gate"} {
			results = append(results, CheckResult{Name: name, Pass: false, Detail: "skipped: auth_ticket failed"})
		}
		return results
	}
	results = append(results, CheckResult{Name: "auth_ticket", Pass: true,
		Detail: "POST /access/ticket issued a ticket and CSRF token for " + compatUsername})

	results = append(results, checkNetworkRead(client, srv.URL, ticket))
	results = append(results, checkSDNZoneCreate(client, srv.URL, ticket, csrf, "sdn_zone_baseline", "cbaseline", "vlan", true, profile))
	results = append(results, checkSDNZoneCreate(client, srv.URL, ticket, csrf, "sdn_fabric_zone_gate", "cfabric", "openfabric", profile.SDNFabricZones, profile))

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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CheckResult{Name: "network_read", Pass: false, Detail: fmt.Sprintf("GET .../network: status %d", resp.StatusCode)}
	}
	return CheckResult{Name: "network_read", Pass: true, Detail: "GET /nodes/" + compatNode + "/network: 200"}
}

// checkSDNZoneCreate POSTs an SDN zone of zoneType and records whether the
// mock's behavior matched wantAccepted. wantAccepted is derived from the
// profile by the caller (runChecks) rather than hardcoded here, so this one
// function serves both the always-accepted baseline check and the
// version-gated fabric-zone check — the latter being the concrete AC2
// "fixture divergence between PVE versions is caught" case this task card
// asks for: on the 8.2 cell, wantAccepted is false, and the check counts as
// PASSING exactly when the mock correctly rejects the request.
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
	defer resp.Body.Close()

	accepted := resp.StatusCode == http.StatusOK
	if accepted == wantAccepted {
		return CheckResult{Name: checkName, Pass: true, Detail: fmt.Sprintf(
			"POST zone type=%s on PVE %s: status %d, accepted=%v as expected", zoneType, profile.Version, resp.StatusCode, accepted)}
	}
	return CheckResult{Name: checkName, Pass: false, Detail: fmt.Sprintf(
		"POST zone type=%s on PVE %s: status %d, accepted=%v, want accepted=%v", zoneType, profile.Version, resp.StatusCode, accepted, wantAccepted)}
}
