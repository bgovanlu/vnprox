// SPDX-License-Identifier: Apache-2.0

package pvecassette

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/redact"
)

// Load reads and validates one cassette file.
//
// The secret scan runs here too, not only on write. A cassette is a file
// in a git repository: it can be hand-edited, cherry-picked from another
// branch, or pasted in by someone who recorded it with an older build.
// Enforcing the rule on the way in as well as on the way out is what makes
// "no cassette this process serves contains a credential" true regardless
// of how the file got there.
func Load(path string) (Cassette, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // cassette paths come from the caller's own testdata tree
	if err != nil {
		return Cassette{}, fmt.Errorf("pvecassette: reading cassette %s: %w", path, err)
	}
	var c Cassette
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Cassette{}, fmt.Errorf("pvecassette: parsing cassette %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Cassette{}, fmt.Errorf("pvecassette: cassette %s: %w", path, err)
	}
	if findings := redact.ScanJSON(BodyRoot, []byte(c.Body)); len(findings) > 0 {
		return Cassette{}, fmt.Errorf("pvecassette: cassette %s: %w", path,
			&SecretError{Method: c.Method, Path: c.Path, Findings: findings})
	}
	return c, nil
}

// LoadDir reads every *.json cassette under dir, recursively, and returns
// them keyed by Key.
//
// Two cassettes claiming the same request is an error, not a
// last-one-wins: which file answered a request would silently decide what
// a test observed.
func LoadDir(dir string) (map[string]Cassette, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("pvecassette: opening cassette dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("pvecassette: cassette dir %s is not a directory", dir)
	}

	out := map[string]Cassette{}
	from := map[string]string{}
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		c, loadErr := Load(path)
		if loadErr != nil {
			return loadErr
		}
		key := c.Key()
		if prev, dup := from[key]; dup {
			return fmt.Errorf("%w: %q is answered by both %s and %s", ErrDuplicateCassette, key, prev, path)
		}
		from[key] = path
		out[key] = c
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("pvecassette: loading cassettes from %s: %w", dir, walkErr)
	}
	return out, nil
}

// Keys returns the sorted request keys of a loaded cassette set — what a
// replay server prints when it cannot match a request, and what a test
// asserts coverage against.
func Keys(set map[string]Cassette) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
