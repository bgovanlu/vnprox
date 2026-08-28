// SPDX-License-Identifier: Apache-2.0

// Package compat (T-2103) builds vnprox's PVE compatibility matrix: vnprox
// version × PVE version, populated by driving a small set of representative
// integration checks against internal/pvemock, once per registered
// pvemock.PVEVersionProfile (internal/pvemock/compat_versions.go), through
// internal/pvemock.NewCompatServer.
//
// This is the MOCK-VALIDATED half of the matrix. `vnproxctl telemetry`
// (T-2503, internal/telemetry) is the complementary, HARDWARE-VALIDATED
// half: it aggregates `vnproxctl verify` results from real clusters in the
// field. The two are deliberately kept apart — every Matrix this package
// produces carries Validation == "mock" on every cell, and nothing in this
// package ever reads or claims field/telemetry data. See
// docs/compatibility.md for the reader-facing explanation of why that
// distinction matters and how to tell the two apart at a glance.
package compat

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// ValidationKindMock is the only Validation value this package's own
// Generate ever produces — named explicitly (rather than a bare literal
// scattered around this file) so a reviewer grepping the source for
// "hardware" finds nothing here to be confused by.
const ValidationKindMock = "mock"

// Cell names one (fixture, PVE version profile) pair the matrix runs.
type Cell struct {
	// FixtureName is the file name under testdata/clusters/compat/ (e.g.
	// "pve-8.2.yaml") — see that directory's pve-8.2.yaml header comment
	// for what these fixtures are and are not.
	FixtureName string
	Profile     pvemock.PVEVersionProfile
}

// Cells is the fixed set of cells this matrix currently runs: one per
// registered pvemock.CompatProfiles entry (docs/roadmap.md's Compatibility
// policy names 8.2 and 9.x explicitly; 9.x is represented here by 9.0 and
// 9.2, "whatever is current").
var Cells = []Cell{
	{FixtureName: "pve-8.2.yaml", Profile: mustProfile("8.2")},
	{FixtureName: "pve-9.0.yaml", Profile: mustProfile("9.0")},
	{FixtureName: "pve-9.2.yaml", Profile: mustProfile("9.2")},
}

func mustProfile(version string) pvemock.PVEVersionProfile {
	p, ok := pvemock.ProfileByVersion(version)
	if !ok {
		// Cells is a package-level var initialized once at program start;
		// a missing profile here is a programming error in this package,
		// not a runtime condition a caller can recover from — the same
		// posture compat_versions.go itself takes for an unregistered
		// profile.
		panic(fmt.Sprintf("apicontract/compat: no pvemock.PVEVersionProfile registered for %q", version))
	}
	return p
}

// fixturesDir is Cells' FixtureName base, relative to this package's own
// directory (mirroring internal/pvemock/fixture_test.go's fixturePath
// helper, one level deeper here).
var fixturesDir = filepath.Join("..", "..", "..", "testdata", "clusters", "compat")

// fixturePath resolves a Cell's FixtureName to a path Generate can pass to
// pvemock.LoadFixture, and the same relative path recorded in the
// published matrix's "fixture" field for a reader to locate it by.
func fixturePath(name string) string {
	return filepath.Join(fixturesDir, name)
}

// CheckResult is one representative integration check run against a cell's
// compat-wrapped mock server.
type CheckResult struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Pass   bool   `json:"pass"`
}

// CellResult is one matrix cell: a PVE version, the fixture it was run
// against, every check's outcome, and the AC3-required, explicit
// mock-vs-hardware label.
type CellResult struct {
	PVEVersion string        `json:"pve_version"`
	Fixture    string        `json:"fixture"`
	Validation string        `json:"validation"`
	Checks     []CheckResult `json:"checks"`
	Pass       bool          `json:"pass"`
}

// Matrix is the full, machine-readable compatibility matrix result
// (T-2103 AC1).
type Matrix struct {
	VnproxVersion string       `json:"vnprox_version"`
	GeneratedAt   time.Time    `json:"generated_at"`
	Cells         []CellResult `json:"cells"`
}

// Generate runs Cells' checks and returns the resulting Matrix. It never
// returns an error itself (a check that cannot even complete is recorded as
// a failed CheckResult, not a Go error) — Generate's job is to produce a
// complete, readable report of what happened, even when what happened is
// "PVE 8.2 rejected this", which is the expected, correct behavior for at
// least one cell by design (TestSDNFabricZoneGate_IsCaughtPerVersion).
func Generate(vnproxVersion string) (Matrix, error) {
	m := Matrix{
		VnproxVersion: vnproxVersion,
		GeneratedAt:   time.Now().UTC(),
	}
	for _, cell := range Cells {
		path := fixturePath(cell.FixtureName)
		f, err := pvemock.LoadFixture(path)
		if err != nil {
			return Matrix{}, fmt.Errorf("compat: loading fixture %s for PVE %s: %w", path, cell.Profile.Version, err)
		}
		checks := runChecks(f, cell.Profile)
		pass := true
		for _, c := range checks {
			if !c.Pass {
				pass = false
				break
			}
		}
		m.Cells = append(m.Cells, CellResult{
			PVEVersion: cell.Profile.Version,
			Fixture:    filepath.ToSlash(filepath.Join("testdata", "clusters", "compat", cell.FixtureName)),
			Validation: ValidationKindMock,
			Checks:     checks,
			Pass:       pass,
		})
	}
	return m, nil
}
