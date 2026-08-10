package gitsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
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

// Source resolves a ref+path to a Revision. It is an interface so T-2702's
// changeset→PR path can add a write-capable implementation beside this one
// without every caller learning about hosts, and so tests can drive the
// service without any network at all.
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
	client   *http.Client
	provider Provider
	// apiBase is the provider's API root for this repository, already
	// resolved from the operator's URL — e.g.
	// "https://api.github.com/repos/org/infra".
	apiBase string
	// describe is the credential-free origin string.
	describe string
	token    string
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
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("gitsync: url is required")
	}
	u, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil {
		return nil, fmt.Errorf("gitsync: parsing url: %w", err)
	}
	if secErr := checkTransportSecurity(u); secErr != nil {
		return nil, secErr
	}
	if u.User != nil {
		// A credential in the URL would end up in every log line, finding
		// and status output that names the origin. Refuse it outright and
		// point at the key that exists for this.
		return nil, fmt.Errorf("gitsync: url must not embed credentials; use [gitsync] token_file")
	}

	provider := cfg.Provider
	if provider == "" {
		provider, err = inferProvider(u.Hostname())
		if err != nil {
			return nil, err
		}
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	src := &HTTPSource{client: client, provider: provider, token: cfg.Token}
	switch provider {
	case ProviderGitHub:
		src.apiBase, err = githubAPIBase(u)
	case ProviderGitLab:
		src.apiBase, err = gitlabAPIBase(u)
	case ProviderRaw:
		src.apiBase = strings.TrimSuffix(u.String(), "/")
	default:
		return nil, fmt.Errorf("gitsync: unknown provider %q (want github, gitlab or raw)", provider)
	}
	if err != nil {
		return nil, err
	}
	// Describe deliberately renders the operator's own URL with any userinfo
	// already refused above, plus the provider — never apiBase with a token.
	src.describe = fmt.Sprintf("%s (%s)", u.Redacted(), provider)
	return src, nil
}

// checkTransportSecurity refuses a plaintext remote except on loopback,
// where the only realistic caller is this repository's own tests and a
// developer's local fixture server. Everything else must be https: the
// credential and the intent document both travel on this connection.
func checkTransportSecurity(u *url.URL) error {
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("gitsync: url must use https (got http for host %q)", u.Hostname())
	default:
		return fmt.Errorf("gitsync: url scheme %q is not supported (want https)", u.Scheme)
	}
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func inferProvider(host string) (Provider, error) {
	switch strings.ToLower(host) {
	case "github.com", "www.github.com":
		return ProviderGitHub, nil
	case "gitlab.com", "www.gitlab.com":
		return ProviderGitLab, nil
	default:
		return "", fmt.Errorf("gitsync: cannot infer provider for host %q — set [gitsync] provider explicitly (github, gitlab or raw)", host)
	}
}

// repoPath splits "/owner/repo(.git)" out of a repository URL.
func repoPath(u *url.URL) (string, error) {
	p := strings.Trim(u.Path, "/")
	p = strings.TrimSuffix(p, ".git")
	if p == "" {
		return "", fmt.Errorf("gitsync: url %q names no repository path", u.Redacted())
	}
	return p, nil
}

func githubAPIBase(u *url.URL) (string, error) {
	repo, err := repoPath(u)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(u.Hostname())
	// github.com's API lives on api.github.com; a GitHub Enterprise host
	// serves it under /api/v3 on the same origin. A non-default port (a test
	// server, an internal GHE) is preserved.
	if host == "github.com" || host == "www.github.com" {
		return "https://api.github.com/repos/" + repo, nil
	}
	return fmt.Sprintf("%s://%s/api/v3/repos/%s", u.Scheme, u.Host, repo), nil
}

func gitlabAPIBase(u *url.URL) (string, error) {
	repo, err := repoPath(u)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s/api/v4/projects/%s", u.Scheme, u.Host, url.PathEscape(repo)), nil
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

// authHeader applies the provider's credential header. GitHub and a generic
// raw host take a bearer token; GitLab takes PRIVATE-TOKEN. Either way the
// credential exists only as a header value on an in-flight request.
func (s *HTTPSource) authHeader(req *http.Request) {
	if s.token == "" {
		return
	}
	if s.provider == ProviderGitLab {
		req.Header.Set("PRIVATE-TOKEN", s.token)
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
}

// do issues one GET and returns the body, bounded at maxSpecBytes.
//
// The error text names the method, the URL and the status — never the
// response body, because a hosting provider's error body routinely quotes
// the request back (GitHub's 401 body, for one), and never the credential,
// which is not formatted into anything here.
func (s *HTTPSource) do(ctx context.Context, rawURL, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gitsync: building request for %s: %w", rawURL, err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	s.authHeader(req)

	resp, err := s.client.Do(req)
	if err != nil {
		// url.Error's own Error() renders the request URL, which carries no
		// credential by construction (userinfo is refused at construction).
		return nil, fmt.Errorf("%w: GET %s: %w", ErrUnreachable, rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%w: GET %s: status %d", ErrRemoteStatus, rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSpecBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: GET %s: reading body: %w", ErrUnreachable, rawURL, err)
	}
	if len(body) > maxSpecBytes {
		return nil, fmt.Errorf("gitsync: GET %s: response exceeds %d bytes", rawURL, maxSpecBytes)
	}
	return body, nil
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
	body, err := s.do(ctx, s.apiBase+"/commits/"+url.PathEscape(ref), "application/vnd.github+json")
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

	content, err := s.do(ctx,
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
	body, err := s.do(ctx, s.apiBase+"/repository/commits/"+url.PathEscape(ref), "application/json")
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

	content, err := s.do(ctx,
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
	content, err := s.do(ctx, s.apiBase+"/"+url.PathEscape(ref)+"/"+escapePath(path), "")
	if err != nil {
		return Revision{}, err
	}
	rev := Revision{Path: path, Content: content}
	rev.SHA = "sha256:" + rev.ContentDigest()
	return rev, nil
}

// escapePath percent-escapes each path segment while keeping the separators,
// so "network/cluster spec.yaml" becomes "network/cluster%20spec.yaml"
// rather than a single escaped blob.
func escapePath(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
