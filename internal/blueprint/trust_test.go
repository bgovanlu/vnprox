// SPDX-License-Identifier: Apache-2.0

package blueprint

import (
	"errors"
	"testing"
)

func TestTrustStore_AddGetListDelete(t *testing.T) {
	ts := NewTrustStore(t.TempDir())

	list, err := ts.List()
	if err != nil {
		t.Fatalf("List on empty store: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected an empty list, got %+v", list)
	}

	fp := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	signer := TrustedSigner{Fingerprint: fp, PublicKey: "cHVia2V5", Label: "ci", AddedBy: "root@pam", AddedAt: 100}
	if addErr := ts.Add(signer); addErr != nil {
		t.Fatalf("Add: %v", addErr)
	}

	got, ok, err := ts.Get(fp)
	if err != nil || !ok {
		t.Fatalf("Get after Add = (%+v, %v, %v)", got, ok, err)
	}
	if got != signer {
		t.Errorf("Get = %+v, want %+v", got, signer)
	}

	list, err = ts.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List after Add = (%+v, %v)", list, err)
	}

	if err := ts.Delete(fp); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := ts.Get(fp); ok {
		t.Error("expected Get to report not-found after Delete")
	}

	if err := ts.Delete(fp); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete on already-removed signer = %v, want ErrNotFound", err)
	}
}

func TestTrustStore_RejectsMalformedFingerprint(t *testing.T) {
	ts := NewTrustStore(t.TempDir())
	bad := []string{"", "not-hex!!", "../../etc/passwd", "abc"}
	for _, fp := range bad {
		if _, _, err := ts.Get(fp); err == nil {
			t.Errorf("Get(%q) = nil error, want a validation error", fp)
		}
		if err := ts.Delete(fp); err == nil {
			t.Errorf("Delete(%q) = nil error, want a validation error", fp)
		}
		if err := ts.Add(TrustedSigner{Fingerprint: fp}); err == nil {
			t.Errorf("Add with fingerprint %q = nil error, want a validation error", fp)
		}
	}
}

func TestTrustStore_ListOnMissingDirectory(t *testing.T) {
	ts := NewTrustStore(t.TempDir() + "/does-not-exist-yet")
	list, err := ts.List()
	if err != nil {
		t.Fatalf("List on missing directory: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %+v", list)
	}
}
