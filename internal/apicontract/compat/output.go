package compat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// JSON renders m as indented JSON with a trailing newline, matching this
// repo's other generated-artifact conventions (docs/automation-contract.json,
// docs/openapi.json).
func (m Matrix) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("compat: marshaling matrix: %w", err)
	}
	return append(b, '\n'), nil
}

// checkGlyph renders a single check's pass/fail as a compact, grep-friendly
// glyph for the markdown table.
func checkGlyph(c CheckResult) string {
	if c.Pass {
		return c.Name + ":ok"
	}
	return c.Name + ":FAIL"
}

// MarkdownTable renders m as a GitHub-flavored-markdown table: one row per
// cell, an explicit "validation" column (AC3 — never blur mock and
// hardware), and a per-check breakdown so a reader can see *which* check
// carried a cell rather than only a single pass/fail bit.
func (m Matrix) MarkdownTable() string {
	var b strings.Builder
	fmt.Fprintf(&b, "vnprox version: `%s` — generated %s\n\n", m.VnproxVersion, m.GeneratedAt.Format("2006-01-02T15:04:05Z"))
	b.WriteString("| PVE version | Validation | Result | Checks | Fixture |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, c := range m.Cells {
		result := "pass"
		if !c.Pass {
			result = "**FAIL**"
		}
		glyphs := make([]string, 0, len(c.Checks))
		for _, chk := range c.Checks {
			glyphs = append(glyphs, checkGlyph(chk))
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | `%s` |\n",
			c.PVEVersion, c.Validation, result, strings.Join(glyphs, ", "), c.Fixture)
	}
	return b.String()
}

// generatedBeginMarker and generatedEndMarker delimit the block
// UpdateGeneratedSection rewrites inside docs/compatibility.md. Everything
// outside them is ordinary, hand-authored prose this package never touches.
const (
	generatedBeginMarker = "<!-- BEGIN T-2103 GENERATED MATRIX (source: internal/apicontract/compat, `make compat-matrix`) -->"
	generatedEndMarker   = "<!-- END T-2103 GENERATED MATRIX -->"
)

// UpdateGeneratedSection replaces the content between
// generatedBeginMarker/generatedEndMarker in the file at docPath with
// table, preserving both marker lines and everything outside them. It
// fails if either marker is missing or out of order, rather than silently
// appending — a doc that lost its markers needs a human to look at it, not
// a generator guessing where the table used to be.
func UpdateGeneratedSection(docPath, table string) error {
	raw, err := os.ReadFile(docPath)
	if err != nil {
		return fmt.Errorf("compat: reading %s: %w", docPath, err)
	}
	begin := bytes.Index(raw, []byte(generatedBeginMarker))
	if begin < 0 {
		return fmt.Errorf("compat: %s is missing %q", docPath, generatedBeginMarker)
	}
	afterBegin := begin + len(generatedBeginMarker)
	end := bytes.Index(raw[afterBegin:], []byte(generatedEndMarker))
	if end < 0 {
		return fmt.Errorf("compat: %s is missing %q after its begin marker", docPath, generatedEndMarker)
	}
	end += afterBegin

	var out bytes.Buffer
	out.Write(raw[:afterBegin])
	out.WriteString("\n\n")
	out.WriteString(table)
	out.WriteString("\n")
	out.Write(raw[end:])

	if err := os.WriteFile(docPath, out.Bytes(), 0o644); err != nil {
		return fmt.Errorf("compat: writing %s: %w", docPath, err)
	}
	return nil
}
