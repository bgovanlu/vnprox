// SPDX-License-Identifier: Apache-2.0

package main

// Exit codes for every vnproxctl command (T-1105). This table is a stable,
// documented CI contract — docs/deployment.md's "Troubleshooting quick
// refs" and this file are the two places it is written down; a script
// branching on `vnproxctl`'s exit status should never need to guess.
//
//	0  ExitSuccess      command completed with nothing left to do (or, for
//	                    `apply --plan`, the spec matches live exactly)
//	1  ExitError        generic/unexpected failure (a 404/5xx from the
//	                    daemon, a decode error, an internal bug) — the
//	                    catch-all bucket every other code is carved out of
//	2  ExitUsage        bad flags/arguments, or (`remote` family) no
//	                    --token/VNPROX_TOKEN supplied — checked before any
//	                    daemon call is attempted, matching the pre-existing
//	                    `status`/`snapshots`/`rollback-now` convention of
//	                    returning 2 for a usage problem
//	3  ExitPending      the daemon returned `422 validation_failed` (a
//	                    changeset has blocking findings), OR `apply --plan`
//	                    found a non-empty diff ("changes pending" in the
//	                    Terraform-plan sense) — one code for both, since
//	                    both mean "this would change something / can't
//	                    proceed automatically, go look"
//	4  ExitAuth         `401 not_authenticated` (missing/invalid/revoked
//	                    token) or `403 forbidden` (token lacks the required
//	                    scope) from the daemon
//	5  ExitNetwork      the daemon could not be reached at all (connection
//	                    refused, DNS failure, TLS handshake failure, request
//	                    timeout) — distinguished from ExitError because a CI
//	                    pipeline usually wants to retry a 5 differently than
//	                    a 1
//	6  ExitApplyTimeout `apply --apply`'s changeset did not reach
//	                    `committed` within --timeout (still stuck in
//	                    `awaiting_confirm`/`applying`, or the daemon simply
//	                    never got there) — distinct from ExitNetwork/
//	                    ExitError because "stuck open" needs a different CI
//	                    response (investigate the changeset) than either
//	                    "couldn't dial" or "the API errored"
//
// The three pre-existing daemon-independent commands (`status`,
// `snapshots`, `rollback-now`, T-206) are UNCHANGED by this table: they keep
// their own long-standing 0/1/2 convention (0 ok, 1 operation failed, 2 bad
// usage) exactly as before — this table's codes 3-6 are additions that only
// the new `remote`/`apply` command family (and their -o json retrofit) ever
// return. A caller scripting against the pre-existing three commands sees no
// behavior change at all.
const (
	ExitSuccess      = 0
	ExitError        = 1
	ExitUsage        = 2
	ExitPending      = 3
	ExitAuth         = 4
	ExitNetwork      = 5
	ExitApplyTimeout = 6
)
