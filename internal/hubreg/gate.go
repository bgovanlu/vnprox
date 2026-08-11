// gate.go is the consumer half: the thing that makes a signed index and its
// revocations *bind* on the existing, unmodified internal/hub client.
//
// Why a transport and not a wrapper around hub.Client: T-2803 AC1 requires the
// existing client to consume the real index unmodified, and hub.Client already
// exposes exactly one seam for this — WithHTTPClient(httpDoer). Sitting there
// means:
//
//   - the client's Index() decodes a body this gate only produces after the
//     signature verified, so a corrupted index surfaces as an error from the
//     client's own API and no entry is ever partially loaded (AC5);
//   - a revoked artifact is refused through the client's own FetchBlueprintBundle
//     / FetchPluginArtifact call (AC3), because the gate answers that fetch from
//     the document it already holds — the refusal path makes no network call at
//     all, which is the second half of AC3 and is asserted directly in the tests;
//   - internal/hub keeps its property of making no trust decision. The gate does,
//     and it is the only new code on the path.
//
// The gate is an allowlist, not a blocklist: it refuses any artifact URL the
// verified catalog does not list, so an entry that never appeared in a signed
// index is not fetchable even if a caller hand-builds a hub.Entry for it.

package hubreg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/hub"
)

// Doer is the minimal HTTP seam — *http.Client satisfies it, and so does
// Gate, which is what lets a Gate be handed to hub.WithHTTPClient.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Gate verifies registry index responses and enforces revocation on artifact
// fetches. It is safe for concurrent use.
type Gate struct {
	doc      Document
	inner    Doer
	trusted  []string
	mu       sync.Mutex
	verified bool
}

// NewGate returns a Gate over inner (defaulting to a 15s-timeout http.Client,
// matching internal/hub's own default) that accepts an index only if it is
// signed by one of trustedFingerprints. An empty trusted set refuses every
// index — the gate has no "verification off" mode; a caller that wants the
// pre-T-2803 unverified behaviour simply does not install a gate.
func NewGate(inner Doer, trustedFingerprints []string) *Gate {
	if inner == nil {
		inner = &http.Client{Timeout: 15 * time.Second}
	}
	trusted := make([]string, 0, len(trustedFingerprints))
	for _, fp := range trustedFingerprints {
		if fp = strings.TrimSpace(fp); fp != "" {
			trusted = append(trusted, fp)
		}
	}
	return &Gate{inner: inner, trusted: trusted}
}

// Document returns the last verified index document (ok=false before any
// index has been fetched and verified).
func (g *Gate) Document() (Document, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.doc, g.verified
}

// Do implements Doer. Index requests are verified and replayed as the
// client-shaped index; everything else is an artifact fetch, gated against
// the verified document.
func (g *Gate) Do(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/index.json") {
		return g.doIndex(req)
	}
	return g.doArtifact(req)
}

// doIndex fetches, verifies, and re-serves the index. On any verification
// failure it returns an error rather than a body: the client never sees a
// partially trustworthy catalog.
func (g *Gate) doIndex(req *http.Request) (*http.Response, error) {
	resp, err := g.inner.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// Let the client report the registry's own status verbatim.
		return resp, nil
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxIndexBytes+1))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("hubreg: reading registry index: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("hubreg: reading registry index: %w", closeErr)
	}
	doc, verr := Verify(raw, g.trusted)
	if verr != nil {
		return nil, verr
	}
	g.mu.Lock()
	g.doc, g.verified = doc, true
	g.mu.Unlock()

	body, merr := marshalIndex(doc)
	if merr != nil {
		return nil, merr
	}
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

// doArtifact decides, from the already-fetched signed index alone, whether a
// fetch may proceed. Both refusal paths return before g.inner is touched —
// that is AC3's "no network access beyond the already-fetched signed index",
// and it is what the offline test asserts by failing if inner is called.
func (g *Gate) doArtifact(req *http.Request) (*http.Response, error) {
	g.mu.Lock()
	doc, ok := g.doc, g.verified
	g.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w (fetching %s)", ErrNoVerifiedIndex, req.URL.Path)
	}
	for _, e := range doc.Entries {
		if !artifactMatches(e, req.URL) {
			continue
		}
		if rev, revoked := doc.IsRevoked(e); revoked {
			return nil, fmt.Errorf("%w: %s %s@%s: %s", ErrRevoked, e.Type, e.ID, e.Version, rev.Reason)
		}
		return g.inner.Do(req)
	}
	return nil, fmt.Errorf("%w: %s", ErrUnlistedArtifact, req.URL.Path)
}

// artifactMatches reports whether u is the URL of e's artifact. Entry URLs are
// absolute paths or absolute URLs (validateEntry enforces that), so resolving
// against the request URL is exact rather than base-dependent.
func artifactMatches(e hub.Entry, u *url.URL) bool {
	ref, err := url.Parse(e.ArtifactURL)
	if err != nil {
		return false
	}
	resolved := u.ResolveReference(ref)
	return strings.EqualFold(resolved.Host, u.Host) &&
		resolved.Path == u.Path &&
		resolved.RawQuery == u.RawQuery
}

// marshalIndex renders the client-facing projection of a verified document.
func marshalIndex(doc Document) ([]byte, error) {
	body, err := json.Marshal(doc.HubIndex())
	if err != nil {
		return nil, fmt.Errorf("hubreg: encoding verified index for the client: %w", err)
	}
	return body, nil
}
