// SPDX-License-Identifier: Apache-2.0

package pvecassette_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvecassette"
	"github.com/bgovanlu/vnprox/internal/redact"
)

func newWriter(t *testing.T) (*pvecassette.Writer, string) {
	t.Helper()
	dir := t.TempDir()
	w, err := pvecassette.NewWriter(dir, "8.3.5", nil)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	return w, dir
}

// countFiles is how every refusal case below proves the write did not
// happen. Asserting only on the returned error would pass against a writer
// that wrote the file and then complained about it.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return n
}

// TestWrite_RefusesASecret is T-2502 AC2: a response containing a PVE
// ticket, a password field, or a private key fails the write and names the
// field. One row per secret class, asserted separately, because "the
// redactor catches secrets" is not a claim any single fixture can support
// — each class reaches the guard by a different route (a key name, a value
// shape, a value shape inside an array element).
func TestWrite_RefusesASecret(t *testing.T) {
	cases := []struct {
		name string
		// body is the response PVE returned.
		body string
		// secret must not appear in anything left on disk.
		secret string
		// wantField is the field path the error must name.
		wantField string
		// wantRule is the rule the error must attribute it to.
		wantRule string
	}{
		{
			name: "PVE ticket (the login response itself)",
			body: `{"data":{"username":"root@pam","ticket":"PVE:root@pam:68A1B2C3::c2VjcmV0c2lnbmF0dXJlYmFzZTY0ZGF0YQ==",` +
				`"CSRFPreventionToken":"68A1B2C3:aG1hY3NpZ25hdHVyZQ=="}}`,
			secret:    "c2VjcmV0c2lnbmF0dXJlYmFzZTY0ZGF0YQ==",
			wantField: "body.data.ticket",
			wantRule:  redact.SecretKeyRule,
		},
		{
			name:      "a password field",
			body:      `{"data":{"storage":"nfs-backup","server":"10.0.0.5","password":"hunter2-not-a-real-one"}}`,
			secret:    "hunter2-not-a-real-one",
			wantField: "body.data.password",
			wantRule:  redact.SecretKeyRule,
		},
		{
			name: "a private key in a value, under a key that names nothing",
			body: `{"data":[{"iface":"wg0","type":"wireguard","comments":"-----BEGIN PRIVATE KEY-----\n` +
				`MIIBVgIBADANBgkqNOTAREALKEY\n-----END PRIVATE KEY-----"}]}`,
			secret:    "MIIBVgIBADANBgkqNOTAREALKEY",
			wantField: "body.data[0].comments",
			wantRule:  "pem-private-key",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The control: the fixture really does contain the secret, so
			// "it never reached disk" is a statement about the writer and
			// not about a typo in this table.
			if !strings.Contains(tc.body, tc.secret) {
				t.Fatalf("CONTROL FAILED: fixture body does not contain %q", tc.secret)
			}

			w, dir := newWriter(t)
			path, err := w.Record("GET", "/api2/json/access/ticket", nil, 200, []byte(tc.body))
			if err == nil {
				t.Fatalf("Write accepted a body containing a %s (wrote %s)", tc.name, path)
			}
			if !errors.Is(err, pvecassette.ErrSecretInCassette) {
				t.Errorf("error is not ErrSecretInCassette: %v", err)
			}

			var secretErr *pvecassette.SecretError
			if !errors.As(err, &secretErr) {
				t.Fatalf("error is not a *SecretError: %v", err)
			}
			var namedField, namedRule bool
			for _, f := range secretErr.Findings {
				if f.Field == tc.wantField {
					namedField = true
					if f.Rule == tc.wantRule {
						namedRule = true
					}
				}
			}
			if !namedField {
				t.Errorf("error did not name field %q; it named %v", tc.wantField, secretErr.Fields())
			}
			if !namedRule {
				t.Errorf("error did not attribute %q to rule %q; findings: %v", tc.wantField, tc.wantRule, secretErr.Findings)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("the error message does not name the field an operator has to act on: %v", err)
			}

			if n := countFiles(t, dir); n != 0 {
				t.Errorf("refused write still left %d file(s) in %s", n, dir)
			}
		})
	}
}

// TestWrite_AcceptsAnOrdinaryResponse is the other direction: a guard that
// refused everything would pass every row above.
func TestWrite_AcceptsAnOrdinaryResponse(t *testing.T) {
	w, dir := newWriter(t)
	body := `{"data":[{"iface":"vmbr0","type":"bridge","address":"10.0.0.9/24","mtu":9000,"bridge_ports":"bond0"}]}` + "\n"

	path, err := w.Record("GET", "/api2/json/nodes/pve1/network", map[string][]string{"type": {"bridge"}}, 200, []byte(body))
	if err != nil {
		t.Fatalf("Write refused an ordinary network listing: %v", err)
	}
	if n := countFiles(t, dir); n != 1 {
		t.Fatalf("expected exactly 1 cassette on disk, found %d", n)
	}

	got, err := pvecassette.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// AC1's foundation: the body survives the round trip byte for byte,
	// trailing newline included. Everything the replay server promises
	// rests on this one comparison.
	if got.Body != body {
		t.Errorf("body changed on the round trip:\n  got:  %q\n  want: %q", got.Body, body)
	}
	if got.PVEVersion != "8.3.5" {
		t.Errorf("PVEVersion = %q, want 8.3.5", got.PVEVersion)
	}
	if got.Key() != "GET /api2/json/nodes/pve1/network?type=bridge" {
		t.Errorf("Key() = %q", got.Key())
	}
	if w.Written()[0] != path {
		t.Errorf("Written() = %v, want [%s]", w.Written(), path)
	}
}

// TestNewWriter_RequiresAPVEVersion: a cassette that cannot say which PVE
// produced it is a hand-written fixture with a timestamp on it.
func TestNewWriter_RequiresAPVEVersion(t *testing.T) {
	if _, err := pvecassette.NewWriter(t.TempDir(), "", nil); err == nil {
		t.Error("NewWriter accepted an empty pveVersion")
	}
	if _, err := pvecassette.NewWriter("", "8.3.5", nil); err == nil {
		t.Error("NewWriter accepted an empty dir")
	}
}

// TestWrite_RejectsAnUnreplayableCassette covers Validate's half: the
// fields without which a cassette cannot be matched or trusted.
func TestWrite_RejectsAnUnreplayableCassette(t *testing.T) {
	w, dir := newWriter(t)
	cases := []struct {
		name string
		c    pvecassette.Cassette
	}{
		{"no method", pvecassette.Cassette{PVEVersion: "8.3.5", Path: "/api2/json/x", Status: 200}},
		{"no path", pvecassette.Cassette{PVEVersion: "8.3.5", Method: "GET", Status: 200}},
		{"relative path", pvecassette.Cassette{PVEVersion: "8.3.5", Method: "GET", Path: "api2/json/x", Status: 200}},
		{"no version", pvecassette.Cassette{Method: "GET", Path: "/api2/json/x", Status: 200}},
		{"no status", pvecassette.Cassette{PVEVersion: "8.3.5", Method: "GET", Path: "/api2/json/x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := w.Write(tc.c); !errors.Is(err, pvecassette.ErrCassetteInvalid) {
				t.Errorf("Write(%s) error = %v, want ErrCassetteInvalid", tc.name, err)
			}
		})
	}
	if n := countFiles(t, dir); n != 0 {
		t.Errorf("invalid cassettes left %d file(s) on disk", n)
	}
}
