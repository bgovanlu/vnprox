package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// BlobRepo is the content-addressed, zstd-compressed blob store backing
// snapshot file content (the `blobs` table, docs/data-model.md §2). Two
// snapshots that captured byte-identical file content share one row here —
// this is the "dedup" T-206's card requires, keyed on the content's sha256.
type BlobRepo struct {
	db *DB
}

// NewBlobRepo constructs a BlobRepo.
func NewBlobRepo(db *DB) *BlobRepo { return &BlobRepo{db: db} }

// Put compresses and stores plaintext under its sha256 hex digest,
// returning that digest. If a blob with the same hash already exists, this
// is a cheap no-op (INSERT OR IGNORE) — the whole point of content
// addressing: storage cost is paid once per distinct byte sequence, no
// matter how many snapshots reference it.
func (r *BlobRepo) Put(ctx context.Context, plaintext string) (string, error) {
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])

	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		return "", fmt.Errorf("store: creating zstd writer for blob %s: %w", hash, err)
	}
	if _, err = zw.Write([]byte(plaintext)); err != nil {
		_ = zw.Close()
		return "", fmt.Errorf("store: compressing blob %s: %w", hash, err)
	}
	if err = zw.Close(); err != nil {
		return "", fmt.Errorf("store: closing zstd writer for blob %s: %w", hash, err)
	}

	_, err = r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO blobs (sha256, content_zstd, size) VALUES (?, ?, ?)
		ON CONFLICT (sha256) DO NOTHING`,
		hash, buf.Bytes(), len(plaintext),
	)
	if err != nil {
		return "", fmt.Errorf("store: storing blob %s: %w", hash, err)
	}
	return hash, nil
}

// Get decompresses and returns the plaintext content for the blob with the
// given sha256 hex digest, or ErrNotFound.
func (r *BlobRepo) Get(ctx context.Context, hash string) (string, error) {
	var compressed []byte
	err := r.db.sqlDB.QueryRowContext(ctx, `SELECT content_zstd FROM blobs WHERE sha256 = ?`, hash).Scan(&compressed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: reading blob %s: %w", hash, err)
	}

	zr, err := zstd.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", fmt.Errorf("store: creating zstd reader for blob %s: %w", hash, err)
	}
	defer zr.Close()
	plaintext, err := io.ReadAll(zr)
	if err != nil {
		return "", fmt.Errorf("store: decompressing blob %s: %w", hash, err)
	}
	return string(plaintext), nil
}

// PruneOrphans deletes every blob no longer referenced by any snapshot_files
// row, returning the number of rows deleted. Called after Prune removes
// expired snapshot rows (and their snapshot_files) so storage is actually
// reclaimed, not just the index rows.
func (r *BlobRepo) PruneOrphans(ctx context.Context) (int64, error) {
	res, err := r.db.sqlDB.ExecContext(ctx, `
		DELETE FROM blobs WHERE sha256 NOT IN (SELECT DISTINCT sha256 FROM snapshot_files)`,
	)
	if err != nil {
		return 0, fmt.Errorf("store: pruning orphaned blobs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: reading orphaned-blob prune count: %w", err)
	}
	return n, nil
}
