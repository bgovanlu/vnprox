package validation

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runRedact pipes input through planning/validation/harness/lib/common.sh's
// redact() shell function, exactly as every harness script does before a
// captured command's output is ever assembled into an evidence blob (see
// common.sh's harness_item). This is a whitebox test of the actual shell
// function — not a Go reimplementation of it — because the redaction that
// matters is the one the harness runs.
func runRedact(t *testing.T, input string) string {
	t.Helper()
	lib := filepath.Join("..", "..", "planning", "validation", "harness", "lib", "common.sh")
	script := "SECTION=test\nMUTATES=0\n. " + shellQuote(lib) + "\nredact\n"
	cmd := exec.Command("bash", "-c", script)
	cmd.Stdin = strings.NewReader(input)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("redact() invocation failed: %v\nstderr:\n%s", err, errBuf.String())
	}
	return out.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// TestRedact_ScrubsEverySecretClass is T-1801 acceptance criterion 4: a
// blob containing a synthetic PVE token, ticket, and WireGuard private key
// emerges with all three redacted. One table-driven case per secret class,
// per the card's design guidance, plus the Authorization/Cookie header and
// PSK cases the same guidance calls out.
func TestRedact_ScrubsEverySecretClass(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		mustHave    string
		mustNotHave []string
	}{
		{
			name:        "pve_ticket",
			input:       `ticket=PVE:root@pam:8B51AE0FF7B21878DEADBEEF`,
			mustNotHave: []string{"8B51AE0FF7B21878DEADBEEF"},
			mustHave:    "[REDACTED-TICKET]",
		},
		{
			name:        "api_token_secret_in_header",
			input:       `Authorization: PVEAPIToken=root@pam!daemon=6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42`,
			mustNotHave: []string{"6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42"},
			mustHave:    "[REDACTED-HEADER]",
		},
		{
			name:        "api_token_secret_bare",
			input:       `configured token: root@pam!daemon=6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42 in fixture`,
			mustNotHave: []string{"6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42"},
			mustHave:    "[REDACTED-SECRET]",
		},
		{
			name:        "wireguard_private_key",
			input:       "PrivateKey = wKxOTUqxTNhVQu5j3n3fUwkSZbBmQwgN2v1jjy5rNXo=",
			mustNotHave: []string{"wKxOTUqxTNhVQu5j3n3fUwkSZbBmQwgN2v1jjy5rNXo="},
			mustHave:    "[REDACTED-WG-KEY]",
		},
		{
			name:        "wireguard_preshared_key",
			input:       "PresharedKey = 4Nx9Yy2q7z1cQvB8mR5tWkLpXsD0aFhU6oIjKgEeC3s=",
			mustNotHave: []string{"4Nx9Yy2q7z1cQvB8mR5tWkLpXsD0aFhU6oIjKgEeC3s="},
			mustHave:    "[REDACTED-WG-KEY]",
		},
		{
			name:        "cookie_header",
			input:       "Cookie: PVEAuthCookie=PVE:root@pam:8b51ae0ff7b21878",
			mustNotHave: []string{"8b51ae0ff7b21878"},
			mustHave:    "[REDACTED-HEADER]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runRedact(t, tc.input+"\n")
			for _, secret := range tc.mustNotHave {
				if strings.Contains(got, secret) {
					t.Errorf("redact() left secret material in output: %q\ninput:  %q\noutput: %q", secret, tc.input, got)
				}
			}
			if !strings.Contains(got, tc.mustHave) {
				t.Errorf("redact() output doesn't contain expected marker %q\ninput:  %q\noutput: %q", tc.mustHave, tc.input, got)
			}
		})
	}
}

// TestRedact_LeavesOrdinaryOutputAlone guards against over-aggressive
// patterns silently eating unrelated evidence (the card prefers over- to
// under-redaction, but "over" should still mean "secret-shaped", not
// "any output at all").
func TestRedact_LeavesOrdinaryOutputAlone(t *testing.T) {
	input := `{"data":{"type":"eth","method":"manual","iface":"eno2","mtu":1500,"autostart":1}}` + "\n"
	got := runRedact(t, input)
	if strings.TrimRight(got, "\n") != strings.TrimRight(input, "\n") {
		t.Errorf("redact() altered ordinary, non-secret JSON output:\ninput:  %q\noutput: %q", input, got)
	}
}
