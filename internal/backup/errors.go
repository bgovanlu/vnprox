package backup

import "errors"

var (
	// ErrUnsupportedFormat: the archive announces a format version or kind
	// this build does not read.
	ErrUnsupportedFormat = errors.New("backup: unsupported archive format")

	// ErrMalformedArchive: structurally wrong — no manifest, manifest not
	// first, an entry the manifest never declared, entries out of declared
	// order, a duplicate name, a non-regular file (symlink, hardlink,
	// device, directory), or trailing data after the last declared entry.
	ErrMalformedArchive = errors.New("backup: malformed archive")

	// ErrUnsafeEntryName: an entry name that is not in the archive's
	// allowlisted name vocabulary — absolute, traversing (`..`), backslashed
	// or otherwise capable of naming a location outside the extraction
	// directory.
	ErrUnsafeEntryName = errors.New("backup: unsafe archive entry name")

	// ErrLimitExceeded: the archive exceeds one of the reader's budgets —
	// entry count, per-entry bytes, total bytes, or manifest bytes. This is
	// the decompression-bomb defence: the budget is absolute and enforced
	// while streaming, so an archive that expands without bound is cut off
	// at the budget rather than after it has filled the disk.
	ErrLimitExceeded = errors.New("backup: archive exceeds a size limit")

	// ErrTruncatedArchive: the stream ended inside the gzip container, the
	// tar structure, or an entry body.
	ErrTruncatedArchive = errors.New("backup: archive is truncated")

	// ErrDigestMismatch: an entry's bytes do not hash to the digest its
	// manifest declared. Covers both corruption and tampering with the
	// entry bodies.
	ErrDigestMismatch = errors.New("backup: archive entry does not match its declared digest")

	// ErrDaemonRunning: a restore was attempted against a store a live
	// vnproxd owns.
	ErrDaemonRunning = errors.New("backup: a vnprox daemon is running against this store")

	// ErrSchemaDowngrade: the archive's store is at a schema version newer
	// than this build understands. Forward migration is supported;
	// downgrade is not, and is refused before the target store is touched.
	ErrSchemaDowngrade = errors.New("backup: archive schema is newer than this build supports")

	// ErrSchemaMismatch: the manifest's declared schema version disagrees
	// with the store actually inside the archive. Either the manifest was
	// edited or the store was swapped; both are refusals.
	ErrSchemaMismatch = errors.New("backup: archive manifest disagrees with the store it contains")

	// ErrWrongKind: restore was pointed at an archive that is not a backup
	// (e.g. a T-1902 support bundle, which is deliberately redacted and
	// would restore an incomplete store).
	ErrWrongKind = errors.New("backup: archive is not a vnprox backup")
)
