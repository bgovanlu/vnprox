// SPDX-License-Identifier: Apache-2.0

package upgradeadvisor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/doctor"
)

// TestCatalogEntriesAreWellFormed guards the shape T-4004 AC1 depends on: a
// catalog that is data, not an if-chain, only stays trustworthy if every
// row is internally consistent — sourced, self-naming, and carrying a
// remediation whenever its Check can fire non-pass.
func TestCatalogEntriesAreWellFormed(t *testing.T) {
	seen := make(map[string]bool)
	for _, e := range Catalog {
		if e.ID == "" {
			t.Fatalf("catalog entry %q has an empty ID", e.Title)
		}
		if seen[e.ID] {
			t.Fatalf("duplicate catalog entry ID %q", e.ID)
		}
		seen[e.ID] = true

		if e.Title == "" {
			t.Errorf("%s: empty Title", e.ID)
		}
		if e.FromVersionRange == "" || e.ToVersionRange == "" {
			t.Errorf("%s: FromVersionRange/ToVersionRange must both be set", e.ID)
		}
		if e.Affects == "" {
			t.Errorf("%s: empty Affects", e.ID)
		}
		if len(e.Evidence) == 0 {
			t.Errorf("%s: no Evidence — every entry must be sourced (T-4004)", e.ID)
		}
		if e.Remediation == "" {
			t.Errorf("%s: empty Remediation", e.ID)
		}
		if e.Check == nil {
			t.Errorf("%s: nil Check func", e.ID)
			continue
		}

		// A Result's Check field must match the entry's own ID under every
		// facts shape this test exercises elsewhere — spot-check with the
		// zero value here so a copy-pasted entry cannot silently answer
		// under the wrong name.
		res := e.Check(Facts{})
		if res.Check != e.ID {
			t.Errorf("%s: Check(Facts{}) returned Result.Check = %q; want %q", e.ID, res.Check, e.ID)
		}
		if err := (doctor.Report{Results: []doctor.Result{res}}).Validate(); err != nil {
			t.Errorf("%s: Check(Facts{}) result fails doctor.Report.Validate: %v", e.ID, err)
		}
	}
}

// TestConntrackProcfsCheck is the table-driven "before"/"after" proof T-4004
// AC3 asks for on its own motivating example: a host whose live conntrack
// read depends on procfs must be flagged; a host that already reads via
// netlink (T-3711's own fix) must stay silent (pass), regardless of the
// upgrade target.
func TestConntrackProcfsCheck(t *testing.T) {
	errPermissionDenied := errors.New("operation not permitted")

	tests := []struct {
		name       string
		wantStatus doctor.Status
		wantSubstr string
		facts      Facts
	}{
		{
			name:       "not probed at all",
			facts:      Facts{},
			wantStatus: doctor.StatusSkip,
			wantSubstr: "not probed",
		},
		{
			// "before" state: T-3711's actual break class — netlink
			// capability itself unavailable (the specific condition that,
			// combined with PVE 9's procfs removal, leaves conntrack
			// reads with no working path at all).
			name: "before: netlink capability probe fails",
			facts: Facts{
				ConntrackNetlinkProbed: true,
				ConntrackNetlinkErr:    errPermissionDenied,
			},
			wantStatus: doctor.StatusWarn,
			wantSubstr: "capability probe failed",
		},
		{
			// "before" state, second shape: an explicit operator override
			// pointed at a procfs-style path.
			name: "before: explicit procfs path override configured",
			facts: Facts{
				ConntrackNetlinkProbed: true,
				ConntrackPathOverride:  "/proc/net/nf_conntrack",
			},
			wantStatus: doctor.StatusFail,
			wantSubstr: "overridden to a text-format procfs path",
		},
		{
			// "after" state: T-3711's fix in effect — netlink capability
			// probe succeeds, no override configured. Must stay silent.
			name: "after: netlink capability probe succeeds, no override",
			facts: Facts{
				ConntrackNetlinkProbed: true,
				ConntrackNetlinkErr:    nil,
			},
			wantStatus: doctor.StatusPass,
			wantSubstr: "does not depend on /proc/net/nf_conntrack",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := checkConntrackProcfs(tt.facts)
			if res.Status != tt.wantStatus {
				t.Errorf("status = %s; want %s (detail: %s)", res.Status, tt.wantStatus, res.Detail)
			}
			if !strings.Contains(res.Detail, tt.wantSubstr) {
				t.Errorf("detail %q does not contain %q", res.Detail, tt.wantSubstr)
			}
			if res.Check != checkConntrackProcfsID {
				t.Errorf("Check = %q; want %q", res.Check, checkConntrackProcfsID)
			}
			if (tt.wantStatus == doctor.StatusWarn || tt.wantStatus == doctor.StatusFail) && res.Remediation == "" {
				t.Errorf("status %s with no remediation", tt.wantStatus)
			}
		})
	}
}

// TestNftablesEngineSplitCheck covers the second sourced entry (T-3904):
// only the specific combination that is actually misleading — firewall
// on, nftables engine not active — should warn. Every other combination
// (unprobed, firewall off, nftables already active) is either a skip or a
// pass "after" state.
func TestNftablesEngineSplitCheck(t *testing.T) {
	tests := []struct {
		name       string
		wantStatus doctor.Status
		wantSubstr string
		facts      Facts
	}{
		{
			name:       "not probed at all",
			facts:      Facts{},
			wantStatus: doctor.StatusSkip,
			wantSubstr: "not probed",
		},
		{
			name: "firewall disabled",
			facts: Facts{
				FirewallStateProbed:       true,
				FirewallEnabled:           false,
				NftablesEngineStateProbed: true,
				NftablesEngineActive:      false,
			},
			wantStatus: doctor.StatusPass,
			wantSubstr: "disabled",
		},
		{
			// "before" state: exactly pvecube's own observed configuration
			// were its firewall turned on (evidence transcript §3) —
			// firewall enabled, force-disable flag file present, so
			// iptables (not nftables) is the effective engine.
			name: "before: firewall enabled, iptables is the effective engine",
			facts: Facts{
				FirewallStateProbed:       true,
				FirewallEnabled:           true,
				NftablesEngineStateProbed: true,
				NftablesEngineActive:      false,
			},
			wantStatus: doctor.StatusWarn,
			wantSubstr: "will report an empty ruleset",
		},
		{
			// "after" state: operator has opted into the nftables tech
			// preview (or it is otherwise the active engine) — the
			// inspector's read is no longer ambiguous.
			name: "after: nftables engine active",
			facts: Facts{
				FirewallStateProbed:       true,
				FirewallEnabled:           true,
				NftablesEngineStateProbed: true,
				NftablesEngineActive:      true,
			},
			wantStatus: doctor.StatusPass,
			wantSubstr: "matches what PVE actually enforces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := checkNftablesEngineSplit(tt.facts)
			if res.Status != tt.wantStatus {
				t.Errorf("status = %s; want %s (detail: %s)", res.Status, tt.wantStatus, res.Detail)
			}
			if !strings.Contains(res.Detail, tt.wantSubstr) {
				t.Errorf("detail %q does not contain %q", res.Detail, tt.wantSubstr)
			}
			if (tt.wantStatus == doctor.StatusWarn || tt.wantStatus == doctor.StatusFail) && res.Remediation == "" {
				t.Errorf("status %s with no remediation", tt.wantStatus)
			}
		})
	}
}

// TestRunFiltersByTargetVersion proves Run only evaluates entries whose
// BreaksAt the requested target actually reaches, and that an affected
// host produces a report matching AC1's "flags it by name with a link to
// the remediation" (a Result naming the entry's ID with a non-empty
// Remediation).
func TestRunFiltersByTargetVersion(t *testing.T) {
	affected := Facts{
		ConntrackNetlinkProbed:    true,
		ConntrackNetlinkErr:       errors.New("permission denied"),
		FirewallStateProbed:       true,
		FirewallEnabled:           true,
		NftablesEngineStateProbed: true,
		NftablesEngineActive:      false,
	}

	t.Run("target below every entry's BreaksAt yields no results", func(t *testing.T) {
		report := Run(Version{Major: 8, Minor: 2}, affected, time.Now(), "test")
		if len(report.Results) != 0 {
			t.Errorf("got %d results for a PVE 8.2 target; want 0 (no catalog entry applies below 9.0)", len(report.Results))
		}
		if report.Summary != (doctor.Summary{}) {
			t.Errorf("summary = %+v; want zero value", report.Summary)
		}
	})

	t.Run("target at BreaksAt fires both entries against affected facts", func(t *testing.T) {
		report := Run(Version{Major: 9, Minor: 0}, affected, time.Now(), "test")
		if len(report.Results) != len(Catalog) {
			t.Fatalf("got %d results; want %d (every catalog entry, all BreaksAt 9.0)", len(report.Results), len(Catalog))
		}
		if report.Summary.Warn != len(Catalog) {
			t.Errorf("summary.Warn = %d; want %d (every entry should fire against affected facts)", report.Summary.Warn, len(Catalog))
		}
		byCheck := make(map[string]doctor.Result, len(report.Results))
		for _, r := range report.Results {
			byCheck[r.Check] = r
		}
		for _, e := range Catalog {
			r, ok := byCheck[e.ID]
			if !ok {
				t.Errorf("no result for catalog entry %q", e.ID)
				continue
			}
			if r.Remediation == "" {
				t.Errorf("%s: fired with no remediation", e.ID)
			}
		}
		if err := report.Validate(); err != nil {
			t.Errorf("report fails its own invariants: %v", err)
		}
	})

	t.Run("unaffected facts stay silent (pass) even past BreaksAt", func(t *testing.T) {
		unaffected := Facts{
			ConntrackNetlinkProbed:    true,
			FirewallStateProbed:       true,
			FirewallEnabled:           true,
			NftablesEngineStateProbed: true,
			NftablesEngineActive:      true,
		}
		report := Run(Version{Major: 9, Minor: 2}, unaffected, time.Now(), "test")
		if report.Summary.Warn != 0 || report.Summary.Fail != 0 {
			t.Errorf("summary = %+v; want no warn/fail against unaffected facts", report.Summary)
		}
		if report.Summary.Pass != len(Catalog) {
			t.Errorf("summary.Pass = %d; want %d", report.Summary.Pass, len(Catalog))
		}
	})
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		v, other Version
		want     bool
	}{
		{Version{9, 0}, Version{9, 0}, true},
		{Version{9, 2}, Version{9, 0}, true},
		{Version{10, 0}, Version{9, 2}, true},
		{Version{8, 2}, Version{9, 0}, false},
		{Version{9, 0}, Version{9, 1}, false},
	}
	for _, tt := range tests {
		if got := tt.v.AtLeast(tt.other); got != tt.want {
			t.Errorf("%s.AtLeast(%s) = %v; want %v", tt.v, tt.other, got, tt.want)
		}
	}
}
