// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestScrub is the value-level redactor's table. Every row is a shape a
// credential actually takes in vnprox's own logs and config, and each
// asserts three things rather than one:
//
//  1. the secret is gone;
//  2. the diagnostic context around it survives (a redactor that returned
//     "" for every line would pass a "secret is gone" assertion and make
//     the bundle useless); and
//  3. Redacted appears, so a reader can tell "removed" from "never there".
func TestScrub(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// gone must not appear in the output.
		gone string
		// keep must appear in the output: the diagnostic half.
		keep string
	}{
		{
			name: "PVE session ticket in a log line",
			in:   `pve: login failed ticket=PVE:root@pam:68A1B2C3::c2VjcmV0c2lnbmF0dXJlYmFzZTY0ZGF0YQ== url=https://pve1:8006`,
			gone: "c2VjcmV0c2lnbmF0dXJlYmFzZTY0ZGF0YQ==",
			keep: "https://pve1:8006",
		},
		{
			name: "PVE API token header value",
			in:   `pve: request failed status=401 header Authorization=PVEAPIToken=vnprox@pve!daemon=1a2b3c4d-5e6f-7081-92a3-b4c5d6e7f809`,
			gone: "1a2b3c4d-5e6f-7081-92a3-b4c5d6e7f809",
			keep: "status=401",
		},
		{
			name: "Authorization: Bearer header",
			in:   `outbound request Authorization: Bearer vnpx_9f3a7c21d8e64b05 to https://hooks.example`,
			gone: "vnpx_9f3a7c21d8e64b05",
			keep: "https://hooks.example",
		},
		{
			name: "URL userinfo",
			in:   `dialing https://admin:hunter2@switch-1.example/restconf`,
			gone: "hunter2",
			keep: "switch-1.example/restconf",
		},
		{
			name: "keyed assignment with an equals sign",
			in:   `config: dev_ticket_password=correct-horse-battery-staple realm=pam`,
			gone: "correct-horse-battery-staple",
			keep: "realm=pam",
		},
		{
			name: "keyed assignment with a colon",
			in:   `wireguard tunnel wg0: private_key: aFakePrivateKeyValue123 listen_port: 51820`,
			gone: "aFakePrivateKeyValue123",
			keep: "51820",
		},
		{
			name: "quoted keyed assignment",
			in:   `client_secret = "s3cr3t-oidc-value"`,
			gone: "s3cr3t-oidc-value",
			keep: "client_secret",
		},
		{
			name: "bare base64 32-byte key (a WireGuard private key's wire form)",
			in:   `wg0 peer added key=yAnECJ2z8Xf4qLkTvB1mNpQrSuVwXyZ0123456789ab= endpoint=203.0.113.5:51820`,
			gone: "yAnECJ2z8Xf4qLkTvB1mNpQrSuVwXyZ0123456789ab=",
			keep: "203.0.113.5:51820",
		},
		{
			name: "PEM private key block",
			in:   "cert error\n-----BEGIN PRIVATE KEY-----\nMIIBVgIBADANBgkq\n-----END PRIVATE KEY-----\nafterwards",
			gone: "MIIBVgIBADANBgkq",
			keep: "afterwards",
		},
		{
			name: "preshared key",
			in:   `peer configured presharedKey=ThisIsThePresharedSecretValue allowed_ips=10.9.0.2/32`,
			gone: "ThisIsThePresharedSecretValue",
			keep: "10.9.0.2/32",
		},
		{
			name: "kubeconfig",
			in:   `k8s: kubeconfig=apiVersion-v1-client-certificate-data-XYZ cluster=prod`,
			gone: "apiVersion-v1-client-certificate-data-XYZ",
			keep: "cluster=prod",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The control for this row: the secret IS in the input. A typo
			// in the fixture would otherwise make "gone" trivially true.
			if !strings.Contains(tc.in, tc.gone) {
				t.Fatalf("CONTROL FAILED: the fixture does not contain %q, so asserting it is removed proves nothing", tc.gone)
			}
			out := Scrub(tc.in)
			if strings.Contains(out, tc.gone) {
				t.Errorf("Scrub left %q in the output:\n  in:  %s\n  out: %s", tc.gone, tc.in, out)
			}
			if !strings.Contains(out, tc.keep) {
				t.Errorf("Scrub removed the diagnostic context %q — a bundle that redacts everything is useless:\n  out: %s", tc.keep, out)
			}
			if !strings.Contains(out, Redacted) {
				t.Errorf("Scrub removed something without saying so; a reader cannot tell 'removed' from 'never there':\n  out: %s", out)
			}
			// Idempotence: a bundle's log passes through Scrub once, but a
			// value can reach it having already been scrubbed elsewhere.
			if again := Scrub(out); again != out {
				t.Errorf("Scrub is not idempotent:\n  once:  %s\n  twice: %s", out, again)
			}
		})
	}
}

// TestScrub_LeavesOrdinaryDiagnosticsAlone is the other direction. A
// redactor that mangles ordinary log lines makes the bundle unreadable, and
// "it removed everything" would pass every test above.
func TestScrub_LeavesOrdinaryDiagnosticsAlone(t *testing.T) {
	unchanged := []string{
		`time=2026-07-30T11:02:03Z level=INFO msg="collector poll complete" source=pve duration=412ms`,
		`iface vmbr0 inet static address 10.0.0.9/24 gateway 10.0.0.1 mtu 9000`,
		`changeset 01J8ZQ2N3P4R5S6T7V8W9X apply failed on node pve2: bridge vmbr9 already exists`,
		`listen tcp 0.0.0.0:8007: bind: address already in use`,
		`store: schema version 33, migration 0034 applied in 12ms`,
		`peer pve3 unreachable: dial tcp 10.0.0.11:8007: connect: connection refused`,
		`sha256 blob a3f1c9e2b7d40518a3f1c9e2b7d40518a3f1c9e2b7d40518a3f1c9e2b7d40518 stored`,
	}
	for _, line := range unchanged {
		if got := Scrub(line); got != line {
			t.Errorf("Scrub changed an ordinary diagnostic line:\n  in:  %s\n  out: %s", line, got)
		}
	}
}

// TestRedactJSON is the changeset-ops redactor.
//
// The property it defends is the one internal/api's redactOpSecrets cannot
// give a support bundle: redactOpSecrets strips one known field from one
// known op type, which is right for a []change.Op response and wrong for an
// archive that must be safe against the op type that lands next phase. This
// walk is by key NAME, so a field invented later is covered on arrival.
func TestRedactJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		gone []string
		keep []string
	}{
		{
			name: "a wg.peer.add op's preshared key, in both its forms",
			in: `[{"type":"wg.peer.add","node":"pve1","params":{"tunnelId":"wg-1",` +
				`"publicKey":"peerpub","presharedKey":"PLAINTEXT-PSK-VALUE",` +
				`"presharedKeyEnc":"BASE64CIPHERTEXT","allowedIps":["10.9.0.2/32"]}}]`,
			gone: []string{"PLAINTEXT-PSK-VALUE", "BASE64CIPHERTEXT"},
			keep: []string{"wg.peer.add", "pve1", "10.9.0.2/32", "peerpub"},
		},
		{
			name: "a field type nobody has written yet",
			in:   `[{"type":"switch.configure","params":{"gnmiCredential":"SUPER-SECRET","port":"eth1/1"}}]`,
			gone: []string{"SUPER-SECRET"},
			keep: []string{"switch.configure", "eth1/1"},
		},
		{
			name: "nested deep inside an apply log",
			in: `{"nodes":[{"node":"pve2","steps":[{"cmd":"ifreload -a","stderr":"ok"},` +
				`{"cmd":"wg set wg0 private-key /tmp/k","env":{"WG_TOKEN":"LEAKED-TOKEN"}}]}]}`,
			gone: []string{"LEAKED-TOKEN"},
			keep: []string{"pve2", "ifreload -a"},
		},
		{
			name: "a secret in a string VALUE with no telltale key",
			in:   `[{"type":"note","params":{"text":"use PVE:root@pam:68A1B2C3::c2lnbmF0dXJlZ29lc2hlcmU= to log in"}}]`,
			gone: []string{"c2lnbmF0dXJlZ29lc2hlcmU="},
			keep: []string{"to log in"},
		},
		{
			name: "sealed-at-rest columns by suffix",
			in:   `{"credential_enc":"CIPHERTEXT-A","token_hash":"HASHVALUE","name":"dc2"}`,
			gone: []string{"CIPHERTEXT-A", "HASHVALUE"},
			keep: []string{"dc2"},
		},
		{
			name: "numbers and booleans are left alone",
			in:   `[{"type":"net.iface.create","params":{"mtu":9000,"vlanAware":true,"name":"vmbr9"}}]`,
			gone: nil,
			keep: []string{"9000", "true", "vmbr9"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, g := range tc.gone {
				if !strings.Contains(tc.in, g) {
					t.Fatalf("CONTROL FAILED: the fixture does not contain %q", g)
				}
			}
			out := string(redactJSON([]byte(tc.in)))
			for _, g := range tc.gone {
				if strings.Contains(out, g) {
					t.Errorf("redactJSON left %q in:\n  %s", g, out)
				}
			}
			for _, k := range tc.keep {
				if !strings.Contains(out, k) {
					t.Errorf("redactJSON removed the diagnostic value %q:\n  %s", k, out)
				}
			}
			if !json.Valid([]byte(out)) {
				t.Errorf("redactJSON produced invalid JSON: %s", out)
			}
		})
	}
}

// TestRedactJSON_RefusesWhatItCannotParse: "I could not parse it" and "I
// know it is safe" are not the same statement, so unparsable input is
// replaced wholesale rather than passed through.
func TestRedactJSON_RefusesWhatItCannotParse(t *testing.T) {
	junk := `{"credential": "TOP-SECRET-VALUE", this is not json`
	out := string(redactJSON([]byte(junk)))
	if strings.Contains(out, "TOP-SECRET-VALUE") {
		t.Errorf("unparsable input was passed through with its contents intact: %s", out)
	}
	if !strings.Contains(out, Redacted) {
		t.Errorf("unparsable input was dropped silently rather than marked: %s", out)
	}
	if redactJSON(nil) != nil {
		t.Error("redactJSON(nil) should stay nil so an absent column stays absent")
	}
}

// TestRedactedOptionValue covers the key-name rule as it applies to formats
// with no JSON structure at all — an interfaces(5) option, a TOML key.
func TestRedactedOptionValue(t *testing.T) {
	cases := []struct {
		key, value   string
		wantRedacted bool
	}{
		{"wireguard-private-key", "aFakeWireGuardPrivateKeyValue", true},
		{"wireguard-preshared-key", "aFakePresharedKeyValue", true},
		{"wg-psk", "aFakePSK", true},
		{"ovs_options", "tag=10", false},
		{"bridge-ports", "eno1 eno2", false},
		{"mtu", "9000", false},
		{"client_secret", "abc", true},
		{"bearer", "abc", true},
		{"address", "10.0.0.9/24", false},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got, redacted := redactedOptionValue(tc.key, tc.value)
			if redacted != tc.wantRedacted {
				t.Errorf("redactedOptionValue(%q) redacted = %v, want %v (value %q)", tc.key, redacted, tc.wantRedacted, got)
			}
			if tc.wantRedacted && strings.Contains(got, tc.value) {
				t.Errorf("redactedOptionValue(%q) returned the original value %q", tc.key, got)
			}
			if !tc.wantRedacted && got != tc.value {
				t.Errorf("redactedOptionValue(%q) altered a safe value: %q -> %q", tc.key, tc.value, got)
			}
		})
	}
}
