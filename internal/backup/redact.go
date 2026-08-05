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
//     is declared, it is the *content* that is unbounded. These get the two
//     redactors below.
//
// The redactors are deliberately conservative in one direction only: they
// over-redact. A support bundle with a redacted MTU is a mild annoyance; a
// support bundle with a WireGuard private key in it is the failure this
// whole card exists to prevent. Every heuristic here therefore errs toward
// removing.

package backup

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Redacted is the placeholder every redactor substitutes. It is a single
// fixed string on purpose: a reader scanning a bundle for what was removed
// greps for one token, and a test asserting "the secret is gone" can also
// assert "and something says so", which distinguishes redaction from a
// collector that silently emitted nothing.
// It contains no whitespace and is bracketed, and the keyed-assignment
// pattern below deliberately excludes `[` and `]` from what it considers a
// value. That combination is what makes Scrub idempotent: a second pass
// over already-redacted text cannot match the marker as if it were the
// secret and replace it again. TestScrub asserts that on every row.
const Redacted = "[REDACTED-BY-VNPROXCTL-SUPPORT-BUNDLE]"

// secretTerms is the single vocabulary of "this key names a credential",
// shared by secretKeyPattern below and by the keyed-assignment rule further
// down. One list rather than two: they were two lists in the first draft,
// and TestBundle_AC1_ContainsNoSecretClass caught the consequence — a
// `session_key=` in a log line was not redacted because only one of them
// knew the term.
//
// Each entry is a *named class of key*, not the bare word "key". "key" on
// its own would redact `key_file=/etc/vnprox/keys/session.key`, and the
// path is frequently the whole diagnosis.
const secretTerms = `secret|password|passwd|passphrase|` +
	`token|ticket|credential|` +
	`priv(?:ate)?[_-]?key|preshared[_-]?key|psk|` +
	`session[_-]?key|signing[_-]?key|encryption[_-]?key|secret[_-]?key|` +
	`ssh[_-]?key|wg[_-]?key|auth[_-]?key|access[_-]?key|host[_-]?key|api[_-]?key|` +
	`kubeconfig|client[_-]?secret`

// secretKeyPattern matches a *key* name (a JSON object key, a TOML key, an
// interfaces(5) option name, or the left-hand side of a `key: value` in a
// log line) whose value must never leave this node.
//
// It is an allowlist inverted into a denylist on purpose, and that is worth
// being explicit about: for structured documents vnprox owns, the enforced
// allowlist in bundleschema.go is the control and this is belt-and-braces.
// This pattern is the *primary* control only for shapes vnprox does not own
// (a changeset op's params, an interfaces(5) file an operator hand-edited),
// where there is no finite field set to allowlist against.
var secretKeyPattern = regexp.MustCompile(`(?i)(` + secretTerms + `|` +
	`cred$|authorization|auth$|bearer|cookie|session_?id|` +
	`_enc$|_hash$` +
	`)`)

// valuePatterns are the shapes a secret takes when it appears in text that
// has no key at all — a log line, an error message, a URL. Each is written
// to match the *format* of a credential rather than any particular value,
// so it catches secrets this build has never seen.
var valuePatterns = []struct {
	name string
	re   *regexp.Regexp
	// repl is the replacement template. It keeps just enough of the
	// original to stay diagnostic (which realm, which token id) while
	// removing the part that authenticates.
	repl string
}{
	{
		// A PVE session ticket: `PVE:user@realm:HEXTIME::BASE64SIG`. The
		// whole thing goes, identity included: keeping the username would
		// be nicer to read, but the surrounding log line almost always
		// names the user anyway, and a partial match that leaves the
		// `PVE:` prefix behind gives the keyed-assignment rule below
		// something to chew on a second time.
		name: "pve-ticket",
		re:   regexp.MustCompile(`PVE:[^\s:]+:[0-9A-Fa-f]+::[A-Za-z0-9+/=]+`),
		repl: Redacted,
	},
	{
		// A PVE API token header value:
		// `PVEAPIToken=user@realm!tokenid=UUID`. Everything after the
		// scheme name goes. The token id is mildly useful and the UUID is
		// a credential, and the card's stated bias is to over-redact.
		name: "pve-api-token",
		re:   regexp.MustCompile(`(PVEAPIToken=)\S+`),
		repl: "${1}" + Redacted,
	},
	{
		// An HTTP Authorization header of any scheme.
		name: "authorization-header",
		re:   regexp.MustCompile(`(?i)(authorization\s*:\s*)(\S+\s+)?\S+`),
		repl: "${1}${2}" + Redacted,
	},
	{
		// URL userinfo: scheme://user:password@host. The password is the
		// secret; the rest is topology.
		name: "url-userinfo",
		re:   regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s/:@]+):[^\s/@]+@`),
		repl: "${1}:" + Redacted + "@",
	},
	{
		// `key = value` / `key: value` where the key looks like a secret.
		// Deliberately runs after the specific shapes above so those keep
		// their more informative replacements.
		name: "keyed-assignment",
		re: regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:` + secretTerms + `)[A-Za-z0-9_.-]*)` +
			// `[` and `]` are excluded from a "value" so this rule cannot
			// match the redaction marker itself; that is what makes Scrub
			// idempotent.
			`(\s*[:=]\s*)("?)([^\s"',;\[\]]+)("?)`),
		repl: "${1}${2}${3}" + Redacted + "${5}",
	},
	{
		// A bare base64-encoded 32-byte key. This is exactly the wire form
		// of a WireGuard private key, a preshared key, and vnprox's own
		// session key — 43 base64 characters and a single `=` of padding.
		// Matching the *shape* is what makes this catch a key pasted into a
		// log line by code that has not been written yet.
		name: "base64-32-byte-key",
		re:   regexp.MustCompile(`\b[A-Za-z0-9+/]{43}=`),
		repl: Redacted,
	},
	{
		// A PEM private key block, however it was line-wrapped.
		name: "pem-private-key",
		re:   regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
		repl: Redacted,
	},
}

// Scrub removes credential-shaped substrings from free-form text.
//
// Used on every byte of every free-form thing a bundle carries: log lines,
// changeset titles, error strings, certificate subjects, and the string
// leaves of any JSON redactJSON walks. It is idempotent and never
// lengthens a line unboundedly.
func Scrub(s string) string {
	if s == "" {
		return s
	}
	for _, p := range valuePatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// redactJSON parses raw as JSON and returns it re-encoded with every value
// under a secret-looking key replaced by Redacted and every remaining
// string leaf passed through Scrub.
//
// This is what makes it safe to put a changeset's ops, plan, apply log and
// findings into a bundle. Those are `json.RawMessage` columns holding op
// parameters whose shape is defined by internal/change and grows every
// phase — internal/api's redactOpSecrets strips exactly one known field
// (WgPeerAddParams' preshared key) from exactly one known op type, which is
// the right tool for an API response typed as []change.Op and the wrong one
// here: a bundle must be safe against the op type that lands next month.
// Walking the JSON by key name is type-agnostic, so a new sealed field
// called anything matching secretKeyPattern is redacted the day it appears
// rather than the day someone remembers to update a list.
//
// Input that is not valid JSON is not passed through: it is replaced
// wholesale, because "I could not parse it" and "I know it is safe" are not
// the same statement.
func redactJSON(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return json.RawMessage(mustJSON(Redacted + " (unparsable JSON)"))
	}
	out, err := json.Marshal(redactValue(v))
	if err != nil {
		return json.RawMessage(mustJSON(Redacted + " (unencodable after redaction)"))
	}
	return out
}

// redactValue is redactJSON's recursive walk.
func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic output; encoding/json sorts too
		for _, k := range keys {
			if secretKeyPattern.MatchString(k) {
				out[k] = Redacted
				continue
			}
			out[k] = redactValue(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = redactValue(t[i])
		}
		return out
	case string:
		return Scrub(t)
	default:
		// Numbers (json.Number), booleans and null pass through: none of
		// them can carry a credential, and a redacted MTU helps nobody.
		return v
	}
}

// redactedOptionValue applies the key-name rule to a single key/value pair
// from a format with no JSON structure — an interfaces(5) option line, a
// TOML key. Returns the value to emit and whether it was redacted.
func redactedOptionValue(key, value string) (string, bool) {
	if secretKeyPattern.MatchString(key) {
		return Redacted, true
	}
	scrubbed := Scrub(value)
	return scrubbed, scrubbed != value
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// v is always a string here; Marshal of a string cannot fail.
		return []byte(`"<redacted>"`)
	}
	return b
}
