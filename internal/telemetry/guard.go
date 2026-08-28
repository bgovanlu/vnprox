// SPDX-License-Identifier: Apache-2.0

package telemetry

// guard.go is the check that runs on the payload immediately before it is
// sent, and again before any preview is printed.
//
// It runs on the marshalled BYTES rather than on the Payload type on
// purpose. A type-level check ("Payload has no Nodes field") proves the
// shape and nothing about the contents, and the contents are the whole risk:
// `Kernel` is a `string`, and `uname -r` on a machine whose kernel package
// was built locally can carry a hostname in it. The requirement is that such
// a payload FAILS, not that it was unlikely.
//
// Two independent halves, because neither is sufficient:
//
//   - **Shape rules** catch what has a recognisable form no matter where it
//     appears: an IPv4 or IPv6 address, a MAC, a dotted FQDN. These need no
//     knowledge of the cluster and would catch a leak on a machine we have
//     never seen.
//   - **Known-value rules** catch what has no form at all. A hostname like
//     `pve1`, a guest called `web-prod-01` and a cluster called `office` are
//     indistinguishable from any other word, so the only way to catch them
//     is to be told what they are. KnownFromReport harvests the ones the
//     source report knows (node names, the endpoint's host); a caller that
//     knows more — guest names, the cluster name — passes them in.
//
// On top of both, the payload is validated against a CLOSED field
// allowlist: an unknown key, or a value that does not match its field's
// documented shape, is a violation. That is what makes a field added
// without thought fail here rather than ship: there is no "everything else
// passes through" branch.
//
// Every rule fails CLOSED. A guard that cannot parse the payload refuses it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/verify"
)

// Class names what kind of thing a violation found. The first five are the
// classes T-2503 names explicitly; ClassStructure covers the closed-schema
// rules (an unknown field, a value that is not the shape its field is
// documented to hold).
type Class string

const (
	// ClassHostname is a node/host name, or anything FQDN-shaped.
	ClassHostname Class = "hostname"
	// ClassIP is an IPv4 or IPv6 address.
	ClassIP Class = "ip"
	// ClassMAC is a hardware address.
	ClassMAC Class = "mac"
	// ClassGuest is a VM/CT name.
	ClassGuest Class = "guest-name"
	// ClassCluster is the PVE cluster's name.
	ClassCluster Class = "cluster-name"
	// ClassStructure is a payload that is not the documented shape.
	ClassStructure Class = "structure"
)

// Known is one identifier the payload must not contain, and what kind of
// identifier it is. Values shorter than minKnownLength are ignored: a
// one-letter node name cannot be searched for without matching everything.
//
// The match is a case-insensitive SUBSTRING match, deliberately, and it
// over-matches: a node actually named `pve` matches the substring in a
// kernel string like `6.8.12-4-pve`, and that install will refuse to send
// until it is renamed or the operator turns telemetry off. That is the
// direction to be wrong in. A false positive costs a compatibility data
// point and prints exactly which value caused it; a false negative sends a
// hostname to a stranger, which is the one thing this package exists to
// prevent.

type Known struct {
	Value string
	Class Class
}

// minKnownLength is the shortest known value that is searched for.
const minKnownLength = 3

// Violation is one reason a payload may not be sent.
type Violation struct {
	// Class is what was found.
	Class Class
	// Rule is which rule found it, so a false positive can be argued with.
	Rule string
	// Path is where in the payload, e.g. "$.checks[2].id".
	Path string
	// Sample is the offending text. It stays local — it is printed to the
	// operator's own terminal and never sent anywhere, which is the point.
	Sample string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s at %s (%s rule): %q", v.Class, v.Path, v.Rule, v.Sample)
}

// GuardError is what Guard returns when a payload may not leave.
type GuardError struct {
	Violations []Violation
}

func (e *GuardError) Error() string {
	parts := make([]string, 0, len(e.Violations))
	for _, v := range e.Violations {
		parts = append(parts, fmt.Sprintf("%s at %s (%s rule): %q", v.Class, v.Path, v.Rule, v.Sample))
	}
	return "telemetry: this payload will not be sent — " + strings.Join(parts, "; ")
}

// Classes returns the distinct classes this error carries, sorted, so a
// caller (and a test) can assert on what was caught rather than on a
// message.
func (e *GuardError) Classes() []Class {
	seen := map[Class]bool{}
	var out []Class
	for _, v := range e.Violations {
		if !seen[v.Class] {
			seen[v.Class] = true
			out = append(out, v.Class)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Guard reports whether raw may be sent. A nil return is the only thing
// that permits a send.
func Guard(raw []byte, known []Known) error {
	violations := Inspect(raw, known)
	if len(violations) == 0 {
		return nil
	}
	return &GuardError{Violations: violations}
}

// Inspect returns EVERY violation in raw, not the first. Guard turns that
// list into one error that names all of them, so an operator whose payload
// is refused fixes one thing and learns about the rest in the same run
// rather than one per attempt.
func Inspect(raw []byte, known []Known) []Violation {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		// Fail closed: a payload we cannot read is a payload we cannot
		// vouch for.
		return []Violation{{Class: ClassStructure, Rule: "unparsable", Path: "$", Sample: err.Error()}}
	}
	if dec.More() {
		return []Violation{{Class: ClassStructure, Rule: "trailing-content", Path: "$", Sample: "more than one JSON document"}}
	}

	var out []Violation
	out = append(out, checkSchema(doc)...)
	out = append(out, scanValue("$", doc, known)...)
	return out
}

// --- the closed schema ----------------------------------------------------

var (
	ulidPattern    = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	checkIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	// freeTextPattern is what the three version/kernel strings may contain:
	// printable ASCII, no control characters, nothing that could smuggle a
	// second document in. "unknown" (verify's own fallback) passes.
	freeTextPattern = regexp.MustCompile(`^[\x20-\x7e]{1,200}$`)
)

// validSuites is Payload.Suite's closed vocabulary.
var validSuites = map[string]bool{"hardware": true, "multinode": true, "destructive": true, SelectionSuite: true}

// checkSchema enforces the field allowlist. Written as an explicit walk of
// the documented shape rather than a generic reflection pass so that the
// list of permitted keys is readable in one place and cannot be widened by
// a struct tag.
func checkSchema(doc any) []Violation {
	root, ok := doc.(map[string]any)
	if !ok {
		return []Violation{{Class: ClassStructure, Rule: "not-an-object", Path: "$", Sample: fmt.Sprintf("%T", doc)}}
	}

	var out []Violation
	bad := func(rule, path string, sample any) {
		out = append(out, Violation{Class: ClassStructure, Rule: rule, Path: path, Sample: fmt.Sprintf("%v", sample)})
	}

	allowed := map[string]bool{
		"payloadVersion": true, "installId": true, "vnproxVersion": true,
		"pveVersion": true, "kernel": true, "nicPciIds": true,
		"nodeCount": true, "suite": true, "checks": true,
	}
	for key := range root {
		if !allowed[key] {
			// The gate the package doc promises: a field added to Payload
			// without being thought about lands here, not on the wire.
			bad("unknown-field", "$."+key, key)
		}
	}
	for key := range allowed {
		if _, present := root[key]; !present {
			bad("missing-field", "$."+key, key)
		}
	}

	if n, numOK := jsonInt(root["payloadVersion"]); !numOK || n != PayloadVersion {
		bad("payload-version", "$.payloadVersion", root["payloadVersion"])
	}
	if s, strOK := root["installId"].(string); !strOK || !ulidPattern.MatchString(s) {
		bad("install-id-not-a-ulid", "$.installId", root["installId"])
	}
	for _, field := range []string{"vnproxVersion", "pveVersion", "kernel"} {
		if s, strOK := root[field].(string); !strOK || !freeTextPattern.MatchString(s) {
			bad("not-printable-text", "$."+field, root[field])
		}
	}
	if n, numOK := jsonInt(root["nodeCount"]); !numOK || n < 0 {
		bad("node-count", "$.nodeCount", root["nodeCount"])
	}
	if s, strOK := root["suite"].(string); !strOK || !validSuites[s] {
		bad("unknown-suite", "$.suite", root["suite"])
	}

	switch ids := root["nicPciIds"].(type) {
	case nil:
		// `null` — an install with no NIC line the reduction understood.
	case []any:
		for i, raw := range ids {
			path := fmt.Sprintf("$.nicPciIds[%d]", i)
			s, strOK := raw.(string)
			if !strOK || !pciIDPattern.MatchString(s) {
				bad("not-a-pci-id", path, raw)
			}
		}
	default:
		bad("not-a-list", "$.nicPciIds", root["nicPciIds"])
	}

	switch checks := root["checks"].(type) {
	case nil:
	case []any:
		for i, raw := range checks {
			path := fmt.Sprintf("$.checks[%d]", i)
			entry, objOK := raw.(map[string]any)
			if !objOK {
				bad("not-an-object", path, raw)
				continue
			}
			for key := range entry {
				if key != "id" && key != "status" && key != "durationMs" {
					bad("unknown-field", path+"."+key, key)
				}
			}
			if s, strOK := entry["id"].(string); !strOK || !checkIDPattern.MatchString(s) {
				bad("not-a-check-id", path+".id", entry["id"])
			}
			if s, strOK := entry["status"].(string); !strOK || (s != "pass" && s != "fail" && s != "skip") {
				bad("unknown-status", path+".status", entry["status"])
			}
			if n, numOK := jsonInt(entry["durationMs"]); !numOK || n < 0 {
				bad("bad-duration", path+".durationMs", entry["durationMs"])
			}
		}
	default:
		bad("not-a-list", "$.checks", root["checks"])
	}

	return out
}

func jsonInt(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	if err != nil {
		return 0, false
	}
	return i, true
}

// --- the deny scan --------------------------------------------------------

// scanValue walks every string in the document — values AND keys — and
// applies the shape and known-value rules to each. Keys are scanned too
// because a map-shaped field added later would put attacker-or-operator
// controlled text in key position, where a value-only scan would miss it.
func scanValue(path string, v any, known []Known) []Violation {
	switch t := v.(type) {
	case map[string]any:
		var out []Violation
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := path + "." + k
			out = append(out, scanString(child+" (key)", k, known)...)
			out = append(out, scanValue(child, t[k], known)...)
		}
		return out
	case []any:
		var out []Violation
		for i, item := range t {
			out = append(out, scanValue(fmt.Sprintf("%s[%d]", path, i), item, known)...)
		}
		return out
	case string:
		return scanString(path, t, known)
	default:
		return nil
	}
}

// scanString is where the five classes are actually caught.
func scanString(path, s string, known []Known) []Violation {
	var out []Violation

	for _, m := range macPattern.FindAllString(s, -1) {
		out = append(out, Violation{Class: ClassMAC, Rule: "mac-shape", Path: path, Sample: m})
	}
	for _, m := range findAddresses(s) {
		out = append(out, Violation{Class: ClassIP, Rule: "ip-shape", Path: path, Sample: m})
	}
	if !fqdnExemptPath.MatchString(path) {
		for _, m := range fqdnPattern.FindAllString(s, -1) {
			out = append(out, Violation{Class: ClassHostname, Rule: "fqdn-shape", Path: path, Sample: m})
		}
	}

	lower := strings.ToLower(s)
	for _, k := range known {
		value := strings.ToLower(strings.TrimSpace(k.Value))
		if len(value) < minKnownLength {
			continue
		}
		if strings.Contains(lower, value) {
			out = append(out, Violation{Class: k.Class, Rule: "known-value", Path: path, Sample: k.Value})
		}
	}
	return out
}

var (
	// macPattern is exactly six colon- or dash-separated octets (plus the
	// three-group dotted spelling switches use), so it does not compete with
	// the PCI `0x8086:0x1521` shape or with an IPv6 address. Written as
	// alternatives rather than with a back-reference because RE2 has none —
	// which also means `aa:bb-cc:dd-ee:ff` is not matched as a MAC, and
	// nothing produces that spelling.
	macPattern = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{2}:){5}[0-9a-f]{2}\b|\b(?:[0-9a-f]{2}-){5}[0-9a-f]{2}\b|\b(?:[0-9a-f]{4}\.){2}[0-9a-f]{4}\b`)

	// addressCandidate is deliberately coarse: it finds anything that COULD
	// be an address, and net.ParseIP decides. A regex precise enough to
	// recognise every IPv6 form and nothing else is a regex nobody can
	// review, and the failure mode of getting it slightly wrong is a leak.
	addressCandidate = regexp.MustCompile(`[0-9a-fA-F:.]{3,}`)

	// fqdnPattern catches a dotted name whose last label is alphabetic —
	// `pve1.example.com`, `node.lan`. It deliberately does NOT match
	// `9.2.4` or `6.8.12-4-pve`, which is why the last label must start
	// with a letter and be at least two long: version strings are the
	// legitimate dotted values in this payload.
	fqdnPattern = regexp.MustCompile(`(?i)\b[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*\.[a-z]{2,63}\b`)

	// fqdnExemptPath is the one place the FQDN rule cannot run: a check id.
	//
	// Check ids are dotted lowercase identifiers — `drift.config_vs_live`,
	// `iface.lacp_partner_observed` — and `drift.config` is shape-identical
	// to a hostname. Running the rule there would refuse every payload ever
	// built, which is not a strict guard, it is a broken one.
	//
	// What covers that field instead is NOT nothing, and this is the part
	// worth checking: a check id must match checkIDPattern (a closed shape,
	// 64 chars, no uppercase, no spaces), it is scanned by the MAC, IP and
	// known-value rules exactly like every other string, and the ids
	// themselves come from verify's compiled-in registry rather than from
	// anything a cluster says. A node name that reached this field would be
	// caught by the known-value rule, which is the rule that catches bare
	// hostnames anyway — the FQDN rule only ever adds coverage for DOTTED
	// names, and a dotted name here would have to survive checkIDPattern
	// first.
	fqdnExemptPath = regexp.MustCompile(`^\$\.checks\[\d+\]\.id$`)
)

// findAddresses returns every substring of s that parses as an IP address.
func findAddresses(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, cand := range addressCandidate.FindAllString(s, -1) {
		trimmed := strings.Trim(cand, ".:")
		if trimmed == "" || seen[trimmed] {
			continue
		}
		if ip := net.ParseIP(trimmed); ip != nil {
			seen[trimmed] = true
			out = append(out, trimmed)
			continue
		}
		// A CIDR is an address too, and `10.0.0.0/24` reaches here as
		// `10.0.0.0` only because the candidate stops at the slash — this
		// branch covers the bare-prefix spelling anyway.
		if ip, _, err := net.ParseCIDR(trimmed); err == nil && ip != nil {
			seen[trimmed] = true
			out = append(out, trimmed)
		}
	}
	return out
}

// --- known values ---------------------------------------------------------

// KnownFromReport harvests, from the report being reduced, the identifiers
// that report is known to contain and the payload must not.
//
// This is the honest half of the guard's coverage and the doc comment says
// so plainly: a T-2501 report structurally carries node names and the PVE
// endpoint, so those are harvested here and the hostname/IP classes are
// covered on real data. It carries no cluster name and no guest name in any
// field the reduction reads, so nothing can be harvested for those classes —
// their defence is the shape rules plus the closed schema, and a caller that
// DOES know those names (a future collector-side check, a test) passes them
// to Build as extra Known values.
func KnownFromReport(rep verify.Report) []Known {
	var out []Known
	for _, n := range rep.Environment.Nodes {
		if strings.TrimSpace(n) != "" {
			out = append(out, Known{Value: n, Class: ClassHostname})
		}
	}
	if host := endpointHost(rep.Environment.PVEEndpoint); host != "" {
		class := ClassHostname
		if net.ParseIP(host) != nil {
			class = ClassIP
		}
		out = append(out, Known{Value: host, Class: class})
	}
	return out
}

// endpointHost pulls the host out of a PVE API URL without importing
// net/url's full parse failure surface into the guard: anything unparsable
// yields "", and the shape rules still cover it.
func endpointHost(endpoint string) string {
	s := endpoint
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	return strings.Trim(strings.TrimSpace(s), "[]")
}

// AsGuardError extracts a *GuardError from err, for callers that want to
// report the classes rather than the message.
func AsGuardError(err error) (*GuardError, bool) {
	var ge *GuardError
	if errors.As(err, &ge) {
		return ge, true
	}
	return nil, false
}
