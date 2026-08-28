// SPDX-License-Identifier: Apache-2.0

package spec

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Marshal renders s to YAML. Because Spec is a tree of typed structs with
// fixed field order (never a map), yaml.Marshal's output is deterministic:
// two Marshal calls over identical state produce byte-identical bytes, which
// is the property that makes `git diff` on a committed spec empty for an
// unchanged cluster (docs/data-model.md §5). Callers that build the Spec via
// Export get that guarantee end-to-end because Export additionally sorts
// every slice by a stable key.
func Marshal(s Spec) ([]byte, error) {
	b, err := yaml.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("spec: marshaling spec: %w", err)
	}
	return b, nil
}

// Parse decodes a YAML spec document and validates its specVersion. It is
// the inverse of Marshal for the fields Marshal emits. An unknown/absent
// specVersion is rejected (validateVersion) rather than reconciled against.
func Parse(data []byte) (Spec, error) {
	var s Spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return Spec{}, fmt.Errorf("spec: parsing spec document: %w", err)
	}
	if err := validateVersion(s); err != nil {
		return Spec{}, err
	}
	return s, nil
}
