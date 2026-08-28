// SPDX-License-Identifier: Apache-2.0

package redact_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/redact"
)

// TestScanJSON is the detection half of the redactor: the same rules that
// drive Scrub, asked "is this safe" instead of "make this safe".
//
// Every row is a body a PVE endpoint could plausibly return, and each
// asserts the *path* as well as the rule — an error that says "a secret is
// in here somewhere" is not actionable, and naming the field is what
// T-2502 AC2 requires of the recorder that consumes this.
func TestScanJSON(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []redact.Finding
	}{
		{
			name: "POST /access/ticket — the login response",
			body: `{"data":{"username":"root@pam","ticket":"PVE:root@pam:68A1B2C3::c2lnbmF0dXJl",` +
				`"CSRFPreventionToken":"68A1B2C3:aG1hYw=="}}`,
			want: []redact.Finding{
				{Field: "body.data.CSRFPreventionToken", Rule: redact.SecretKeyRule},
				{Field: "body.data.ticket", Rule: redact.SecretKeyRule},
			},
		},
		{
			name: "a storage config carrying a password",
			body: `{"data":{"storage":"nfs1","server":"10.0.0.5","password":"hunter2"}}`,
			want: []redact.Finding{{Field: "body.data.password", Rule: redact.SecretKeyRule}},
		},
		{
			name: "a private key in a value under an innocent key",
			body: `{"data":[{"iface":"wg0","comments":"-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----"}]}`,
			want: []redact.Finding{{Field: "body.data[0].comments", Rule: "pem-private-key"}},
		},
		{
			name: "a WireGuard key's wire form, which no key name announces",
			body: `{"data":{"peer":"site-b","publicKey":"yAnECJ2z8Xf4qLkTvB1mNpQrSuVwXyZ0123456789ab="}}`,
			want: []redact.Finding{{Field: "body.data.publicKey", Rule: "base64-32-byte-key"}},
		},
		{
			name: "an sdn zone pointing at an external IPAM",
			body: `{"data":[{"zone":"evpn1","type":"evpn","ipam":"netbox","api_token":"abc123"}]}`,
			want: []redact.Finding{{Field: "body.data[0].api_token", Rule: redact.SecretKeyRule}},
		},
		{
			name: "a credential in a free-text error message",
			body: `{"data":null,"message":"proxy error dialing https://admin:hunter2@switch-1.example/restconf"}`,
			want: []redact.Finding{{Field: "body.message", Rule: "url-userinfo"}},
		},
		{
			name: "nested several levels down",
			body: `{"data":{"nodes":[{"node":"pve1","cfg":{"session_key":"AAAA"}}]}}`,
			want: []redact.Finding{{Field: "body.data.nodes[0].cfg.session_key", Rule: redact.SecretKeyRule}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redact.ScanJSON("body", []byte(tc.body))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ScanJSON() =\n  %v\nwant\n  %v", got, tc.want)
			}
			// A scan that fires must agree with the redactor that would
			// have replaced the same material: one vocabulary, two
			// verdicts, never one without the other.
			if scrubbed := string(redact.JSON([]byte(tc.body))); !strings.Contains(scrubbed, redact.Placeholder) {
				t.Errorf("ScanJSON found %v but JSON() redacted nothing:\n  %s", got, scrubbed)
			}
		})
	}
}

// TestScanJSON_LeavesOrdinaryPVEResponsesAlone is the control, and it is
// the more important half of this file.
//
// A scanner that fired on everything would make record mode unusable and
// would pass every row above. Each body here is real pvemock output for an
// endpoint the shipped cassette set covers; a false positive on any of
// them means an operator cannot record that endpoint at all.
func TestScanJSON_LeavesOrdinaryPVEResponsesAlone(t *testing.T) {
	clean := []string{
		`{"data":[{"type":"cluster","name":"pve-cluster-a","nodes":3,"quorate":1},{"type":"node","name":"pve1","ip":"10.10.0.11","online":1,"local":1}]}`,
		`{"data":[{"type":"bridge","method":"static","address":"10.10.0.11/24","gateway":"10.10.0.1","iface":"vmbr0","comments":"cluster/mgmt trunk, VLAN-aware","bridge_ports":"bond0","mtu":1500,"bridge_vlan_aware":1,"autostart":1}]}`,
		`{"data":[{"bond_mode":"802.3ad","type":"bond","iface":"bond0","slaves":"eno1 eno2"}]}`,
		`{"data":{"/":{"Sys.Audit":1,"Sys.Modify":1,"VM.Config.Network":1}}}`,
		`{"data":[{"vnet":"vnet100","zone":"vlanz","alias":"app-tier","tag":100}]}`,
		`{"data":[{"type":"in","action":"ACCEPT","proto":"tcp","dport":"22","comment":"cluster-wide SSH","pos":0,"enable":true}]}`,
		`{"data":{"net0":"virtio=BC:24:11:AA:02:C8,bridge=vmbr0,tag=100,firewall=1","name":"app01","cores":"4"}}`,
		`{"data":null,"message":"permission check failed (Sys.Modify)"}`,
		`{"data":"UPID:pve1:00001234:0000ABCD:68A1B2C3:srvreload:networking:root@pam:"}`,
	}
	for _, body := range clean {
		if got := redact.ScanJSON("body", []byte(body)); len(got) != 0 {
			t.Errorf("ScanJSON fired on an ordinary PVE response %v:\n  %s", got, body)
		}
	}
}

// TestScanJSON_ScansUnparsableInputAsText: "I could not parse it" and "I
// know it is safe" are not the same statement, in this direction as well
// as in JSON()'s.
func TestScanJSON_ScansUnparsableInputAsText(t *testing.T) {
	junk := `<html>login failed for PVE:root@pam:68A1B2C3::c2lnbmF0dXJl</html>`
	got := redact.ScanJSON("body", []byte(junk))
	if len(got) == 0 {
		t.Fatalf("ScanJSON passed unparsable input through: %s", junk)
	}
	if got[0].Field != "body" || got[0].Rule != "pve-ticket" {
		t.Errorf("ScanJSON() = %v", got)
	}
	if n := len(redact.ScanJSON("body", nil)); n != 0 {
		t.Errorf("ScanJSON(nil) reported %d findings", n)
	}
}

// TestSecretKey covers the key-name vocabulary directly, including the
// terms that must NOT match: "key" on its own would redact
// `key_file=/etc/vnprox/keys/session.key`, and the path is frequently the
// whole diagnosis.
func TestSecretKey(t *testing.T) {
	cases := map[string]bool{
		"password": true, "ticket": true, "CSRFPreventionToken": true,
		"private_key": true, "privkey": true, "preshared_key": true, "psk": true,
		"api_key": true, "client_secret": true, "kubeconfig": true,
		"credential_enc": true, "token_hash": true, "Authorization": true, "cookie": true,
		"iface": false, "bridge_ports": false, "mtu": false, "key_file": false,
		"address": false, "vlan_id": false, "bond_mode": false, "comments": false,
	}
	for name, want := range cases {
		if got := redact.SecretKey(name); got != want {
			t.Errorf("SecretKey(%q) = %v, want %v", name, got, want)
		}
	}
}
