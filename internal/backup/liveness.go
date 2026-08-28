// SPDX-License-Identifier: Apache-2.0

// liveness.go answers one question: is a vnprox daemon running against the
// store this restore is about to replace?
//
// Getting a false "no" here is the dangerous direction. A restore that
// swaps the database file out from under a live daemon leaves that daemon
// holding open file descriptors onto a store that is no longer the one on
// disk: its in-flight commit-confirm timers, its WAL, and the restored
// content diverge silently, and the first write checkpoints a mixture of
// the two. So the check uses two independent signals and refuses if
// *either* fires.
//
//  1. internal/store's runtime lock. A daemon built with T-1901 or later
//     holds an advisory flock on <db>.lock for its whole lifetime. This is
//     precise (it names the exact store) and cannot go stale, because the
//     kernel drops the lock when the holder dies.
//
//  2. The configured listen address. A daemon built *before* T-1901 takes
//     no lock at all, and that is exactly the daemon most likely to be
//     running when someone reaches for a restore. Attempting to bind
//     [server] listen catches it: Go's listeners set SO_REUSEADDR but not
//     SO_REUSEPORT, so a second bind of an address a live daemon holds
//     fails with EADDRINUSE.
//
// Signal 2 also fires for a *different* service on that port (PBS on 8007,
// the collision docs/deployment.md already documents). That is the correct
// trade: a restore that is refused because something unexpected owns the
// port costs an operator one diagnostic message, and the message names the
// address so it is a two-second diagnosis. The opposite error costs them
// the store.

package backup

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"

	"github.com/bgovanlu/vnprox/internal/store"
)

// LivenessCheck reports whether a daemon owns dbPath. It returns a non-nil
// error wrapping ErrDaemonRunning when one does.
type LivenessCheck func() error

// DaemonLiveness builds the production liveness check for a store and a
// listen address. listen may be empty (no config available), in which case
// only the runtime lock is consulted and the caller is told so.
func DaemonLiveness(dbPath, listen string) LivenessCheck {
	return func() error {
		held, err := store.RuntimeLockHeld(dbPath)
		if err != nil {
			// An unreadable lock file is not proof of anything, but it is
			// also not proof of absence. Refuse rather than guess: this is
			// the fail-closed direction.
			return fmt.Errorf("%w: cannot determine whether a daemon holds %s: %v",
				ErrDaemonRunning, store.RuntimeLockPath(dbPath), err)
		}
		if held {
			return fmt.Errorf("%w: another process holds the runtime lock %s (stop it with `systemctl stop vnprox` and retry)",
				ErrDaemonRunning, store.RuntimeLockPath(dbPath))
		}
		if listen == "" {
			return nil
		}
		if bound, err := addrInUse(listen); err != nil {
			return fmt.Errorf("%w: cannot determine whether %s is in use: %v", ErrDaemonRunning, listen, err)
		} else if bound {
			return fmt.Errorf("%w: something is listening on %s (stop it with `systemctl stop vnprox` and retry; if that is not vnprox, pass --config for the right node)",
				ErrDaemonRunning, listen)
		}
		return nil
	}
}

// addrInUse reports whether addr is already bound.
func addrInUse(addr string) (bool, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		if closeErr := ln.Close(); closeErr != nil {
			return false, closeErr
		}
		return false, nil
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true, nil
	}
	// EADDRNOTAVAIL means the address does not exist on this host — a
	// config that names another node's IP, say. That is not "in use", and
	// it is not an error worth refusing a restore over.
	if errors.Is(err, syscall.EADDRNOTAVAIL) {
		return false, nil
	}
	// modernc/netpoll wrap syscall errors variously across platforms;
	// string-matching is the fallback, not the primary test.
	if strings.Contains(strings.ToLower(err.Error()), "address already in use") {
		return true, nil
	}
	return false, err
}
