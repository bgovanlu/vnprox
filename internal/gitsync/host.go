package gitsync

// host.go is T-2702's WRITE seam: the branch/commit/pull-request surface a
// changeset is proposed through.
//
// # Why this is a sibling of Source and not an extension of it
//
// Source (source.go) is read-only by design. This interface was declared
// BESIDE it rather than added to it, and the two share nothing but URL
// arithmetic and one request function (hosturl.go). That separation is the
// point:
//
//   - The sync Service (service.go) holds a Source and nothing else. There
//     is no method on the type it holds that could push, so "vnprox never
//     pushes on the sync path" is a property of the type system, not a
//     comment or a code-review habit. propose_test.go's TestSeamsStaySeparate
//     asserts it in both directions — Source carries no write verb, and
//     *HTTPSource does not satisfy Host.
//   - The credential differs too. A read-only sync token is enough for
//     everything T-2701 does; opening a pull request needs a write-scoped
//     one. They are separate config keys ([gitsync] token_file and
//     push_token_file) held by separate objects, so enabling proposals is an
//     explicit operator act and a sync-only deployment's credential is never
//     silently widened.
//
// # What this interface deliberately does NOT have
//
// No merge, no approve, no review-request, no status/check write, no poll.
// vnprox opens a pull request and stops; whatever happens to it next comes
// back through T-2701's ordinary sync. A method that merged would be
// unreviewable safety-critical automation, so there is nowhere to put one —
// asserted by TestHostHasNoMergeVerb.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PullRequest is one opened proposal, as the host reports it.
type PullRequest struct {
	// ID is the host's own identifier for the request — GitHub's pull number,
	// GitLab's merge-request iid — carried as an opaque string so nothing
	// above this interface has to know which.
	ID string
	// URL is the human-facing page. This is what the changeset records and
	// what the review surface links to.
	URL string
	// Title is the request's current title.
	Title string
}

// PullRequestInput is what to open (or bring an existing request up to date
// with). Base is the branch the change is proposed INTO.
//
//nolint:govet // fieldalignment: field order reads as the request being made, not packing.
type PullRequestInput struct {
	Branch string
	Base   string
	Title  string
	Body   string
}

// CommitRequest is one file write on one branch.
//
//nolint:govet // fieldalignment: field order reads as the commit being made, not packing.
type CommitRequest struct {
	Branch  string
	Path    string
	Message string
	Content []byte
}

// Host is the write surface a proposal needs, and exactly that.
//
// Every method is a single host API call (except CommitFile, which reads the
// existing blob first because both hosts require it for an update). Nothing
// above this interface knows GitHub from GitLab: the Proposer drives these
// eleven verbs and never branches on a provider (T-2702 AC6).
type Host interface {
	// Describe returns a credential-free description of where this host
	// writes to, for logs, audit rows and errors.
	Describe() string
	// ResolveRef resolves a branch/tag/sha to a commit sha.
	ResolveRef(ctx context.Context, ref string) (string, error)
	// BranchHead returns the sha at branch, and false when the branch does
	// not exist. A missing branch is not an error: it is the ordinary state
	// before a first proposal.
	BranchHead(ctx context.Context, branch string) (sha string, exists bool, err error)
	// CreateBranch creates branch pointing at fromSHA.
	CreateBranch(ctx context.Context, branch, fromSHA string) error
	// DeleteBranch removes branch. It is the compensating action for a
	// proposal that could not be completed (T-2702 AC3) and must tolerate a
	// branch that is already gone.
	DeleteBranch(ctx context.Context, branch string) error
	// ReadFile reads path at ref, returning false when there is no such file.
	ReadFile(ctx context.Context, ref, path string) (content []byte, exists bool, err error)
	// CommitFile writes one file on one branch, creating or updating it, and
	// returns the resulting commit sha.
	CommitFile(ctx context.Context, req CommitRequest) (string, error)
	// FindOpenPullRequest returns the open request whose source is branch,
	// and false when there is none. This is what makes proposing twice update
	// one request instead of opening a second (T-2702 AC4).
	FindOpenPullRequest(ctx context.Context, branch string) (PullRequest, bool, error)
	// OpenPullRequest opens a new request.
	OpenPullRequest(ctx context.Context, in PullRequestInput) (PullRequest, error)
	// UpdatePullRequest rewrites an existing request's title and body.
	UpdatePullRequest(ctx context.Context, id string, in PullRequestInput) (PullRequest, error)
}

// HostConfig configures an HTTPHost. It mirrors SourceConfig field for field
// so the [gitsync] section maps onto both the same way — with one deliberate
// difference: Token here is the PUSH credential ([gitsync] push_token_file),
// never the read one.
//
//nolint:govet // fieldalignment: field order mirrors SourceConfig and the config section, not packing.
type HostConfig struct {
	URL      string
	Provider Provider
	Token    string
	Client   *http.Client
}

// HTTPHost speaks GitHub's and GitLab's write REST surfaces over plain
// net/http — no git binary and no git library, for exactly the reasons this
// package's doc comment gives for the read path. A "push" here is a contents
// API call, which is why no working tree, no packfile and no ssh key is
// involved.
//
// The push credential is held here and only ever leaves as a request header:
// it is never placed in a URL, never returned by Describe, never written into
// a commit message, a branch name or a pull-request body (T-2702 AC5), and
// never formatted into an error.
type HTTPHost struct {
	httpTransport
	apiBase  string
	describe string
	owner    string
}

// compile-time: the write implementation satisfies the write seam, and
// nothing forces the read one to.
var _ Host = (*HTTPHost)(nil)

// NewHTTPHost validates cfg and builds a Host. It performs no I/O.
//
// A raw file host is refused: ProviderRaw has no branch or pull-request API
// at all, so a proposal against one could only ever be a silent no-op, and
// answering "configured" for something that can never work is worse than
// refusing at startup.
func NewHTTPHost(cfg HostConfig) (*HTTPHost, error) {
	rem, err := resolveRemote(cfg.URL, cfg.Provider, "push_token_file")
	if err != nil {
		return nil, err
	}
	if rem.provider == ProviderRaw {
		return nil, fmt.Errorf("gitsync: provider %q has no branch or pull-request API; proposing a changeset needs github or gitlab", ProviderRaw)
	}
	if cfg.Token == "" {
		// Opening a pull request anonymously is not a thing on either host.
		// Say so now rather than at the first 401.
		return nil, fmt.Errorf("gitsync: a write-scoped credential is required to propose a changeset; set [gitsync] push_token_file")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPHost{
		httpTransport: httpTransport{client: client, provider: rem.provider, token: cfg.Token},
		apiBase:       rem.apiBase,
		describe:      rem.describe,
		owner:         rem.owner,
	}, nil
}

// Describe implements Host.
func (h *HTTPHost) Describe() string { return h.describe }

func (h *HTTPHost) get(ctx context.Context, rawURL, accept string) ([]byte, error) {
	return h.do(ctx, request{method: http.MethodGet, url: rawURL, accept: accept})
}

func (h *HTTPHost) send(ctx context.Context, method, rawURL string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("gitsync: encoding request for %s: %w", rawURL, err)
	}
	return h.do(ctx, request{method: method, url: rawURL, accept: "application/json", body: body})
}

// --- ref resolution --------------------------------------------------------

// ResolveRef implements Host.
func (h *HTTPHost) ResolveRef(ctx context.Context, ref string) (string, error) {
	var path string
	if h.provider == ProviderGitHub {
		path = h.apiBase + "/commits/" + url.PathEscape(ref)
	} else {
		path = h.apiBase + "/repository/commits/" + url.PathEscape(ref)
	}
	body, err := h.get(ctx, path, "application/json")
	if err != nil {
		return "", err
	}
	var resp struct {
		SHA string `json:"sha"`
		ID  string `json:"id"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("gitsync: decoding commit for ref %q: %w", ref, err)
	}
	sha := firstNonEmptyString(resp.SHA, resp.ID)
	if sha == "" {
		return "", fmt.Errorf("gitsync: commit for ref %q carries no sha", ref)
	}
	return sha, nil
}

// BranchHead implements Host.
func (h *HTTPHost) BranchHead(ctx context.Context, branch string) (string, bool, error) {
	var path string
	if h.provider == ProviderGitHub {
		path = h.apiBase + "/branches/" + url.PathEscape(branch)
	} else {
		path = h.apiBase + "/repository/branches/" + url.PathEscape(branch)
	}
	body, err := h.get(ctx, path, "application/json")
	if err != nil {
		if errors.Is(err, ErrRemoteNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	var resp struct {
		Commit struct {
			SHA string `json:"sha"`
			ID  string `json:"id"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", false, fmt.Errorf("gitsync: decoding branch %q: %w", branch, err)
	}
	return firstNonEmptyString(resp.Commit.SHA, resp.Commit.ID), true, nil
}

// CreateBranch implements Host.
func (h *HTTPHost) CreateBranch(ctx context.Context, branch, fromSHA string) error {
	if h.provider == ProviderGitHub {
		_, err := h.send(ctx, http.MethodPost, h.apiBase+"/git/refs", map[string]string{
			"ref": "refs/heads/" + branch, "sha": fromSHA,
		})
		if err != nil {
			return fmt.Errorf("gitsync: creating branch %s: %w", branch, err)
		}
		return nil
	}
	_, err := h.send(ctx, http.MethodPost, h.apiBase+"/repository/branches", map[string]string{
		"branch": branch, "ref": fromSHA,
	})
	if err != nil {
		return fmt.Errorf("gitsync: creating branch %s: %w", branch, err)
	}
	return nil
}

// DeleteBranch implements Host. A branch that is already gone is success:
// this is the compensating action of a failed proposal (AC3), and its
// post-condition is "the branch does not exist", not "we removed it".
func (h *HTTPHost) DeleteBranch(ctx context.Context, branch string) error {
	var path string
	if h.provider == ProviderGitHub {
		path = h.apiBase + "/git/refs/heads/" + escapePath(branch)
	} else {
		path = h.apiBase + "/repository/branches/" + url.PathEscape(branch)
	}
	if _, err := h.do(ctx, request{method: http.MethodDelete, url: path, accept: "application/json"}); err != nil {
		if errors.Is(err, ErrRemoteNotFound) {
			return nil
		}
		return fmt.Errorf("gitsync: deleting branch %s: %w", branch, err)
	}
	return nil
}

// --- file contents ---------------------------------------------------------

// fileState is what a host reports about one file at one ref: its content and
// the blob id an update has to quote back.
//
//nolint:govet // fieldalignment: the content first, then what identifies it — the order documents the answer, not packing.
type fileState struct {
	content []byte
	blobID  string
	exists  bool
}

func (h *HTTPHost) fileAt(ctx context.Context, ref, path string) (fileState, error) {
	var reqURL string
	if h.provider == ProviderGitHub {
		reqURL = h.apiBase + "/contents/" + escapePath(path) + "?ref=" + url.QueryEscape(ref)
	} else {
		reqURL = h.apiBase + "/repository/files/" + url.PathEscape(path) + "?ref=" + url.QueryEscape(ref)
	}
	body, err := h.get(ctx, reqURL, "application/json")
	if err != nil {
		if errors.Is(err, ErrRemoteNotFound) {
			return fileState{}, nil
		}
		return fileState{}, err
	}
	var resp struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		SHA      string `json:"sha"`
		BlobID   string `json:"blob_id"`
	}
	if decErr := json.Unmarshal(body, &resp); decErr != nil {
		return fileState{}, fmt.Errorf("gitsync: decoding contents of %s at %s: %w", path, ref, decErr)
	}
	// Both hosts base64 the content. GitHub wraps its base64 in newlines;
	// GitLab does not. Strip whitespace before decoding rather than assuming
	// either shape.
	raw, err := base64.StdEncoding.DecodeString(stripSpace(resp.Content))
	if err != nil {
		return fileState{}, fmt.Errorf("gitsync: decoding contents of %s at %s: %w", path, ref, err)
	}
	return fileState{content: raw, blobID: firstNonEmptyString(resp.SHA, resp.BlobID), exists: true}, nil
}

// ReadFile implements Host.
func (h *HTTPHost) ReadFile(ctx context.Context, ref, path string) ([]byte, bool, error) {
	st, err := h.fileAt(ctx, ref, path)
	if err != nil {
		return nil, false, err
	}
	return st.content, st.exists, nil
}

// CommitFile implements Host: create the file if the branch does not have it,
// update it in place if it does. Both hosts require the existing blob id on
// an update, which is why this reads before it writes — and that read is also
// what stops a proposal committing content byte-identical to what is already
// there.
func (h *HTTPHost) CommitFile(ctx context.Context, req CommitRequest) (string, error) {
	current, err := h.fileAt(ctx, req.Branch, req.Path)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(req.Content)

	if h.provider == ProviderGitHub {
		payload := map[string]string{
			"message": req.Message, "content": encoded, "branch": req.Branch,
		}
		if current.exists {
			payload["sha"] = current.blobID
		}
		body, putErr := h.send(ctx, http.MethodPut, h.apiBase+"/contents/"+escapePath(req.Path), payload)
		if putErr != nil {
			return "", fmt.Errorf("gitsync: committing %s on %s: %w", req.Path, req.Branch, putErr)
		}
		var resp struct {
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		if decErr := json.Unmarshal(body, &resp); decErr != nil {
			return "", fmt.Errorf("gitsync: decoding commit response for %s: %w", req.Path, decErr)
		}
		return resp.Commit.SHA, nil
	}

	method := http.MethodPost
	if current.exists {
		method = http.MethodPut
	}
	body, err := h.send(ctx, method, h.apiBase+"/repository/files/"+url.PathEscape(req.Path), map[string]string{
		"branch": req.Branch, "content": encoded, "encoding": "base64", "commit_message": req.Message,
	})
	if err != nil {
		return "", fmt.Errorf("gitsync: committing %s on %s: %w", req.Path, req.Branch, err)
	}
	var resp struct {
		CommitID string `json:"commit_id"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("gitsync: decoding commit response for %s: %w", req.Path, err)
	}
	return resp.CommitID, nil
}

// --- pull requests ---------------------------------------------------------

// hostPullRequest is the union of the two hosts' request shapes. Decoding
// both into one struct keeps the provider difference inside this file, which
// is the whole contract of the Host seam.
//
//nolint:govet // fieldalignment: the two hosts' fields are paired (number/iid, html_url/web_url) so the union is readable, not packed.
type hostPullRequest struct {
	Number  int    `json:"number"`
	IID     int    `json:"iid"`
	HTMLURL string `json:"html_url"`
	WebURL  string `json:"web_url"`
	Title   string `json:"title"`
}

func (p hostPullRequest) toPullRequest() PullRequest {
	id := p.Number
	if id == 0 {
		id = p.IID
	}
	return PullRequest{
		ID:    strconv.Itoa(id),
		URL:   firstNonEmptyString(p.HTMLURL, p.WebURL),
		Title: p.Title,
	}
}

// FindOpenPullRequest implements Host.
func (h *HTTPHost) FindOpenPullRequest(ctx context.Context, branch string) (PullRequest, bool, error) {
	var reqURL string
	if h.provider == ProviderGitHub {
		// GitHub's head filter is "owner:branch"; the owner comes from the
		// configured repository URL, never from the caller.
		reqURL = h.apiBase + "/pulls?state=open&head=" + url.QueryEscape(h.owner+":"+branch)
	} else {
		reqURL = h.apiBase + "/merge_requests?state=opened&source_branch=" + url.QueryEscape(branch)
	}
	body, err := h.get(ctx, reqURL, "application/json")
	if err != nil {
		if errors.Is(err, ErrRemoteNotFound) {
			return PullRequest{}, false, nil
		}
		return PullRequest{}, false, err
	}
	var list []hostPullRequest
	if err := json.Unmarshal(body, &list); err != nil {
		return PullRequest{}, false, fmt.Errorf("gitsync: decoding open requests for branch %s: %w", branch, err)
	}
	if len(list) == 0 {
		return PullRequest{}, false, nil
	}
	return list[0].toPullRequest(), true, nil
}

// OpenPullRequest implements Host.
func (h *HTTPHost) OpenPullRequest(ctx context.Context, in PullRequestInput) (PullRequest, error) {
	var (
		reqURL  string
		payload map[string]string
	)
	if h.provider == ProviderGitHub {
		reqURL = h.apiBase + "/pulls"
		payload = map[string]string{"title": in.Title, "body": in.Body, "head": in.Branch, "base": in.Base}
	} else {
		reqURL = h.apiBase + "/merge_requests"
		payload = map[string]string{
			"title": in.Title, "description": in.Body,
			"source_branch": in.Branch, "target_branch": in.Base,
		}
	}
	body, err := h.send(ctx, http.MethodPost, reqURL, payload)
	if err != nil {
		return PullRequest{}, fmt.Errorf("gitsync: opening a pull request for %s: %w", in.Branch, err)
	}
	var resp hostPullRequest
	if err := json.Unmarshal(body, &resp); err != nil {
		return PullRequest{}, fmt.Errorf("gitsync: decoding the opened pull request for %s: %w", in.Branch, err)
	}
	return resp.toPullRequest(), nil
}

// UpdatePullRequest implements Host.
func (h *HTTPHost) UpdatePullRequest(ctx context.Context, id string, in PullRequestInput) (PullRequest, error) {
	var (
		reqURL  string
		method  string
		payload map[string]string
	)
	if h.provider == ProviderGitHub {
		reqURL, method = h.apiBase+"/pulls/"+url.PathEscape(id), http.MethodPatch
		payload = map[string]string{"title": in.Title, "body": in.Body}
	} else {
		reqURL, method = h.apiBase+"/merge_requests/"+url.PathEscape(id), http.MethodPut
		payload = map[string]string{"title": in.Title, "description": in.Body}
	}
	body, err := h.send(ctx, method, reqURL, payload)
	if err != nil {
		return PullRequest{}, fmt.Errorf("gitsync: updating pull request %s: %w", id, err)
	}
	var resp hostPullRequest
	if err := json.Unmarshal(body, &resp); err != nil {
		return PullRequest{}, fmt.Errorf("gitsync: decoding the updated pull request %s: %w", id, err)
	}
	return resp.toPullRequest(), nil
}

// --- helpers ---------------------------------------------------------------

func firstNonEmptyString(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, s)
}
