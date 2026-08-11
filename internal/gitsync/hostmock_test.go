package gitsync_test

// hostmock_test.go is the hardware-free git host T-2702's acceptance tests
// run against: one in-memory repository (branches, one file per branch, a
// list of open pull requests) served through BOTH hosts' REST write shapes.
//
// It exists so AC6 ("GitHub and GitLab are both exercised against a mock
// host") is a real exercise of the real HTTPHost rather than of a Go double:
// every test that names a provider drives gitsync.NewHTTPHost against this
// server, so the URL shapes, the base64 content encoding, the create-vs-update
// distinction and the pull-request lookup are all genuinely executed.
//
// It records what it was told — every commit message, branch name and pull
// request body — which is what makes AC5's "credentials are absent from the
// PR body, the commit message and the branch name" an assertion about what
// actually crossed the wire.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// mockPR is one open pull request / merge request.
//
//nolint:govet // fieldalignment: test double; field order mirrors the request being modelled.
type mockPR struct {
	ID     int
	Branch string
	Base   string
	Title  string
	Body   string
}

// hostFailure names one host operation that should answer with a status
// instead of doing its job. The keys are the operations the Proposer drives,
// so a test can fail exactly one step and assert what the compensating path
// did (AC3).
type hostFailure string

const (
	failResolveRef   hostFailure = "resolveRef"
	failBranchHead   hostFailure = "branchHead"
	failCreateBranch hostFailure = "createBranch"
	failReadFile     hostFailure = "readFile"
	failCommit       hostFailure = "commit"
	failFindPR       hostFailure = "findPR"
	failOpenPR       hostFailure = "openPR"
	failUpdatePR     hostFailure = "updatePR"
	failDeleteBranch hostFailure = "deleteBranch"
)

//nolint:govet // fieldalignment: test double; fields are grouped by what they model, not packed.
type gitHostServer struct {
	mu sync.Mutex

	provider string // "github" | "gitlab"
	owner    string
	repo     string

	// baseSHA is what the configured base ref resolves to.
	baseSHA string
	// branches maps branch name -> head sha. The base branch is present from
	// construction; everything else is created by the code under test.
	branches map[string]string
	// files maps branch -> path -> content.
	files map[string]map[string][]byte
	prs   []mockPR
	nextP int

	fail map[hostFailure]int

	// recorded surfaces, for the credential-leak assertions.
	commitMessages   []string
	createdBranches  []string
	deletedBranches  []string
	sawAuthorization string
	sawPrivateToken  string
	requests         []string
}

func newGitHostServer(provider, baseBranch string, baseFiles map[string][]byte) *gitHostServer {
	files := map[string][]byte{}
	for k, v := range baseFiles {
		files[k] = append([]byte(nil), v...)
	}
	return &gitHostServer{
		provider: provider, owner: "org", repo: "infra",
		baseSHA:  "base0000000000000000000000000000000000000",
		branches: map[string]string{baseBranch: "base0000000000000000000000000000000000000"},
		files:    map[string]map[string][]byte{baseBranch: files},
		nextP:    41,
		fail:     map[hostFailure]int{},
	}
}

// failNext makes op answer with status until cleared.
func (s *gitHostServer) failWith(op hostFailure, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[op] = status
}

func (s *gitHostServer) clearFailures() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = map[hostFailure]int{}
}

func (s *gitHostServer) branchNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.branches))
	for b := range s.branches {
		out = append(out, b)
	}
	return out
}

func (s *gitHostServer) hasBranch(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.branches[name]
	return ok
}

// fileOn reads one file at a ref, which may be a branch name OR a commit sha
// — the READ source resolves the ref to a sha first and then reads at that
// sha, so the mock has to answer both the way a real host does.
func (s *gitHostServer) fileOn(ref, path string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	branch, ok := s.branchForRefLocked(ref)
	if !ok {
		return nil, false
	}
	content, ok := s.files[branch][path]
	return content, ok
}

func (s *gitHostServer) branchForRefLocked(ref string) (string, bool) {
	if _, ok := s.branches[ref]; ok {
		return ref, true
	}
	for name, sha := range s.branches {
		if sha == ref {
			return name, true
		}
	}
	return "", false
}

func (s *gitHostServer) openPRs() []mockPR {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mockPR(nil), s.prs...)
}

func (s *gitHostServer) surfaces() (commits, branches []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commitMessages...), append([]string(nil), s.createdBranches...)
}

func (s *gitHostServer) credentialSeen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sawAuthorization != "" {
		return s.sawAuthorization
	}
	return s.sawPrivateToken
}

// refused answers with the configured status for op, if any. The body quotes
// the Authorization header back — the realistic worst case a leak test must
// see, exactly as T-2701's read mock does.
func (s *gitHostServer) refused(w http.ResponseWriter, r *http.Request, op hostFailure) bool {
	s.mu.Lock()
	status := s.fail[op]
	s.mu.Unlock()
	if status == 0 {
		return false
	}
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"message":"refused (sent: %s / %s)"}`,
		r.Header.Get("Authorization"), r.Header.Get("PRIVATE-TOKEN"))
	return true
}

func (s *gitHostServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if v := r.Header.Get("Authorization"); v != "" {
		s.sawAuthorization = v
	}
	if v := r.Header.Get("PRIVATE-TOKEN"); v != "" {
		s.sawPrivateToken = v
	}
	s.requests = append(s.requests, r.Method+" "+r.URL.EscapedPath())
	s.mu.Unlock()

	// EscapedPath, not Path: GitLab addresses a branch/file by a
	// percent-encoded segment, and Path would have already turned %2F back
	// into a separator.
	if s.provider == "github" {
		s.serveGitHub(w, r)
		return
	}
	s.serveGitLab(w, r)
}

func (s *gitHostServer) body(r *http.Request) map[string]string {
	var m map[string]string
	_ = json.NewDecoder(r.Body).Decode(&m)
	return m
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// --- shared repository operations -------------------------------------------

func (s *gitHostServer) createBranch(name, fromSHA string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.branches[name] = fromSHA
	s.createdBranches = append(s.createdBranches, name)
	// A new branch starts as a copy of whichever branch fromSHA points at —
	// which is the base branch, for every branch the Proposer creates.
	files := map[string][]byte{}
	for src, sha := range s.branches {
		if src == name || sha != fromSHA {
			continue
		}
		for p, c := range s.files[src] {
			files[p] = append([]byte(nil), c...)
		}
		break
	}
	s.files[name] = files
}

func (s *gitHostServer) deleteBranch(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.branches[name]; !ok {
		return false
	}
	delete(s.branches, name)
	delete(s.files, name)
	s.deletedBranches = append(s.deletedBranches, name)
	return true
}

func (s *gitHostServer) commit(branch, path, message string, content []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files[branch] == nil {
		s.files[branch] = map[string][]byte{}
	}
	s.files[branch][path] = content
	s.commitMessages = append(s.commitMessages, message)
	sha := fmt.Sprintf("c0mm1t%034d", len(s.commitMessages))
	s.branches[branch] = sha
	return sha
}

func (s *gitHostServer) findPR(branch string) (mockPR, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pr := range s.prs {
		if pr.Branch == branch {
			return pr, true
		}
	}
	return mockPR{}, false
}

func (s *gitHostServer) openPR(branch, base, title, body string) mockPR {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextP++
	pr := mockPR{ID: s.nextP, Branch: branch, Base: base, Title: title, Body: body}
	s.prs = append(s.prs, pr)
	return pr
}

func (s *gitHostServer) updatePR(id int, title, body string) (mockPR, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.prs {
		if s.prs[i].ID != id {
			continue
		}
		s.prs[i].Title = title
		s.prs[i].Body = body
		return s.prs[i], true
	}
	return mockPR{}, false
}

func (s *gitHostServer) prURL(id int) string {
	if s.provider == "github" {
		return fmt.Sprintf("https://github.test/%s/%s/pull/%d", s.owner, s.repo, id)
	}
	return fmt.Sprintf("https://gitlab.test/%s/%s/-/merge_requests/%d", s.owner, s.repo, id)
}

func (s *gitHostServer) branchSHA(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sha, ok := s.branches[name]
	return sha, ok
}

// --- GitHub ------------------------------------------------------------------

//nolint:gocyclo // a routing table for one host's REST shape; splitting it would scatter the shape it exists to document.
func (s *gitHostServer) serveGitHub(w http.ResponseWriter, r *http.Request) {
	// A GitHub Enterprise host (which is what an httptest server looks like
	// to the URL resolver) serves the API under /api/v3 on the same origin.
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v3")
	path = strings.TrimPrefix(path, "/repos/"+s.owner+"/"+s.repo)

	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/commits/"):
		if s.refused(w, r, failResolveRef) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sha": s.baseSHA})

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/branches/"):
		if s.refused(w, r, failBranchHead) {
			return
		}
		name := unescape(strings.TrimPrefix(path, "/branches/"))
		sha, ok := s.branchSHA(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"commit": map[string]any{"sha": sha}})

	case r.Method == http.MethodPost && path == "/git/refs":
		if s.refused(w, r, failCreateBranch) {
			return
		}
		b := s.body(r)
		s.createBranch(strings.TrimPrefix(b["ref"], "refs/heads/"), b["sha"])
		writeJSON(w, http.StatusCreated, map[string]any{"ref": b["ref"]})

	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/git/refs/heads/"):
		if s.refused(w, r, failDeleteBranch) {
			return
		}
		if !s.deleteBranch(unescape(strings.TrimPrefix(path, "/git/refs/heads/"))) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/contents/"):
		if s.refused(w, r, failReadFile) {
			return
		}
		content, ok := s.fileOn(r.URL.Query().Get("ref"), unescape(strings.TrimPrefix(path, "/contents/")))
		if !ok {
			http.NotFound(w, r)
			return
		}
		// GitHub serves the raw blob when asked for it (which is how the READ
		// source fetches the spec) and a JSON envelope otherwise (which is how
		// the WRITE host learns the blob sha it must quote back on an update).
		if strings.Contains(r.Header.Get("Accept"), "raw") {
			_, _ = w.Write(content)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"content": base64.StdEncoding.EncodeToString(content), "encoding": "base64", "sha": "blob123",
		})

	case r.Method == http.MethodPut && strings.HasPrefix(path, "/contents/"):
		if s.refused(w, r, failCommit) {
			return
		}
		b := s.body(r)
		raw, err := base64.StdEncoding.DecodeString(b["content"])
		if err != nil {
			http.Error(w, "bad content", http.StatusBadRequest)
			return
		}
		sha := s.commit(b["branch"], unescape(strings.TrimPrefix(path, "/contents/")), b["message"], raw)
		writeJSON(w, http.StatusOK, map[string]any{"commit": map[string]any{"sha": sha}})

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/pulls"):
		if s.refused(w, r, failFindPR) {
			return
		}
		head := r.URL.Query().Get("head")
		_, branch, _ := strings.Cut(head, ":")
		out := []map[string]any{}
		if pr, ok := s.findPR(branch); ok {
			out = append(out, map[string]any{"number": pr.ID, "html_url": s.prURL(pr.ID), "title": pr.Title})
		}
		writeJSON(w, http.StatusOK, out)

	case r.Method == http.MethodPost && path == "/pulls":
		if s.refused(w, r, failOpenPR) {
			return
		}
		b := s.body(r)
		pr := s.openPR(b["head"], b["base"], b["title"], b["body"])
		writeJSON(w, http.StatusCreated, map[string]any{"number": pr.ID, "html_url": s.prURL(pr.ID), "title": pr.Title})

	case r.Method == http.MethodPatch && strings.HasPrefix(path, "/pulls/"):
		if s.refused(w, r, failUpdatePR) {
			return
		}
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/pulls/"))
		b := s.body(r)
		pr, ok := s.updatePR(id, b["title"], b["body"])
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"number": pr.ID, "html_url": s.prURL(pr.ID), "title": pr.Title})

	default:
		http.NotFound(w, r)
	}
}

// --- GitLab ------------------------------------------------------------------

//nolint:gocyclo // as serveGitHub: one host's REST shape, kept in one place.
func (s *gitHostServer) serveGitLab(w http.ResponseWriter, r *http.Request) {
	prefix := "/api/v4/projects/" + url.PathEscape(s.owner+"/"+s.repo)
	path := strings.TrimPrefix(r.URL.EscapedPath(), prefix)

	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/repository/commits/"):
		if s.refused(w, r, failResolveRef) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": s.baseSHA})

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/repository/branches/"):
		if s.refused(w, r, failBranchHead) {
			return
		}
		sha, ok := s.branchSHA(unescape(strings.TrimPrefix(path, "/repository/branches/")))
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"commit": map[string]any{"id": sha}})

	case r.Method == http.MethodPost && path == "/repository/branches":
		if s.refused(w, r, failCreateBranch) {
			return
		}
		b := s.body(r)
		s.createBranch(b["branch"], b["ref"])
		writeJSON(w, http.StatusCreated, map[string]any{"name": b["branch"]})

	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/repository/branches/"):
		if s.refused(w, r, failDeleteBranch) {
			return
		}
		if !s.deleteBranch(unescape(strings.TrimPrefix(path, "/repository/branches/"))) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/repository/files/"):
		if s.refused(w, r, failReadFile) {
			return
		}
		filePath := unescape(strings.TrimPrefix(path, "/repository/files/"))
		// GitLab's raw route is the file path with "/raw" appended — that is
		// how the READ source fetches the spec; the plain route returns the
		// JSON envelope the WRITE host reads.
		if raw := strings.TrimSuffix(filePath, "/raw"); raw != filePath {
			content, ok := s.fileOn(r.URL.Query().Get("ref"), raw)
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(content)
			return
		}
		content, ok := s.fileOn(r.URL.Query().Get("ref"), filePath)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"content": base64.StdEncoding.EncodeToString(content), "encoding": "base64", "blob_id": "blob123",
		})

	case (r.Method == http.MethodPost || r.Method == http.MethodPut) && strings.HasPrefix(path, "/repository/files/"):
		if s.refused(w, r, failCommit) {
			return
		}
		b := s.body(r)
		raw, err := base64.StdEncoding.DecodeString(b["content"])
		if err != nil {
			http.Error(w, "bad content", http.StatusBadRequest)
			return
		}
		sha := s.commit(b["branch"], unescape(strings.TrimPrefix(path, "/repository/files/")), b["commit_message"], raw)
		writeJSON(w, http.StatusOK, map[string]any{"commit_id": sha, "file_path": path})

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/merge_requests"):
		if s.refused(w, r, failFindPR) {
			return
		}
		out := []map[string]any{}
		if pr, ok := s.findPR(r.URL.Query().Get("source_branch")); ok {
			out = append(out, map[string]any{"iid": pr.ID, "web_url": s.prURL(pr.ID), "title": pr.Title})
		}
		writeJSON(w, http.StatusOK, out)

	case r.Method == http.MethodPost && path == "/merge_requests":
		if s.refused(w, r, failOpenPR) {
			return
		}
		b := s.body(r)
		pr := s.openPR(b["source_branch"], b["target_branch"], b["title"], b["description"])
		writeJSON(w, http.StatusCreated, map[string]any{"iid": pr.ID, "web_url": s.prURL(pr.ID), "title": pr.Title})

	case r.Method == http.MethodPut && strings.HasPrefix(path, "/merge_requests/"):
		if s.refused(w, r, failUpdatePR) {
			return
		}
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/merge_requests/"))
		b := s.body(r)
		pr, ok := s.updatePR(id, b["title"], b["description"])
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"iid": pr.ID, "web_url": s.prURL(pr.ID), "title": pr.Title})

	default:
		http.NotFound(w, r)
	}
}

func unescape(s string) string {
	out, err := url.PathUnescape(s)
	if err != nil {
		return s
	}
	return out
}
