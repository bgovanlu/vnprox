// SPDX-License-Identifier: Apache-2.0

package gitsync

// hosturl.go holds exactly what the READ source (source.go, T-2701) and the
// WRITE host (host.go, T-2702) genuinely share: how an operator's repository
// URL becomes a provider API base, how a repository path is escaped into one,
// and the single HTTP round-trip both go through.
//
// It is deliberately the only thing they share. Source and Host stay separate
// types with separate credentials (see host.go's doc comment) so "vnprox never
// pushes on the sync path" remains a property of the type system rather than a
// comment; what they have in common is URL arithmetic and one carefully
// written request function, and duplicating either of those would be the
// worse outcome — particularly the request function, whose most important
// property is negative.
//
// THAT PROPERTY: do() never puts a response body into an error. A hosting
// provider's error body routinely quotes the request back (GitHub's 401 body
// does), so echoing it is how a credential reaches a log. T-2701's AC6 leak
// test rests on this and T-2702's AC5 extends it to the write credential;
// verified in both directions by making do() echo a 401 body and watching the
// leak tests redden. The credential itself is never formatted into anything
// here: it exists only as a header value on an in-flight request.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// httpTransport is the shared credential-carrying HTTP round-trip. Both
// HTTPSource and HTTPHost embed one; neither can reach the other's.
type httpTransport struct {
	client   *http.Client
	provider Provider
	token    string
}

// authHeader applies the provider's credential header. GitHub and a generic
// raw host take a bearer token; GitLab takes PRIVATE-TOKEN.
func (t *httpTransport) authHeader(req *http.Request) {
	if t.token == "" {
		return
	}
	if t.provider == ProviderGitLab {
		req.Header.Set("PRIVATE-TOKEN", t.token)
		return
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
}

// request is one HTTP call's inputs. body nil means no request body; a
// non-nil body is sent as JSON.
type request struct {
	method string
	url    string
	accept string
	body   []byte
}

// do issues one request and returns the response body, bounded at
// maxSpecBytes.
//
// The error text names the method, the URL and the status — never the
// response body, and never the credential (see this file's doc comment). A
// 404 additionally wraps ErrRemoteNotFound so a caller can tell "this branch
// does not exist yet" from "this host refused us", which is the difference
// between creating a branch and abandoning a proposal.
func (t *httpTransport) do(ctx context.Context, r request) ([]byte, error) {
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, r.url, body)
	if err != nil {
		return nil, fmt.Errorf("gitsync: building request for %s: %w", r.url, err)
	}
	if r.accept != "" {
		req.Header.Set("Accept", r.accept)
	}
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	t.authHeader(req)

	resp, err := t.client.Do(req)
	if err != nil {
		// url.Error's own Error() renders the request URL, which carries no
		// credential by construction (userinfo is refused at construction).
		return nil, fmt.Errorf("%w: %s %s: %w", ErrUnreachable, r.method, r.url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %w: %s %s: status 404", ErrRemoteStatus, ErrRemoteNotFound, r.method, r.url)
		}
		return nil, fmt.Errorf("%w: %s %s: status %d", ErrRemoteStatus, r.method, r.url, resp.StatusCode)
	}
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxSpecBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s %s: reading body: %w", ErrUnreachable, r.method, r.url, err)
	}
	if len(out) > maxSpecBytes {
		return nil, fmt.Errorf("gitsync: %s %s: response exceeds %d bytes", r.method, r.url, maxSpecBytes)
	}
	return out, nil
}

// --- URL arithmetic --------------------------------------------------------

// remote is an operator's repository URL resolved into the three things both
// the read source and the write host need: which API shape to speak, where
// its root is, and how to describe the origin without a credential in it.
type remote struct {
	provider Provider
	apiBase  string
	describe string
	// owner is the repository's owner/namespace segment ("org" of
	// "org/infra"), empty for ProviderRaw. GitHub's pull-request search
	// filters by "owner:branch"; nothing above the host client knows that.
	owner string
	// repo is the full "owner/repo" path, empty for ProviderRaw.
	repo string
}

// resolveRemote validates rawURL and resolves it. credentialKey names the
// config key that should have carried the credential instead, so a URL with
// userinfo in it is refused with a message pointing at the right place —
// [gitsync] token_file for a read, push_token_file for a write.
//
// Both callers share this because both refusals are security-relevant and a
// second copy would eventually diverge: the userinfo rule keeps a credential
// out of every log line, finding and status string that names the origin, and
// the https-except-loopback rule is what stops either credential travelling
// in the clear.
func resolveRemote(rawURL string, provider Provider, credentialKey string) (remote, error) {
	if strings.TrimSpace(rawURL) == "" {
		return remote{}, fmt.Errorf("gitsync: url is required")
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return remote{}, fmt.Errorf("gitsync: parsing url: %w", err)
	}
	if secErr := checkTransportSecurity(u); secErr != nil {
		return remote{}, secErr
	}
	if u.User != nil {
		// A credential in the URL would end up in every log line, finding
		// and status output that names the origin. Refuse it outright and
		// point at the key that exists for this.
		return remote{}, fmt.Errorf("gitsync: url must not embed credentials; use [gitsync] %s", credentialKey)
	}
	if provider == "" {
		if provider, err = inferProvider(u.Hostname()); err != nil {
			return remote{}, err
		}
	}

	out := remote{provider: provider}
	switch provider {
	case ProviderGitHub:
		out.apiBase, err = githubAPIBase(u)
	case ProviderGitLab:
		out.apiBase, err = gitlabAPIBase(u)
	case ProviderRaw:
		out.apiBase = strings.TrimSuffix(u.String(), "/")
	default:
		return remote{}, fmt.Errorf("gitsync: unknown provider %q (want github, gitlab or raw)", provider)
	}
	if err != nil {
		return remote{}, err
	}
	if provider != ProviderRaw {
		if out.repo, err = repoPath(u); err != nil {
			return remote{}, err
		}
		out.owner, _, _ = strings.Cut(out.repo, "/")
	}
	// Describe deliberately renders the operator's own URL with any userinfo
	// already refused above, plus the provider — never apiBase with a token.
	out.describe = fmt.Sprintf("%s (%s)", u.Redacted(), provider)
	return out, nil
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

// repoPath splits "owner/repo" out of a repository URL.
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
