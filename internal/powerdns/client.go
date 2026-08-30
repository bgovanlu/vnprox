// SPDX-License-Identifier: Apache-2.0

// Package powerdns is vnprox's client for the PowerDNS Authoritative Server
// HTTP API — the server PVE's SDN DNS plugin writes records into (T-4112).
//
// # Why this package exists
//
// vnprox used to read SDN DNS records through five `internal/pve` methods on
// `/cluster/sdn/dns/{zone}/records` and `/cluster/sdn/dns/{zone}/resolve`.
// Those routes do not exist. `pvesh usage` on PVE 9.2.4 answers
// `no such resource` for both
// (planning/reports/evidence/pve-9.2.4-sdn-dns-surface.txt), and nothing ever
// failed because internal/pvemock served the invented URL space too — the
// exact failure CLAUDE.md warns about, where a mock and the code that calls
// it agree with each other and with nothing real.
//
// PVE keeps no record copy of its own. `PVE/Network/SDN/Dns/PowerdnsPlugin.pm`
// (read off pvecube; transcript in the evidence file) writes each record
// straight into the backing PowerDNS server. So there is exactly one place
// records can be read from, and this package is it.
//
// # The contract, taken from the plugin rather than from documentation
//
// Every detail below is copied from `PowerdnsPlugin.pm` and the
// `PVE::Network::SDN::api_request` it calls, because the operator configures
// ONE endpoint (`/cluster/sdn/dns`'s `url`) and both PVE and vnprox must mean
// the same thing by it:
//
//   - The plugin's `url` is already a full API base including the server
//     segment — it appends `/zones/$zone` to it, and its own health check
//     (`on_update_hook`) GETs the bare url. So url is
//     `https://host:8081/api/v1/servers/localhost`, and this package appends
//     the same suffixes to the same base. It does NOT add `/api/v1` itself.
//   - Auth is the header `X-API-Key: <key>`, with
//     `Content-Type: application/json; charset=UTF-8`.
//   - Reads are `GET /zones/{zone}` (full, with rrsets) and
//     `GET /zones/{zone}?rrsets=false` (existence only, the plugin's
//     `verify_zone`).
//   - Writes are a single `PATCH /zones/{zone}` carrying `{"rrsets": [...]}`
//     with a per-rrset `changetype` of REPLACE or DELETE. PowerDNS has no
//     per-record route; an rrset is replaced whole. See rrset.go.
//   - The timeout is 30s, matching `api_request`'s `LWP::UserAgent` timeout.
//     A vnprox poll cycle must not hang on an unreachable DNS server.
//
// # TLS
//
// `fingerprint` is a real field on `/cluster/sdn/dns` (it is in the create
// usage block of the evidence transcript), and PVE's own handling of it is
// specific: when set, `api_request` disables hostname verification and
// installs a verify callback that accepts any certificate at depth != 0 and
// requires an exact SHA-256 match at depth 0. That is leaf pinning, and it is
// deliberately not the same as "verify normally, and also check the
// fingerprint" — a pinned self-signed certificate is the ordinary case for a
// PowerDNS box on a management network. pinnedTLS below reproduces it exactly;
// see its doc comment for why the pin is a real check and not a bypass.
//
// When no fingerprint is configured, the system trust store applies with
// hostname verification on, which is also what PVE does.
package powerdns

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// requestTimeout matches PVE::Network::SDN::api_request's own
// `LWP::UserAgent->new(timeout => 30)`. It is a per-request ceiling: a caller
// with a shorter context deadline (a collector poll, say) still wins.
const requestTimeout = 30 * time.Second

// maxResponseBytes caps how much of a response body this client will read. A
// PowerDNS zone with a large rrset list is legitimately big, so this is
// generous — it exists so a hostile or wedged endpoint cannot make vnproxd
// allocate without bound, not to constrain real zones.
const maxResponseBytes = 32 << 20 // 32 MiB

// Config is one PowerDNS server connection, as configured on PVE's side by a
// `/cluster/sdn/dns` plugin instance. The field names are PVE's, not
// PowerDNS's, because that is where an operator sets them.
//
// URL is the API base the plugin appends paths to (see the package comment —
// it already contains `/api/v1/servers/<server>`). Key is the X-API-Key.
// Fingerprint is the optional pinned SHA-256 of the server's leaf
// certificate, in PVE's colon-separated hex form. TTL is the plugin's default
// record TTL, applied by callers when a record carries no TTL of its own;
// PowerdnsPlugin.pm's own default when the field is unset is 14400.
type Config struct {
	URL         string
	Key         string
	Fingerprint string
	TTL         int
}

// DefaultTTL is PowerdnsPlugin.pm's fallback when a plugin instance sets no
// ttl: `my $ttl = $plugin_config->{ttl} ? $plugin_config->{ttl} : 14400;`.
// vnprox uses the same number so a record vnprox writes and a record PVE
// writes to the same zone do not differ in TTL for no reason.
const DefaultTTL = 14400

// EffectiveTTL returns the TTL to write for a record, applying the plugin
// default and then PowerDNS's own, exactly as PowerdnsPlugin.pm does.
func (c Config) EffectiveTTL(recordTTL int) int {
	if recordTTL > 0 {
		return recordTTL
	}
	if c.TTL > 0 {
		return c.TTL
	}
	return DefaultTTL
}

// Client talks to one PowerDNS server. It is safe for concurrent use.
type Client struct {
	http *http.Client
	base string
	key  string
}

// ErrNoURL is returned by New when a plugin instance carries no url. PVE
// declares url non-optional, so this means vnprox read the plugin config
// through a route or a permission level that redacted it — worth an explicit
// error rather than requests to the empty string.
var ErrNoURL = errors.New("powerdns: plugin instance has no url")

// New builds a client for one PowerDNS server. The returned client holds its
// own *http.Client because the TLS configuration is per-server: two plugin
// instances may pin different certificates.
func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if base == "" {
		return nil, ErrNoURL
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("powerdns: parsing url %q: %w", base, err)
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	if fp := strings.TrimSpace(cfg.Fingerprint); fp != "" {
		tlsCfg, err := pinnedTLS(fp)
		if err != nil {
			return nil, err
		}
		tr.TLSClientConfig = tlsCfg
	}

	return &Client{
		http: &http.Client{Transport: tr, Timeout: requestTimeout},
		base: base,
		key:  cfg.Key,
	}, nil
}

// APIError is a non-2xx response from PowerDNS. PowerDNS reports its own
// errors as `{"error": "..."}`, so Message carries that when present and the
// raw body otherwise — an operator reading a vnprox log should see what
// PowerDNS actually said, not "request failed".
type APIError struct {
	Method  string
	Path    string
	Message string
	Status  int
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("powerdns: %s %s: %d: %s", e.Method, e.Path, e.Status, e.Message)
	}
	return fmt.Sprintf("powerdns: %s %s: %d", e.Method, e.Path, e.Status)
}

// NotFound reports whether this error is PowerDNS's "no such zone". Callers
// distinguish it because a configured zone that PowerDNS does not serve is a
// config problem worth reporting, while an unreachable server is a transport
// problem worth retrying.
func (e *APIError) NotFound() bool { return e.Status == http.StatusNotFound }

// IsNotFound reports whether err is an APIError with status 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.NotFound()
}

// do issues one request against the plugin's API base. path is appended to
// the base verbatim (it must start with "/"), matching
// `powerdns_api_request`'s `"$config->{url}${path}"`.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("powerdns: encoding %s %s body: %w", method, path, err)
		}
		rdr = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return fmt.Errorf("powerdns: building %s %s: %w", method, path, err)
	}
	req.Header.Set("X-API-Key", c.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("powerdns: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	limited := io.LimitReader(resp.Body, maxResponseBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("powerdns: reading %s %s response: %w", method, path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Method: method, Path: path, Status: resp.StatusCode, Message: errorMessage(raw)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("powerdns: decoding %s %s response: %w", method, path, err)
	}
	return nil
}

// errorMessage pulls PowerDNS's own `{"error": "..."}` out of a failure body,
// falling back to the trimmed raw body. The fallback is bounded because this
// string ends up in a log line and in a changeset's lastError.
func errorMessage(raw []byte) string {
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Error != "" {
		return envelope.Error
	}
	msg := strings.TrimSpace(string(raw))
	const maxMsg = 512
	if len(msg) > maxMsg {
		return msg[:maxMsg] + "…"
	}
	return msg
}

// pinnedTLS reproduces PVE::Network::SDN::api_request's fingerprint mode
// exactly: hostname verification off, chain verification replaced by an exact
// SHA-256 match on the LEAF certificate.
//
// InsecureSkipVerify is set here and that is not a weakening — with a
// VerifyPeerCertificate callback that requires an exact digest match, the
// check is strictly stronger than PKI verification for this use, and it is
// the only way Go lets a caller replace (rather than add to) the default
// verification. The callback below never returns nil for an unmatched leaf,
// so there is no path where a certificate is accepted unchecked. A
// fingerprint that cannot be parsed is an error at construction time, not a
// silently-unpinned client — the failure mode where a typo turns pinning off
// is exactly what this must not do.
func pinnedTLS(fingerprint string) (*tls.Config, error) {
	want, err := parseFingerprint(fingerprint)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		// #nosec G402 -- replaced, not disabled: VerifyPeerCertificate below
		// requires an exact SHA-256 leaf match. See this function's comment.
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("powerdns: server presented no certificate")
			}
			// Depth 0 is the leaf, which is rawCerts[0] — the same
			// certificate PVE's own callback checks and the same one it
			// ignores everything else in favour of.
			got := sha256.Sum256(rawCerts[0])
			if got != want {
				return fmt.Errorf("powerdns: certificate fingerprint %s does not match the pinned %s",
					formatFingerprint(got), formatFingerprint(want))
			}
			return nil
		},
	}, nil
}

// parseFingerprint accepts PVE's colon-separated uppercase hex form (the
// `fingerprint-sha256` standard option, `([A-Fa-f0-9]{2}:){31}[A-Fa-f0-9]{2}`)
// and also the bare 64-hex form, case-insensitively. Anything else is an
// error: a fingerprint vnprox cannot parse must never become "no pinning".
func parseFingerprint(s string) ([32]byte, error) {
	var out [32]byte
	cleaned := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
	if len(cleaned) != 64 {
		return out, fmt.Errorf("powerdns: %q is not a SHA-256 fingerprint (want 64 hex digits, got %d)", s, len(cleaned))
	}
	raw, err := hex.DecodeString(cleaned)
	if err != nil {
		return out, fmt.Errorf("powerdns: parsing fingerprint %q: %w", s, err)
	}
	copy(out[:], raw)
	return out, nil
}

// formatFingerprint renders a digest in PVE's own colon-separated uppercase
// hex, so a mismatch message can be pasted straight into `pvesh set
// /cluster/sdn/dns/<id> --fingerprint ...`.
func formatFingerprint(sum [32]byte) string {
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	return strings.Join(parts, ":")
}
