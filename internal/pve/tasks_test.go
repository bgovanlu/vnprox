package pve_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// rawLogin performs POST /access/ticket directly via net/http (bypassing
// the pve.Client under test) so these tests can drive
// internal/pvemock's mock-only failure/latency injection query params
// (?mock_fail=1, ?mock_latency_ms=...) on the raw PUT request that starts
// a task — those knobs are deliberately not part of pve.Client's typed,
// documented-surface-only API (T-101 acceptance criterion 4).
func rawLogin(t *testing.T, baseURL, username, password string) (ticket, csrf string) {
	t.Helper()
	form := url.Values{"username": {username}, "password": {password}}
	resp, err := http.Post(baseURL+"/api2/json/access/ticket", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("rawLogin POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var envelope struct {
		Data struct {
			Ticket string `json:"ticket"`
			CSRF   string `json:"CSRFPreventionToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("rawLogin decoding response %s: %v", body, err)
	}
	return envelope.Data.Ticket, envelope.Data.CSRF
}

// rawStartReloadTask issues PUT /nodes/{node}/network directly, with
// pvemock's mock-only query overrides, and returns the resulting task
// UPID.
func rawStartReloadTask(t *testing.T, baseURL, node, ticket, csrf, query string) string {
	t.Helper()
	u := baseURL + "/api2/json/nodes/" + node + "/network"
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequest(http.MethodPut, u, nil)
	if err != nil {
		t.Fatalf("building reload request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
	req.Header.Set("CSRFPreventionToken", csrf)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reload request status %d: %s", resp.StatusCode, body)
	}
	var envelope struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decoding reload response %s: %v", body, err)
	}
	if envelope.Data == "" {
		t.Fatalf("reload response had empty UPID: %s", body)
	}
	return envelope.Data
}

func TestWaitTask_Success(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	ticket, csrf := rawLogin(t, ts.URL, "root@pam", "vnprox-mock")
	upid := rawStartReloadTask(t, ts.URL, "pve1", ticket, csrf, "")

	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	final, err := c.WaitTask(context.Background(), "pve1", upid, pve.WaitOptions{
		Interval: 5 * time.Millisecond,
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("WaitTask: %v", err)
	}
	if final.ExitStatus != "OK" {
		t.Fatalf("final.ExitStatus = %q, want OK", final.ExitStatus)
	}
}

func TestWaitTask_Failure(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	ticket, csrf := rawLogin(t, ts.URL, "root@pam", "vnprox-mock")
	upid := rawStartReloadTask(t, ts.URL, "pve1", ticket, csrf, "mock_fail=1&mock_fail_reason=ifupdown2%20error")

	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	final, err := c.WaitTask(context.Background(), "pve1", upid, pve.WaitOptions{
		Interval: 5 * time.Millisecond,
		Timeout:  2 * time.Second,
	})
	if err == nil {
		t.Fatalf("expected WaitTask to report the injected failure")
	}
	var failed *pve.ErrPVETaskFailed
	if !errors.As(err, &failed) {
		t.Fatalf("errors.As(err, &failed) failed; got %#v (%v)", err, err)
	}
	if !strings.Contains(failed.ExitStatus, "ifupdown2 error") {
		t.Errorf("failed.ExitStatus = %q, want it to mention the injected reason", failed.ExitStatus)
	}
	// WaitTask still returns the final (failed) status alongside the error.
	if final == nil || final.ExitStatus == "OK" {
		t.Errorf("final = %+v, want a non-nil failed status", final)
	}

	// The rolled-back node should show no pending changes (the mock
	// discards staged edits on a failed reload).
	live, err := c.GetNodeNetworkInterface(context.Background(), "pve1", "vmbr0")
	if err != nil {
		t.Fatalf("GetNodeNetworkInterface: %v", err)
	}
	if live.Pending != pve.PendingNone {
		t.Errorf("live.Pending = %q, want none after a rolled-back failed reload", live.Pending)
	}
}

func TestWaitTask_ClientSideTimeout(t *testing.T) {
	ts := newMockServer(t, fixtureSingleNode)
	ticket, csrf := rawLogin(t, ts.URL, "root@pam", "vnprox-mock")
	// A task that takes far longer than the client-side wait timeout
	// below, proving WaitTask's own Timeout fires rather than the mock
	// ever completing the task within the test's patience.
	upid := rawStartReloadTask(t, ts.URL, "pve1", ticket, csrf, "mock_latency_ms=5000")

	c := newTicketClient(t, ts.URL, "root@pam", "vnprox-mock")
	start := time.Now()
	_, err := c.WaitTask(context.Background(), "pve1", upid, pve.WaitOptions{
		Interval: 5 * time.Millisecond,
		Timeout:  100 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected WaitTask to time out")
	}
	var timeoutErr *pve.ErrPVETaskTimeout
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("errors.As(err, &timeoutErr) failed; got %#v (%v)", err, err)
	}
	if elapsed > time.Second {
		t.Errorf("WaitTask took %s to time out, want well under 1s (its own 100ms timeout should have fired)", elapsed)
	}
}
