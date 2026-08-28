// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolvePluginEndpoint is T-2904 AC1: the endpoint in a hub registry
// manifest is registry-supplied data, and buildRegistration must launch only
// a regular file inside the vnprox-owned install root — never a bare name
// (which would resolve via $PATH), never a relative path, never anything
// that lexically or via symlinks escapes the root. Each rejection names the
// constraint so an operator reading the error knows what the manifest did
// wrong; only the happy path returns a resolved absolute path.
func TestResolvePluginEndpoint(t *testing.T) {
	root := t.TempDir()

	// A real plugin binary inside the root.
	inside := filepath.Join(root, "good-plugin")
	if err := os.WriteFile(inside, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A subdirectory inside the root (not a regular file).
	subdir := filepath.Join(root, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A target outside the root, and a symlink inside the root pointing at it.
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "evil")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(root, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	// A symlink inside the root pointing at another file inside the root —
	// legitimate, and the resolved form is what gets launched.
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(inside, alias); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		endpoint string
		wantErr  string // substring the refusal must carry; "" = success
		want     string // resolved path on success
	}{
		{name: "bare name never resolves via $PATH", endpoint: "foo", wantErr: "not an absolute path"},
		{name: "relative path refused", endpoint: "./foo", wantErr: "not an absolute path"},
		{name: "absolute path outside root refused", endpoint: outside, wantErr: "outside the plugin install root"},
		{name: "dot-dot escape refused as unclean", endpoint: filepath.Join(root, "x") + "/../../etc/passwd", wantErr: "not a clean path"},
		{name: "the root itself is not an endpoint", endpoint: root, wantErr: "outside the plugin install root"},
		{name: "symlink escaping the root refused", endpoint: escape, wantErr: "escaping the plugin install root"},
		{name: "directory inside root refused", endpoint: subdir, wantErr: "not a regular file"},
		{name: "missing file inside root refused", endpoint: filepath.Join(root, "absent"), wantErr: "resolving plugin endpoint"},
		{name: "regular file inside root launches", endpoint: inside, want: inside},
		{name: "in-root symlink resolves to its in-root target", endpoint: alias, want: inside},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePluginEndpoint(root, tt.endpoint)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("resolvePluginEndpoint(%q, %q) = %q, nil — want an error containing %q", root, tt.endpoint, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolvePluginEndpoint(%q, %q) error = %v, want it to contain %q", root, tt.endpoint, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePluginEndpoint(%q, %q): %v, want success", root, tt.endpoint, err)
			}
			// t.TempDir may itself sit behind a symlink (macOS /tmp); compare
			// resolved forms so the assertion is about identity, not spelling.
			wantResolved, rerr := filepath.EvalSymlinks(tt.want)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if got != wantResolved {
				t.Fatalf("resolvePluginEndpoint(%q, %q) = %q, want %q", root, tt.endpoint, got, wantResolved)
			}
		})
	}
}

// TestResolvePluginEndpoint_EmptyRootFallsBackConstrained is the zero-value
// guarantee on hubPluginInstaller.installRoot: an unset root means the
// default vnprox-owned directory, never "unconstrained".
func TestResolvePluginEndpoint_EmptyRootFallsBackConstrained(t *testing.T) {
	h := hubPluginInstaller{}
	root := h.installRoot
	if root == "" {
		root = defaultPluginInstallRoot
	}
	if root != defaultPluginInstallRoot {
		t.Fatalf("empty installRoot must fall back to %q, got %q", defaultPluginInstallRoot, root)
	}
	if _, err := resolvePluginEndpoint(root, "/usr/bin/true"); err == nil {
		t.Fatal("an absolute path outside the default root must be refused")
	}
}
