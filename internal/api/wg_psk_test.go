// SPDX-License-Identifier: Apache-2.0

package api

// Finding 1 (T-1401 adversarial review) API coverage: a wg.peer.add op's
// preshared key is sealed on ingest and never echoed on any changeset read
// surface — not the create response, not GET /changesets/{id}. The op itself
// survives redaction (only the secret is stripped).

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

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

func newChangesetTestServiceWithSealer(t *testing.T) *change.Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})
	key := make([]byte, store.KeySize)
	for i := range key {
		key[i] = byte(i + 7)
	}
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		t.Fatalf("store.NewSessionCipher: %v", err)
	}
	svc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Sealer:     cipher,
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

// TestChangesets_WgPeerPresharedKeyNotEchoed is Finding 1's regression (a):
// GET /changesets (and the create response) never return the preshared key —
// neither the plaintext nor any presharedKey* field — for a wg.peer.add op.
func TestChangesets_WgPeerPresharedKeyNotEchoed(t *testing.T) {
	svc := newChangesetTestServiceWithSealer(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	const psk = "cGxhaW50ZXh0LXByZXNoYXJlZC1rZXktc2VjcmV0LXZhbHVl"
	body := `{"title":"add peer","ops":[{"op":"wg.peer.add","target":"wg-peer:pve1:tun1/cGVlcg==","params":{"publicKey":"cGVlcg==","presharedKey":"` + psk + `","allowedIps":["10.0.0.2/32"]}}]}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /changesets status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), psk) {
		t.Fatalf("create response leaked the plaintext preshared key: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "presharedKey") {
		t.Fatalf("create response carried a preshared-key field: %s", rec.Body.String())
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("could not read created changeset id: err=%v body=%s", err, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /changesets/{id} status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	got := getRec.Body.String()
	if strings.Contains(got, psk) {
		t.Fatalf("GET /changesets leaked the plaintext preshared key: %s", got)
	}
	// Catches both the write-only "presharedKey" and the sealed "presharedKeyEnc".
	if strings.Contains(got, "presharedKey") {
		t.Fatalf("GET /changesets carried a preshared-key field (plaintext or sealed): %s", got)
	}
	// Redaction is targeted: the wg.peer.add op and its non-secret fields remain.
	if !strings.Contains(got, "wg.peer.add") || !strings.Contains(got, `"publicKey":"cGVlcg=="`) {
		t.Fatalf("GET /changesets dropped the wg.peer.add op or its public key: %s", got)
	}
}
