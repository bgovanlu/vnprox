// SPDX-License-Identifier: Apache-2.0

// collect.go is the collection seam T-1902's support bundle is meant to
// reuse.
//
// The shape is deliberately narrow: a Collector declares, before it runs,
// which secret classes its output can contain, and writes only through a
// Staging area that records a digest for everything it emits. Two
// consequences follow, and they are the reason this is an interface rather
// than three functions:
//
//   - Nothing reaches an archive without being declared. The manifest is
//     built from what Staging recorded, not from what a collector says it
//     wrote, so a collector cannot emit an entry the manifest does not
//     describe.
//   - "What secrets can this archive contain" is answerable statically, by
//     unioning Emits() over the collectors that ran. That is what the
//     --include-keys warning prints, and it is the hook T-1902's
//     "a collector that cannot describe its output does not ship" needs.
//
// A backup's policy is "collect everything, and be honest about it"; a
// support bundle's will be "collect only what an allowlist permits, and
// redact the rest". The difference lives entirely in the collectors, not in
// this file.

package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Collector gathers one class of artifact into a Staging area.
type Collector interface {
	// Name is a short, stable identifier used in logs, --dry-run output and
	// errors.
	Name() string
	// Emits declares every secret class this collector's output can contain
	// **in the clear**. A collector whose output only contains sealed
	// ciphertext declares nothing here — the ciphertext's class is a
	// property of the store, not of this collection step, and is reported
	// separately.
	Emits() []SecretClass
	// Collect writes this collector's artifacts into st.
	Collect(ctx context.Context, st *Staging) error
}

// Staging is a private, 0700 scratch directory collectors write into, plus
// the entry list that becomes the archive manifest.
//
//nolint:govet // fieldalignment: an options struct read top-to-bottom by humans; grouping by meaning beats packing a handful of bytes.
type Staging struct {
	dir     string
	entries []Entry
	names   map[string]bool
}

// NewStaging creates a staging area under parent. The directory is 0700:
// it holds an unencrypted copy of the store and, under --include-keys, the
// session key, for as long as the archive takes to write.
func NewStaging(parent string) (*Staging, error) {
	dir, err := os.MkdirTemp(parent, "vnprox-collect-")
	if err != nil {
		return nil, fmt.Errorf("backup: creating staging directory under %s: %w", parent, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("backup: securing staging directory %s: %w", dir, err)
	}
	return &Staging{dir: dir, names: map[string]bool{}}, nil
}

// Dir is the staging directory's path. Collectors that need to hand a path
// to something else (internal/store.SnapshotTo, say) resolve it through
// Reserve rather than joining onto this themselves.
func (s *Staging) Dir() string { return s.dir }

// Remove deletes the staging area and everything in it. Callers defer this
// unconditionally: on the success path it removes a plaintext store copy
// that has already been archived, and on the failure path it removes a
// partial one.
func (s *Staging) Remove() error {
	if s == nil || s.dir == "" {
		return nil
	}
	if err := os.RemoveAll(s.dir); err != nil {
		return fmt.Errorf("backup: removing staging directory %s: %w", s.dir, err)
	}
	return nil
}

// Reserve validates an entry name and returns the absolute path a collector
// should write it to, without creating anything. Used by collectors that
// hand a destination path to another package (SnapshotTo), which then have
// to call Record once the file exists.
func (s *Staging) Reserve(name string) (string, error) {
	if !validEntryName(name) {
		return "", fmt.Errorf("%w: collector asked for entry name %q", ErrUnsafeEntryName, name)
	}
	if s.names[name] {
		return "", fmt.Errorf("backup: entry %q collected twice", name)
	}
	path := filepath.Join(s.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("backup: creating staging subdirectory for %s: %w", name, err)
	}
	return path, nil
}

// Record adds an already-written staged file to the entry list, hashing it
// as it goes. Every entry in an archive gets here one way or another; there
// is no path that adds an entry without a digest.
func (s *Staging) Record(name, role string, origin string) error {
	if !validEntryName(name) {
		return fmt.Errorf("%w: collector recorded entry name %q", ErrUnsafeEntryName, name)
	}
	if s.names[name] {
		return fmt.Errorf("backup: entry %q collected twice", name)
	}
	path := filepath.Join(s.dir, filepath.FromSlash(name))
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("backup: staging entry %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup: staged entry %s is not a regular file", name)
	}
	digest, err := fileDigest(path)
	if err != nil {
		return err
	}
	s.names[name] = true
	s.entries = append(s.entries, Entry{
		Name:   name,
		Role:   role,
		Size:   info.Size(),
		SHA256: digest,
		Mode:   uint32(info.Mode().Perm()),
		Origin: origin,
	})
	return nil
}

// CopyFile copies srcPath into the staging area as name and records it,
// preserving the source's permission bits (so a 0600 key file stays 0600
// inside the archive and after extraction).
func (s *Staging) CopyFile(name, role, srcPath string) error {
	dst, err := s.Reserve(name)
	if err != nil {
		return err
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("backup: reading %s: %w", srcPath, err)
	}
	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("backup: stat %s: %w", srcPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup: %s is not a regular file", srcPath)
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("backup: staging %s: %w", name, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("backup: copying %s: %w", srcPath, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("backup: closing staged %s: %w", name, err)
	}
	return s.Record(name, role, srcPath)
}

// WriteFile writes data into the staging area as name and records it.
func (s *Staging) WriteFile(name, role string, mode fs.FileMode, data []byte) error {
	dst, err := s.Reserve(name)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, mode.Perm()); err != nil {
		return fmt.Errorf("backup: staging %s: %w", name, err)
	}
	return s.Record(name, role, "")
}

// Entries returns the recorded entries, sorted by name so an archive's
// layout is deterministic regardless of collector ordering or filesystem
// iteration order.
func (s *Staging) Entries() []Entry {
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("backup: hashing %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("backup: hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// declaredSecretClasses unions Emits() over a set of collectors, in
// inventory order and without duplicates.
func declaredSecretClasses(cs []Collector) []SecretClass {
	seen := map[string]bool{}
	var out []SecretClass
	for _, inv := range secretClasses {
		for _, c := range cs {
			for _, e := range c.Emits() {
				if e.ID == inv.ID && !seen[inv.ID] {
					seen[inv.ID] = true
					out = append(out, inv)
				}
			}
		}
	}
	return out
}
