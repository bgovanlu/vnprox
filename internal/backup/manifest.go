// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"fmt"
	"regexp"
	"strings"
)

// FormatVersion is the archive format this build writes and the highest it
// reads. A reader refuses anything it does not recognise rather than
// guessing — an archive is untrusted input, and "try to cope" is how
// parsers grow holes.
const FormatVersion = 1

// ManifestName is the archive's first entry, always. Putting it first is
// what lets Inspect decide "is this a key-bearing archive / is this from a
// newer schema" after reading a few kilobytes, and what lets the reader
// validate every subsequent entry against a declaration made before it.
const ManifestName = "manifest.json"

// Kind distinguishes archive purposes that share this format. T-1901 writes
// KindBackup; T-1902's support bundle is expected to write
// KindSupportBundle over the same Writer/Inspect machinery with a harsher
// collection policy. Restore refuses anything that is not KindBackup — a
// support bundle is redacted by construction and would restore a
// deliberately incomplete store.
type Kind string

const (
	KindBackup        Kind = "backup"
	KindSupportBundle Kind = "support-bundle"
)

// Entry roles. Restore keys off these rather than off entry names, so the
// layout inside the archive can change without the restore logic having to
// pattern-match paths.
const (
	RoleStore  = "store"  // the SQLite store snapshot
	RoleConfig = "config" // vnprox.toml
	RoleKey    = "key"    // key material — present only with --include-keys
	RoleMeta   = "meta"   // human-readable notes, never restored anywhere
)

// Manifest is the archive header: what this archive is, what produced it,
// what is in it, and — the part that matters most — whether it contains key
// material.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing.
type Manifest struct {
	// Format is FormatVersion. First field so a truncated/garbage archive
	// fails on something cheap.
	Format int `json:"format"`
	// Kind is KindBackup or KindSupportBundle.
	Kind Kind `json:"kind"`
	// CreatedAt is RFC3339 in UTC.
	CreatedAt string `json:"createdAt"`
	// Tool and ToolVersion record what wrote this.
	Tool        string `json:"tool"`
	ToolVersion string `json:"toolVersion"`
	// Node is the hostname the archive was taken on. Restoring onto
	// different hardware is supported and expected; this records where it
	// came from so an operator can tell.
	Node string `json:"node"`
	// SchemaVersion is the store's schema version at capture time. Restore
	// refuses an archive whose SchemaVersion exceeds what the running
	// binary understands (the downgrade direction) and cross-checks this
	// against the extracted store itself, so a manifest cannot lie its way
	// past the check.
	SchemaVersion int `json:"schemaVersion"`
	// IncludesKeyMaterial marks a total-compromise archive: one that
	// carries the session key and its siblings in the clear. This is the
	// archive header marking T-1901's safety analysis requires; it drives
	// the restore-side warning and the `-with-keys` filename suffix.
	IncludesKeyMaterial bool `json:"includesKeyMaterial"`
	// SecretClasses lists, by SecretClass.ID, the classes present in this
	// archive *in the clear*. Empty for a default backup: the store's
	// sealed columns are ciphertext, which is not a class in the clear.
	SecretClasses []string `json:"secretClasses"`
	// Entries declares every file after this manifest, in the exact order
	// they appear. The reader enforces that correspondence entry by entry.
	Entries []Entry `json:"entries"`
}

// Entry is one declared file in the archive.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing.
type Entry struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
	// Origin is the absolute path this entry was collected from, for the
	// operator's benefit. Never used to decide where anything is written —
	// restore derives every destination from the running config.
	Origin string `json:"origin,omitempty"`
}

// entryNamePattern is the whole vocabulary of legal entry names: lowercase
// alphanumerics, dot, dash, underscore, and `/` as a separator. It is an
// allowlist on purpose. Traversal, absolute paths, `..`, backslashes,
// NUL bytes, drive letters and every encoding trick are excluded by not
// being expressible, rather than by a blocklist that has to anticipate
// them.
var entryNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*$`)

// hexDigestPattern matches a lowercase hex SHA-256.
var hexDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validEntryName reports whether name is a legal archive entry name.
//
// The pattern above already excludes `..` as a whole path element (an
// element must start with an alphanumeric), but the explicit check is kept
// because this is the single most important property in this file and a
// future pattern edit must not be able to quietly lose it.
func validEntryName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	if strings.Contains(name, "\x00") {
		return false
	}
	if !entryNamePattern.MatchString(name) {
		return false
	}
	for _, elem := range strings.Split(name, "/") {
		if elem == "." || elem == ".." {
			return false
		}
	}
	return true
}

// validate checks a decoded manifest for internal consistency before any
// entry body is read. Everything here is cheap and everything here is a
// precondition for trusting the entry stream that follows.
func (m *Manifest) validate(lim Limits) error {
	if m.Format != FormatVersion {
		return fmt.Errorf("%w: archive format version %d, this build reads %d", ErrUnsupportedFormat, m.Format, FormatVersion)
	}
	switch m.Kind {
	case KindBackup, KindSupportBundle:
	default:
		return fmt.Errorf("%w: unknown archive kind %q", ErrUnsupportedFormat, m.Kind)
	}
	if len(m.Entries) > lim.MaxEntries {
		return fmt.Errorf("%w: manifest declares %d entries, limit is %d", ErrLimitExceeded, len(m.Entries), lim.MaxEntries)
	}
	if m.SchemaVersion < 0 {
		return fmt.Errorf("%w: negative schema version %d", ErrMalformedArchive, m.SchemaVersion)
	}

	var total int64
	seen := make(map[string]bool, len(m.Entries))
	for i, e := range m.Entries {
		if !validEntryName(e.Name) {
			return fmt.Errorf("%w: entry %d name %q is not a legal archive path", ErrUnsafeEntryName, i, e.Name)
		}
		if e.Name == ManifestName {
			return fmt.Errorf("%w: entry %d re-declares %s", ErrMalformedArchive, i, ManifestName)
		}
		if seen[e.Name] {
			return fmt.Errorf("%w: entry %q declared twice", ErrMalformedArchive, e.Name)
		}
		seen[e.Name] = true
		if e.Size < 0 {
			return fmt.Errorf("%w: entry %q declares a negative size", ErrMalformedArchive, e.Name)
		}
		if e.Size > lim.MaxEntryBytes {
			return fmt.Errorf("%w: entry %q declares %d bytes, per-entry limit is %d", ErrLimitExceeded, e.Name, e.Size, lim.MaxEntryBytes)
		}
		total += e.Size
		if total > lim.MaxTotalBytes {
			return fmt.Errorf("%w: manifest declares %d bytes total, limit is %d", ErrLimitExceeded, total, lim.MaxTotalBytes)
		}
		if !hexDigestPattern.MatchString(e.SHA256) {
			return fmt.Errorf("%w: entry %q has no valid sha256", ErrMalformedArchive, e.Name)
		}
		switch e.Role {
		case RoleStore, RoleConfig, RoleKey, RoleMeta:
		default:
			return fmt.Errorf("%w: entry %q has unknown role %q", ErrMalformedArchive, e.Name, e.Role)
		}
	}

	// A key-bearing archive must say so, and a non-key-bearing archive must
	// not carry key entries. Both directions are checked: the first is the
	// loud-marking requirement, the second stops a crafted manifest from
	// smuggling a key file past a reader that trusts the flag.
	hasKeyEntry := false
	for _, e := range m.Entries {
		if e.Role == RoleKey {
			hasKeyEntry = true
			break
		}
	}
	if hasKeyEntry && !m.IncludesKeyMaterial {
		return fmt.Errorf("%w: archive carries key material but its manifest does not declare it", ErrMalformedArchive)
	}
	if m.IncludesKeyMaterial && !hasKeyEntry {
		return fmt.Errorf("%w: manifest declares key material but the archive carries no key entry", ErrMalformedArchive)
	}
	return nil
}

// Entry returns the single entry with the given role, and whether exactly
// one exists. Restore uses this for the store: "exactly one" is the point —
// an archive with two store entries is malformed, not a choice to make.
func (m *Manifest) Entry(role string) (Entry, bool) {
	var found Entry
	n := 0
	for _, e := range m.Entries {
		if e.Role == role {
			found = e
			n++
		}
	}
	return found, n == 1
}

// EntriesWithRole returns every entry with the given role, in order.
func (m *Manifest) EntriesWithRole(role string) []Entry {
	var out []Entry
	for _, e := range m.Entries {
		if e.Role == role {
			out = append(out, e)
		}
	}
	return out
}
