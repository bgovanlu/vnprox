package blueprint

import (
	"fmt"
	"regexp"
	"strconv"
)

// builtinNodes is the one substitution token every blueprint gets for
// free, without declaring it as a param: the instantiate request's target
// node list (docs/features/blueprints.md §1's cluster-wide-consistency
// use — the SDN starters' zone "nodes" field uses this rather than
// requiring a redundant nodeList param that would just have to be kept in
// sync with the top-level Nodes the caller already sends).
const builtinNodes = "__nodes__"

var tokenPattern = regexp.MustCompile(`\{\{\s*(\w+)\s*\}\}`)

// tokenNames returns every distinct {{name}} token referenced in s, in
// first-appearance order.
func tokenNames(s string) []string {
	matches := tokenPattern.FindAllStringSubmatch(s, -1)
	if matches == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// wholeTokenPattern matches a string that is *exactly* one placeholder
// (optionally surrounded by whitespace), e.g. "{{mgmtCidr}}" but not
// "vmbr-{{n}}" — whole-value substitution preserves the param's JSON type
// (a []any param substituted into a bare "{{guestVlans}}" field stays an
// array), where a partial match can only ever produce a string.
var wholeTokenPattern = regexp.MustCompile(`^\s*\{\{\s*(\w+)\s*\}\}\s*$`)

// substituteValue recursively replaces every {{name}} token in v (a
// string, []any, or map[string]any — the shapes JSON unmarshaling into
// map[string]any/[]any/string/float64/bool ever produces) using bindings.
// A value that is a bare whole-token string is replaced with the bound
// value verbatim (any JSON type); a value that mixes literal text and
// tokens, or is nested inside one, is replaced with its string form via
// fmt.Sprint. Missing bindings are an error (Validate/resolveParams should
// have already caught an undeclared param name; a token that names a
// param the caller simply didn't supply a value for, and which had no
// default, is caught here too).
func substituteValue(v any, bindings map[string]any) (any, error) {
	switch val := v.(type) {
	case string:
		if m := wholeTokenPattern.FindStringSubmatch(val); m != nil {
			bound, ok := bindings[m[1]]
			if !ok {
				return nil, fmt.Errorf("no value bound for {{%s}}", m[1])
			}
			return bound, nil
		}
		var substErr error
		out := tokenPattern.ReplaceAllStringFunc(val, func(tok string) string {
			m := tokenPattern.FindStringSubmatch(tok)
			bound, ok := bindings[m[1]]
			if !ok {
				substErr = fmt.Errorf("no value bound for {{%s}}", m[1])
				return tok
			}
			return stringify(bound)
		})
		if substErr != nil {
			return nil, substErr
		}
		return out, nil

	case []any:
		out := make([]any, len(val))
		for i, e := range val {
			sub, err := substituteValue(e, bindings)
			if err != nil {
				return nil, err
			}
			out[i] = sub
		}
		return out, nil

	case map[string]any:
		out := make(map[string]any, len(val))
		for k, e := range val {
			sub, err := substituteValue(e, bindings)
			if err != nil {
				return nil, err
			}
			out[k] = sub
		}
		return out, nil

	default:
		return v, nil
	}
}

// substituteFields substitutes every field value in fields, returning a
// fresh map (fields itself is never mutated — Blueprint values may be
// shared, e.g. a starter's compiled-in template reused across concurrent
// instantiate calls).
func substituteFields(fields map[string]any, bindings map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		sub, err := substituteValue(v, bindings)
		if err != nil {
			return nil, fmt.Errorf("fields.%s: %w", k, err)
		}
		out[k] = sub
	}
	return out, nil
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

// toInt coerces a JSON-decoded numeric value (float64 from
// encoding/json) or a plain int/int64 (from Go-literal test fixtures and
// starter definitions built directly as Go values) to int.
func toInt(v any) (int, error) {
	switch x := v.(type) {
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case int64:
		return int(x), nil
	default:
		return 0, fmt.Errorf("%v is not a number", v)
	}
}
