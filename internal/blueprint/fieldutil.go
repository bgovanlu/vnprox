// SPDX-License-Identifier: Apache-2.0

package blueprint

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/change"
)

// fieldString reads fields[key] as a string, ok=false if absent.
func fieldString(fields map[string]any, key string) (string, bool, error) {
	v, ok := fields[key]
	if !ok {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("fields.%s must be a string", key)
	}
	return s, true, nil
}

// fieldBool reads fields[key] as a bool, ok=false if absent.
func fieldBool(fields map[string]any, key string) (bool, bool, error) {
	v, ok := fields[key]
	if !ok {
		return false, false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, false, fmt.Errorf("fields.%s must be a boolean", key)
	}
	return b, true, nil
}

// fieldInt reads fields[key] as an int, ok=false if absent.
func fieldInt(fields map[string]any, key string) (int, bool, error) {
	v, ok := fields[key]
	if !ok {
		return 0, false, nil
	}
	n, err := toInt(v)
	if err != nil {
		return 0, false, fmt.Errorf("fields.%s: %w", key, err)
	}
	return n, true, nil
}

// fieldStringSlice reads fields[key] as a []string, ok=false if absent.
func fieldStringSlice(fields map[string]any, key string) ([]string, bool, error) {
	v, ok := fields[key]
	if !ok {
		return nil, false, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false, fmt.Errorf("fields.%s must be an array of strings", key)
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, false, fmt.Errorf("fields.%s[%d] must be a string", key, i)
		}
		out[i] = s
	}
	return out, true, nil
}

// fieldVids reads fields[key] as a []change.VidRange built from a flat
// array of individual VLAN ids (docs/features/blueprints.md's guest-VLAN
// starters describe VLANs as a plain id list, not compacted ranges).
func fieldVids(fields map[string]any, key string) ([]change.VidRange, bool, error) {
	v, ok := fields[key]
	if !ok {
		return nil, false, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false, fmt.Errorf("fields.%s must be an array of vlan ids", key)
	}
	out := make([]change.VidRange, len(arr))
	for i, e := range arr {
		n, err := toInt(e)
		if err != nil {
			return nil, false, fmt.Errorf("fields.%s[%d]: %w", key, i, err)
		}
		out[i] = change.VidRange{Low: n, High: n}
	}
	return out, true, nil
}

// setEqual reports whether a and b contain the same strings, ignoring
// order and duplicates.
func setEqual(a, b []string) bool {
	return canonSet(a) == canonSet(b)
}

func canonSet(ss []string) string {
	cp := append([]string(nil), ss...)
	sort.Strings(cp)
	out := ""
	for i, s := range cp {
		if i > 0 {
			out += "\x00"
		}
		out += s
	}
	return out
}

// vidsEqual reports whether two VidRange sets are the same, ignoring
// order.
func vidsEqual(a, b []change.VidRange) bool {
	return canonVids(a) == canonVids(b)
}

func canonVids(vids []change.VidRange) string {
	ss := make([]string, len(vids))
	for i, v := range vids {
		if v.Low == v.High {
			ss[i] = fmt.Sprintf("%d", v.Low)
		} else {
			ss[i] = fmt.Sprintf("%d-%d", v.Low, v.High)
		}
	}
	sort.Strings(ss)
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// missingFrom returns the elements of want not present in have (set
// difference), preserving want's order.
func missingFrom(want, have []string) []string {
	haveSet := make(map[string]bool, len(have))
	for _, h := range have {
		haveSet[h] = true
	}
	var out []string
	for _, w := range want {
		if !haveSet[w] {
			out = append(out, w)
		}
	}
	return out
}
