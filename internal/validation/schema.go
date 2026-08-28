// SPDX-License-Identifier: Apache-2.0

package validation

import (
	"encoding/json"
	"fmt"
	"time"
)

// SupportedSchemaVersion is the evidence-blob schema version this build of
// vnprox understands. A harness script and this package are versioned
// together (see planning/validation/README.md's "changing the schema"
// note); bump both in the same change.
const SupportedSchemaVersion = "1.0"

// Blob is the evidence a planning/validation/harness/<section>.sh script
// prints to stdout: one JSON object, schema-versioned, with per-item raw
// (already-redacted) command output. A script never decides pass/fail —
// that is Triage's job, run against a *committed* blob so a wrong verdict
// stays auditable after the fact (docs/roadmap-proven.md D7).
type Blob struct {
	Node           NodeInfo   `json:"node"`
	PVEVersion     PVEVersion `json:"pve_version"`
	SchemaVersion  string     `json:"schema_version"`
	HarnessVersion string     `json:"harness_version"`
	Section        string     `json:"section"`
	GeneratedAt    string     `json:"generated_at"`
	Items          []Item     `json:"items"`
	Mutates        bool       `json:"mutates"`
}

// NodeInfo identifies which node an evidence blob was captured on.
type NodeInfo struct {
	Hostname string `json:"hostname"`
	Identity string `json:"identity"`
}

// PVEVersion records how the harness learned the PVE version (or that it
// couldn't — "unknown" is an honest, schema-valid answer, notably when a
// script runs against internal/pvemock rather than a real node).
type PVEVersion struct {
	Source string `json:"source"`
	Raw    string `json:"raw"`
}

// Item is one checklist observation: a command, its verbatim (redacted)
// output, its exit code, and any structured verdict-input signals a triage
// step can compare against an expected-outcome table entry.
type Item struct {
	VerdictInputs map[string]any `json:"verdict_inputs"`
	ID            string         `json:"id"`
	ChecklistRef  string         `json:"checklist_ref"`
	Command       string         `json:"command"`
	Raw           string         `json:"raw"`
	ExitCode      int            `json:"exit_code"`
}

// ParseBlob decodes and validates raw JSON as an evidence blob. It is
// deliberately two-pass: a generic map decode first, so a required field
// that is present-but-empty (a script bug) is distinguished from a field
// genuinely absent from the JSON (a schema violation) — encoding/json's
// struct decode alone can't tell those apart.
func ParseBlob(raw []byte) (*Blob, error) {
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("evidence blob is not valid JSON: %w", err)
	}

	var blob Blob
	if err := json.Unmarshal(raw, &blob); err != nil {
		return nil, fmt.Errorf("decoding evidence blob: %w", err)
	}

	if err := validateRequiredKeys(generic); err != nil {
		return nil, err
	}
	if err := blob.Validate(); err != nil {
		return nil, err
	}
	return &blob, nil
}

var topLevelRequiredKeys = []string{
	"schema_version", "harness_version", "section", "generated_at",
	"mutates", "node", "pve_version", "items",
}

func validateRequiredKeys(generic map[string]any) error {
	for _, k := range topLevelRequiredKeys {
		if _, ok := generic[k]; !ok {
			return fmt.Errorf("evidence blob missing required top-level field %q", k)
		}
	}
	node, ok := generic["node"].(map[string]any)
	if !ok {
		return fmt.Errorf("evidence blob's %q field is not an object", "node")
	}
	for _, k := range []string{"hostname", "identity"} {
		if _, present := node[k]; !present {
			return fmt.Errorf("evidence blob's node object missing required field %q", k)
		}
	}
	pv, ok := generic["pve_version"].(map[string]any)
	if !ok {
		return fmt.Errorf("evidence blob's %q field is not an object", "pve_version")
	}
	for _, k := range []string{"source", "raw"} {
		if _, present := pv[k]; !present {
			return fmt.Errorf("evidence blob's pve_version object missing required field %q", k)
		}
	}
	items, ok := generic["items"].([]any)
	if !ok {
		return fmt.Errorf("evidence blob's %q field is not an array", "items")
	}
	for i, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("evidence blob's items[%d] is not an object", i)
		}
		for _, k := range []string{"id", "checklist_ref", "command", "raw", "exit_code", "verdict_inputs"} {
			if _, ok := item[k]; !ok {
				return fmt.Errorf("evidence blob's items[%d] missing required field %q", i, k)
			}
		}
	}
	return nil
}

// Validate checks the semantic constraints ParseBlob's key-presence pass
// doesn't: a supported schema version, a parseable timestamp, non-empty
// identifiers, and no duplicate item IDs (triage looks items up by ID —
// a duplicate would silently shadow one of them).
func (b *Blob) Validate() error {
	if b.SchemaVersion != SupportedSchemaVersion {
		return fmt.Errorf("unsupported schema_version %q (this build understands %q)", b.SchemaVersion, SupportedSchemaVersion)
	}
	if b.HarnessVersion == "" {
		return fmt.Errorf("harness_version is empty")
	}
	if b.Section == "" {
		return fmt.Errorf("section is empty")
	}
	if _, err := time.Parse(time.RFC3339, b.GeneratedAt); err != nil {
		return fmt.Errorf("generated_at %q is not RFC3339: %w", b.GeneratedAt, err)
	}
	if b.Node.Hostname == "" {
		return fmt.Errorf("node.hostname is empty")
	}
	if b.PVEVersion.Source == "" {
		return fmt.Errorf("pve_version.source is empty")
	}
	seen := make(map[string]bool, len(b.Items))
	for i, item := range b.Items {
		if item.ID == "" {
			return fmt.Errorf("items[%d] has an empty id", i)
		}
		if seen[item.ID] {
			return fmt.Errorf("items[%d]: duplicate item id %q", i, item.ID)
		}
		seen[item.ID] = true
		if item.Command == "" {
			return fmt.Errorf("item %q has an empty command", item.ID)
		}
	}
	return nil
}

// ItemByID returns the item with the given ID, if present.
func (b *Blob) ItemByID(id string) (Item, bool) {
	for _, item := range b.Items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}
