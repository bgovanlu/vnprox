// SPDX-License-Identifier: Apache-2.0

package powerdns

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordingServer stands in for a PowerDNS Authoritative Server. It records
// what the client actually sent, because every assertion in this file is
// really about matching PowerdnsPlugin.pm's wire behaviour — the previous
// implementation of this feature passed its tests too, against routes that
// did not exist.
type recordingServer struct {
	*httptest.Server
	mu       chan struct{}
	requests []recordedRequest
}

type recordedRequest struct {
	Method string
	Path   string
	Query  string
	APIKey string
	CType  string
	Body   string
}

func newRecordingServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *recordingServer {
	t.Helper()
	rs := &recordingServer{mu: make(chan struct{}, 1)}
	rs.mu <- struct{}{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(strings.Builder)
		if r.Body != nil {
			buf := make([]byte, 1<<16)
			for {
				n, err := r.Body.Read(buf)
				body.Write(buf[:n])
				if err != nil {
					break
				}
			}
		}
		<-rs.mu
		rs.requests = append(rs.requests, recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			APIKey: r.Header.Get("X-API-Key"),
			CType:  r.Header.Get("Content-Type"),
			Body:   body.String(),
		})
		rs.mu <- struct{}{}
		handler(w, r)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordingServer) last(t *testing.T) recordedRequest {
	t.Helper()
	<-rs.mu
	defer func() { rs.mu <- struct{}{} }()
	if len(rs.requests) == 0 {
		t.Fatal("no request was made")
	}
	return rs.requests[len(rs.requests)-1]
}

// clientFor builds a client whose base URL mirrors what an operator puts in
// PVE's `url` field: a full API base including the server segment, because
// PowerdnsPlugin.pm appends "/zones/$zone" to it and adds nothing else.
func clientFor(t *testing.T, rs *recordingServer) *Client {
	t.Helper()
	c, err := New(Config{URL: rs.URL + "/api/v1/servers/localhost", Key: "sekrit"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestZone_HitsThePluginsOwnPath(t *testing.T) {
	rs := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Zone{
			ID: "example.com.", Name: "example.com.", Kind: "Native",
			RRSets: []RRSet{{
				Name: "web.example.com.", Type: "A", TTL: 300,
				Records: []Record{{Content: "10.0.0.5"}},
			}},
		})
	})
	c := clientFor(t, rs)

	z, err := c.Zone(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("Zone: %v", err)
	}

	got := rs.last(t)
	// The path is the plugin's: "$url/zones/$zone". Not /api/v1 twice, not a
	// PVE-shaped /cluster/... route.
	if want := "/api/v1/servers/localhost/zones/example.com."; got.Path != want {
		t.Errorf("path = %q, want %q", got.Path, want)
	}
	if got.APIKey != "sekrit" {
		t.Errorf("X-API-Key = %q, want the plugin's key", got.APIKey)
	}
	if len(z.RRSets) != 1 || z.RRSets[0].Records[0].Content != "10.0.0.5" {
		t.Errorf("rrsets = %+v, want the one A record", z.RRSets)
	}
}

func TestVerifyZone_AsksForNoRRSets(t *testing.T) {
	rs := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"example.com."}`))
	})
	c := clientFor(t, rs)

	if err := c.VerifyZone(context.Background(), "example.com."); err != nil {
		t.Fatalf("VerifyZone: %v", err)
	}
	got := rs.last(t)
	if got.Query != "rrsets=false" {
		t.Errorf("query = %q, want rrsets=false (the plugin's verify_zone)", got.Query)
	}
}

func TestPing_GetsTheBareBase(t *testing.T) {
	rs := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	c := clientFor(t, rs)

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if want := "/api/v1/servers/localhost"; rs.last(t).Path != want {
		t.Errorf("path = %q, want %q (on_update_hook GETs the bare url)", rs.last(t).Path, want)
	}
}

func TestPatch_SendsTheRRSetsEnvelope(t *testing.T) {
	rs := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	c := clientFor(t, rs)

	err := c.Patch(context.Background(), "example.com", []RRSet{
		SetSingle("web.example.com.", "A", "10.0.0.9", 300),
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got := rs.last(t)
	if got.Method != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", got.Method)
	}
	if !strings.HasPrefix(got.CType, "application/json") {
		t.Errorf("content-type = %q, want application/json", got.CType)
	}
	var body struct {
		RRSets []RRSet `json:"rrsets"`
	}
	if err := json.Unmarshal([]byte(got.Body), &body); err != nil {
		t.Fatalf("decoding sent body %q: %v", got.Body, err)
	}
	if len(body.RRSets) != 1 || body.RRSets[0].ChangeType != ChangeReplace {
		t.Fatalf("body = %+v, want one REPLACE rrset", body.RRSets)
	}
	if body.RRSets[0].Records[0].Content != "10.0.0.9" {
		t.Errorf("content = %q, want 10.0.0.9", body.RRSets[0].Records[0].Content)
	}
}

// A changetype-less rrset is silently ignored by PowerDNS, which would make
// an apply report success having written nothing. That must be a client-side
// error, not a 200.
func TestPatch_RefusesAnRRSetWithNoChangeType(t *testing.T) {
	rs := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a malformed patch must not reach the server")
	})
	c := clientFor(t, rs)

	err := c.Patch(context.Background(), "example.com", []RRSet{{Name: "a.example.com.", Type: "A"}})
	if err == nil {
		t.Fatal("want an error for a missing changetype")
	}
	if !strings.Contains(err.Error(), "changetype") {
		t.Errorf("error = %v, want it to name the missing changetype", err)
	}
}

func TestPatch_NoChangesIsNoRequest(t *testing.T) {
	rs := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an empty change list must not produce a request")
	})
	c := clientFor(t, rs)

	if err := c.Patch(context.Background(), "example.com", nil); err != nil {
		t.Fatalf("Patch(nil): %v", err)
	}
}

func TestAPIError_CarriesPowerDNSsOwnMessage(t *testing.T) {
	rs := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Could not find domain 'nope.example.'"}`))
	})
	c := clientFor(t, rs)

	_, err := c.Zone(context.Background(), "nope.example")
	if err == nil {
		t.Fatal("want an error")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false, want true", err)
	}
	if !strings.Contains(err.Error(), "Could not find domain") {
		t.Errorf("error = %v, want PowerDNS's own message in it", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Errorf("want an *APIError with status 404, got %#v", err)
	}
}

func TestNew_RejectsAnEmptyURL(t *testing.T) {
	if _, err := New(Config{Key: "k"}); !errors.Is(err, ErrNoURL) {
		t.Errorf("New with no url = %v, want ErrNoURL", err)
	}
}

// The pin must be a real check. An unparseable fingerprint has to fail at
// construction — a client that quietly falls back to "no pinning" because the
// operator typed the digest wrong is the whole reason to be strict here.
func TestNew_RejectsAnUnparseableFingerprint(t *testing.T) {
	for _, bad := range []string{"not-hex", "AA:BB", strings.Repeat("z", 64)} {
		if _, err := New(Config{URL: "https://pdns.example:8081/api/v1/servers/localhost", Fingerprint: bad}); err == nil {
			t.Errorf("New with fingerprint %q succeeded, want an error", bad)
		}
	}
}

func TestParseFingerprint_AcceptsBothOfPVEsForms(t *testing.T) {
	sum := sha256.Sum256([]byte("cert"))
	colon := formatFingerprint(sum)
	bare := strings.ReplaceAll(colon, ":", "")

	for name, in := range map[string]string{
		"pve colon-separated uppercase": colon,
		"bare hex, lowercased":          strings.ToLower(bare),
	} {
		got, err := parseFingerprint(in)
		if err != nil {
			t.Fatalf("%s: parseFingerprint: %v", name, err)
		}
		if got != sum {
			t.Errorf("%s: parsed digest does not round-trip", name)
		}
	}
}

// End-to-end over real TLS: a pinned client must accept the certificate whose
// digest it was given, and reject any other — including one the system trust
// store would also reject, so the test is not accidentally passing on
// ordinary verification.
func TestPinnedTLS_AcceptsTheMatchAndRejectsTheRest(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"example.com."}`))
	}))
	t.Cleanup(srv.Close)

	leaf := srv.Certificate()
	good := formatFingerprint(sha256.Sum256(leaf.Raw))

	c, err := New(Config{URL: srv.URL + "/api/v1/servers/localhost", Fingerprint: good})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if pingErr := c.Ping(context.Background()); pingErr != nil {
		t.Fatalf("pinned client rejected the certificate it pinned: %v", pingErr)
	}

	wrong := formatFingerprint(sha256.Sum256([]byte("some other certificate")))
	c2, err := New(Config{URL: srv.URL + "/api/v1/servers/localhost", Fingerprint: wrong})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pingErr := c2.Ping(context.Background())
	if pingErr == nil {
		t.Fatal("pinned client accepted a certificate that does not match the pin")
	}
	if !strings.Contains(pingErr.Error(), "fingerprint") {
		t.Errorf("error = %v, want it to name the fingerprint mismatch", pingErr)
	}
}

// PVE's own callback disables hostname verification when a fingerprint is
// set, because a pinned PowerDNS box is normally self-signed with a name that
// does not match. vnprox does the same, and this proves it: httptest's
// certificate is for 127.0.0.1/example.com, so a request via a different name
// would fail hostname verification — the pin is what carries the connection.
func TestPinnedTLS_DoesNotDependOnTheSystemTrustStore(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	// Unpinned, the same URL must fail: httptest's CA is not in the trust
	// store. If this passed, the pinned case above would prove nothing.
	unpinned, err := New(Config{URL: srv.URL + "/api/v1/servers/localhost"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if pingErr := unpinned.Ping(context.Background()); pingErr == nil {
		t.Fatal("an unpinned client trusted httptest's self-signed certificate — the pinned test proves nothing")
	}

	pinned, err := New(Config{
		URL:         srv.URL + "/api/v1/servers/localhost",
		Fingerprint: formatFingerprint(sha256.Sum256(srv.Certificate().Raw)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := pinned.Ping(context.Background()); err != nil {
		t.Fatalf("pinned client failed against a self-signed server: %v", err)
	}
}

func TestPinnedTLS_MinimumVersion(t *testing.T) {
	cfg, err := pinnedTLS(formatFingerprint(sha256.Sum256([]byte("x"))))
	if err != nil {
		t.Fatalf("pinnedTLS: %v", err)
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, want at least TLS 1.2", cfg.MinVersion)
	}
	if cfg.VerifyPeerCertificate == nil {
		t.Fatal("InsecureSkipVerify is set with no VerifyPeerCertificate — that IS a bypass")
	}
	// The callback must reject an empty chain rather than treat "no
	// certificate" as "nothing to check".
	if err := cfg.VerifyPeerCertificate(nil, nil); err == nil {
		t.Error("the verify callback accepted an empty certificate chain")
	}
}

func TestEffectiveTTL_MatchesThePluginsFallbackChain(t *testing.T) {
	tests := []struct {
		name      string
		cfgTTL    int
		recordTTL int
		want      int
	}{
		{"record ttl wins", 600, 300, 300},
		{"plugin ttl when the record has none", 600, 0, 600},
		{"powerdns plugin default when neither is set", 0, 0, DefaultTTL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Config{TTL: tt.cfgTTL}).EffectiveTTL(tt.recordTTL); got != tt.want {
				t.Errorf("EffectiveTTL = %d, want %d", got, tt.want)
			}
		})
	}
}
