package verify

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// backupContractGolden is a REAL `vnproxctl backup -o json` response, captured
// from pvecube on 2026-08-16 (only the archive path is normalised).
//
// It exists because `backup.archive_round_trip` spent its whole life decoding
// field names the CLI has never emitted — "sizeBytes" and "includedKeys"
// against the program's "bytes" and "includesKeyMaterial". encoding/json
// silently leaves absent fields at their zero value, so the check reported
// every healthy node as having "wrote a 0-byte archive: an empty backup of a
// live store", and its key-material assertion could never fire at all. The
// unit fixture had invented the same wrong names, so check and test agreed
// with each other and the program was never consulted.
//
// A fixture an author wrote from memory tests that the author is consistent.
// This file is the program's own output, so the two tests below fail if
// either side drifts from it.
const backupContractGolden = "testdata/vnproxctl-backup.json"

// TestBackupCheckDecodesRealCLIOutput is the regression test: the check's
// struct must decode the CLI's actual JSON, not a paraphrase of it.
func TestBackupCheckDecodesRealCLIOutput(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(backupContractGolden)
	if err != nil {
		t.Fatalf("reading %s: %v", backupContractGolden, err)
	}

	d := healthyDeps()
	hostOf(&d).cmds["vnproxctl backup -o json"] = string(raw)

	got := checkBackupRoundTrip(context.Background(), d)
	if got.Status != StatusPass {
		t.Fatalf("status = %v (%s), want pass — the check must read the CLI's real field names", got.Status, got.Detail)
	}
	// Not just "it passed": the numbers must have actually arrived, or a
	// future rename to two more absent fields would still pass a zero-value
	// struct through the default branch.
	for _, want := range []string{"739398 bytes", "schema 48"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail %q does not carry %q — the field decoded to its zero value", got.Detail, want)
		}
	}
}

// TestBackupGoldenCarriesEveryFieldTheCheckReads pins the contract from the
// other side. If someone renames a key in cmd/vnproxctl/backupcmd.go and
// re-captures this golden without updating the check, the test above fails;
// if they edit the check without re-capturing, this one does.
func TestBackupGoldenCarriesEveryFieldTheCheckReads(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(backupContractGolden)
	if err != nil {
		t.Fatalf("reading %s: %v", backupContractGolden, err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("golden is not valid JSON: %v", err)
	}
	for _, key := range []string{"path", "bytes", "schemaVersion", "entries", "includesKeyMaterial"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("golden has no %q key: either the CLI renamed it (update checkBackupRoundTrip too) or the capture is incomplete", key)
		}
	}
	// The two names the check used to read. Their absence here is the whole
	// point — if they ever appear, someone has "fixed" the contract in the
	// wrong direction.
	for _, gone := range []string{"sizeBytes", "includedKeys"} {
		if _, ok := generic[gone]; ok {
			t.Errorf("golden contains %q, which vnproxctl does not emit — do not reintroduce the names the broken check invented", gone)
		}
	}
}

// TestBackupCheckFailsAnActuallyEmptyArchive keeps the check's real purpose
// working: a genuinely empty archive must still fail, now that the field it
// reads is populated on healthy runs.
func TestBackupCheckFailsAnActuallyEmptyArchive(t *testing.T) {
	t.Parallel()

	d := healthyDeps()
	hostOf(&d).cmds["vnproxctl backup -o json"] = `{"path":"/tmp/b.tar.gz","bytes":0,"schemaVersion":48,"entries":0,"includesKeyMaterial":false}`

	got := checkBackupRoundTrip(context.Background(), d)
	if got.Status != StatusFail {
		t.Fatalf("status = %v (%s), want fail on a 0-byte archive", got.Status, got.Detail)
	}
}

// TestBackupCheckFailsUnrequestedKeyMaterial exercises the assertion that was
// dead for the whole of the check's life: --include-keys was not passed, so
// includesKeyMaterial must be false.
func TestBackupCheckFailsUnrequestedKeyMaterial(t *testing.T) {
	t.Parallel()

	d := healthyDeps()
	hostOf(&d).cmds["vnproxctl backup -o json"] = `{"path":"/tmp/b.tar.gz","bytes":1234,"schemaVersion":48,"entries":3,"includesKeyMaterial":true}`

	got := checkBackupRoundTrip(context.Background(), d)
	if got.Status != StatusFail {
		t.Fatalf("status = %v (%s), want fail — a backup carrying key material without --include-keys is the assertion this check exists for", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "key material") {
		t.Errorf("detail %q does not name the key-material problem", got.Detail)
	}
}

// realPVESANDump is what `openssl x509 -noout -ext subjectAltName` actually
// prints for pvecube's PVE-issued leaf (captured 2026-08-16). The header sits
// on its own line and every SAN follows on the next — the shape that made the
// old firstLine()-based evidence render as an empty SAN list.
// The node name is the fixture's (pve1) rather than pvecube's, so the case
// under test is the real one: node name covered, dial address absent.
const realPVESANDump = "X509v3 Subject Alternative Name: \n" +
	"    IP Address:127.0.0.1, IP Address:0:0:0:0:0:0:0:1, DNS:localhost, IP Address:192.168.100.99, DNS:pve1, DNS:pve1.localdomain."

// TestPeerCAChainAcceptsANodeNameSAN is the regression test for a FALSE
// FAILURE observed on real hardware on 2026-08-16.
//
// The check demanded that the dial address appear in the leaf's SAN list. That
// is the pre-T-2303 rule. T-2303 changed peer verification so a certificate
// covering the node name is verified against that name instead, which is the
// whole point of the fix — yet the check kept reporting T-1906-bug-01's
// "failure mode" against nodes where the product now behaves correctly.
func TestPeerCAChainAcceptsANodeNameSAN(t *testing.T) {
	t.Parallel()

	d := healthyDeps()
	h := hostOf(&d)
	h.files["/etc/pve/pve-root-ca.pem"] = "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----"
	h.cmds["openssl verify -CAfile /etc/pve/pve-root-ca.pem /etc/pve/local/pve-ssl.pem"] = "/etc/pve/local/pve-ssl.pem: OK"
	h.cmds["openssl x509 -in /etc/pve/local/pve-ssl.pem -noout -ext subjectAltName"] = realPVESANDump

	got := checkPeerCAPinsRealChain(context.Background(), d)
	if got.Status != StatusPass {
		t.Fatalf("status = %v (%s), want pass — the leaf covers DNS:%s, which is what certs.ResolveVerifyName resolves the dial host to", got.Status, got.Detail, localNode(d.Nodes))
	}
}

// TestPeerCAChainFailsWhenNothingIsCovered keeps the check's real purpose: a
// leaf covering neither the node name nor the dial address genuinely fails
// closed on the first peer call, and must still be reported.
func TestPeerCAChainFailsWhenNothingIsCovered(t *testing.T) {
	t.Parallel()

	d := healthyDeps()
	h := hostOf(&d)
	h.files["/etc/pve/pve-root-ca.pem"] = "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----"
	h.cmds["openssl verify -CAfile /etc/pve/pve-root-ca.pem /etc/pve/local/pve-ssl.pem"] = "/etc/pve/local/pve-ssl.pem: OK"
	h.cmds["openssl x509 -in /etc/pve/local/pve-ssl.pem -noout -ext subjectAltName"] =
		"X509v3 Subject Alternative Name: \n    DNS:someone-elses-node, IP Address:10.9.9.9"

	got := checkPeerCAPinsRealChain(context.Background(), d)
	if got.Status != StatusFail {
		t.Fatalf("status = %v (%s), want fail — neither the node name nor the dial address is covered", got.Status, got.Detail)
	}
	// The evidence must carry the SANs the certificate actually has. The old
	// firstLine() rendering printed the header and dropped every value.
	if !strings.Contains(got.Detail, "someone-elses-node") {
		t.Errorf("failure detail %q does not include the actual SAN list — an operator cannot act on an empty one", got.Detail)
	}
}
