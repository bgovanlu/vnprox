// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

const (
	fixtureSingleNode = "../../testdata/clusters/single-node.yaml"
	fixtureThreeNode  = "../../testdata/clusters/three-node-vlan.yaml"
)

// newTestStore opens a fresh SQLite-backed store.DB + SessionCipher in a
// temp directory, returning ready-to-use repositories. The DB is closed
// automatically via t.Cleanup.
func newTestStore(t *testing.T) (*store.SessionRepo, *store.AuditRepo, *store.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key := make([]byte, store.KeySize) // all-zero key: fine for tests, never for production.
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		t.Fatalf("store.NewSessionCipher: %v", err)
	}
	return store.NewSessionRepo(db, cipher), store.NewAuditRepo(db), db
}

// newMockServer starts an httptest.Server wrapping internal/pvemock loaded
// from fixturePath, mirroring internal/pve's own integration test helper
// (internal/pve/integration_test.go).
func newMockServer(t *testing.T, fixturePath string) (*httptest.Server, *pvemock.Fixture) {
	t.Helper()
	f, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, f
}

// fixtureIdentityFactory builds the real production ticket-mode identity
// factory against a live pvemock server: every Login/Renew/Permissions/
// ClusterNodes call is real HTTP against ts, including GET
// /access/permissions (which internal/pvemock implements by reporting the
// fixture user's flat privilege list at path "/"). This is the exact
// production derivation path, mock server aside.
func fixtureIdentityFactory(ts *httptest.Server, renewAfter time.Duration) auth.IdentityFactory {
	return auth.NewClientIdentityFactory(pve.Config{
		APIURL:           ts.URL,
		HTTPClient:       ts.Client(),
		TicketRenewAfter: renewAfter,
	})
}

// stubIdentity is a PVEIdentity that never touches the network, for tests
// that only need to exercise this package's own handler/middleware logic
// (validation, cookies, CSRF, rate limiting) — not internal/pve or
// internal/pvemock. Its Login checks an expected OTP the way a real PVE
// realm with a second factor configured would (rejecting at the
// ticket-login call itself, not at client construction), which lets
// TestHandleLogin_OTPPassthrough unit test the handler's OTP forwarding
// in isolation; the end-to-end flow against a real mock TOTP user is
// TestIntegration_TOTPLoginAgainstMock.
type stubIdentity struct {
	nodesErr    error
	renewErr    error
	perms       pve.Permissions
	wantOTP     string
	gotOTP      string
	ticket      string
	csrf        string
	renewTicket string
	renewCSRF   string
	nodes       []string
}

func (s *stubIdentity) Login(context.Context) (string, string, error) {
	if s.wantOTP != "" && s.gotOTP != s.wantOTP {
		return "", "", &pve.ErrPVEAuth{Message: "missing or invalid second factor"}
	}
	return s.ticket, s.csrf, nil
}

func (s *stubIdentity) Renew(context.Context) (string, string, error) {
	if s.renewErr != nil {
		return "", "", s.renewErr
	}
	if s.renewTicket != "" {
		return s.renewTicket, s.renewCSRF, nil
	}
	return s.ticket, s.csrf, nil
}

func (s *stubIdentity) Permissions(context.Context) (pve.Permissions, error) {
	return s.perms, nil
}

func (s *stubIdentity) ClusterNodes(context.Context) ([]string, error) {
	if s.nodesErr != nil {
		return nil, s.nodesErr
	}
	return s.nodes, nil
}

// stubFactory returns an auth.IdentityFactory that always hands back
// template (a *stubIdentity value, copied per call so concurrent logins
// don't share state), with the submitted OTP recorded on the copy so
// Login can check it.
func stubFactory(template stubIdentity) auth.IdentityFactory {
	return func(_, _, _, otp string) (auth.PVEIdentity, error) {
		id := template
		id.gotOTP = otp
		return &id, nil
	}
}
