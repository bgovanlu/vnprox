// archive.go is the archive container: a gzip-compressed tar whose first
// entry is always the manifest, followed by exactly the files that manifest
// declares, in that order.
//
// The reader treats the archive as hostile input, because on restore it is:
// an operator restores whatever file they were handed, and the extraction
// target is a root-owned directory on a hypervisor. The properties it
// enforces, all of them before any byte reaches disk:
//
//   - Entry names come from an allowlisted vocabulary (manifest.go's
//     validEntryName), so traversal is not expressible rather than being
//     filtered out.
//   - Only regular files exist. Symlinks, hardlinks, directories, devices
//     and FIFOs are refused outright — a symlink entry is the classic way
//     to make a later "safe" entry name land outside the target.
//   - Every read is bounded: manifest bytes, per-entry bytes, total bytes
//     and entry count all have absolute budgets, enforced while streaming.
//     A gzip stream that expands without bound hits the budget and stops.
//   - Every entry must be declared by the manifest, in the manifest's own
//     order, with the manifest's size — checked from the tar header before
//     the body is read — and must hash to the manifest's digest.
//   - The stream must end exactly where the manifest says it does. Trailing
//     entries are a refusal, not something to ignore.
//
// Inspect() runs all of the above writing nothing at all; Extract() runs
// the identical walk with a real sink. Restore calls Inspect first and
// Extract second, which is what makes "rejected before anything touches
// disk" literally true rather than approximately true.

package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Limits are the reader's absolute budgets.
//
// Absolute byte budgets rather than a compression-ratio heuristic: ratio
// checks are brittle (a store full of zero pages legitimately compresses
// enormously) and can be tuned around by an attacker who pads. A hard
// ceiling on what will ever be written cannot be.
type Limits struct {
	MaxEntries       int
	MaxEntryBytes    int64
	MaxTotalBytes    int64
	MaxManifestBytes int64
}

// DefaultLimits are sized for a real vnprox install with headroom: a store
// on a busy cluster is tens to low hundreds of MiB (audit rows, snapshots,
// flow samples), so 4 GiB per entry and 8 GiB total leave a wide margin
// while still bounding an adversarial archive to something a hypervisor's
// root filesystem survives.
func DefaultLimits() Limits {
	return Limits{
		MaxEntries:       256,
		MaxEntryBytes:    4 << 30,
		MaxTotalBytes:    8 << 30,
		MaxManifestBytes: 1 << 20,
	}
}

// ---------------------------------------------------------------- writing

// Write serialises manifest + the staged files it declares into dest.
//
// dest is created 0600 and is written through a temporary sibling that is
// renamed into place at the end, so an interrupted backup never leaves a
// truncated file that looks like an archive.
//
// srcDir is the staging directory: every manifest entry's Name is resolved
// relative to it. Entries are written in manifest order, which is what the
// reader enforces.
func Write(dest string, m Manifest, srcDir string) (int64, error) {
	if err := m.validate(DefaultLimits()); err != nil {
		return 0, fmt.Errorf("backup: refusing to write an archive with an invalid manifest: %w", err)
	}

	tmp := dest + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("backup: creating %s: %w", tmp, err)
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	manifestBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		cleanup()
		return 0, fmt.Errorf("backup: encoding manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	if manifestErr := writeTarFile(tw, ManifestName, 0o600, manifestBytes); manifestErr != nil {
		cleanup()
		return 0, manifestErr
	}

	for _, e := range m.Entries {
		src := filepath.Join(srcDir, filepath.FromSlash(e.Name))
		if copyErr := copyTarFile(tw, e, src); copyErr != nil {
			cleanup()
			return 0, copyErr
		}
	}

	if tarErr := tw.Close(); tarErr != nil {
		cleanup()
		return 0, fmt.Errorf("backup: finalizing tar: %w", tarErr)
	}
	if gzErr := gz.Close(); gzErr != nil {
		cleanup()
		return 0, fmt.Errorf("backup: finalizing gzip: %w", gzErr)
	}
	if syncErr := f.Sync(); syncErr != nil {
		cleanup()
		return 0, fmt.Errorf("backup: syncing %s: %w", tmp, syncErr)
	}
	size, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		cleanup()
		return 0, fmt.Errorf("backup: sizing %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("backup: closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("backup: publishing %s: %w", dest, err)
	}
	return size, nil
}

func writeTarFile(tw *tar.Writer, name string, mode int64, data []byte) error {
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Mode:     mode,
		Size:     int64(len(data)),
		// A fixed epoch rather than time.Now(): the timestamps that matter
		// are in the manifest, and a per-entry mtime only adds a second,
		// unverified source of "when was this taken".
		ModTime: time.Unix(0, 0).UTC(),
		Format:  tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: writing tar header for %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("backup: writing %s: %w", name, err)
	}
	return nil
}

func copyTarFile(tw *tar.Writer, e Entry, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("backup: reading staged entry %s: %w", e.Name, err)
	}
	defer func() { _ = f.Close() }()

	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     e.Name,
		Mode:     int64(e.Mode),
		Size:     e.Size,
		ModTime:  time.Unix(0, 0).UTC(),
		Format:   tar.FormatPAX,
	}
	if hdrErr := tw.WriteHeader(hdr); hdrErr != nil {
		return fmt.Errorf("backup: writing tar header for %s: %w", e.Name, hdrErr)
	}
	n, err := io.Copy(tw, f)
	if err != nil {
		return fmt.Errorf("backup: writing %s: %w", e.Name, err)
	}
	if n != e.Size {
		return fmt.Errorf("backup: staged entry %s changed size while being archived (%d != %d)", e.Name, n, e.Size)
	}
	return nil
}

// ---------------------------------------------------------------- reading

// sink receives one validated entry's bytes. Inspect's sink discards;
// Extract's writes a file. Both get the same validation because both go
// through walk.
type sink func(e Entry) (io.WriteCloser, error)

// discardSink satisfies sink without touching the filesystem.
func discardSink(Entry) (io.WriteCloser, error) {
	return nopWriteCloser{io.Discard}, nil
}

type nopWriteCloser struct{ w io.Writer }

func (n nopWriteCloser) Write(p []byte) (int, error) { return n.w.Write(p) }
func (nopWriteCloser) Close() error                  { return nil }

// Inspect fully validates the archive read from r and returns its manifest,
// writing nothing anywhere. This is what `restore --dry-run` reports and
// what Restore runs before it creates so much as a staging directory.
func Inspect(r io.Reader, lim Limits) (*Manifest, error) {
	return walk(r, lim, discardSink)
}

// Extract validates the archive read from r exactly as Inspect does and
// writes its entries into destDir, which must already exist and should be
// private to this operation (Restore creates it 0700).
//
// Nothing is written outside destDir: every entry name has already been
// checked against the allowlist, the join is re-verified against destDir
// afterwards as a second, independent barrier, and no entry type that could
// redirect a write (symlink, hardlink) is accepted at all.
func Extract(r io.Reader, destDir string, lim Limits) (*Manifest, error) {
	abs, err := filepath.Abs(destDir)
	if err != nil {
		return nil, fmt.Errorf("backup: resolving extraction directory %s: %w", destDir, err)
	}
	return walk(r, lim, func(e Entry) (io.WriteCloser, error) {
		target := filepath.Join(abs, filepath.FromSlash(e.Name))
		// Belt and braces: validEntryName already makes escape
		// inexpressible, but a containment check here means a future
		// loosening of the name vocabulary cannot silently become a
		// traversal bug.
		rel, relErr := filepath.Rel(abs, target)
		if relErr != nil || rel == ".." || filepath.IsAbs(rel) ||
			(len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%w: entry %q resolves outside the extraction directory", ErrUnsafeEntryName, e.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, fmt.Errorf("backup: creating directory for %s: %w", e.Name, err)
		}
		// O_EXCL: an entry never overwrites anything, including another
		// entry, so a duplicate name that somehow got past the manifest
		// check still cannot clobber.
		f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(e.Mode).Perm())
		if err != nil {
			return nil, fmt.Errorf("backup: creating %s: %w", target, err)
		}
		return f, nil
	})
}

// the archive's safety properties across functions that must stay in
// lockstep with each other.
//
//nolint:gocyclo // one linear validation pass; splitting it would scatter
func walk(r io.Reader, lim Limits, out sink) (*Manifest, error) {
	// The outermost bound: whatever the gzip stream expands to, the reader
	// will not consume more than this. +1 so hitting the budget exactly is
	// distinguishable from exceeding it.
	bounded := io.LimitReader(r, lim.MaxTotalBytes+lim.MaxManifestBytes+1)

	gz, err := gzip.NewReader(bounded)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("%w: gzip stream ends before its header: %v", ErrTruncatedArchive, err)
		}
		return nil, fmt.Errorf("%w: not a gzip stream: %v", ErrMalformedArchive, err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)

	// --- entry 0 must be the manifest -----------------------------------
	hdr, err := tr.Next()
	if err != nil {
		return nil, wrapStreamErr(err, "reading the archive's first entry")
	}
	if hdr.Name != ManifestName {
		return nil, fmt.Errorf("%w: first entry is %q, expected %s", ErrMalformedArchive, hdr.Name, ManifestName)
	}
	if hdr.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrMalformedArchive, ManifestName)
	}
	if hdr.Size > lim.MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest is %d bytes, limit is %d", ErrLimitExceeded, hdr.Size, lim.MaxManifestBytes)
	}
	manifestBytes, err := io.ReadAll(io.LimitReader(tr, lim.MaxManifestBytes+1))
	if err != nil {
		return nil, wrapStreamErr(err, "reading the manifest")
	}
	if int64(len(manifestBytes)) > lim.MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest exceeds %d bytes", ErrLimitExceeded, lim.MaxManifestBytes)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(manifestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%w: decoding manifest: %v", ErrMalformedArchive, err)
	}
	if err := m.validate(lim); err != nil {
		return nil, err
	}

	// --- then exactly the declared entries, in order ---------------------
	var total int64
	for i, want := range m.Entries {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("%w: manifest declares %d entries, archive ends after %d", ErrTruncatedArchive, len(m.Entries), i)
			}
			return nil, wrapStreamErr(err, fmt.Sprintf("reading entry %d", i))
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("%w: entry %q is a %s, only regular files are allowed",
				ErrMalformedArchive, hdr.Name, typeflagName(hdr.Typeflag))
		}
		if hdr.Name != want.Name {
			return nil, fmt.Errorf("%w: entry %d is %q, manifest declares %q", ErrMalformedArchive, i, hdr.Name, want.Name)
		}
		// Re-check the name from the *header* as well as from the manifest:
		// they are supposed to be equal, and the check above enforces that,
		// but validating the value that actually names the file on disk is
		// the one that matters.
		if !validEntryName(hdr.Name) {
			return nil, fmt.Errorf("%w: entry %d name %q", ErrUnsafeEntryName, i, hdr.Name)
		}
		// Size is checked from the header, BEFORE the body is read: this is
		// where an over-large entry is stopped, not after it has been
		// streamed somewhere.
		if hdr.Size != want.Size {
			return nil, fmt.Errorf("%w: entry %q is %d bytes, manifest declares %d", ErrMalformedArchive, hdr.Name, hdr.Size, want.Size)
		}
		if hdr.Size > lim.MaxEntryBytes {
			return nil, fmt.Errorf("%w: entry %q is %d bytes, per-entry limit is %d", ErrLimitExceeded, hdr.Name, hdr.Size, lim.MaxEntryBytes)
		}
		total += hdr.Size
		if total > lim.MaxTotalBytes {
			return nil, fmt.Errorf("%w: archive exceeds the %d byte total limit at entry %q", ErrLimitExceeded, lim.MaxTotalBytes, hdr.Name)
		}

		w, err := out(want)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(w, h), io.LimitReader(tr, want.Size+1))
		closeErr := w.Close()
		if copyErr != nil {
			return nil, wrapStreamErr(copyErr, fmt.Sprintf("reading entry %q", hdr.Name))
		}
		if closeErr != nil {
			return nil, fmt.Errorf("backup: writing entry %q: %w", hdr.Name, closeErr)
		}
		if n != want.Size {
			return nil, fmt.Errorf("%w: entry %q ended after %d of %d bytes", ErrTruncatedArchive, hdr.Name, n, want.Size)
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != want.SHA256 {
			return nil, fmt.Errorf("%w: entry %q hashes to %s, manifest declares %s", ErrDigestMismatch, hdr.Name, got, want.SHA256)
		}
	}

	// --- and nothing else ------------------------------------------------
	if extra, err := tr.Next(); err == nil {
		return nil, fmt.Errorf("%w: archive carries %q, which the manifest does not declare", ErrMalformedArchive, extra.Name)
	} else if !errors.Is(err, io.EOF) {
		return nil, wrapStreamErr(err, "reading past the last declared entry")
	}

	// tar's end-of-archive marker comes before gzip's trailer, so reaching
	// the end of the tar stream does NOT verify the gzip CRC32. Drain the
	// remainder so it is checked: without this, an archive whose bytes were
	// corrupted or tampered with in a region tar happens not to read is
	// accepted with every other check passing. (Verified by
	// TestArchive_AC5_MaliciousArchivesAreRejected's "corrupt gzip
	// checksum" row, which passes validation without this drain.)
	if _, err := io.Copy(io.Discard, io.LimitReader(gz, lim.MaxTotalBytes+1)); err != nil {
		return nil, wrapStreamErr(err, "verifying the archive's checksum")
	}
	return &m, nil
}

// wrapStreamErr maps the stream-level failures of gzip/tar onto this
// package's sentinels, so a truncated archive is reported as truncated
// rather than as a generic I/O error an operator cannot act on.
func wrapStreamErr(err error, what string) error {
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return fmt.Errorf("%w: %s: %v", ErrTruncatedArchive, what, err)
	case errors.Is(err, gzip.ErrChecksum), errors.Is(err, gzip.ErrHeader):
		return fmt.Errorf("%w: %s: %v", ErrMalformedArchive, what, err)
	case errors.Is(err, tar.ErrHeader):
		return fmt.Errorf("%w: %s: %v", ErrMalformedArchive, what, err)
	default:
		return fmt.Errorf("%w: %s: %v", ErrMalformedArchive, what, err)
	}
}

func typeflagName(t byte) string {
	switch t {
	case tar.TypeSymlink:
		return "symlink"
	case tar.TypeLink:
		return "hard link"
	case tar.TypeDir:
		return "directory"
	case tar.TypeChar:
		return "character device"
	case tar.TypeBlock:
		return "block device"
	case tar.TypeFifo:
		return "FIFO"
	default:
		return fmt.Sprintf("tar type %q", t)
	}
}
