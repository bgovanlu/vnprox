// SPDX-License-Identifier: Apache-2.0

// Package upgradeadvisor implements T-4004: a checkable catalog of known
// network-affecting breaks between PVE versions, run as a preflight before
// an operator upgrades a node — so the T-3711 class of break ("the OS
// changed under a feature that still looks fine") is a check anyone can
// run, not tribal memory one engineer happens to remember.
//
// Design constraints, stated once here rather than per entry:
//
//   - **The catalog is data, not an if-chain.** Catalog is a []Entry; a
//     second entry is one more struct literal, never a restructuring of the
//     first (T-4004 AC1).
//   - **Every entry is sourced.** Entry.Evidence names the real commit,
//     task card, or evidence transcript the entry was written from —
//     mirroring internal/pvemock/compat_versions.go's own rule (see that
//     file's SDNFabrics doc comment for what happens when this rule is
//     skipped: a check that tests a changelog, not a real PVE). A guessed
//     entry is worse than no entry, because it will be believed.
//   - **Detection prefers a capability probe over a version-string
//     inference wherever a real probe exists.** ConntrackNetlinkErr below
//     is a live netlink dump attempt, not "PVE >= 9.0 therefore broken" —
//     see checks.go's checkConntrackProcfs.
//   - **This package never mutates anything.** Every Facts field is filled
//     from a read: a local file stat/read, or a capability probe that
//     itself performs no write. No internal/change op and no pvesh write
//     verb appears anywhere in this package or its caller
//     (cmd/vnproxctl/upgradecheckcmd.go) — T-4004 AC2.
//
// Output reuses internal/doctor's Result/Report/Status vocabulary rather
// than inventing a second one: this is the same "preflight, never applies
// anything, every warn/fail carries a remediation" surface doctor already
// established, and Run below produces a doctor.Report so `vnproxctl
// upgrade-check` renders and JSON-encodes exactly the way `vnproxctl
// doctor` already does.
package upgradeadvisor

import (
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/doctor"
)

// Version is a PVE release line, at the major.minor granularity
// internal/pvemock.PVEVersionProfile and docs/compatibility.md's matrix
// already use — the granularity at which this repository has ever
// observed (or been told about, and later corrected — see
// docs/compatibility.md's SDNFabrics history) a real API/behavior
// divergence. Patch versions (the ".4" in "9.2.4") are not modeled: no
// entry in this catalog is sourced to a patch-level change.
type Version struct {
	Major int
	Minor int
}

// String renders "Major.Minor".
func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// AtLeast reports whether v is the same as, or later than, other.
func (v Version) AtLeast(other Version) bool {
	if v.Major != other.Major {
		return v.Major > other.Major
	}
	return v.Minor >= other.Minor
}

// Facts is every host-observed signal this catalog's entries check
// against — the read-only counterpart of internal/doctor.Env/Facts,
// narrowed to what upgrade-advisor entries need. A caller
// (cmd/vnproxctl/upgradecheckcmd.go) that could not probe a given signal
// leaves its "Probed" field false; every Check below treats an unprobed
// signal as StatusSkip, never as a silent pass (internal/doctor's own
// "unknown is not pass" rule, restated here).
//
// Field order is densest-pointer-first (the error, then the string, then
// the bare bools): govet's fieldalignment measures bytes up to the final
// pointer, so a pointer-free field sitting above one costs alignment for
// nothing.
type Facts struct {
	// ConntrackNetlinkErr is the error the probe returned, or nil if it
	// succeeded. Only meaningful when ConntrackNetlinkProbed is true.
	ConntrackNetlinkErr error
	// ConntrackPathOverride is internal/host.Real.ConntrackPath's
	// configured value on this host, if any operator override is set —
	// empty means "no override; this host reads conntrack via netlink by
	// default" (T-3711).
	ConntrackPathOverride string
	// ConntrackNetlinkProbed reports whether a live netlink conntrack dump
	// was actually attempted on this host. False means "not probed" (e.g.
	// running vnproxctl on a non-Linux dev machine, or the probe was
	// skipped) — distinct from a probe that ran and succeeded.
	ConntrackNetlinkProbed bool
	// FirewallStateProbed reports whether FirewallEnabled below reflects a
	// real read of this node's firewall enable state (cluster.fw's
	// `enable` option) rather than a zero value.
	FirewallStateProbed bool
	// FirewallEnabled is PVE's datacenter/host firewall enable flag.  Only
	// meaningful when FirewallStateProbed is true.
	FirewallEnabled bool
	// NftablesEngineStateProbed reports whether NftablesEngineActive below
	// reflects a real read of this node's active firewall compile engine.
	NftablesEngineStateProbed bool
	// NftablesEngineActive reports whether the nftables tech-preview
	// engine (proxmox-firewall) is the engine that would actually compile
	// rules on this node right now — derived from the absence of
	// /run/proxmox-nftables-firewall-force-disable, the same flag file PVE
	// itself uses internally (see checks.go's doc comment and
	// planning/reports/evidence/pve-9.2.4-nftables-firewall-engine-2026-08-28.txt
	// §3). Only meaningful when NftablesEngineStateProbed is true.
	NftablesEngineActive bool
}

// Entry is one catalog row: a known network-affecting change between PVE
// versions, named, sourced, and checkable — the
// {fromVersionRange, toVersionRange, affects, check, remediation} shape
// T-4004's card asks for. FromVersionRange/ToVersionRange are the
// human-readable halves of that shape (rendered in docs and -o json
// output); BreaksAt is their machine-checkable distillation: the first PVE
// version (inclusive) at which the new/breaking behavior is present. Run
// below only evaluates an Entry when the operator's requested target
// version is at or beyond BreaksAt.
//
//nolint:govet // fieldalignment: readability (grouped by the card's own field order), not packing — this is a handful of catalog rows, not a hot struct.
type Entry struct {
	// ID is this entry's stable check name — reported as Result.Check, so
	// it is a wire contract once shipped (-o json), mirroring
	// internal/doctor's own check-name constants.
	ID string
	// Title is a one-line human name for the break, shown in docs and any
	// summary view.
	Title string
	// FromVersionRange describes, in prose, the PVE versions where the
	// prior (unaffected, or differently-affected) behavior held.
	FromVersionRange string
	// ToVersionRange describes, in prose, the PVE versions where the new
	// (breaking) behavior is present.
	ToVersionRange string
	// BreaksAt is ToVersionRange's lower bound, machine-checkable: Run
	// evaluates this entry iff the requested target version is at least
	// this version.
	BreaksAt Version
	// Affects names the vnprox feature/package this break touches.
	Affects string
	// Evidence lists the real, checked-in sources this entry was written
	// from: a task card, an evidence transcript, a commit. Never empty —
	// catalog_test.go enforces this.
	Evidence []string
	// Remediation is the operator-facing "what to do" — required whenever
	// Check can return StatusWarn or StatusFail (catalog_test.go enforces
	// non-empty).
	Remediation string
	// Check evaluates this entry against a host's live Facts and returns a
	// doctor.Result whose Check field must equal ID (catalog_test.go
	// enforces this too, so a copy-pasted entry cannot silently answer
	// under the wrong name).
	Check func(Facts) doctor.Result
}

// Catalog is the fixed, sourced set of known network-affecting PVE-version
// changes this package currently checks. Two entries today — see
// checks.go for both: the conntrack procfs→netlink break T-3711 fixed
// (the card's own motivating example) and the nftables/iptables firewall
// compile-engine split PVE 9 introduced (T-3904's evidence transcript). A
// third candidate — the SDN Fabrics API family
// (internal/pvemock/compat_versions.go's SDNFabrics) — was considered and
// rejected for this catalog: it is a new API surface, not a break in an
// existing one, and internal/pvemock/compat_versions.go plus
// docs/compatibility.md already track it. Duplicating it here would be a
// second place for that fact to drift out of sync with the first.
//
//nolint:gochecknoglobals // a read-only catalog table, the same shape internal/findings.allCheckNames and internal/doctor.AllChecks already use
var Catalog = []Entry{
	entryConntrackProcfs,
	entryNftablesEngineSplit,
}

// Run evaluates every catalog entry whose BreaksAt is at or before target
// against facts, and assembles a doctor.Report the same shape
// doctor.Run produces — reused, not reinvented, so `vnproxctl
// upgrade-check`'s rendering/JSON-encoding/exit-code logic is identical to
// `vnproxctl doctor`'s.
//
// An entry whose BreaksAt is beyond target is omitted entirely (not even a
// skip row): it names a change this particular upgrade path does not
// cross, and a report cluttered with "not applicable" rows for every
// future PVE release this catalog might one day track would bury the
// entries that matter.
func Run(target Version, facts Facts, now time.Time, binaryVersion string) doctor.Report {
	var results []doctor.Result
	for _, entry := range Catalog {
		if !target.AtLeast(entry.BreaksAt) {
			continue
		}
		results = append(results, entry.Check(facts))
	}
	return doctor.Report{
		GeneratedAt: now.UTC(),
		Version:     binaryVersion,
		Results:     results,
		Summary:     summarize(results),
	}
}

// summarize counts Result statuses — internal/doctor's own summarize is
// unexported, so this is a deliberate, tiny duplicate rather than a new
// export on that package for one caller.
func summarize(results []doctor.Result) doctor.Summary {
	var s doctor.Summary
	for _, r := range results {
		switch r.Status {
		case doctor.StatusPass:
			s.Pass++
		case doctor.StatusWarn:
			s.Warn++
		case doctor.StatusFail:
			s.Fail++
		case doctor.StatusSkip:
			s.Skip++
		}
	}
	return s
}
