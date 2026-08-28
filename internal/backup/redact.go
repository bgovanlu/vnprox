// SPDX-License-Identifier: Apache-2.0

// redact.go is T-1902's redaction layer: the two mechanisms a support
// bundle uses to be safe *by construction* rather than by review.
//
// A bundle's collected material splits cleanly in two, and each half gets
// the mechanism that actually works for it:
//
//  1. **Structured, typed documents** (environment.json, store/summary.json,
//     probes.json, ...). These are Go structs whose every field is declared
//     in bundleschema.go and checked by reflection, so a field cannot be
//     added without a declaration. Nothing in this file applies to them
//     beyond Scrub on the handful of fields that carry free-form text.
//
//  2. **Free-form material vnprox does not own the shape of** — log lines,
//     an operator's changeset title, a validation error string, the JSON in
//     changesets.ops_json, /etc/network/interfaces option values, a remote
//     certificate's subject. A field allowlist is powerless here: the field
//     is declared, it is the *content* that is unbounded. These get the
//     redactors below.
//
// The rules themselves moved to internal/redact in T-2502, unchanged, so
// the PVE cassette recorder could apply the same vocabulary without
// internal/pve or internal/pvemock importing this package (backup -> host
// -> pvemock is an existing edge; the reverse would be a cycle). What
// remains here are the names this package and its tests already use,
// bound to that one implementation. Adding a term means editing
// internal/redact, and both callers get it in the same commit — which is
// the property that made a single vocabulary worth the move.

package backup

import (
	"encoding/json"

	"github.com/bgovanlu/vnprox/internal/redact"
)

// Redacted is the placeholder every redactor substitutes. See
// redact.Placeholder for why it is one fixed, bracketed, whitespace-free
// string.
const Redacted = redact.Placeholder

// Scrub removes credential-shaped substrings from free-form text.
//
// Used on every byte of every free-form thing a bundle carries: log lines,
// changeset titles, error strings, certificate subjects, and the string
// leaves of any JSON redactJSON walks. It is idempotent and never
// lengthens a line unboundedly.
func Scrub(s string) string { return redact.Scrub(s) }

// redactJSON parses raw as JSON and returns it re-encoded with every value
// under a secret-looking key replaced by Redacted and every remaining
// string leaf passed through Scrub. Unparsable input is replaced wholesale
// rather than passed through.
func redactJSON(raw []byte) json.RawMessage { return redact.JSON(raw) }

// redactedOptionValue applies the key-name rule to a single key/value pair
// from a format with no JSON structure — an interfaces(5) option line, a
// TOML key. Returns the value to emit and whether it was redacted.
func redactedOptionValue(key, value string) (string, bool) {
	return redact.OptionValue(key, value)
}
