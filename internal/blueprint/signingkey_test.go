// SPDX-License-Identifier: Apache-2.0

package blueprint

import (
	"path/filepath"
	"testing"
)

func TestGenerateAndLoadSigningKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "blueprint-signing.key")
	if err := GenerateSigningKeyFile(path); err != nil {
		t.Fatalf("GenerateSigningKeyFile: %v", err)
	}

	priv, err := LoadSigningKeyFile(path)
	if err != nil {
		t.Fatalf("LoadSigningKeyFile: %v", err)
	}
	if len(priv) == 0 {
		t.Fatal("loaded an empty private key")
	}

	// A second generate at the same path must fail (never silently
	// overwrite an existing identity).
	if err := GenerateSigningKeyFile(path); err == nil {
		t.Fatal("expected GenerateSigningKeyFile to refuse to overwrite an existing key")
	}
}

func TestLoadSigningKeyFile_MissingFile(t *testing.T) {
	if _, err := LoadSigningKeyFile(filepath.Join(t.TempDir(), "nope.key")); err == nil {
		t.Fatal("expected an error loading a missing signing key file")
	}
}
