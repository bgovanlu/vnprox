// sigstoregate.go is sigstore.go's consumer half — SigstoreGate is
// gate.go's Gate, shaped for the Sigstore index-trust scheme instead of the
// Ed25519 one. It rides the identical hub.Client seam (WithHTTPClient) for
// the identical reason gate.go documents: the existing, unmodified client
// decodes a body this gate only produces after verification passes, and a
// revoked artifact is refused through the client's own fetch call with no
// extra network access.
//
// A daemon is wired with EXACTLY ONE of Gate or SigstoreGate, chosen by
// [hub] sig_mode (cmd/vnproxd/hubinstall.go) — never both, and never chosen
// by what a served index looks like. That is what makes a downgrade attack
// (serving an Ed25519-shaped index to a Sigstore-pinned installation, or vice
// versa) structurally impossible rather than merely checked: SigstoreGate
// never calls Verify (Ed25519), and Gate never calls VerifySigstore, so
// there is no code path in either that would even consider the other
// document shape trustworthy.

package hubreg

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// SigstoreGate verifies registry index responses under the Sigstore scheme
// (sigstore.go) and enforces revocation on artifact fetches, exactly as Gate
// does for Ed25519. Safe for concurrent use.
type SigstoreGate struct {
	doc      Document
	inner    Doer
	verifier *SigstoreVerifier
	mu       sync.Mutex
	verified bool
}

// NewSigstoreGate returns a SigstoreGate over inner (defaulting to a
// 15s-timeout http.Client, matching Gate's own default) that accepts an
// index only when its sibling Sigstore bundle (SigstoreBundleName) verifies
// against verifier. verifier is required — a SigstoreGate with no verifier
// configured refuses every index (ErrInvalidSigstoreConfig), the same
// fail-closed posture Gate takes with an empty trusted-fingerprint set.
func NewSigstoreGate(inner Doer, verifier *SigstoreVerifier) *SigstoreGate {
	if inner == nil {
		inner = &http.Client{Timeout: 15 * time.Second}
	}
	return &SigstoreGate{inner: inner, verifier: verifier}
}

// Document returns the last verified index document (ok=false before any
// index has been fetched and verified).
func (g *SigstoreGate) Document() (Document, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.doc, g.verified
}

// Do implements Doer. Index requests are verified (index bytes plus the
// sibling Sigstore bundle) and replayed as the client-shaped index;
// everything else is an artifact fetch, gated against the verified document.
func (g *SigstoreGate) Do(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/index.json") {
		return g.doIndex(req)
	}
	g.mu.Lock()
	doc, ok := g.doc, g.verified
	g.mu.Unlock()
	return gateArtifact(g.inner, doc, ok, req)
}

// doIndex fetches index.json and its sibling Sigstore bundle, verifies them
// together, and re-serves the client-shaped index. On any verification
// failure it returns an error rather than a body — the client never sees a
// partially trustworthy catalog, matching Gate.doIndex's contract.
func (g *SigstoreGate) doIndex(req *http.Request) (*http.Response, error) {
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

	bundleReq, err := http.NewRequestWithContext(req.Context(), http.MethodGet, sigstoreBundleURL(req.URL), nil)
	if err != nil {
		return nil, fmt.Errorf("hubreg: building sigstore bundle request: %w", err)
	}
	bundleResp, err := g.inner.Do(bundleReq)
	if err != nil {
		return nil, fmt.Errorf("hubreg: fetching sigstore bundle: %w", err)
	}
	if bundleResp.StatusCode != http.StatusOK {
		_ = bundleResp.Body.Close()
		return nil, fmt.Errorf("%w: fetching %s: registry returned %d", ErrInvalidSigstoreBundle, bundleReq.URL.Path, bundleResp.StatusCode)
	}
	bundleRaw, bundleReadErr := io.ReadAll(io.LimitReader(bundleResp.Body, MaxSigstoreBundleBytes+1))
	bundleCloseErr := bundleResp.Body.Close()
	if bundleReadErr != nil {
		return nil, fmt.Errorf("hubreg: reading sigstore bundle: %w", bundleReadErr)
	}
	if bundleCloseErr != nil {
		return nil, fmt.Errorf("hubreg: reading sigstore bundle: %w", bundleCloseErr)
	}

	doc, verr := VerifySigstore(raw, bundleRaw, g.verifier)
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
		Body:          io.NopCloser(strings.NewReader(string(body))),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

// sigstoreBundleURL derives the sibling bundle URL from an index.json
// request URL, on the exact same origin and directory — never a different
// host, so a served index can never point its own bundle fetch off-origin.
func sigstoreBundleURL(indexURL *url.URL) string {
	u := *indexURL
	u.Path = path.Join(path.Dir(u.Path), SigstoreBundleName)
	u.RawQuery = ""
	return u.String()
}
