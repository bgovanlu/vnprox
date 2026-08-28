// SPDX-License-Identifier: Apache-2.0

// Package telemetry implements T-2503: the opt-in compatibility report.
//
// One cluster validated by us is an anecdote. The compatibility matrix
// docs/status-matrix.md wants — which T-2501 check passes on which PVE
// version, kernel and NIC — cannot be produced from a single dev box, so
// this package exists to let an operator who wants to help send us the
// verdicts of a `vnproxctl verify` run and nothing else.
//
// Everything here is shaped by one fact: this is a privacy surface, and
// every promise it makes is a promise that something does NOT happen. A
// promise like that is trivially easy to assert vacuously, so each one is
// built as a mechanism rather than a rule:
//
//   - **Off is structural, not a default.** Nothing in this package reads
//     the store, builds a payload or constructs a request unless
//     Destination.Enabled is true AND an endpoint was named. vnprox ships no
//     default endpoint at all (internal/config.TelemetryConfig), so the "no
//     collector was contacted" claim does not rest on a boolean somebody
//     could flip in a fixture and forget.
//   - **Preview and send are the same bytes, by construction.** Build
//     marshals the payload exactly once into a Snapshot. `telemetry preview`
//     writes that buffer; Submit posts that buffer. There is no second
//     marshal anywhere in the package — a test reads this package's own
//     source and fails if one appears (snapshot_test.go), because "both
//     paths call the same function" is a property that survives exactly
//     until somebody adds a header, a wrapper or a pretty-printer to one
//     side.
//   - **The payload is checked, not trusted.** Guard runs over the marshalled
//     BYTES, not over the type: a `string` field named Kernel can hold a
//     hostname, and the point is that it fails rather than ships. Every send
//     re-runs it immediately before the request is built, so a Snapshot
//     assembled by any route other than Build still cannot leave.
//
// What is deliberately NOT in the payload: node names, the PVE endpoint,
// any address, any MAC, any guest, the cluster name, evidence bodies, and
// any timestamp of our own (the collector's receipt time is enough, and a
// local clock is a fingerprint). See docs/security.md, "Compatibility
// telemetry (T-2503)", which is compared field-by-field against the struct
// below on every `make check` (docs.go).
package telemetry

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/verify"
)

// PayloadVersion is the reduction's schema version. A collector that does
// not recognise it must say so rather than guess, the same contract
// verify.CurrentReportVersion carries for the artifact this reduces.
const PayloadVersion = 1

// Payload is the whole of what is sent. It is T-2501's report with
// everything identifying removed, not a report with a filter applied on the
// way out: the fields below are the only ones that exist.
//
// Adding a field here fails the build until it is also documented in
// docs/security.md (docs.go's TestDocSectionMatchesPayload) and allowed by
// name in guard.go's field allowlist. That is two independent gates, both
// deliberate: the first makes the operator-facing promise stay true, the
// second makes the payload closed rather than open.
//
// order, which a human reads in `telemetry preview` before any program does.
//
//nolint:govet // fieldalignment: this is the on-the-wire artifact's field
type Payload struct {
	// PayloadVersion is PayloadVersion.
	PayloadVersion int `json:"payloadVersion"`
	// InstallID is the locally generated ULID from the store — the only
	// correlator, resettable with `vnproxctl telemetry reset-id`. It is not
	// derived from anything about the machine: it is random, so two installs
	// that reset on the same day are indistinguishable.
	InstallID string `json:"installId"`
	// VnproxVersion is the vnproxctl build that produced the report.
	VnproxVersion string `json:"vnproxVersion"`
	// PVEVersion is what the cluster reported, e.g. "pve-manager/9.2.4".
	PVEVersion string `json:"pveVersion"`
	// Kernel is `uname -r` from the node the suite ran against.
	Kernel string `json:"kernel"`
	// NICPCIIDs are the PCI `vendor:device` ids of the NICs observed.
	//
	// The card asks for "NIC driver names". The report this reduces records
	// each NIC as `<ifname> <vendor>:<device> <modalias>` (verify's
	// collectNICModels), and the interface name in that line is a name we
	// have no business sending — `tap101i0` names a guest. So the reduction
	// keeps ONLY the vendor:device pair, which is the part the compatibility
	// matrix actually needs (it identifies the hardware a driver binds to)
	// and which structurally cannot carry a name: anything that does not
	// match pciIDPattern is dropped here and rejected by the guard.
	NICPCIIDs []string `json:"nicPciIds"`
	// NodeCount is how many cluster members the run saw. A count, never the
	// names — a three-node cluster and a thirty-node cluster read very
	// differently against a failed check, and neither needs an identity.
	NodeCount int `json:"nodeCount"`
	// Suite is which suite ran: "hardware", "multinode", "destructive", or
	// SelectionSuite when the operator ran an explicit --only list.
	Suite string `json:"suite"`
	// Checks is one entry per check, in report order.
	Checks []CheckVerdict `json:"checks"`
}

// CheckVerdict is one check's outcome, reduced to the three facts a
// compatibility matrix is built from. Notably absent: Detail, SkipReason and
// Evidence — all free text, all of which routinely quote a node name, an
// address or a command line.
type CheckVerdict struct {
	// ID is the registry id (verify.Check.ID).
	ID string `json:"id"`
	// Status is "pass", "fail" or "skip". A skip is carried because a matrix
	// that cannot tell "not tested here" from "tested and fine" is the exact
	// conflation T-2501 exists to prevent.
	Status string `json:"status"`
	// DurationMS is how long the check took. Useful for spotting a check that
	// has quietly become a timeout on somebody else's hardware.
	DurationMS int64 `json:"durationMs"`
}

// SelectionSuite is Payload.Suite for a run that was an explicit --only
// selection rather than a whole suite. Named rather than left empty so the
// collector never has to interpret a blank.
const SelectionSuite = "selection"

// ErrMockReport is returned by Build for a report produced against a mock
// PVE endpoint. Such a report is not hardware evidence — that is the whole
// argument behind verify's --allow-mock stamp — and a compatibility matrix
// polluted with runs against internal/pvemock would be worse than a smaller
// one, because it would look larger.
var ErrMockReport = fmt.Errorf("this report was produced against a mock PVE endpoint and is not compatibility evidence")

// pciIDPattern is the only shape NICPCIIDs may contain.
var pciIDPattern = regexp.MustCompile(`^0x[0-9a-f]{1,4}:0x[0-9a-f]{1,4}$`)

// nicFieldPattern picks the vendor:device pair out of one of verify's
// nicModels lines, wherever in the line it sits.
var nicFieldPattern = regexp.MustCompile(`\b0x[0-9a-fA-F]{1,4}:0x[0-9a-fA-F]{1,4}\b`)

// Reduce turns a T-2501 report into the payload.
//
// It is a projection, not a copy with omissions: it names each field it
// produces and reads nothing else, so a new field appearing in verify.Report
// (an evidence body, a node list, an endpoint) cannot arrive here by
// accident the way `json:"-"` tagging or a shared struct would allow.
func Reduce(rep verify.Report, installID string) Payload {
	suite := string(rep.Suite)
	if len(rep.Selection) > 0 {
		suite = SelectionSuite
	}

	checks := make([]CheckVerdict, 0, len(rep.Results))
	for _, res := range rep.Results {
		checks = append(checks, CheckVerdict{
			ID:         res.ID,
			Status:     string(res.Status),
			DurationMS: res.DurationMS,
		})
	}

	return Payload{
		PayloadVersion: PayloadVersion,
		InstallID:      installID,
		VnproxVersion:  rep.Environment.VnproxVersion,
		PVEVersion:     rep.Environment.PVEVersion,
		Kernel:         rep.Environment.Kernel,
		NICPCIIDs:      pciIDs(rep.Environment.NICModels),
		NodeCount:      len(rep.Environment.Nodes),
		Suite:          suite,
		Checks:         checks,
	}
}

// pciIDs extracts the vendor:device pairs from verify's nicModels lines,
// deduplicated and sorted. A line with no such pair contributes nothing:
// dropping a NIC we cannot identify safely is strictly better than sending
// a string we have not understood.
func pciIDs(models []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range models {
		for _, match := range nicFieldPattern.FindAllString(line, -1) {
			id := strings.ToLower(match)
			if !pciIDPattern.MatchString(id) || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
