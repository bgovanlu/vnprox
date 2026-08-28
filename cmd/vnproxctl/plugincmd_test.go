// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPlugin_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"plugin", "bogus"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage", code)
	}
	if code := run([]string{"plugin"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("bare `plugin` exit code = %d, want ExitUsage", code)
	}
}

func TestRunPluginScaffold_RequiresExactlyOneName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"plugin", "scaffold"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("no name: exit code = %d, want ExitUsage (stderr: %s)", code, stderr.String())
	}
}

// TestRunPluginScaffold_WritesRenamedFiles is the acceptance test for T-3811
// AC1's shape: scaffold writes the template's four files with the plugin
// name substituted everywhere the template used its own placeholder token
// and display name.
func TestRunPluginScaffold_WritesRenamedFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "widgetfinder")
	var stdout, stderr bytes.Buffer
	code := run([]string{"plugin", "scaffold", "--out", dir, "widget finder"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	for _, name := range []string{"manifest.go", "producer.go", "producer_test.go", "README.md"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading scaffolded %s: %v", name, err)
		}
		content := string(raw)
		if strings.Contains(content, "plugintemplate") {
			t.Errorf("%s still contains the template's own placeholder token %q:\n%s", name, "plugintemplate", content)
		}
		if strings.Contains(content, "Plugin Template") {
			t.Errorf("%s still contains the template's own display name", name)
		}
	}

	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.go"))
	if err != nil {
		t.Fatalf("reading manifest.go: %v", err)
	}
	for _, want := range []string{"package widgetfinder", `const ManifestID = "com.example.widgetfinder"`, `Name:            "Widget Finder",`} {
		if !strings.Contains(string(manifest), want) {
			t.Errorf("manifest.go missing %q; got:\n%s", want, manifest)
		}
	}
}

func TestRunPluginScaffold_RefusesNonEmptyDirWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"plugin", "scaffold", "--out", dir, "name"}, &stdout, &stderr); code != ExitError {
		t.Fatalf("exit code = %d, want ExitError (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--force") {
		t.Errorf("stderr should mention --force, got: %s", stderr.String())
	}

	// --force overwrites.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"plugin", "scaffold", "--out", dir, "--force", "name"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("--force: exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
}

func TestRunPluginScaffold_JSONOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jsontest")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"plugin", "scaffold", "--out", dir, "-o", "json", "jsontest"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got["name"] != "jsontest" {
		t.Errorf("json name = %v, want jsontest", got["name"])
	}
	if got["manifestId"] != "com.example.jsontest" {
		t.Errorf("json manifestId = %v, want com.example.jsontest", got["manifestId"])
	}
	assertDocumentedJSON(t, "plugin scaffold", stdout.Bytes())
}

func TestSanitizePluginToken(t *testing.T) {
	cases := map[string]string{
		"My Cool Finder": "mycoolfinder",
		"widget-finder":  "widgetfinder",
		"9lives":         "p9lives",
		"UPPER_CASE":     "uppercase",
	}
	for in, want := range cases {
		got, err := sanitizePluginToken(in)
		if err != nil {
			t.Fatalf("sanitizePluginToken(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("sanitizePluginToken(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := sanitizePluginToken("!!!"); err == nil {
		t.Error("sanitizePluginToken(\"!!!\") should error: no usable letters/digits")
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"my cool finder": "My Cool Finder",
		"widget-finder":  "Widget Finder",
	}
	for in, want := range cases {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}
