// SPDX-License-Identifier: Apache-2.0

package redact

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Finding is one place a scan decided a credential lives.
//
// It exists because T-2502's cassette recorder needs a different verdict
// from the same rules than a support bundle does. A bundle asks "make this
// safe"; a recorder asks "is this safe" and, when the answer is no, refuses
// to write the file and says which field made it refuse. Substituting
// Placeholder into a cassette instead would produce a fixture that no
// longer describes what PVE returned — the "fixture invented the shape the
// code expected" defect class this card exists to eliminate, arrived at by
// a different road.
type Finding struct {
	// Field is the JSON path to the offending value, rooted at whatever
	// name the caller passed in (e.g. "body.data.ticket",
	// "body.data[0].comments"). For material that is not JSON at all it is
	// the caller's root name unchanged.
	Field string
	// Rule is the stable identifier of the rule that fired:
	// "secret-key-name" for the key-name vocabulary, or one of the value
	// shapes ("pve-ticket", "pem-private-key", "base64-32-byte-key", ...).
	Rule string
}

func (f Finding) String() string {
	return fmt.Sprintf("field %q matches secret rule %q", f.Field, f.Rule)
}

// SecretKeyRule is the Rule value reported when a key *name* — rather than
// the shape of its value — is what made the scan fire.
const SecretKeyRule = "secret-key-name"

// ScanText reports every value-shape rule that fires anywhere in s,
// attributed to field. An empty result means no rule matched; it is never
// a claim that s is meaningless, only that none of the known credential
// shapes appear in it.
func ScanText(field, s string) []Finding {
	if s == "" {
		return nil
	}
	var out []Finding
	for _, p := range valuePatterns {
		if p.re.MatchString(s) {
			out = append(out, Finding{Field: field, Rule: p.name})
		}
	}
	return out
}

// ScanJSON walks raw as JSON and reports every credential the same rules
// Scrub and JSON use would have replaced, without replacing anything.
//
// root names the document for the purposes of the reported paths — pass
// "body" and a ticket under {"data":{"ticket":...}} is reported as
// "body.data.ticket".
//
// Input that is not valid JSON is not waved through: it is scanned as text,
// because "I could not parse it" and "I know it is safe" are not the same
// statement (the same principle JSON applies in the other direction).
func ScanJSON(root string, raw []byte) []Finding {
	if len(raw) == 0 {
		return nil
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return ScanText(root, string(raw))
	}
	return scanValue(root, v)
}

func scanValue(path string, v any) []Finding {
	switch t := v.(type) {
	case map[string]any:
		var out []Finding
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic report order
		for _, k := range keys {
			child := joinPath(path, k)
			if SecretKey(k) {
				// The key name alone condemns the value; descending would
				// add noise, not information.
				out = append(out, Finding{Field: child, Rule: SecretKeyRule})
				continue
			}
			out = append(out, scanValue(child, t[k])...)
		}
		return out
	case []any:
		var out []Finding
		for i := range t {
			out = append(out, scanValue(fmt.Sprintf("%s[%d]", path, i), t[i])...)
		}
		return out
	case string:
		return ScanText(path, t)
	default:
		// Numbers, booleans and null cannot carry a credential.
		return nil
	}
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
