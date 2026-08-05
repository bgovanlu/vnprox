// runtimelock.go gives a running vnproxd a way to *advertise* that it owns
// a particular store file, so `vnproxctl restore` can refuse to swap that
// file out from under it (T-1901: "a restore is a mutation of the daemon's
// own authoritative state and must refuse to run against a live daemon").
//
// Why an flock and not something else:
//
//   - A pidfile can go stale (a killed daemon leaves one behind) and then
//     permanently blocks a legitimate recovery — exactly the wrong failure
//     direction for a disaster-recovery command.
//   - `systemctl is-active` is neither available in a container nor
//     trustworthy for this question: vnprox.service is Type=simple, so it
//     reports active before the process has even opened the store
//     (packaging/test/upgrade-service.sh, T-1807).
//   - SQLite's own locking says nothing useful here: WAL mode is designed
//     to let a second process read and write concurrently, so "can I write
//     to this database" is true even with the daemon running.
//
// An advisory whole-file flock is released by the kernel when the holding
// process dies, however it dies, so it cannot go stale; and it is held for
// the daemon's whole lifetime, so it cannot be missed by sampling at the
// wrong moment.
//
// Acquisition is deliberately non-fatal for the daemon (cmd/vnproxd logs a
// warning and carries on if it cannot take the lock): this file exists to
// make a *restore* safe, and must not become a new way for the daemon to
// refuse to start.

package store

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// runtimeLockPerm matches dbFilePerm: the lock file sits next to the store
// in /var/lib/vnprox and should be no more readable than the store is.
const runtimeLockPerm = 0o600

// RuntimeLock is an advisory exclusive lock on a store's lock file, held
// for the lifetime of the process that acquired it.
type RuntimeLock struct {
	f    *os.File
	path string
}

// RuntimeLockPath returns the lock file path for a given store path. It is
// a sibling of the database (same directory, `.lock` suffix) so it inherits
// the store directory's own permissions and lifecycle — `apt purge` removes
// /var/lib/vnprox wholesale and takes this with it.
func RuntimeLockPath(dbPath string) string { return dbPath + ".lock" }

// ErrRuntimeLockHeld is returned by AcquireRuntimeLock when another process
// already holds the lock for this store — in practice, a running vnproxd.
var ErrRuntimeLockHeld = errors.New("store: runtime lock is held by another process")

// AcquireRuntimeLock takes the advisory exclusive lock for dbPath. It
// returns ErrRuntimeLockHeld if another live process holds it.
//
// The returned lock must be kept alive (not garbage collected / not
// closed) for as long as the caller wants to be seen as running; Release
// drops it.
func AcquireRuntimeLock(dbPath string) (*RuntimeLock, error) {
	path := RuntimeLockPath(dbPath)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, runtimeLockPerm)
	if err != nil {
		return nil, fmt.Errorf("store: opening runtime lock %s: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", ErrRuntimeLockHeld, path)
		}
		return nil, fmt.Errorf("store: locking %s: %w", path, err)
	}
	return &RuntimeLock{f: f, path: path}, nil
}

// Release drops the lock. The lock file itself is left on disk: unlinking
// it would race another process that has already opened it, and an empty
// 0600 file costs nothing.
func (l *RuntimeLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	// Closing the descriptor releases the flock; do both, and report the
	// close error since that is the one that can actually fail.
	_ = unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("store: releasing runtime lock %s: %w", l.path, err)
	}
	l.f = nil
	return nil
}

// Path reports the lock file this lock is held on.
func (l *RuntimeLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// RuntimeLockHeld reports whether some *other* process currently holds the
// runtime lock for dbPath — i.e. whether a vnproxd is running against this
// store.
//
// A missing lock file means no daemon has ever run against this store with
// a build new enough to take the lock; that is reported as "not held"
// rather than being created as a side effect of asking. Callers that need
// to cover an older daemon binary (which takes no lock at all) must not
// rely on this signal alone — see internal/backup's liveness check, which
// also probes the configured listen address for exactly that reason.
func RuntimeLockHeld(dbPath string) (bool, error) {
	path := RuntimeLockPath(dbPath)
	f, err := os.OpenFile(path, os.O_RDWR, runtimeLockPerm)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("store: checking runtime lock %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return true, nil
		}
		return false, fmt.Errorf("store: checking runtime lock %s: %w", path, err)
	}
	// We got it, so nobody else has it. Drop it again immediately — this
	// is a probe, not an acquisition.
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return false, nil
}
