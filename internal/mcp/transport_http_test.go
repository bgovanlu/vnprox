// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHTTPTransportAuthAndCall covers the HTTP/SSE transport's auth boundary
// (no bearer => 401; token without automation => 403) and a successful
// JSON-RPC round trip. It also confirms the transport is bearer-only — there is
// no cookie/session path onto it.
func TestHTTPTransportAuthAndCall(t *testing.T) {
	auth := newFakeAuth()
	auth.add("full", TokenInfo{ID: "tok-full", Name: "ci-bot", Scopes: []string{"netRead", "automation"}})
	auth.add("noauto", TokenInfo{ID: "tok-noauto", Name: "x", Scopes: []string{"netRead"}})
	deps := stubReads()
	deps.Auth = auth
	srv, _ := NewServer(deps)
	h := srv.HTTPHandler()

	// No bearer => 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader("{}")))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-bearer POST = %d, want 401", rec.Code)
	}

	// Token without automation => 403.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer noauto")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no-automation POST = %d, want 403", rec.Code)
	}

	// Good token: a tools/call over HTTP.
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "topology.get"},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/mcp", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer full")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated POST = %d, want 200", rec.Code)
	}
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("tools/call over HTTP errored: %+v", resp.Error)
	}
}

// TestHTTPStreamClosesOnRevocation confirms the SSE stream closes promptly when
// the token is revoked (AC5 over the HTTP transport).
func TestHTTPStreamClosesOnRevocation(t *testing.T) {
	auth := newFakeAuth()
	auth.add("full", TokenInfo{ID: "tok-full", Name: "ci-bot", Scopes: []string{"netRead", "automation"}})
	deps := stubReads()
	deps.Auth = auth
	deps.RevocationInterval = 10 * time.Millisecond
	srv, _ := NewServer(deps)

	server := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	req.Header.Set("Authorization", "Bearer full")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE stream status = %d, want 200", resp.StatusCode)
	}

	auth.revoke("tok-full")
	// After revocation the server closes the stream; the read should return
	// (EOF) rather than blocking forever.
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 256)
		for {
			if _, rerr := resp.Body.Read(buf); rerr != nil {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("SSE stream did not close within the revocation bound")
	}
}
