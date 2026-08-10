// policy_load.go parses a policy document. The authoring format is YAML
// (`vnproxctl policy test --policy=f.yaml`), which — being a strict
// superset of JSON — means the same parser accepts the JSON body the API
// takes, so there is exactly one decoder and one set of error messages for
// both surfaces.
//
// gopkg.in/yaml.v3 is already an approved, direct dependency of this module
// (docs/development.md; internal/spec, internal/pvemock and internal/k8s
// use it). No new dependency is introduced by this file — and deliberately
// no policy-engine dependency at all: parsing is all a declarative rule set
// needs.

package change

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// policyDoc is the on-disk document shape. It exists separately from
// PolicySet only so the decoder can reject unknown top-level keys while
// PolicySet itself stays a plain value type callers can construct.
type policyDoc PolicySet

// LoadPolicyFile reads and fully validates a policy file. Every failure is
// a *PolicyLoadError naming the file and — wherever the failure can be
// attributed to one — the rule id and field (AC5).
//
// A missing file is an error here, unlike LoadProtectedConfig's tolerant
// "not configured yet": a daemon told to load a policy file that is not
// there must not start silently unguarded.
func LoadPolicyFile(path string) (PolicySet, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied config path, same trust level as protected.json
	if err != nil {
		return PolicySet{}, &PolicyLoadError{File: path, Msg: fmt.Sprintf("reading policy file: %v", err)}
	}
	return ParsePolicySet(path, data)
}

// ParsePolicySet decodes and validates one policy document. file is used
// only to build error messages ("" for an in-memory document).
func ParsePolicySet(file string, data []byte) (PolicySet, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var doc policyDoc
	if err := dec.Decode(&doc); err != nil {
		return PolicySet{}, decodePolicyError(file, data, err)
	}
	set := PolicySet(doc)
	if err := set.Validate(file); err != nil {
		return PolicySet{}, err
	}
	if set.Version == 0 {
		set.Version = PolicyFormatVersion
	}
	return set, nil
}

// unknownYAMLField extracts the offending key from yaml.v3's KnownFields
// error text ("line 7: field severty not found in type change.PolicyRule").
// The library exposes no structured field for it, exactly as encoding/json
// does not (compare op.go's unknownFieldPattern, which solves the same
// problem for the op decoder).
var unknownYAMLField = regexp.MustCompile(`field ([A-Za-z0-9_.\-]+) not found`)

// yamlErrorLine extracts the first "line N" the decoder reported, so the
// failure can be attributed to the rule whose block contains that line.
var yamlErrorLine = regexp.MustCompile(`line (\d+)`)

// decodePolicyError turns a raw yaml decode failure into a *PolicyLoadError
// that names the file, the rule the failing line belongs to, and the field,
// so an operator is never handed a bare "line 7: ..." for a document with
// forty rules in it.
func decodePolicyError(file string, data []byte, err error) error {
	msg := err.Error()
	if strings.Contains(msg, "EOF") && len(bytes.TrimSpace(data)) == 0 {
		return &PolicyLoadError{File: file, Msg: "policy file is empty"}
	}

	out := &PolicyLoadError{File: file, Msg: cleanYAMLMessage(msg)}
	if m := unknownYAMLField.FindStringSubmatch(msg); m != nil {
		out.Field = m[1]
	}
	if m := yamlErrorLine.FindStringSubmatch(msg); m != nil {
		var line int
		if _, ferr := fmt.Sscanf(m[1], "%d", &line); ferr == nil {
			out.RuleID = ruleIDAtLine(data, line)
		}
	}
	return out
}

// cleanYAMLMessage strips yaml.v3's leading "yaml: unmarshal errors:\n  "
// preamble and collapses the remainder onto one line, so the message reads
// as one sentence inside PolicyLoadError's own framing.
func cleanYAMLMessage(msg string) string {
	msg = strings.TrimPrefix(msg, "yaml: unmarshal errors:\n")
	msg = strings.TrimPrefix(msg, "yaml: ")
	fields := strings.Fields(strings.ReplaceAll(msg, "\n", " "))
	return strings.Join(fields, " ")
}

// ruleIDAtLine reports the id of the rule whose YAML block contains line,
// by re-parsing the document leniently into a node tree. Returns "" when
// the line falls outside any rule (a document-level error) or the document
// is too malformed to walk at all.
func ruleIDAtLine(data []byte, line int) string {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return ""
	}
	if len(root.Content) == 0 {
		return ""
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return ""
	}
	var rules *yaml.Node
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == "rules" {
			rules = doc.Content[i+1]
			break
		}
	}
	if rules == nil || rules.Kind != yaml.SequenceNode {
		return ""
	}
	best := ""
	for _, item := range rules.Content {
		if item.Line > line {
			break
		}
		best = mappingValue(item, "id")
	}
	return best
}

func mappingValue(n *yaml.Node, key string) string {
	if n.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1].Value
		}
	}
	return ""
}

// MarshalPolicySet renders a policy set back to the canonical YAML
// authoring format — used by `vnproxctl policy test` to echo the effective
// rule set and by the API's export path, so what an operator reads back is
// the same shape they wrote.
func MarshalPolicySet(set PolicySet) ([]byte, error) {
	if set.Version == 0 {
		set.Version = PolicyFormatVersion
	}
	out, err := yaml.Marshal(set)
	if err != nil {
		return nil, fmt.Errorf("change: marshaling policy set: %w", err)
	}
	return out, nil
}
