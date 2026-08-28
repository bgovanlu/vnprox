// SPDX-License-Identifier: Apache-2.0

package main

// `vnproxctl upgrade-check <target-pve-version>` (T-4004): a preflight an
// operator runs before upgrading a node's PVE version. It evaluates
// internal/upgradeadvisor's sourced catalog of known network-affecting PVE
// breaks against this host's live, probed state and reports which entries
// would fire on the way to the named target — the same doctor.Report
// shape `vnproxctl doctor` already renders and JSON-encodes, so scripting
// against one works the same way against the other.
//
// Every fact this command gathers is a read: a local pmxcfs file read, a
// stat on a runtime flag file PVE itself maintains, or a live netlink
// conntrack dump (discarded, never written anywhere). No internal/change
// op and no pvesh write verb appears anywhere on this path — T-4004 AC2.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/upgradeadvisor"
)

// nftablesForceDisableFlagFile is the same runtime flag file PVE's own
// Firewall.pm maintains on every compile pass (see
// planning/reports/evidence/pve-9.2.4-nftables-firewall-engine-2026-08-28.txt
// §3): present means the legacy iptables engine is the one that would
// actually compile rules; absent means the nftables tech-preview engine
// is. Reading it is a stat, not a pvesh call — it works even when the
// daemon (and pveproxy) are down, matching doctor's own "works daemon-down"
// contract.
const nftablesForceDisableFlagFile = "/run/proxmox-nftables-firewall-force-disable"

// defaultPmxcfsDir mirrors doctor's own default (checkPmxcfs in
// internal/doctor/checks.go).
const defaultPmxcfsDir = "/etc/pve"

func runUpgradeCheck(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("upgrade-check", flag.ContinueOnError)
	fset.SetOutput(stderr)
	var (
		outputFmt = fset.String("o", defaultOutputFormat, outputFlagUsage)
		pmxcfsDir = fset.String("pmxcfs", defaultPmxcfsDir, "path to the mounted pmxcfs directory (for reading this node's firewall enable state)")
	)
	if err := fset.Parse(args); err != nil {
		return ExitUsage
	}
	asJSON, err := parseOutputFormat(*outputFmt)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl upgrade-check: %v\n", err)
		return ExitUsage
	}

	rest := fset.Args()
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "vnproxctl upgrade-check: exactly one target PVE version is required, e.g. vnproxctl upgrade-check 9.2\n")
		return ExitUsage
	}
	target, err := parseUpgradeTargetVersion(rest[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl upgrade-check: %v\n", err)
		return ExitUsage
	}

	ctx := context.Background()
	facts := collectUpgradeFacts(ctx, *pmxcfsDir)
	report := upgradeadvisor.Run(target, facts, time.Now(), version)

	// Mirrors doctor's own "a malformed report is a bug in a check, say so
	// loudly" handling.
	if err := report.Validate(); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl upgrade-check: internal error assembling the report: %v\n", err)
		return ExitError
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl upgrade-check: %v\n", err)
			return ExitError
		}
	} else {
		_, _ = fmt.Fprintf(stdout, "upgrade-check: catalog entries applicable to a PVE %s upgrade\n\n", target)
		_, _ = fmt.Fprint(stdout, report.Render())
	}

	// Same convention as doctor: non-zero iff a check FAILs. A warn (the
	// common case here — "this will bite you, go read the remediation")
	// does not gate, so this command is safe to run unattended in a
	// pre-upgrade script without aborting on every advisory note.
	if report.Failed() {
		return ExitError
	}
	return ExitSuccess
}

// parseUpgradeTargetVersion accepts "9", "9.2", or "9.2.4" (the patch
// component, if present, is accepted but ignored — internal/upgradeadvisor
// only ever models at major.minor granularity, matching
// internal/pvemock.PVEVersionProfile's own granularity).
func parseUpgradeTargetVersion(s string) (upgradeadvisor.Version, error) {
	parts := strings.SplitN(strings.TrimSpace(s), ".", 3)
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return upgradeadvisor.Version{}, fmt.Errorf("%q is not a PVE version (want e.g. \"9.2\")", s)
	}
	minor := 0
	if len(parts) > 1 {
		minor, err = strconv.Atoi(parts[1])
		if err != nil || minor < 0 {
			return upgradeadvisor.Version{}, fmt.Errorf("%q is not a PVE version (want e.g. \"9.2\")", s)
		}
	}
	return upgradeadvisor.Version{Major: major, Minor: minor}, nil
}

// collectUpgradeFacts gathers every live signal internal/upgradeadvisor's
// catalog entries check against. Anything that cannot be read leaves its
// "Probed" field false, so the corresponding entry reports Skip rather
// than a false pass or a false warn (internal/doctor's "unknown is not
// pass" rule, restated for this command).
func collectUpgradeFacts(ctx context.Context, pmxcfsDir string) upgradeadvisor.Facts {
	var f upgradeadvisor.Facts

	// Conntrack netlink capability (T-4004's own instruction: probe the
	// capability, don't infer from a kernel/PVE version string). A real
	// dump attempt, discarded — read-only, and the same call
	// internal/host.Real.Conntrack makes in production (netlink_linux.go).
	if _, err := host.NewReal().Conntrack(ctx, ""); err != nil {
		if errors.Is(err, host.ErrUnsupportedPlatform) {
			// Not probed: this build of vnproxctl is not running on the
			// Linux host it would be checking (e.g. a contributor's
			// non-Linux dev machine, or CI). Leave ConntrackNetlinkProbed
			// false rather than reporting a false warn.
		} else {
			f.ConntrackNetlinkProbed = true
			f.ConntrackNetlinkErr = err
		}
	} else {
		f.ConntrackNetlinkProbed = true
	}

	if enabled, ok := readFirewallEnabled(pmxcfsDir); ok {
		f.FirewallStateProbed = true
		f.FirewallEnabled = enabled
	}
	if active, ok := readNftablesEngineActive(nftablesForceDisableFlagFile); ok {
		f.NftablesEngineStateProbed = true
		f.NftablesEngineActive = active
	}

	return f
}

// readFirewallEnabled reads pmxcfsDir/firewall/cluster.fw's [OPTIONS]
// `enable` value directly — the same file
// planning/reports/evidence/pve-9.2.4-nftables-firewall-engine-2026-08-28.txt
// §2 captured verbatim (`cat /etc/pve/firewall/cluster.fw` -> `enable: 0`).
// A `.fw` file's OPTIONS block is "key: value" per line, one option per
// line — no nesting and no quoting to worry about, so a linear scan for
// the "enable" key is exact, not a best-effort parse.
//
// ok is false only when the file could not be read at all (no pmxcfs, no
// permission). A file that exists but has no explicit `enable` line
// reports enabled=false: that is PVE's own documented default for a fresh
// cluster.fw (confirmed at
// pvesh get /cluster/firewall/options -> {"enable":0} absent any override,
// same evidence file §2).
func readFirewallEnabled(pmxcfsDir string) (enabled bool, ok bool) {
	data, err := os.ReadFile(filepath.Join(pmxcfsDir, "firewall", "cluster.fw"))
	if err != nil {
		return false, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, val, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "enable") {
			v := strings.TrimSpace(val)
			return v != "" && v != "0", true
		}
	}
	return false, true
}

// readNftablesEngineActive reports whether the nftables tech-preview
// engine (proxmox-firewall) is the engine that would actually compile
// rules on this node right now, from flagFile's presence/absence — the
// exact same signal PVE::Firewall::Helpers::is_nftables() reads, per the
// evidence transcript's §3 source excerpt. flagFile is
// nftablesForceDisableFlagFile in production; a parameter here only so
// tests can point it at a temp path instead of the real /run. This is a
// capability read, not a version inference: on a node where
// proxmox-firewall has never run an update pass (freshly installed,
// package not present), the flag file's absence is not distinguishable
// from "nftables is active" by this stat alone — a known limitation,
// stated rather than hidden, and one more reason this entry's Check only
// ever warns (never fails).
func readNftablesEngineActive(flagFile string) (active bool, ok bool) {
	_, err := os.Stat(flagFile)
	switch {
	case err == nil:
		return false, true
	case os.IsNotExist(err):
		return true, true
	default:
		return false, false
	}
}
