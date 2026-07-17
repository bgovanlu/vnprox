package store

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHexTokenFile_GenerateAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "metrics.key")

	if err := GenerateHexTokenFile(path); err != nil {
		t.Fatalf("GenerateHexTokenFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat generated token file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 0600", perm)
	}

	token, err := LoadHexTokenFile(path)
	if err != nil {
		t.Fatalf("LoadHexTokenFile: %v", err)
	}
	if len(token) != TokenSize*2 {
		t.Errorf("loaded token length = %d hex chars, want %d", len(token), TokenSize*2)
	}
	if _, decErr := hex.DecodeString(token); decErr != nil {
		t.Errorf("loaded token %q is not valid hex: %v", token, decErr)
	}

	// A second GenerateHexTokenFile at the same path must refuse to clobber
	// it — the same "never silently overwrite" contract GenerateKeyFile
	// enforces for the session key.
	if genErr := GenerateHexTokenFile(path); genErr == nil {
		t.Error("GenerateHexTokenFile over an existing token: got nil error, want a refusal")
	}
	// And the on-disk token must be unchanged after the refused overwrite.
	again, err := LoadHexTokenFile(path)
	if err != nil {
		t.Fatalf("LoadHexTokenFile after refused overwrite: %v", err)
	}
	if again != token {
		t.Error("token changed after a refused GenerateHexTokenFile overwrite")
	}
}

func TestGenerateHexTokenFile_TwoCallsProduceDifferentTokens(t *testing.T) {
	pathA := filepath.Join(t.TempDir(), "a.key")
	pathB := filepath.Join(t.TempDir(), "b.key")
	if err := GenerateHexTokenFile(pathA); err != nil {
		t.Fatalf("GenerateHexTokenFile(a): %v", err)
	}
	if err := GenerateHexTokenFile(pathB); err != nil {
		t.Fatalf("GenerateHexTokenFile(b): %v", err)
	}
	tokenA, err := LoadHexTokenFile(pathA)
	if err != nil {
		t.Fatalf("LoadHexTokenFile(a): %v", err)
	}
	tokenB, err := LoadHexTokenFile(pathB)
	if err != nil {
		t.Fatalf("LoadHexTokenFile(b): %v", err)
	}
	if tokenA == tokenB {
		t.Error("two independently generated tokens are equal — rand.Reader not exercised")
	}
}

func TestLoadHexTokenFile_RejectsNonHex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.key")
	if err := os.WriteFile(path, []byte("not-hex-at-all!!\n"), 0o600); err != nil {
		t.Fatalf("writing bad token file: %v", err)
	}
	if _, err := LoadHexTokenFile(path); err == nil {
		t.Error("LoadHexTokenFile(non-hex): got nil error, want a failure")
	}
}

func TestLoadHexTokenFile_RejectsWrongSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.key")
	// Valid hex, but decodes to far fewer than TokenSize bytes.
	if err := os.WriteFile(path, []byte("aabbcc\n"), 0o600); err != nil {
		t.Fatalf("writing short token file: %v", err)
	}
	if _, err := LoadHexTokenFile(path); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("LoadHexTokenFile(wrong size): got %v, want ErrInvalidKey", err)
	}
}

func TestLoadHexTokenFile_TrimsWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.key")
	if err := GenerateHexTokenFile(path); err != nil {
		t.Fatalf("GenerateHexTokenFile: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading generated token file: %v", err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("generated token file has no trailing newline")
	}
	token, err := LoadHexTokenFile(path)
	if err != nil {
		t.Fatalf("LoadHexTokenFile: %v", err)
	}
	if strings.ContainsAny(token, "\n\r \t") {
		t.Errorf("loaded token %q retains whitespace", token)
	}
}
