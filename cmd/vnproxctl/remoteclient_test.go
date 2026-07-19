package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestFlagSet builds a discard-output FlagSet for tests that exercise
// addRemoteFlags/buildRemoteClient directly, without going through a full
// runRemote* command (which would print its own usage on a parse error).
func newTestFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func TestApiBaseFromListen(t *testing.T) {
	tests := []struct {
		listen string
		want   string
	}{
		{"127.0.0.1:8007", "https://127.0.0.1:8007/api/v1"},
		{"0.0.0.0:8007", "https://127.0.0.1:8007/api/v1"},
		{":8007", "https://127.0.0.1:8007/api/v1"},
	}
	for _, tt := range tests {
		got, err := apiBaseFromListen(tt.listen)
		if err != nil {
			t.Fatalf("apiBaseFromListen(%q): %v", tt.listen, err)
		}
		if got != tt.want {
			t.Errorf("apiBaseFromListen(%q) = %q, want %q", tt.listen, got, tt.want)
		}
	}
}

func TestApiBaseFromListen_Invalid(t *testing.T) {
	if _, err := apiBaseFromListen("not-a-valid-listen-address"); err == nil {
		t.Fatal("expected an error for a malformed listen address")
	}
}

func newTestRemoteClient(t *testing.T, srv *httptest.Server, token string) *remoteClient {
	t.Helper()
	return &remoteClient{
		http:    srv.Client(),
		baseURL: srv.URL,
		token:   token,
	}
}

func TestDoJSON_Success(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("Authorization header = %q, want Bearer secret-token", got)
		}
		if r.URL.Path != "/widgets" {
			t.Errorf("path = %q, want /widgets", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "gizmo"})
	}))
	defer srv.Close()

	client := newTestRemoteClient(t, srv, "secret-token")
	var out map[string]string
	status, apiErr, err := client.doJSON(context.Background(), "GET", "/widgets", nil, &out)
	if err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if apiErr != nil {
		t.Fatalf("unexpected apiErr: %+v", apiErr)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if out["name"] != "gizmo" {
		t.Errorf("out = %+v, want name=gizmo", out)
	}
}

func TestDoJSON_ErrorEnvelope(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "forbidden", "message": "missing required capability: netWrite"},
		})
	}))
	defer srv.Close()

	client := newTestRemoteClient(t, srv, "tok")
	status, apiErr, err := client.doJSON(context.Background(), "POST", "/changesets", map[string]string{"title": "x"}, nil)
	if err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if apiErr == nil {
		t.Fatal("expected a non-nil apiErr")
	}
	if apiErr.Code != "forbidden" {
		t.Errorf("apiErr.Code = %q, want forbidden", apiErr.Code)
	}
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
	if exitForAPIError(status) != ExitAuth {
		t.Errorf("exitForAPIError(403) = %d, want ExitAuth", exitForAPIError(status))
	}
}

func TestDoJSON_ValidationFailedMapsToExitPending(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "validation_failed", "message": "blocking findings"},
		})
	}))
	defer srv.Close()

	client := newTestRemoteClient(t, srv, "tok")
	status, apiErr, err := client.doJSON(context.Background(), "POST", "/changesets/x/apply", nil, nil)
	if err != nil || apiErr == nil {
		t.Fatalf("doJSON: err=%v apiErr=%v", err, apiErr)
	}
	if exitForAPIError(status) != ExitPending {
		t.Errorf("exitForAPIError(422) = %d, want ExitPending", exitForAPIError(status))
	}
}

func TestDoJSON_NetworkErrorMapsToExitNetwork(t *testing.T) {
	client := &remoteClient{
		http:    &http.Client{Timeout: 200 * time.Millisecond},
		baseURL: "https://127.0.0.1:1", // nothing listens here
		token:   "tok",
	}
	_, apiErr, err := client.doJSON(context.Background(), "GET", "/topology", nil, nil)
	if err == nil {
		t.Fatal("expected a network error")
	}
	if apiErr != nil {
		t.Fatalf("unexpected apiErr: %+v", apiErr)
	}
	if !isNetworkError(err) {
		t.Errorf("isNetworkError(%v) = false, want true", err)
	}
	if exitForErr(err) != ExitNetwork {
		t.Errorf("exitForErr = %d, want ExitNetwork", exitForErr(err))
	}
}

func TestBuildRemoteClient_NoTokenFailsFastWithoutDialing(t *testing.T) {
	dialed := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialed = true
	}))
	defer srv.Close()

	t.Setenv("VNPROX_TOKEN", "")
	fs := newTestFlagSet()
	rf := addRemoteFlags(fs)
	if err := fs.Parse([]string{"--url", srv.URL}); err != nil {
		t.Fatalf("parsing flags: %v", err)
	}

	var stderr bytes.Buffer
	client, code := buildRemoteClient(rf, "vnproxctl remote test", &stderr)
	if client != nil {
		t.Fatal("expected a nil client with no token")
	}
	if code != ExitAuth {
		t.Errorf("exit code = %d, want ExitAuth", code)
	}
	if dialed {
		t.Error("no daemon call should have been attempted before the token check")
	}
	if !strings.Contains(stderr.String(), "VNPROX_TOKEN") {
		t.Errorf("stderr = %q, want it to mention VNPROX_TOKEN", stderr.String())
	}
}

func TestBuildRemoteClient_TokenFromEnv(t *testing.T) {
	t.Setenv("VNPROX_TOKEN", "env-token")
	fs := newTestFlagSet()
	rf := addRemoteFlags(fs)
	if err := fs.Parse([]string{"--url", "https://example.invalid/api/v1"}); err != nil {
		t.Fatalf("parsing flags: %v", err)
	}
	var stderr bytes.Buffer
	client, code := buildRemoteClient(rf, "vnproxctl remote test", &stderr)
	if client == nil {
		t.Fatalf("expected a client, got nil (code %d, stderr %s)", code, stderr.String())
	}
	if client.token != "env-token" {
		t.Errorf("token = %q, want env-token", client.token)
	}
}

func TestBuildRemoteClient_ExplicitTokenWinsOverEnv(t *testing.T) {
	t.Setenv("VNPROX_TOKEN", "env-token")
	fs := newTestFlagSet()
	rf := addRemoteFlags(fs)
	if err := fs.Parse([]string{"--url", "https://example.invalid/api/v1", "--token", "flag-token"}); err != nil {
		t.Fatalf("parsing flags: %v", err)
	}
	var stderr bytes.Buffer
	client, _ := buildRemoteClient(rf, "vnproxctl remote test", &stderr)
	if client == nil || client.token != "flag-token" {
		t.Fatalf("token = %+v, want flag-token", client)
	}
}
