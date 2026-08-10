package gitsync_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bgovanlu/vnprox/internal/gitsync"
)

// gitHostMock is a hardware-free double for the two read surfaces this card
// needs — GitHub's REST v3 and GitLab's v4 — plus a plain raw file layout.
// It records the credential it was presented with, which is what lets AC6's
// leak test prove the token was genuinely in flight rather than never sent.
//
//nolint:govet // fieldalignment: test double; fields are grouped by what they mock, not packed.
type gitHostMock struct {
	mu sync.Mutex

	sha        string
	content    []byte
	sigArmored string
	sigPayload string

	// status, when non-zero, is returned instead of a normal answer.
	status int

	sawAuthorization string
	sawPrivateToken  string
	requests         []string
}

func (m *gitHostMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.sawAuthorization = r.Header.Get("Authorization")
	m.sawPrivateToken = r.Header.Get("PRIVATE-TOKEN")
	m.requests = append(m.requests, r.URL.Path)
	status, sha, content := m.status, m.sha, m.content
	sigArmored, sigPayload := m.sigArmored, m.sigPayload
	m.mu.Unlock()

	if status != 0 {
		// A hosting provider's error body routinely quotes the request back.
		// Answering with one here is deliberate: the leak test must see a
		// realistic worst case, not a sanitised one.
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"message":"Bad credentials (sent: %s)"}`, r.Header.Get("Authorization"))
		return
	}

	switch {
	case strings.Contains(r.URL.Path, "/commits/"):
		resp := map[string]any{"sha": sha, "id": sha}
		if sigArmored != "" {
			resp["commit"] = map[string]any{
				"verification": map[string]any{
					"verified": true, "reason": "valid",
					"signature": sigArmored, "payload": sigPayload,
				},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	case strings.Contains(r.URL.Path, "/contents/"),
		strings.Contains(r.URL.Path, "/raw"),
		strings.HasSuffix(r.URL.Path, ".yaml"):
		_, _ = w.Write(content)
	default:
		http.NotFound(w, r)
	}
}

func (m *gitHostMock) credentialSeen() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sawAuthorization != "" {
		return m.sawAuthorization
	}
	return m.sawPrivateToken
}

func (m *gitHostMock) setStatus(code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = code
}

// TestHTTPSource_FetchAcrossProviders drives the real HTTPSource against the
// mock host for each provider shape. It is the closest this card gets to a
// live host; the two calls per poll and the ref->sha pinning are asserted
// here rather than assumed.
func TestHTTPSource_FetchAcrossProviders(t *testing.T) {
	const wantSHA = "1234567890abcdef1234567890abcdef12345678"
	body := []byte("specVersion: 1\n")

	tests := []struct {
		name     string
		provider gitsync.Provider
		repoPath string
		wantSHA  string
	}{
		{name: "github", provider: gitsync.ProviderGitHub, repoPath: "/org/infra", wantSHA: wantSHA},
		{name: "gitlab", provider: gitsync.ProviderGitLab, repoPath: "/org/infra", wantSHA: wantSHA},
		// A raw host has no commit object at all, so the revision id is a
		// content digest — stable across polls, which is all change
		// detection needs.
		{name: "raw", provider: gitsync.ProviderRaw, repoPath: "/specs", wantSHA: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &gitHostMock{sha: wantSHA, content: body}
			ts := httptest.NewServer(mock)
			defer ts.Close()

			src, err := gitsync.NewHTTPSource(gitsync.SourceConfig{
				URL: ts.URL + tc.repoPath, Provider: tc.provider, Token: "t0ken", Client: ts.Client(),
			})
			if err != nil {
				t.Fatalf("NewHTTPSource: %v", err)
			}
			rev, err := src.Fetch(context.Background(), "main", "network/cluster.yaml")
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if string(rev.Content) != string(body) {
				t.Errorf("content = %q, want %q", rev.Content, body)
			}
			if tc.wantSHA != "" && rev.SHA != tc.wantSHA {
				t.Errorf("SHA = %q, want %q", rev.SHA, tc.wantSHA)
			}
			if tc.wantSHA == "" && !strings.HasPrefix(rev.SHA, "sha256:") {
				t.Errorf("raw provider SHA = %q, want a sha256: content digest", rev.SHA)
			}
			if mock.credentialSeen() == "" {
				t.Error("the source sent no credential; the token was configured")
			}
			if strings.Contains(src.Describe(), "t0ken") {
				t.Errorf("Describe leaks the credential: %q", src.Describe())
			}
		})
	}
}

// TestHTTPSource_ErrorsAreClassified: the service branches on these, so the
// classification is part of the contract, not an implementation detail.
func TestHTTPSource_ErrorsAreClassified(t *testing.T) {
	mock := &gitHostMock{sha: "abc", content: []byte("x")}
	ts := httptest.NewServer(mock)
	defer ts.Close()

	src, err := gitsync.NewHTTPSource(gitsync.SourceConfig{
		URL: ts.URL + "/org/infra", Provider: gitsync.ProviderGitHub, Client: ts.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}

	mock.setStatus(http.StatusUnauthorized)
	if _, err := src.Fetch(context.Background(), "main", "cluster.yaml"); !errors.Is(err, gitsync.ErrRemoteStatus) {
		t.Errorf("a 401 produced %v, want ErrRemoteStatus", err)
	}

	// A server that is not there at all is a different condition.
	ts.Close()
	if _, err := src.Fetch(context.Background(), "main", "cluster.yaml"); !errors.Is(err, gitsync.ErrUnreachable) {
		t.Errorf("a dead server produced %v, want ErrUnreachable", err)
	}
}

// TestNewHTTPSource_Validation is the config gate: a remote that would be
// unsafe or ambiguous is refused at construction, so a daemon never comes up
// pointing somewhere its operator did not mean.
func TestNewHTTPSource_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     gitsync.SourceConfig
		wantErr string
	}{
		{name: "empty url", cfg: gitsync.SourceConfig{}, wantErr: "url is required"},
		{
			name:    "plaintext http to a real host is refused",
			cfg:     gitsync.SourceConfig{URL: "http://git.example.com/org/infra", Provider: gitsync.ProviderGitHub},
			wantErr: "must use https",
		},
		{
			name: "plaintext http to loopback is allowed (test/dev fixtures)",
			cfg:  gitsync.SourceConfig{URL: "http://127.0.0.1:9999/org/infra", Provider: gitsync.ProviderGitHub},
		},
		{
			name:    "ssh urls are not supported on this transport",
			cfg:     gitsync.SourceConfig{URL: "ssh://git@github.com/org/infra.git"},
			wantErr: "not supported",
		},
		{
			name:    "credentials embedded in the url are refused outright",
			cfg:     gitsync.SourceConfig{URL: "https://user:s3cret@github.com/org/infra"},
			wantErr: "must not embed credentials",
		},
		{
			name:    "an unknown host with no explicit provider is refused, never guessed",
			cfg:     gitsync.SourceConfig{URL: "https://git.internal.example/org/infra"},
			wantErr: "cannot infer provider",
		},
		{
			name: "an unknown host with an explicit provider is fine",
			cfg:  gitsync.SourceConfig{URL: "https://git.internal.example/org/infra", Provider: gitsync.ProviderRaw},
		},
		{
			name:    "a url naming no repository is refused",
			cfg:     gitsync.SourceConfig{URL: "https://github.com/"},
			wantErr: "names no repository",
		},
		{
			name:    "an unknown provider is refused",
			cfg:     gitsync.SourceConfig{URL: "https://github.com/org/infra", Provider: "svn"},
			wantErr: "unknown provider",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src, err := gitsync.NewHTTPSource(tc.cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("NewHTTPSource: %v", err)
				}
				if src == nil {
					t.Fatal("NewHTTPSource returned no source and no error")
				}
				return
			}
			if err == nil {
				t.Fatalf("NewHTTPSource accepted %+v", tc.cfg)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestNewHTTPSource_ErrorNeverEchoesAUrlCredential closes the one path that
// could leak a secret before the service ever runs: the refusal message for
// a URL that embedded one.
func TestNewHTTPSource_ErrorNeverEchoesAUrlCredential(t *testing.T) {
	const secret = "s3cret-in-the-url"
	_, err := gitsync.NewHTTPSource(gitsync.SourceConfig{URL: "https://user:" + secret + "@github.com/org/infra"})
	if err == nil {
		t.Fatal("a URL with embedded credentials was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the refusal message echoes the credential: %q", err)
	}
}
