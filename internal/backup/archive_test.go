// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- helpers

// rawArchive builds a tar.gz by hand, entry by entry, bypassing Write
// entirely. Every hostile case below is constructed with this: an attacker
// does not use our writer, so neither does the test.
//
//nolint:govet // fieldalignment: a test fixture struct; readability beats packing.
type rawEntry struct {
	name     string
	body     []byte
	typeflag byte
	linkname string
	// sizeOverride, when non-nil, writes a tar header claiming a different
	// size from the body actually written.
	sizeOverride *int64
	mode         int64
}

func rawArchive(t *testing.T, manifest any, entries []rawEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if manifest != nil {
		var mb []byte
		switch m := manifest.(type) {
		case []byte:
			mb = m
		case json.RawMessage:
			mb = m
		default:
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("encoding manifest: %v", err)
			}
			mb = b
		}
		entries = append([]rawEntry{{name: ManifestName, body: mb, typeflag: tar.TypeReg, mode: 0o600}}, entries...)
	}

	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		size := int64(len(e.body))
		if e.sizeOverride != nil {
			size = *e.sizeOverride
		}
		if tf != tar.TypeReg {
			size = 0
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o600
		}
		hdr := &tar.Header{
			Typeflag: tf,
			Name:     e.name,
			Linkname: e.linkname,
			Mode:     mode,
			Size:     size,
			ModTime:  time.Unix(0, 0).UTC(),
			Format:   tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("writing header %q: %v", e.name, err)
		}
		if tf == tar.TypeReg && len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				// A deliberately over-large declared size makes tar refuse
				// the short body; that case supplies its own body length.
				t.Fatalf("writing body %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip: %v", err)
	}
	return buf.Bytes()
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// goodManifest is a minimal, valid manifest for one store entry.
func goodManifest(body []byte) Manifest {
	return Manifest{
		Format:        FormatVersion,
		Kind:          KindBackup,
		CreatedAt:     "2026-01-01T00:00:00Z",
		Tool:          "test",
		ToolVersion:   "test",
		Node:          "pve1",
		SchemaVersion: 1,
		SecretClasses: []string{},
		Entries: []Entry{{
			Name: entryStore, Role: RoleStore, Size: int64(len(body)),
			SHA256: digest(body), Mode: 0o600,
		}},
	}
}

// ---------------------------------------------------------------- AC5

// TestArchive_AC5_MaliciousArchivesAreRejected is T-1901 AC5.
//
// Two properties are asserted for every row, not one:
//
//  1. the archive is rejected, with the *specific* sentinel that names why
//     (a generic "some error" would let a decompression bomb pass as a
//     digest mismatch and nobody would notice); and
//  2. nothing was written outside the extraction directory — checked
//     positively, by planting a canary next to it and asserting the canary
//     is byte-identical afterwards, plus asserting the extraction directory
//     itself is still empty.
//
// The extraction target for every row is a fresh directory whose *sibling*
// holds the canary, so a `../` traversal that worked would land squarely on
// it. That is the whole point of the layout.
func TestArchive_AC5_MaliciousArchivesAreRejected(t *testing.T) {
	body := []byte("a small pretend sqlite store")

	// A gzip stream that expands to far more than the reader's budget,
	// built from a highly compressible body. Limits are lowered for this
	// row so the test stays fast; the mechanism under test (an absolute,
	// streamed budget) is identical at production size.
	bombBody := bytes.Repeat([]byte{0}, 4<<20) // 4 MiB of zeros, ~4 KiB compressed
	bombManifest := goodManifest(bombBody)

	truncated := rawArchive(t, goodManifest(body), []rawEntry{{name: entryStore, body: body}})

	//nolint:govet // fieldalignment: a table-driven test case struct; row readability beats packing.
	cases := []struct {
		name string
		// build returns the archive bytes.
		build func(t *testing.T) []byte
		lim   Limits
		want  error
		// why documents the attack this row represents.
		why string
	}{
		{
			name: "relative path traversal in an entry name",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries[0].Name = "../../../../etc/vnprox/keys/session.key"
				return rawArchive(t, m, []rawEntry{{name: "../../../../etc/vnprox/keys/session.key", body: body}})
			},
			want: ErrUnsafeEntryName,
			why:  "the classic tar traversal: write outside the extraction root by climbing out of it",
		},
		{
			name: "absolute path in an entry name",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries[0].Name = "/etc/vnprox/keys/session.key"
				return rawArchive(t, m, []rawEntry{{name: "/etc/vnprox/keys/session.key", body: body}})
			},
			want: ErrUnsafeEntryName,
			why:  "an absolute name ignores the extraction root entirely",
		},
		{
			name: "traversal hidden mid-path",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries[0].Name = "store/../../escaped.db"
				return rawArchive(t, m, []rawEntry{{name: "store/../../escaped.db", body: body}})
			},
			want: ErrUnsafeEntryName,
			why:  "a name that looks rooted until it is cleaned",
		},
		{
			name: "backslash-separated name",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries[0].Name = `..\..\escaped.db`
				return rawArchive(t, m, []rawEntry{{name: `..\..\escaped.db`, body: body}})
			},
			want: ErrUnsafeEntryName,
			why:  "backslashes are not path separators on Linux, but the name vocabulary excludes them anyway rather than reasoning about it",
		},
		{
			name: "symlink entry pointing outside",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries[0].Name = "store/vnprox.db"
				return rawArchive(t, m, []rawEntry{
					{name: "store/vnprox.db", typeflag: tar.TypeSymlink, linkname: "../../canary.txt"},
				})
			},
			want: ErrMalformedArchive,
			why:  "the two-stage attack: extract a symlink with a safe name, then let a later entry write through it",
		},
		{
			name: "hard link entry",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				return rawArchive(t, m, []rawEntry{
					{name: entryStore, typeflag: tar.TypeLink, linkname: "../canary.txt"},
				})
			},
			want: ErrMalformedArchive,
			why:  "same attack, hard link flavour",
		},
		{
			name: "device node entry",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				return rawArchive(t, m, []rawEntry{
					{name: entryStore, typeflag: tar.TypeChar},
				})
			},
			want: ErrMalformedArchive,
			why:  "an archive has no business creating device nodes as root on a hypervisor",
		},
		{
			name: "decompression bomb over the total budget",
			build: func(t *testing.T) []byte {
				return rawArchive(t, bombManifest, []rawEntry{{name: entryStore, body: bombBody}})
			},
			lim: Limits{
				MaxEntries: 256, MaxEntryBytes: 64 << 10, MaxTotalBytes: 64 << 10, MaxManifestBytes: 1 << 20,
			},
			want: ErrLimitExceeded,
			why:  "a few KiB on the wire expanding to megabytes on disk; the budget is absolute and enforced from the tar header, before the body is read",
		},
		{
			name: "entry count over the budget",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries = nil
				var raw []rawEntry
				for i := 0; i < 40; i++ {
					name := "meta/note-" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".txt"
					m.Entries = append(m.Entries, Entry{
						Name: name, Role: RoleMeta, Size: int64(len(body)), SHA256: digest(body), Mode: 0o600,
					})
					raw = append(raw, rawEntry{name: name, body: body})
				}
				return rawArchive(t, m, raw)
			},
			lim: Limits{
				MaxEntries: 8, MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20, MaxManifestBytes: 1 << 20,
			},
			want: ErrLimitExceeded,
			why:  "many small entries are a bomb too; the count is bounded independently of the bytes",
		},
		{
			name: "oversized manifest",
			build: func(t *testing.T) []byte {
				return rawArchive(t, json.RawMessage(bytes.Repeat([]byte("x"), 128<<10)), nil)
			},
			lim: Limits{
				MaxEntries: 256, MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20, MaxManifestBytes: 1 << 10,
			},
			want: ErrLimitExceeded,
			why:  "the manifest is read before anything is known about the archive, so its own size must be bounded first",
		},
		{
			name: "truncated mid-entry",
			build: func(t *testing.T) []byte {
				// Cut the gzip stream in half: the manifest is readable,
				// the store entry is not.
				return truncated[:len(truncated)/2]
			},
			want: ErrTruncatedArchive,
			why:  "an interrupted scp or a filled disk produces exactly this; it must never be restored from",
		},
		{
			name:  "truncated before the gzip header",
			build: func(t *testing.T) []byte { return truncated[:2] },
			want:  ErrTruncatedArchive,
			why:   "a zero-length or near-zero-length file must not be reported as a malformed archive of unknown provenance",
		},
		{
			name: "not a gzip stream at all",
			build: func(t *testing.T) []byte {
				return []byte("PK\x03\x04 this is a zip file, not a vnprox backup")
			},
			want: ErrMalformedArchive,
			why:  "the operator restored the wrong file",
		},
		{
			name: "manifest is not the first entry",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				mb, _ := json.Marshal(m)
				return rawArchive(t, nil, []rawEntry{
					{name: entryStore, body: body},
					{name: ManifestName, body: mb},
				})
			},
			want: ErrMalformedArchive,
			why:  "reordering the manifest to the end would let entries be processed before the declaration that bounds them",
		},
		{
			name: "no manifest at all",
			build: func(t *testing.T) []byte {
				return rawArchive(t, nil, []rawEntry{{name: entryStore, body: body}})
			},
			want: ErrMalformedArchive,
			why:  "an ordinary tarball someone renamed",
		},
		{
			name: "entry body does not match its declared digest",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				tampered := append([]byte{}, body...)
				tampered[0] ^= 0xff
				m.Entries[0].Size = int64(len(tampered))
				return rawArchive(t, m, []rawEntry{{name: entryStore, body: tampered}})
			},
			want: ErrDigestMismatch,
			why:  "the store was swapped for another one while the manifest was left alone",
		},
		{
			name: "entry not declared by the manifest",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				extra := []byte("smuggled")
				return rawArchive(t, m, []rawEntry{
					{name: entryStore, body: body},
					{name: "keys/session.key", body: extra},
				})
			},
			want: ErrMalformedArchive,
			why:  "an undeclared trailing entry is how key material would be smuggled into an archive whose manifest says it holds none",
		},
		{
			name: "manifest declares more entries than the archive holds",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries = append(m.Entries, Entry{
					Name: "config/vnprox.toml", Role: RoleConfig, Size: 4, SHA256: digest([]byte("abcd")), Mode: 0o600,
				})
				return rawArchive(t, m, []rawEntry{{name: entryStore, body: body}})
			},
			want: ErrTruncatedArchive,
			why:  "a partial archive whose manifest survived intact",
		},
		{
			name: "tar header size disagrees with the manifest",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries[0].Size = 4
				m.Entries[0].SHA256 = digest(body[:4])
				return rawArchive(t, m, []rawEntry{{name: entryStore, body: body}})
			},
			want: ErrMalformedArchive,
			why:  "the size check happens from the header, before the body is read — that is what makes it a bomb defence and not a post-hoc audit",
		},
		{
			name: "duplicate entry names in the manifest",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries = append(m.Entries, m.Entries[0])
				return rawArchive(t, m, []rawEntry{
					{name: entryStore, body: body}, {name: entryStore, body: body},
				})
			},
			want: ErrMalformedArchive,
			why:  "the second write of the same name is an overwrite; the format simply has no legitimate use for one",
		},
		{
			name: "unknown format version",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Format = FormatVersion + 7
				return rawArchive(t, m, []rawEntry{{name: entryStore, body: body}})
			},
			want: ErrUnsupportedFormat,
			why:  "a future format is refused rather than guessed at",
		},
		{
			name: "unknown archive kind",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Kind = "something-else"
				return rawArchive(t, m, []rawEntry{{name: entryStore, body: body}})
			},
			want: ErrUnsupportedFormat,
			why:  "the kind decides policy; an unrecognised one has no policy",
		},
		{
			name: "unknown entry role",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries[0].Role = "something-new"
				return rawArchive(t, m, []rawEntry{{name: entryStore, body: body}})
			},
			want: ErrMalformedArchive,
			why:  "roles drive where things go; an unknown one must not default to anything",
		},
		{
			name: "key entry in an archive that claims to hold no key material",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				m.Entries[0].Role = RoleKey
				m.IncludesKeyMaterial = false
				return rawArchive(t, m, []rawEntry{{name: entryStore, body: body}})
			},
			want: ErrMalformedArchive,
			why:  "the header marking must be trustworthy in BOTH directions, or an operator reading `includesKeyMaterial: false` learns nothing",
		},
		{
			name: "manifest with an unknown field",
			build: func(t *testing.T) []byte {
				m := goodManifest(body)
				mb, _ := json.Marshal(m)
				var generic map[string]any
				_ = json.Unmarshal(mb, &generic)
				generic["extractTo"] = "/etc/vnprox/keys"
				mb2, _ := json.Marshal(generic)
				return rawArchive(t, json.RawMessage(mb2), []rawEntry{{name: entryStore, body: body}})
			},
			want: ErrMalformedArchive,
			why:  "an unknown manifest field is either a newer format or an attempt to influence a future reader; neither is something to ignore silently",
		},
		{
			name: "corrupt gzip checksum",
			build: func(t *testing.T) []byte {
				a := rawArchive(t, goodManifest(body), []rawEntry{{name: entryStore, body: body}})
				b := append([]byte{}, a...)
				b[len(b)-5] ^= 0xff // inside the trailing CRC32/ISIZE
				return b
			},
			want: ErrMalformedArchive,
			why:  "bit rot on the archive itself",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lim := tc.lim
			if lim == (Limits{}) {
				lim = DefaultLimits()
			}
			data := tc.build(t)

			// The canary sits NEXT TO the extraction directory, so any
			// successful `../` lands on it.
			root := t.TempDir()
			canary := filepath.Join(root, "canary.txt")
			const canaryContent = "untouched"
			if err := os.WriteFile(canary, []byte(canaryContent), 0o600); err != nil {
				t.Fatalf("planting canary: %v", err)
			}
			dest := filepath.Join(root, "extract")
			if err := os.Mkdir(dest, 0o700); err != nil {
				t.Fatalf("creating extraction dir: %v", err)
			}

			// Inspect must reject it too — that is the pass Restore runs
			// first, and it never touches disk at all.
			if _, err := Inspect(bytes.NewReader(data), lim); !errors.Is(err, tc.want) {
				t.Errorf("Inspect error = %v, want %v (%s)", err, tc.want, tc.why)
			}
			if _, err := Extract(bytes.NewReader(data), dest, lim); !errors.Is(err, tc.want) {
				t.Errorf("Extract error = %v, want %v (%s)", err, tc.want, tc.why)
			}

			got, err := os.ReadFile(canary)
			if err != nil {
				t.Fatalf("canary is gone: %v", err)
			}
			if string(got) != canaryContent {
				t.Errorf("canary was overwritten (%q) — the archive escaped the extraction directory", got)
			}
			// Files may exist inside the extraction directory for a case
			// that fails on a later entry; nothing may exist outside it.
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatalf("listing extraction root: %v", err)
			}
			for _, e := range entries {
				if e.Name() != "canary.txt" && e.Name() != "extract" {
					t.Errorf("archive created %q outside the extraction directory", e.Name())
				}
			}
		})
	}
}

// TestArchive_AC5_TheRejectionTestIsNotVacuous is the companion the table
// above needs: it proves that an archive built by the SAME helper, with the
// same body and the same extraction call, is ACCEPTED when nothing hostile
// is done to it. Without this, every row above could be passing because
// rawArchive produces something the reader rejects for an unrelated reason.
func TestArchive_AC5_TheRejectionTestIsNotVacuous(t *testing.T) {
	body := []byte("a small pretend sqlite store")
	data := rawArchive(t, goodManifest(body), []rawEntry{{name: entryStore, body: body}})

	m, err := Inspect(bytes.NewReader(data), DefaultLimits())
	if err != nil {
		t.Fatalf("a benign archive from the same builder was rejected (%v) — every rejection case above is suspect", err)
	}
	if m.SchemaVersion != 1 || len(m.Entries) != 1 {
		t.Fatalf("Inspect returned an unexpected manifest: %+v", m)
	}

	dest := t.TempDir()
	if _, extractErr := Extract(bytes.NewReader(data), dest, DefaultLimits()); extractErr != nil {
		t.Fatalf("Extract of a benign archive: %v", extractErr)
	}
	got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(entryStore)))
	if err != nil {
		t.Fatalf("extracted store missing: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("extracted store = %q, want %q", got, body)
	}
}

// TestArchive_InspectWritesNothing pins the property Restore's ordering
// depends on: the validation pass creates no files at all, so "rejected
// before anything touches disk" is literal.
func TestArchive_InspectWritesNothing(t *testing.T) {
	dir := t.TempDir()
	before := listTree(t, dir)

	body := []byte("store bytes")
	good := rawArchive(t, goodManifest(body), []rawEntry{{name: entryStore, body: body}})
	if _, err := Inspect(bytes.NewReader(good), DefaultLimits()); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	m := goodManifest(body)
	m.Entries[0].Name = "../escape"
	bad := rawArchive(t, m, []rawEntry{{name: "../escape", body: body}})
	if _, err := Inspect(bytes.NewReader(bad), DefaultLimits()); err == nil {
		t.Fatal("Inspect accepted a traversing archive")
	}

	if after := listTree(t, dir); after != before {
		t.Errorf("Inspect changed the filesystem: %q -> %q", before, after)
	}
}

func listTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		b.WriteString(p)
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return b.String()
}

// TestArchive_RoundTripPreservesModes covers the writer/reader pair on the
// happy path, including the property that matters for a key file: a 0600
// entry comes back 0600, not 0644-by-umask.
func TestArchive_RoundTripPreservesModes(t *testing.T) {
	stageParent := t.TempDir()
	st, err := NewStaging(stageParent)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	defer func() { _ = st.Remove() }()

	if stageErr := st.WriteFile(entryStore, RoleStore, 0o600, []byte("store")); stageErr != nil {
		t.Fatalf("staging store: %v", stageErr)
	}
	if stageKeyErr := st.WriteFile("keys/session.key", RoleKey, 0o600, []byte("0123456789abcdef0123456789abcdef")); stageKeyErr != nil {
		t.Fatalf("staging key: %v", stageKeyErr)
	}
	if stageCfgErr := st.WriteFile(entryConfig, RoleConfig, 0o644, []byte("[server]\n")); stageCfgErr != nil {
		t.Fatalf("staging config: %v", stageCfgErr)
	}

	m := Manifest{
		Format: FormatVersion, Kind: KindBackup, CreatedAt: "2026-01-01T00:00:00Z",
		Tool: "test", ToolVersion: "test", Node: "pve1", SchemaVersion: 3,
		IncludesKeyMaterial: true, SecretClasses: []string{"session_key"},
		Entries: st.Entries(),
	}
	dest := filepath.Join(t.TempDir(), "a.tar.gz")
	if _, writeErr := Write(dest, m, st.Dir()); writeErr != nil {
		t.Fatalf("Write: %v", writeErr)
	}
	if info, statErr := os.Stat(dest); statErr != nil {
		t.Fatalf("stat archive: %v", statErr)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("archive permissions = %04o, want 0600 — an archive is at least as sensitive as the store in it", perm)
	}

	out := t.TempDir()
	f, err := os.Open(dest)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	got, err := Extract(f, out, DefaultLimits())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got.SchemaVersion != 3 || !got.IncludesKeyMaterial {
		t.Errorf("manifest round-trip lost fields: %+v", got)
	}
	for _, want := range []struct {
		name string
		mode os.FileMode
	}{
		{entryStore, 0o600},
		{"keys/session.key", 0o600},
		{entryConfig, 0o644},
	} {
		info, err := os.Stat(filepath.Join(out, filepath.FromSlash(want.name)))
		if err != nil {
			t.Errorf("%s: %v", want.name, err)
			continue
		}
		if perm := info.Mode().Perm(); perm != want.mode {
			t.Errorf("%s extracted with mode %04o, want %04o", want.name, perm, want.mode)
		}
	}
}

// TestArchive_WriteLeavesNoPartialFileOnFailure: an interrupted backup must
// not leave something that looks like an archive next to the good ones,
// where retention or an operator could later mistake it for one.
func TestArchive_WriteLeavesNoPartialFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "vnprox-backup-pve1-20260101-000000.tar.gz")
	m := goodManifest([]byte("body"))
	// The staging directory does not contain the declared entry, so
	// copyTarFile fails partway through.
	if _, err := Write(dest, m, filepath.Join(dir, "nonexistent-staging")); err == nil {
		t.Fatal("Write succeeded with a missing staged entry")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		t.Errorf("Write left %q behind after failing", e.Name())
	}
}

// TestValidEntryName is the name allowlist on its own, table-driven, since
// it is the single load-bearing function for traversal safety.
func TestValidEntryName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"store/vnprox.db", true},
		{"config/vnprox.toml", true},
		{"keys/session.key", true},
		{"readme.txt", true},
		{"a", true},
		{"", false},
		{".", false},
		{"..", false},
		{"../x", false},
		{"a/../b", false},
		{"a/..", false},
		{"/abs", false},
		{"a//b", false},
		{"a/", false},
		{"./a", false},
		{`a\b`, false},
		{"a\x00b", false},
		{"A/B", false},   // uppercase is outside the vocabulary
		{"-lead", false}, // an element may not start with punctuation
		{".hidden", false},
		{"a b", false},
		{strings.Repeat("a", 256), false},
		{"a$b", false},
		{"a\nb", false},
	}
	for _, tc := range cases {
		if got := validEntryName(tc.name); got != tc.ok {
			t.Errorf("validEntryName(%q) = %v, want %v", tc.name, got, tc.ok)
		}
	}
}

// TestArchive_InspectStopsAtTheBudgetRatherThanReadingEverything proves the
// bomb defence is a *streaming* bound, not a post-hoc check: a reader that
// consumed the whole 4 MiB and then complained would still have done the
// work an attacker wanted.
func TestArchive_InspectStopsAtTheBudgetRatherThanReadingEverything(t *testing.T) {
	bomb := bytes.Repeat([]byte{0}, 16<<20)
	data := rawArchive(t, goodManifest(bomb), []rawEntry{{name: entryStore, body: bomb}})

	counted := &countingReader{r: bytes.NewReader(data)}
	lim := Limits{MaxEntries: 256, MaxEntryBytes: 64 << 10, MaxTotalBytes: 64 << 10, MaxManifestBytes: 1 << 20}
	if _, err := Inspect(counted, lim); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Inspect error = %v, want ErrLimitExceeded", err)
	}
	// The manifest plus a tar header is a couple of KiB; the reader must
	// not have pulled the whole compressed bomb through.
	if counted.n > 64<<10 {
		t.Errorf("Inspect read %d compressed bytes before refusing; it should stop at the header that declares the over-budget size", counted.n)
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
