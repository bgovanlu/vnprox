// SPDX-License-Identifier: Apache-2.0

package gitsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Provider names the read surface a remote speaks. It is deliberately a
// small closed set rather than sniffed per-request: an operator states what
// they are pointing at, and an unrecognised value is a config error at
// startup rather than a surprise on the first poll.
type Provider string

const (
	// ProviderGitHub speaks GitHub's REST v3 read surface (github.com or a
	// GitHub Enterprise host). It is the one provider that returns the raw
	// signed commit object, so it is the one that can carry a locally
	// verifiable signature.
	ProviderGitHub Provider = "github"
	// ProviderGitLab speaks GitLab's REST v4 read surface. GitLab reports a
	// commit's signature *status* but not the signed payload, so a GitLab
	// source never supplies locally verifiable signature material — with
	// require_signed_commits set it fails closed (ErrUnverifiableSignature),
	// which is the documented, intended behaviour rather than a silent pass.
	ProviderGitLab Provider = "gitlab"
	// ProviderRaw treats the configured URL as a directory base and fetches
	// "<base>/<ref>/<path>" — the shape a plain static file server, a Gitea
	// raw route, or an internal artifact host presents. There is no commit
	// object at all on this path, so the revision id is a content digest and
	// require_signed_commits fails closed.
	ProviderRaw Provider = "raw"
)

// maxSpecBytes bounds a fetched document. It is the same 4 MiB headroom
// internal/api's maxSpecBodyBytes allows a spec upload — a whole-cluster
// spec is tens of KB — and it exists so a hostile or misconfigured remote
// cannot stream the daemon out of memory.
const maxSpecBytes = 4 << 20

// CommitSignature is the material needed to verify a commit signature
// locally: the exact bytes git signed (the commit object with its own
// signature header removed) and the armored signature block. Both come from
// the host; neither is trusted, because the verification is done here
// against the operator's own allowed-signers file.
//
//nolint:govet // fieldalignment: payload first, then the signature over it — the order documents what verifies what.
type CommitSignature struct {
	Payload []byte
	Armored string
}

// Revision is one resolved read of one file at one ref.
//
//nolint:govet // fieldalignment: field order documents the shape, not packing.
type Revision struct {
	// SHA identifies this revision for change detection. For a git-aware
	// provider it is the commit sha; for ProviderRaw it is "sha256:<hex>"
	// over the content, because there is no commit to name.
	SHA string
	// Path is the path within the repository that was read.
	Path string
	// Content is the fetched document.
	Content []byte
	// Signature is the commit's signature material, or nil when the host
	// reported none (or cannot report it at all).
	Signature *CommitSignature
}

// ContentDigest is a stable hex digest of the fetched content, used
// alongside SHA so an unchanged spec under a moved branch head is still
// recognised as unchanged (T-2701 AC2's "no second draft, no store write").
func (r Revision) ContentDigest() string {
	sum := sha256.Sum256(r.Content)
	return hex.EncodeToString(sum[:])
}

// Source resolves a ref+path to a Revision.
//
// It is READ-ONLY BY DESIGN, and stays that way: T-2702's changeset→PR path
// did not extend this interface, it declared a SIBLING one (host.go's Host).
// Keeping them apart is what makes "vnprox never pushes on the sync path" a
// property of the type system — the sync Service holds a Source and there is
// no method on it that could write, so an applying/pushing sync cannot be
// written without editing this file. A reflection test (propose_test.go's
// TestSeamsStaySeparate) asserts the omission in both directions.
type Source interface {
	// Describe returns a human-readable, credential-free description of
	// where this source reads from. It is rendered into findings and into
	// `vnproxctl gitsync status`, so it must never contain a token.
	Describe() string
	// Fetch reads path at ref. It must return an error wrapping
	// ErrUnreachable for a transport failure and ErrRemoteStatus for a
	// non-2xx answer, so the service can tell them apart.
	Fetch(ctx context.Context, ref, path string) (Revision, error)
}

// HTTPSource is the plain-HTTPS Source: two ordinary GETs per poll (resolve
// the ref, then read the file at the resolved sha), no git binary and no git
// library — see this package's doc comment for why.
//
// The credential is held here and only ever leaves as a request header. It
// is never placed in a URL (URLs are logged by proxies and appear in error
// text), never returned by Describe, and never included in any error this
// type produces: nothing in this file formats c.token into anything.
type HTTPSource struct {
	// httpTransport carries the client, the provider and the read
	// credential (hosturl.go). It is embedded rather than duplicated so the
	// "never echo a response body into an error" property lives in exactly
	// one place.
	httpTransport
	// apiBase is the provider's API root for this repository, already
	// resolved from the operator's URL — e.g.
	// "https://api.github.com/repos/org/infra".
	apiBase string
	// describe is the credential-free origin string.
	describe string
}

// SourceConfig configures an HTTPSource.
//
//nolint:govet // fieldalignment: field order matches the [gitsync] config section it is built from, not packing.
type SourceConfig struct {
	// URL is the repository URL as the operator wrote it, e.g.
	// "https://github.com/org/infra". For ProviderRaw it is a directory base.
	URL string
	// Provider selects the read surface. Empty infers from the host
	// (github.com -> github, gitlab.com -> gitlab, anything else -> an
	// error, because guessing an API shape from an unknown host is how a
	// sync silently reads the wrong thing).
	Provider Provider
	// Token is the credential, already read from disk by the caller. Empty
	// means an unauthenticated (public) read.
	Token string
	// Client overrides the HTTP client, for tests. Nil builds one with a
	// bounded timeout.
	Client *http.Client
}

// NewHTTPSource validates cfg and builds a Source. It performs no I/O: a
// misconfigured remote is a startup error, not a first-poll surprise.
func NewHTTPSource(cfg SourceConfig) (*HTTPSource, error) {
	rem, err := resolveRemote(cfg.URL, cfg.Provider, "token_file")
	if err != nil {
		return nil, err
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPSource{
		httpTransport: httpTransport{client: client, provider: rem.provider, token: cfg.Token},
		apiBase:       rem.apiBase,
		describe:      rem.describe,
	}, nil
}

// Describe implements Source.
func (s *HTTPSource) Describe() string { return s.describe }

// Fetch implements Source: resolve ref to a commit, then read path at that
// exact commit. Reading at the resolved sha rather than at the branch name
// is what makes the two calls describe one consistent point even if the
// branch moves between them.
func (s *HTTPSource) Fetch(ctx context.Context, ref, path string) (Revision, error) {
	switch s.provider {
	case ProviderGitHub:
		return s.fetchGitHub(ctx, ref, path)
	case ProviderGitLab:
		return s.fetchGitLab(ctx, ref, path)
	case ProviderRaw:
		return s.fetchRaw(ctx, ref, path)
	default:
		return Revision{}, fmt.Errorf("gitsync: unknown provider %q", s.provider)
	}
}

// get issues one GET through the shared transport (hosturl.go), which is
// where the "an error never carries the response body or the credential"
// property lives.
func (s *HTTPSource) get(ctx context.Context, rawURL, accept string) ([]byte, error) {
	return s.do(ctx, request{method: http.MethodGet, url: rawURL, accept: accept})
}

// --- GitHub ---------------------------------------------------------------

type githubCommitResponse struct {
	SHA    string `json:"sha"`
	Commit struct {
		Verification struct {
			Signature string `json:"signature"`
			Payload   string `json:"payload"`
		} `json:"verification"`
	} `json:"commit"`
}

func (s *HTTPSource) fetchGitHub(ctx context.Context, ref, path string) (Revision, error) {
	body, err := s.get(ctx, s.apiBase+"/commits/"+url.PathEscape(ref), "application/vnd.github+json")
	if err != nil {
		return Revision{}, err
	}
	var commit githubCommitResponse
	if decErr := json.Unmarshal(body, &commit); decErr != nil {
		return Revision{}, fmt.Errorf("gitsync: decoding commit for ref %q: %w", ref, decErr)
	}
	if commit.SHA == "" {
		return Revision{}, fmt.Errorf("gitsync: commit for ref %q carries no sha", ref)
	}

	content, err := s.get(ctx,
		s.apiBase+"/contents/"+escapePath(path)+"?ref="+url.QueryEscape(commit.SHA),
		"application/vnd.github.raw")
	if err != nil {
		return Revision{}, err
	}

	rev := Revision{SHA: commit.SHA, Path: path, Content: content}
	if sig := commit.Commit.Verification; sig.Signature != "" {
		rev.Signature = &CommitSignature{Payload: []byte(sig.Payload), Armored: sig.Signature}
	}
	return rev, nil
}

// --- GitLab ---------------------------------------------------------------

type gitlabCommitResponse struct {
	ID string `json:"id"`
}

func (s *HTTPSource) fetchGitLab(ctx context.Context, ref, path string) (Revision, error) {
	body, err := s.get(ctx, s.apiBase+"/repository/commits/"+url.PathEscape(ref), "application/json")
	if err != nil {
		return Revision{}, err
	}
	var commit gitlabCommitResponse
	if decErr := json.Unmarshal(body, &commit); decErr != nil {
		return Revision{}, fmt.Errorf("gitsync: decoding commit for ref %q: %w", ref, decErr)
	}
	if commit.ID == "" {
		return Revision{}, fmt.Errorf("gitsync: commit for ref %q carries no id", ref)
	}

	content, err := s.get(ctx,
		s.apiBase+"/repository/files/"+url.PathEscape(path)+"/raw?ref="+url.QueryEscape(commit.ID),
		"application/json")
	if err != nil {
		return Revision{}, err
	}
	// Signature stays nil: GitLab exposes a commit's verification *status*
	// but not the signed payload, so there is nothing to verify locally.
	// require_signed_commits therefore fails closed against a GitLab source
	// rather than trusting the host's own boolean.
	return Revision{SHA: commit.ID, Path: path, Content: content}, nil
}

// --- raw ------------------------------------------------------------------

func (s *HTTPSource) fetchRaw(ctx context.Context, ref, path string) (Revision, error) {
	content, err := s.get(ctx, s.apiBase+"/"+url.PathEscape(ref)+"/"+escapePath(path), "")
	if err != nil {
		return Revision{}, err
	}
	rev := Revision{Path: path, Content: content}
	rev.SHA = "sha256:" + rev.ContentDigest()
	return rev, nil
}
