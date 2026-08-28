// SPDX-License-Identifier: Apache-2.0

package peer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
)

// TestClient_DoesNotCancelResponseBodyBeforeItIsRead is T-3702's regression
// test for the defect in planning/reports/audit-2026-08-21-peer-body-cancel.md:
// do() used to wrap each request in its own context.WithTimeout and
// defer cancel() it *before returning* to the caller, so by the time
// decodeInto read the response body the request's context was already
// cancelled. On loopback with small fixtures the transport had already
// buffered the whole body by the time cancel() ran, so the race resolved
// benignly every time -- which is exactly why no existing test caught this
// in production, where the largest peer payload (host links) reliably hit
// it (17,197 warnings/24h).
//
// Rather than lean on a body large enough to still be streaming (which is
// real but timing/machine-dependent), this test uses an explicit
// synchronisation point: the stub server flushes the first half of the
// response, so httpClient.Do() returns as soon as headers arrive, then
// sleeps before writing the rest. That guarantees the body is still
// streaming at the moment do() returns -- deterministically, not
// probabilistically -- which is exactly when the old code's deferred
// cancel() fired.
func TestClient_DoesNotCancelResponseBodyBeforeItIsRead(t *testing.T) {
	links := make([]host.LinkState, 200)
	for i := range links {
		links[i] = host.LinkState{
			Name:      "eth0",
			Kind:      "physical",
			OperState: "up",
			Mac:       "00:11:22:33:44:55",
			Driver:    "ixgbe",
		}
	}
	data, err := json.Marshal(linksResponse{Links: links})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	const streamDelay = 60 * time.Millisecond

	mux := http.NewServeMux()
	mux.HandleFunc("/api/peer/host/links", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		half := len(data) / 2
		if _, writeErr := w.Write(data[:half]); writeErr != nil {
			return
		}
		flusher.Flush() // headers + partial body land now -- Do() can return
		time.Sleep(streamDelay)
		_, _ = w.Write(data[half:]) // rest of the body streams in after Do() has already returned
	})
	stub := httptest.NewServer(mux)
	t.Cleanup(stub.Close)

	// RequestTimeout must comfortably exceed streamDelay so the *fixed*
	// client's http.Client.Timeout (which legitimately bounds the whole
	// call, body read included) doesn't itself cause a false failure.
	c := NewClient(ClientOptions{
		Secrets:        newStaticSecretStore(testSecret),
		Scheme:         "http",
		Logger:         discardLogger(),
		RequestTimeout: 2 * time.Second,
	})
	p := Peer{Node: "pve1", Addr: stub.Listener.Addr().String()}

	got, err := c.Links(context.Background(), p, "pve1")
	if err != nil {
		t.Fatalf("Links: %v (want the streamed body to decode successfully; a defect that cancels the request context before the body is read surfaces this as \"context canceled\")", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("Links returned context.Canceled: %v -- the response body was read from a context already cancelled by the caller's *own* client, not by the caller", err)
	}
	if len(got) != len(links) {
		t.Fatalf("Links returned %d entries, want %d", len(got), len(links))
	}
}
