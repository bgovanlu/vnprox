// SPDX-License-Identifier: Apache-2.0

package sigstoreverify

import (
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// LoadTrustedRoot returns the Fulcio/Rekor/CT trust roots a Verifier needs.
// An empty path fetches the Sigstore public-good instance's current roots
// live via TUF (root.FetchTrustedRoot) — a live, third-party network
// dependency, stated in docs/security.md and docs/hub-registry.md. A
// non-empty path pins a local copy instead (root.NewTrustedRootFromPath),
// avoiding that network dependency entirely.
func LoadTrustedRoot(path string) (root.TrustedMaterial, error) {
	if path == "" {
		tr, err := root.FetchTrustedRoot()
		if err != nil {
			return nil, fmt.Errorf("fetching the sigstore public-good trusted root: %w", err)
		}
		return tr, nil
	}
	tr, err := root.NewTrustedRootFromPath(path)
	if err != nil {
		return nil, fmt.Errorf("loading pinned sigstore trusted root %s: %w", path, err)
	}
	return tr, nil
}
