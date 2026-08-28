// SPDX-License-Identifier: Apache-2.0

package validation

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// --- ParseBlob / Validate unit tests ---------------------------------------

func validBlobJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/fixture-blob.json")
	if err != nil {
		t.Fatalf("reading fixture blob: %v", err)
	}
	return raw
}

func TestParseBlob_ValidFixtureRoundTrips(t *testing.T) {
	blob, err := ParseBlob(validBlobJSON(t))
	if err != nil {
		t.Fatalf("ParseBlob: %v", err)
	}
	if blob.Section != "fixture" {
		t.Errorf("Section = %q, want %q", blob.Section, "fixture")
	}
	if len(blob.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(blob.Items))
	}
	item, ok := blob.ItemByID("a")
	if !ok {
		t.Fatal("ItemByID(a): not found")
	}
	if item.Raw != "ok" {
		t.Errorf("item a Raw = %q, want %q", item.Raw, "ok")
	}
}

func TestParseBlob_RejectsMissingRequiredField(t *testing.T) {
	base := map[string]any{}
	if err := json.Unmarshal(validBlobJSON(t), &base); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	for _, key := range []string{"schema_version", "harness_version", "section", "generated_at", "mutates", "node", "pve_version", "items"} {
		t.Run(key, func(t *testing.T) {
			mutated := map[string]any{}
			for k, v := range base {
				mutated[k] = v
			}
			delete(mutated, key)
			raw, err := json.Marshal(mutated)
			if err != nil {
				t.Fatalf("marshal mutated blob: %v", err)
			}
			if _, err := ParseBlob(raw); err == nil {
				t.Errorf("ParseBlob with %q removed: got nil error, want an error", key)
			}
		})
	}
}

func TestParseBlob_RejectsUnsupportedSchemaVersion(t *testing.T) {
	raw := bytes.ReplaceAll(validBlobJSON(t), []byte(`"schema_version": "1.0"`), []byte(`"schema_version": "99.0"`))
	if _, err := ParseBlob(raw); err == nil {
		t.Fatal("ParseBlob with schema_version 99.0: got nil error, want an error")
	}
}

func TestParseBlob_RejectsDuplicateItemIDs(t *testing.T) {
	raw := bytes.ReplaceAll(validBlobJSON(t), []byte(`"id": "b"`), []byte(`"id": "a"`))
	if _, err := ParseBlob(raw); err == nil {
		t.Fatal("ParseBlob with a duplicate item id: got nil error, want an error")
	}
}

func TestParseBlob_RejectsInvalidJSON(t *testing.T) {
	if _, err := ParseBlob([]byte("not json")); err == nil {
		t.Fatal("ParseBlob(not json): got nil error, want an error")
	}
}

// --- AC1: harness scripts against internal/pvemock, not hardware ----------
//
// "Running any harness script against internal/pvemock (not hardware)
// produces a schema-valid evidence blob — proving the harness itself is
// testable without a cluster."

func startMockPVE(t *testing.T) (baseURL string) {
	t.Helper()
	fixturePath := filepath.Join("..", "..", "testdata", "clusters", "single-node.yaml")
	fixture, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	srv := pvemock.NewServer(fixture)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts.URL + "/api2/json"
}

func harnessPath(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("..", "..", "planning", "validation", "harness", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("harness script %s not found: %v", p, err)
	}
	return p
}

func runHarness(t *testing.T, script string, extraEnv []string, args ...string) (stdout, stderr []byte, err error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), extraEnv...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}

func TestHarness_ReadOnlySectionsProduceSchemaValidBlobs(t *testing.T) {
	baseURL := startMockPVE(t)
	token := "root@pam!daemon=6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42" // testdata/clusters/single-node.yaml

	sections := []string{"pve-api.sh", "host.sh", "firewall.sh", "sdn.sh", "ipam.sh", "wireguard.sh", "capture.sh"}
	for _, section := range sections {
		t.Run(section, func(t *testing.T) {
			script := harnessPath(t, section)
			stdout, stderr, err := runHarness(t, script, []string{
				"PVE_API_BASE_URL=" + baseURL,
				"PVE_API_TOKEN=" + token,
			})
			if err != nil {
				t.Fatalf("running %s: %v\nstderr:\n%s", section, err, stderr)
			}
			blob, perr := ParseBlob(stdout)
			if perr != nil {
				t.Fatalf("%s produced an evidence blob that failed schema validation: %v\nstdout:\n%s", section, perr, stdout)
			}
			wantSection := strings.TrimSuffix(section, ".sh")
			if blob.Section != wantSection {
				t.Errorf("%s: blob.Section = %q, want %q", section, blob.Section, wantSection)
			}
			if blob.Mutates {
				t.Errorf("%s: blob.Mutates = true, want false (this is a read-only section)", section)
			}
			if len(blob.Items) == 0 {
				t.Errorf("%s: blob has zero items", section)
			}
		})
	}
}

func TestHarness_ChangeEngineMutatingSectionProducesSchemaValidBlob(t *testing.T) {
	baseURL := startMockPVE(t)
	script := harnessPath(t, "change-engine.sh")

	stdout, stderr, err := runHarness(t, script, []string{
		"PVE_API_BASE_URL=" + baseURL,
		"PVE_TARGET_IFACE=eno2", // single-node.yaml's spare, unconfigured NIC
	}, "--i-understand-this-mutates")
	if err != nil {
		t.Fatalf("running change-engine.sh --i-understand-this-mutates: %v\nstderr:\n%s", err, stderr)
	}
	blob, perr := ParseBlob(stdout)
	if perr != nil {
		t.Fatalf("change-engine.sh produced an evidence blob that failed schema validation: %v\nstdout:\n%s", perr, stdout)
	}
	if !blob.Mutates {
		t.Error("blob.Mutates = false, want true (change-engine.sh is a MUTATES=1 script)")
	}
	// The reload item (change-engine-04) should show a real task reaching
	// "stopped"/"OK" against the mock's task lifecycle — proving the
	// script exercised a genuine stage->reload round trip, not just a GET.
	item, ok := blob.ItemByID("change-engine-04")
	if !ok {
		t.Fatal("change-engine-04 item missing from blob")
	}
	if !strings.Contains(item.Raw, "stopped") {
		t.Errorf("change-engine-04 raw output doesn't mention a completed task: %q", item.Raw)
	}
}

func TestHarness_ChangeEngineRefusesWithoutMutationFlag(t *testing.T) {
	baseURL := startMockPVE(t)
	script := harnessPath(t, "change-engine.sh")

	stdout, stderr, err := runHarness(t, script, []string{
		"PVE_API_BASE_URL=" + baseURL,
		"PVE_TARGET_IFACE=eno2",
	})
	if err == nil {
		t.Fatal("change-engine.sh without --i-understand-this-mutates: got nil error, want a non-zero exit")
	}
	if len(stdout) != 0 {
		t.Errorf("change-engine.sh without the flag printed to stdout (should refuse before emitting anything): %q", stdout)
	}
	if !bytes.Contains(stderr, []byte("--i-understand-this-mutates")) {
		t.Errorf("change-engine.sh's refusal message doesn't mention the required flag: %q", stderr)
	}
}
