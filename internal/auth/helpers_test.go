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

// fixturePermissionsIdentity decorates a real PVEIdentity (its Login/Renew/
// ClusterNodes talk to a live pvemock server over real HTTP) with a
// Permissions method sourced directly from the fixture's flat
// UserSpec.Privileges list, standing in for the GET /access/permissions
// endpoint internal/pvemock does not implement — see internal/auth's
// doc.go for why, and T-105's completion report for the full reasoning.
type fixturePermissionsIdentity struct {
	auth.PVEIdentity
	privs []string
}

func (f fixturePermissionsIdentity) Permissions(context.Context) (pve.Permissions, error) {
	m := make(map[string]bool, len(f.privs))
	for _, p := range f.privs {
		m[p] = true
	}
	return pve.Permissions{"/": m}, nil
}

// fixtureIdentityFactory builds the real production ticket-mode identity
// factory (every Login/Renew/ClusterNodes call is real HTTP against ts)
// and wraps it so Permissions comes from the fixture instead of a live
// mock endpoint that doesn't exist.
func fixtureIdentityFactory(ts *httptest.Server, f *pvemock.Fixture, renewAfter time.Duration) auth.IdentityFactory {
	real := auth.NewClientIdentityFactory(pve.Config{
		APIURL:           ts.URL,
		HTTPClient:       ts.Client(),
		TicketRenewAfter: renewAfter,
	})
	return func(username, password, realm, otp string) (auth.PVEIdentity, error) {
		id, err := real(username, password, realm, otp)
		if err != nil {
			return nil, err
		}
		return fixturePermissionsIdentity{PVEIdentity: id, privs: privilegesForUser(f, username, realm)}, nil
	}
}

func privilegesForUser(f *pvemock.Fixture, username, realm string) []string {
	full := username
	if realm != "" && !containsAt(username) {
		full = username + "@" + realm
	}
	for _, u := range f.Users {
		if u.UserID == full {
			return u.Privileges
		}
	}
	return nil
}

func containsAt(s string) bool {
	for _, c := range s {
		if c == '@' {
			return true
		}
	}
	return false
}

// stubIdentity is a PVEIdentity that never touches the network, for tests
// that only need to exercise this package's own handler/middleware logic
// (validation, cookies, CSRF, rate limiting) — not internal/pve or
// internal/pvemock. Its Login checks an expected OTP exactly the way a
// real PVE realm with a second factor configured would (rejecting at the
// ticket-login call itself, not at client construction), which is what
// lets TestHandleLogin_OTPPassthrough exercise the OTP code path honestly
// per T-105's task card (see internal/auth's doc.go).
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
