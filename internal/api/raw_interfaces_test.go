package api

import (
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

// stubRawNodeAgent is a minimal change.NodeAgent test double for this
// file's raw-editor route tests; only ReadInterfaces is ever exercised
// through these routes.
type stubRawNodeAgent struct{ content string }

func (a stubRawNodeAgent) ReadInterfaces(context.Context, string) (string, error) {
	return a.content, nil
}
func (a stubRawNodeAgent) StageInterfaces(context.Context, string, string) error { return nil }
func (a stubRawNodeAgent) ReloadInterfaces(context.Context, string) error        { return nil }
func (a stubRawNodeAgent) DiscardStaged(context.Context, string) error           { return nil }

func newRawInterfacesTestService(t *testing.T, nodes change.NodeAgent) *change.Service {
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
	svc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Nodes:      nodes,
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

const rawInterfacesFixture = "auto lo\niface lo inet loopback\n"

func TestGetRawInterfaces_OK(t *testing.T) {
	svc := newRawInterfacesTestService(t, stubRawNodeAgent{content: rawInterfacesFixture})
	router := newChangesetTestRouter(svc, fullCapsAuth("alice@pam"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/pve1/interfaces/raw", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp rawInterfacesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.Node != "pve1" || resp.Content != rawInterfacesFixture {
		t.Errorf("resp = %+v", resp)
	}
	if resp.SHA256 == "" {
		t.Errorf("SHA256 is empty")
	}
}

func TestGetRawInterfaces_ReadFailure(t *testing.T) {
	svc := newRawInterfacesTestService(t, nil) // no NodeAgent configured -> ReadRawInterfaces errors
	router := newChangesetTestRouter(svc, fullCapsAuth("alice@pam"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/pve1/interfaces/raw", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestGetRawInterfaces_RequiresNetRead(t *testing.T) {
	svc := newRawInterfacesTestService(t, stubRawNodeAgent{content: rawInterfacesFixture})
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "auditor@pam"},
		caps:             map[string]bool{},
	}
	router := newChangesetTestRouter(svc, auth)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/pve1/interfaces/raw", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
}

func TestLintInterfaces_OK(t *testing.T) {
	svc := newRawInterfacesTestService(t, nil)
	router := newChangesetTestRouter(svc, fullCapsAuth("alice@pam"))

	body := `{"content":"auto lo\niface lo inet loopback\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/interfaces/lint", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp lintInterfacesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(resp.Errors) != 0 {
		t.Errorf("Errors = %+v, want none", resp.Errors)
	}
}

func TestLintInterfaces_SyntaxError(t *testing.T) {
	svc := newRawInterfacesTestService(t, nil)
	router := newChangesetTestRouter(svc, fullCapsAuth("alice@pam"))

	body := `{"content":"not a valid line"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/interfaces/lint", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp lintInterfacesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Line != 1 {
		t.Fatalf("Errors = %+v, want one marker on line 1", resp.Errors)
	}
}
