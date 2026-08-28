// SPDX-License-Identifier: Apache-2.0

package telemetrycollector

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/telemetry"
)

const testInstallID = "01HZY0Z1QW8V9N7M3K5R2T4B6D"

func newTestServer(t *testing.T, opts ...Option) (*Server, *Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(context.Background(), filepath.Join(dir, "telemetry.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := NewServer(store, opts...)
	return srv, store
}

func validPayloadBytes(t *testing.T) []byte {
	t.Helper()
	p := samplePayload(testInstallID)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestHandleSubmit_Accepts(t *testing.T) {
	srv, store := newTestServer(t)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/submissions", telemetry.ContentType, bytes.NewReader(validPayloadBytes(t)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	n, err := store.Count(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("stored count = %d, %v", n, err)
	}
}

func TestHandleSubmit_RejectsUnknownPayloadVersion(t *testing.T) {
	srv, store := newTestServer(t)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	p := samplePayload(testInstallID)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Bump payloadVersion to something this build does not recognise.
	mutated := strings.Replace(string(raw), `"payloadVersion":1`, `"payloadVersion":999`, 1)
	if mutated == string(raw) {
		t.Fatalf("fixture JSON did not contain the expected payloadVersion literal — adjust the replace")
	}

	resp, err := http.Post(ts.URL+"/v1/submissions", telemetry.ContentType, strings.NewReader(mutated))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}
	if body.Error.Code != "unsupported_payload_version" {
		t.Fatalf("error code = %q, want %q (an unrecognised version must be refused explicitly, not guessed at)", body.Error.Code, "unsupported_payload_version")
	}

	if n, err := store.Count(context.Background()); err != nil || n != 0 {
		t.Fatalf("a rejected submission must not be stored: count=%d err=%v", n, err)
	}
}

func TestHandleSubmit_RejectsUnknownField(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	raw := validPayloadBytes(t)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["hostname"] = "pve1.example.com" // exactly what the closed schema exists to catch
	mutated, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, err := http.Post(ts.URL+"/v1/submissions", telemetry.ContentType, bytes.NewReader(mutated))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleSubmit_RejectsOversizedBody(t *testing.T) {
	srv, _ := newTestServer(t, WithMaxBodyBytes(16))
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/submissions", telemetry.ContentType, bytes.NewReader(validPayloadBytes(t)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestHandleSubmit_RejectsEmptyBody(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/submissions", telemetry.ContentType, bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleSubmit_PerInstallRateLimit(t *testing.T) {
	srv, _ := newTestServer(t, WithPerInstallRateLimit(1, time.Hour))
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	post := func() int {
		resp, err := http.Post(ts.URL+"/v1/submissions", telemetry.ContentType, bytes.NewReader(validPayloadBytes(t)))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	if got := post(); got != http.StatusCreated {
		t.Fatalf("first submission status = %d, want 201", got)
	}
	if got := post(); got != http.StatusTooManyRequests {
		t.Fatalf("second submission status = %d, want 429", got)
	}
}

func TestHandleSubmit_GlobalRateLimit(t *testing.T) {
	srv, _ := newTestServer(t, WithGlobalRateLimit(1, time.Hour), WithPerInstallRateLimit(100, time.Second))
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	post := func(installID string) int {
		p := samplePayload(installID)
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		resp, err := http.Post(ts.URL+"/v1/submissions", telemetry.ContentType, bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	if got := post(testInstallID); got != http.StatusCreated {
		t.Fatalf("first submission status = %d, want 201", got)
	}
	// A DIFFERENT install-id must still be refused: the global bucket has
	// no key, so per-install headroom does not help.
	if got := post("01HZY0Z1QW8V9N7M3K5R2T4B6E"); got != http.StatusTooManyRequests {
		t.Fatalf("second submission (different install) status = %d, want 429", got)
	}
}

func TestHandleSummary(t *testing.T) {
	srv, store := newTestServer(t)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	if err := store.Insert(context.Background(), samplePayload(testInstallID), time.Unix(1000, 0)); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	resp, err := http.Get(ts.URL + "/v1/summary")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var sum Summary
	if err := json.NewDecoder(resp.Body).Decode(&sum); err != nil {
		t.Fatalf("decoding summary: %v", err)
	}
	if sum.TotalSubmissions != 1 {
		t.Fatalf("TotalSubmissions = %d, want 1", sum.TotalSubmissions)
	}
}

func TestHandleDelete_RevokesAndIsIdempotent(t *testing.T) {
	srv, store := newTestServer(t)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	ctx := context.Background()
	if err := store.Insert(ctx, samplePayload(testInstallID), time.Unix(1000, 0)); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := store.Insert(ctx, samplePayload(testInstallID), time.Unix(1001, 0)); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/v1/installs/"+testInstallID, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Deleted int64 `json:"deleted"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding delete response: %v", err)
	}
	if body.Deleted != 2 {
		t.Fatalf("deleted = %d, want 2", body.Deleted)
	}

	var n int64
	if n, err = store.Count(ctx); err != nil || n != 0 {
		t.Fatalf("count after revocation = %d, %v", n, err)
	}

	// Second delete is idempotent, not an error.
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("second DELETE: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second delete status = %d, want 200", resp2.StatusCode)
	}
}

func TestHandleDelete_RejectsNonULID(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/v1/installs/not-a-ulid", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
