// SPDX-License-Identifier: Apache-2.0

// Package pvecassette is T-2502's on-disk format for one observed PVE
// request/response pair, plus the matching rule that decides whether a
// later request is the same request.
//
// It exists because every fixture in this repository was, until now, a
// guess. T-2108 found four defects sitting under green unit tests whose
// fixtures invented the shape the code expected; that is not a testing
// accident, it is the structural consequence of hand-writing every
// fixture. A cassette is the alternative: a response *observed* from a
// real PVE, written down verbatim, with the PVE version that produced it
// recorded alongside it.
//
// Three properties are deliberate and load-bearing:
//
//   - **Verbatim bodies.** Body is the exact bytes PVE sent, held as a
//     JSON string rather than as embedded JSON, so a cassette can be
//     replayed byte-identically (T-2502 AC1). Embedding it as
//     json.RawMessage would read better and would let encoding/json
//     re-compact it on the way out; "reads better" is not worth a fixture
//     that is almost what PVE said.
//   - **Nothing about the request but method, path and query.** Request
//     headers and request bodies are never recorded, in any form. That is
//     what makes it structurally impossible for a cassette to carry the
//     Authorization header or the password field of the login that
//     produced it.
//   - **Refusal over redaction.** A response body carrying a credential
//     fails the write (see Writer.Write) rather than being written with
//     the credential replaced. A cassette with a hole where a ticket used
//     to be is no longer a description of what PVE returned, and the whole
//     point of the format is that it is.
//
// The recorder lives in internal/pve (record.go, driven by
// VNPROX_PVE_RECORD); the replay server lives in internal/pvemock
// (replay.go). Both sides share this package so neither can drift on what
// "the same request" means.
package pvecassette

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Cassette is one recorded PVE request/response pair. Field order is the
// on-disk JSON document's order, because cassettes are read by humans
// before they are committed; construct with keyed literals only.
//
//nolint:govet // fieldalignment: wire shape, see above
type Cassette struct {
	// RecordedAt is when the exchange was observed, in UTC.
	RecordedAt time.Time `json:"recordedAt"`
	// PVEVersion is the PVE release that produced the response — the
	// answer to "which Proxmox says this?", which is the question a
	// hand-written fixture can never answer. It is also the cassette
	// directory name.
	PVEVersion string `json:"pveVersion"`
	// Method is the HTTP method, upper-case.
	Method string `json:"method"`
	// Path is the full request path as sent on the wire, including the
	// "/api2/json" prefix, so a replay server can match on r.URL.Path with
	// no knowledge of the client's URL construction.
	Path string `json:"path"`
	// Query is the parsed query string. Matching is order-independent and
	// value-sensitive; see Key.
	Query map[string][]string `json:"query,omitempty"`
	// Status is the HTTP status code PVE returned. Error responses are
	// worth recording too: a 403's body is exactly the shape internal/pve's
	// error mapping is written against.
	Status int `json:"status"`
	// Body is the response body verbatim.
	Body string `json:"body"`
}

// Key is the canonical identity of the request a cassette answers:
// method, path, and the query normalised so that key order and duplicate
// value order cannot change it, while any value change does.
//
// This is the whole of the matching rule. There is no prefix matching, no
// wildcard, and no "closest cassette" — see internal/pvemock's replay
// server for why an unmatched request must stay unmatched.
func (c Cassette) Key() string {
	return RequestKey(c.Method, c.Path, c.Query)
}

// RequestKey builds a Key from the parts of a live request.
func RequestKey(method, path string, query map[string][]string) string {
	k := strings.ToUpper(method) + " " + path
	if q := canonicalQuery(query); q != "" {
		k += "?" + q
	}
	return k
}

// canonicalQuery encodes query with its keys sorted and, within a key, its
// values sorted.
//
// Sorting the values as well as the keys is the difference between
// "order-independent" and "order-independent except for repeated
// parameters", and PVE does use repeated parameters (a firewall rule
// update sends several `delete=` values). Escaping is url.QueryEscape's,
// so two spellings of the same value ("+" vs "%20") still normalise apart
// — deliberately: a value difference is a difference.
func canonicalQuery(query map[string][]string) string {
	if len(query) == 0 {
		return ""
	}
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		values := append([]string(nil), query[k]...)
		sort.Strings(values)
		for _, v := range values {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	return b.String()
}

// FileName is the cassette's file name within its version directory.
//
// It is derived entirely from Key, so re-recording the same request
// overwrites its cassette instead of accumulating near-duplicates, and so
// a reviewer can tell from `ls` which endpoints a recording session
// covered. The hash suffix disambiguates two requests whose paths slugify
// identically (different query strings, most often).
func (c Cassette) FileName() string {
	sum := sha256.Sum256([]byte(c.Key()))
	return fmt.Sprintf("%s_%s_%s.json", strings.ToUpper(c.Method), slug(c.Path), hex.EncodeToString(sum[:4]))
}

// slug turns a request path into a file-name-safe fragment.
func slug(path string) string {
	p := strings.TrimPrefix(path, "/api2/json")
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "root"
	}
	const maxSlug = 72
	if len(out) > maxSlug {
		out = out[:maxSlug]
	}
	return out
}

// Validate rejects a cassette that cannot be replayed or whose provenance
// is unknown. It runs on both write and load: a hand-edited cassette gets
// the same scrutiny as a freshly recorded one.
func (c Cassette) Validate() error {
	var missing []string
	if c.Method == "" {
		missing = append(missing, "method")
	}
	if c.Path == "" {
		missing = append(missing, "path")
	}
	if !strings.HasPrefix(c.Path, "/") {
		missing = append(missing, "path (must be absolute)")
	}
	if c.PVEVersion == "" {
		// A cassette whose PVE version is unknown is a hand-written
		// fixture wearing a cassette's clothes: it answers none of the
		// questions the format exists to answer.
		missing = append(missing, "pveVersion")
	}
	if c.Status < 100 || c.Status > 599 {
		missing = append(missing, "status")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s (%s %s)", ErrCassetteInvalid, strings.Join(missing, ", "), c.Method, c.Path)
	}
	return nil
}

// MarshalIndentJSON encodes the cassette in the exact on-disk form Writer
// produces. Exported so tests can compare a checked-in cassette against a
// freshly recorded one byte for byte.
func (c Cassette) MarshalIndentJSON() ([]byte, error) {
	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding cassette %s: %w", c.Key(), err)
	}
	return append(out, '\n'), nil
}
