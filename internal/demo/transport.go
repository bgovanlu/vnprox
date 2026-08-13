package demo

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
)

// HTTPClient returns the *only* HTTP client a demo daemon's PVE clients are
// ever built with. Its transport dispatches into the in-process pvemock
// server; it has no Dialer, no proxy, no TLS config, and no reference to
// net at all.
//
// This is the structural half of "demo mode cannot be enabled against a
// real PVE endpoint". The configuration half (internal/config refusing to
// load a demo daemon with a PVE endpoint configured) tells an operator they
// made a mistake; this one means that even if that check were removed, a
// demo daemon's PVE client still could not reach anything, because there is
// no code path from it to a socket.
func (m *Mode) HTTPClient() *http.Client {
	return &http.Client{Transport: inProcessTransport{handler: m.server}}
}

// inProcessTransport is an http.RoundTripper that serves requests by calling
// an http.Handler directly.
//
// Deliberately NOT httptest.NewServer + its client: that binds a real
// loopback socket, which is a network operation, would appear in `ss -ltn`
// on the demo user's machine, and would leave a listener whose port has to
// be registered in testdata/dev-ports.tsv. Nothing here binds anything.
//
// Deliberately NOT net/http/httptest.ResponseRecorder either, even though
// it would do: httptest's package doc says it is for testing, and this runs
// in a shipped binary. The recorder below is ~30 lines and says what it is.
type inProcessTransport struct {
	handler http.Handler
}

func (t inProcessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.handler == nil {
		return nil, fmt.Errorf("demo: in-process transport has no handler; this daemon has no synthetic cluster to answer %s %s", req.Method, req.URL)
	}
	// An already-cancelled caller gets an error, not a served response.
	// *http.Transport does this for a real request; a custom RoundTripper
	// has to do it itself, and a demo daemon should abandon a PVE call when
	// the client that asked for it has hung up.
	if err := req.Context().Err(); err != nil {
		return nil, fmt.Errorf("demo: %s %s: %w", req.Method, req.URL, err)
	}

	// STRIP THE CALLER'S CONTEXT VALUES, KEEP ITS CANCELLATION.
	//
	// This is not defensive tidiness; it is a bug that was found the hard
	// way. When an API request handled by vnproxd's own chi router causes a
	// PVE call (a login, say), the outbound request carries the *inbound*
	// HTTP request's context — and that context holds chi's own
	// *chi.Context under RouteCtxKey. chi's Mux.ServeHTTP checks for
	// exactly that key and, finding one, REUSES it instead of allocating a
	// fresh one. pvemock's router then routes against the outer router's
	// already-consumed RoutePath and answers 404.
	//
	// The symptom was maximally confusing: the collector's PVE calls (which
	// run on a background context) worked, while an operator's login
	// through the very same transport got "404 page not found" from a route
	// that plainly exists.
	//
	// Cancellation and deadlines must survive — a demo daemon should still
	// abandon a PVE call when the client hangs up — so this is not
	// context.Background(): it is the caller's context with Value defanged.
	inbound := req.Clone(withoutValues(req.Context()))
	if inbound.Body == nil {
		inbound.Body = http.NoBody
	}
	// RequestURI must be set for a server-side request and must NOT be set
	// on a client-side one — this is the seam where a request crosses from
	// one to the other.
	inbound.RequestURI = req.URL.RequestURI()

	rec := &responseCapture{header: make(http.Header), status: http.StatusOK}
	t.handler.ServeHTTP(rec, inbound)

	body := rec.body.Bytes()
	resp := &http.Response{
		Status:        fmt.Sprintf("%d %s", rec.status, http.StatusText(rec.status)),
		StatusCode:    rec.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        rec.header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
	return resp, nil
}

// valuelessContext forwards Deadline/Done/Err to its parent and answers
// nil for every Value lookup. See RoundTrip's comment on why.
type valuelessContext struct{ context.Context }

func (valuelessContext) Value(any) any { return nil }

func withoutValues(ctx context.Context) context.Context {
	return valuelessContext{Context: ctx}
}

// responseCapture is a minimal http.ResponseWriter collecting a handler's
// response in memory.
type responseCapture struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

func (c *responseCapture) Header() http.Header { return c.header }

func (c *responseCapture) WriteHeader(status int) {
	if c.wroteHeader {
		return
	}
	c.status = status
	c.wroteHeader = true
}

func (c *responseCapture) Write(p []byte) (int, error) {
	c.wroteHeader = true
	return c.body.Write(p)
}

// Flush satisfies http.Flusher for handlers that stream. There is nothing
// to flush to — the whole response is already in memory.
func (c *responseCapture) Flush() {}
