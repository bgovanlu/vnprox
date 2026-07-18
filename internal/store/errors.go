package store

import "errors"

var (
	// ErrSchemaTooNew is returned by Open when the database's stored schema
	// version is higher than the version this build of vnproxd understands.
	// This happens when a newer vnproxd version has touched the database and
	// an older binary is later pointed at the same file (e.g. a rollback of
	// the package); opening it would risk silently misinterpreting data the
	// older code doesn't have migrations for, so Open refuses outright.
	ErrSchemaTooNew = errors.New("store: database schema is newer than this build supports")

	// ErrNotFound is returned by repository Get methods when no row matches
	// the requested key.
	ErrNotFound = errors.New("store: not found")

	// ErrInvalidKey marks a malformed or wrong-length encryption key passed
	// to the session-secret cipher helpers.
	ErrInvalidKey = errors.New("store: invalid encryption key")

	// ErrIllegalState is returned by a repository mutation that requires the
	// existing row to be in a particular state (e.g. ChangeScheduleRepo.
	// Cancel requiring status = pending) when it is not — the row exists
	// (ErrNotFound does not apply) but is no longer eligible for the
	// requested transition.
	ErrIllegalState = errors.New("store: row is not in a state that allows this operation")
)
