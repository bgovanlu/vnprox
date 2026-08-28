// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- transports -------------------------------------------------------------

// spyTransport is the only transport these tests use, deliberately.
//
// It counts calls, records bodies, and — when fatalOnCall is set — reports a
// failure through `report`. Using ONE type for both the "nothing must be
// sent" assertions and the control legs is what makes those assertions mean
// something: the control legs prove this exact object registers and reports
// a call when a request genuinely reaches the network, so a zero count
// elsewhere is evidence rather than an untested assumption.
type spyTransport struct {
	report      func(format string, args ...any)
	respond     func() (*http.Response, error)
	bodies      [][]byte
	mu          sync.Mutex
	calls       atomic.Int64
	fatalOnCall bool
}

func (s *spyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.calls.Add(1)
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.mu.Unlock()

	if s.fatalOnCall && s.report != nil {
		s.report("an outbound telemetry request was made to %s carrying %d bytes; nothing may be sent here", req.URL, len(body))
	}
	if s.respond != nil {
		return s.respond()
	}
	return okResponse(req), nil
}

func (s *spyTransport) body(i int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[i]
}

func okResponse(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		Header:     http.Header{},
		Request:    req,
	}
}

// failIfCalled builds a spy that fails t the moment anything reaches it.
func failIfCalled(t *testing.T) *spyTransport {
	t.Helper()
	return &spyTransport{fatalOnCall: true, report: func(format string, args ...any) {
		t.Errorf(format, args...)
	}}
}

// --- AC1: off means nothing leaves ------------------------------------------

// TestNothingIsSentWhenTelemetryIsOff is AC1.
//
// The assertion is made by a transport that fails the test if it is called,
// not by re-reading the config the code under test just read — the second
// would pass for an implementation that checked Enabled and then sent
// anyway.
func TestNothingIsSentWhenTelemetryIsOff(t *testing.T) {
	snap := mustBuild(t)

	cases := []struct {
		name string
		dst  Destination
	}{
		{
			name: "telemetry unset (the zero value, which is what an install with no [telemetry] section has)",
			dst:  Destination{},
		},
		{
			name: "telemetry explicitly false, with an endpoint sitting right there in the config",
			dst:  Destination{Enabled: false, Endpoint: "https://collector.example/vnprox"},
		},
		{
			name: "telemetry on but no endpoint — vnprox ships no default collector",
			dst:  Destination{Enabled: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := failIfCalled(t)
			dst := tc.dst
			dst.Transport = tr

			err := Submit(context.Background(), dst, snap)
			if err == nil {
				t.Fatal("Submit reported success while telemetry was off")
			}
			if !errors.Is(err, ErrDisabled) && !errors.Is(err, ErrNoEndpoint) {
				t.Fatalf("want ErrDisabled or ErrNoEndpoint, got %v", err)
			}
			if got := tr.calls.Load(); got != 0 {
				t.Fatalf("%d outbound request(s) were made with telemetry off", got)
			}
		})
	}

	// The control leg, and it is not optional: a transport that fails when
	// called proves nothing until it is shown to notice a call at all. Same
	// type, same wiring, same fatalOnCall — only the config differs.
	t.Run("control: the same fail-if-called transport DOES register and report a call when one is made", func(t *testing.T) {
		var reported string
		tr := &spyTransport{fatalOnCall: true, report: func(format string, args ...any) {
			reported = fmt.Sprintf(format, args...)
		}}
		dst := Destination{Enabled: true, Endpoint: "https://collector.example/vnprox", Transport: tr}

		if err := Submit(context.Background(), dst, snap); err != nil {
			t.Fatalf("Submit with telemetry on: %v", err)
		}
		if got := tr.calls.Load(); got != 1 {
			t.Fatalf("the spy recorded %d calls, want 1 — it cannot detect a leak it does not count", got)
		}
		if !strings.Contains(reported, "an outbound telemetry request was made") {
			t.Fatalf("the spy did not report the call it saw (%q); every 'nothing was sent' assertion above rests on it doing so", reported)
		}
	})
}

// --- AC2: preview and the wire are the same buffer ---------------------------

// TestPreviewAndTheTransmittedBytesAreTheSameBuffer is AC2.
//
// Not "both are produced by the same function" — captured and compared, and
// then compared again by ADDRESS, because two calls that agree today are
// exactly what drifts.
func TestPreviewAndTheTransmittedBytesAreTheSameBuffer(t *testing.T) {
	snap := mustBuild(t)

	var preview bytes.Buffer
	if err := snap.Preview(&preview); err != nil {
		t.Fatalf("Preview: %v", err)
	}

	tr := &spyTransport{}
	dst := Destination{Enabled: true, Endpoint: "https://collector.example/vnprox", Transport: tr}
	if err := Submit(context.Background(), dst, snap); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if tr.calls.Load() != 1 {
		t.Fatalf("the transport saw %d calls, want 1", tr.calls.Load())
	}

	sent := tr.body(0)
	if !bytes.Equal(preview.Bytes(), sent) {
		t.Fatalf("preview and the transmitted body differ.\npreview (%d bytes):\n%s\nsent (%d bytes):\n%s",
			preview.Len(), preview.String(), len(sent), sent)
	}
	if len(sent) == 0 {
		t.Fatal("both were empty, which would make the comparison above vacuous")
	}

	// The structural half: both paths read the snapshot's own allocation.
	// If Bytes() ever starts returning a copy, or Preview or Submit starts
	// re-encoding, these addresses stop matching.
	if &snap.raw[0] != &snap.Bytes()[0] {
		t.Fatal("Bytes() returned a different allocation from the snapshot's own buffer")
	}
	if !bytes.Equal(preview.Bytes(), snap.raw) {
		t.Fatal("Preview did not write the snapshot's buffer")
	}
}

// TestOnlyOnePlaceMarshalsAPayload is the other half of "they cannot drift":
// there is exactly one encoder in this package, so there is no second
// rendering for preview and send to disagree about.
//
// A source-level assertion is unusual and deliberate. The property is "no
// second marshal exists", and no runtime test can observe the absence of
// code that has not been written yet.
func TestOnlyOnePlaceMarshalsAPayload(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	// Parsed, not grepped: the doc comments in this package talk ABOUT
	// json.Marshal, and a test that counted mentions would fail on prose
	// and pass on a call somebody wrote inside a string.
	found := map[string]int{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Clean(name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			sel, isSel := call.Fun.(*ast.SelectorExpr)
			if !isSel {
				return true
			}
			pkg, isIdent := sel.X.(*ast.Ident)
			if isIdent && pkg.Name == "json" && strings.HasPrefix(sel.Sel.Name, "Marshal") {
				found[name]++
			}
			return true
		})
	}
	if len(found) != 1 || found["snapshot.go"] != 1 {
		t.Fatalf("want exactly one json.Marshal in this package, in snapshot.go's marshalPayload; found %v. "+
			"A second encoder is how `telemetry preview` and the bytes on the wire start to differ.", found)
	}
}

// --- AC3, at the send: a bad payload cannot leave ----------------------------

// TestSubmitRefusesAPayloadTheGuardRejects builds a Snapshot by hand,
// bypassing Build entirely, to assert the check runs BEFORE the send rather
// than only at construction.
func TestSubmitRefusesAPayloadTheGuardRejects(t *testing.T) {
	bad := mutatedPayload(t, func(p *Payload) {
		p.Kernel = "6.8.12-4-pve on node-alpha.example.com"
	})
	snap := &Snapshot{raw: bad, known: KnownFromReport(sampleReport())}

	tr := failIfCalled(t)
	err := Submit(context.Background(), Destination{
		Enabled: true, Endpoint: "https://collector.example/vnprox", Transport: tr,
	}, snap)

	if err == nil {
		t.Fatal("Submit sent a payload the guard rejects")
	}
	if !strings.Contains(err.Error(), "refusing to send") {
		t.Errorf("the refusal does not say it refused: %v", err)
	}
	if got := tr.calls.Load(); got != 0 {
		t.Fatalf("%d request(s) were made with a rejected payload", got)
	}
}

// --- AC5: a hanging or broken collector is harmless --------------------------

// TestAHangingCollectorNeverBlocks is AC5, asserted with a transport that
// hangs: Start returns while the request is still stuck inside the
// transport, and nothing waits on it.
func TestAHangingCollectorNeverBlocks(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	tr := &spyTransport{respond: func() (*http.Response, error) {
		entered <- struct{}{}
		<-release
		return nil, errors.New("collector went away")
	}}

	snap := mustBuild(t)
	start := time.Now()
	done := Start(context.Background(), Destination{
		Enabled: true, Endpoint: "https://collector.example/vnprox", Transport: tr, Timeout: time.Minute,
	}, snap, nil)
	elapsed := time.Since(start)

	// The request must actually have reached the (hanging) transport —
	// otherwise this test would pass against an implementation that sends
	// nothing at all.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the transport, so the hang this test is about never happened")
	}

	// And Start must not have waited for it.
	select {
	case err := <-done:
		t.Fatalf("Start waited for the collector before returning (err %v)", err)
	default:
	}
	if elapsed > 2*time.Second {
		t.Errorf("Start took %s to return while the collector hung; it must not wait at all", elapsed)
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the failed send reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the background send never finished after the collector was released")
	}
}

// TestASendFailureIsNonFatal: every way a send can fail produces an error on
// the channel and nothing else. Nothing here panics, blocks or has any
// effect a caller could confuse with a verdict.
func TestASendFailureIsNonFatal(t *testing.T) {
	snap := mustBuild(t)

	cases := []struct {
		name    string
		respond func() (*http.Response, error)
		wantSub string
	}{
		{
			name:    "the collector refuses the connection",
			respond: func() (*http.Response, error) { return nil, errors.New("connection refused") },
			wantSub: "connection refused",
		},
		{
			name: "the collector answers 500",
			respond: func() (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Status:     "500 Internal Server Error",
					Body:       io.NopCloser(strings.NewReader("nope")),
					Header:     http.Header{},
				}, nil
			},
			wantSub: "500",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := &spyTransport{respond: tc.respond}
			done := Start(context.Background(), Destination{
				Enabled: true, Endpoint: "https://collector.example/vnprox", Transport: tr,
			}, snap, nil)
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("want a failure, got success")
				}
				if !strings.Contains(err.Error(), tc.wantSub) {
					t.Errorf("the error does not mention %q: %v", tc.wantSub, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the background send never reported")
			}
		})
	}
}

// TestStartSendsNothingWhenDisabled: the background path has the same gate
// as the foreground one, asserted the same way.
func TestStartSendsNothingWhenDisabled(t *testing.T) {
	tr := failIfCalled(t)
	done := Start(context.Background(), Destination{Transport: tr}, mustBuild(t), nil)
	select {
	case err := <-done:
		if !errors.Is(err, ErrDisabled) {
			t.Fatalf("want ErrDisabled, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start never reported")
	}
	if got := tr.calls.Load(); got != 0 {
		t.Fatalf("%d request(s) were made with telemetry off", got)
	}
}

// TestSubmitSetsTheContentType is a small contract check: a collector that
// gets a body with no content type has to sniff, and sniffing is how a
// payload gets logged somewhere it should not be.
func TestSubmitSetsTheContentType(t *testing.T) {
	var gotType string
	tr := &spyTransport{}
	tr.respond = func() (*http.Response, error) { return okResponse(nil), nil }
	dst := Destination{Enabled: true, Endpoint: "https://collector.example/vnprox", Transport: &headerSpy{inner: tr, seen: &gotType}}
	if err := Submit(context.Background(), dst, mustBuild(t)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if gotType != ContentType {
		t.Errorf("Content-Type = %q, want %q", gotType, ContentType)
	}
}

type headerSpy struct {
	inner http.RoundTripper
	seen  *string
}

func (h *headerSpy) RoundTrip(req *http.Request) (*http.Response, error) {
	*h.seen = req.Header.Get("Content-Type")
	return h.inner.RoundTrip(req)
}
