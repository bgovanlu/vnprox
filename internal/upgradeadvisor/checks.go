// SPDX-License-Identifier: Apache-2.0

package upgradeadvisor

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/doctor"
)

// --- entry 1: conntrack procfs -> netlink (T-3711) --------------------------

const checkConntrackProcfsID = "upgrade_conntrack_procfs_removed"

const conntrackProcfsRemediation = "vnprox itself already reads conntrack via netlink by default " +
	"(internal/host/netlink_linux.go's Real.Conntrack, T-3711) and needs no operator action for that " +
	"alone. This fires only when this host's own conntrack read currently depends on procfs — either " +
	"an explicit Real.ConntrackPath override pointing at a text-format table, or the live netlink " +
	"capability probe itself failing (commonly a missing CAP_NET_ADMIN). In the second case the same " +
	"read already needs attention regardless of PVE version: grant CAP_NET_ADMIN to vnproxd (the " +
	"packaged systemd unit already does) or remove the ConntrackPath override before upgrading, since " +
	"PVE 9 kernels remove the /proc/net/nf_conntrack fallback that could otherwise mask the gap. " +
	"Background: planning/tasks/T-3711-conntrack-procfs.md, " +
	"planning/reports/evidence/T-3711-conntrack-netlink-lab-2026-08-25.txt."

// entryConntrackProcfs is T-4004's motivating example, restated as a
// catalog row: PVE 9 kernels ship CONFIG_NF_CONNTRACK_PROCFS=n, so
// /proc/net/nf_conntrack does not exist even though the nf_conntrack
// module is loaded and the netlink interface works fine. T-3711 fixed
// vnprox's own default read path to use netlink instead — this entry
// exists for the two ways a host can still be exposed: an operator's
// explicit procfs-path override, or a netlink capability that is itself
// broken (which the vanished procfs fallback would otherwise have masked).
//
// Sourced from planning/tasks/T-3711-conntrack-procfs.md and
// planning/reports/evidence/T-3711-conntrack-netlink-lab-2026-08-25.txt —
// both a real kernel config read (`grep NF_CONNTRACK_PROCFS
// /boot/config-$(uname -r)`) and a real `ls /proc/net/nf_conntrack`
// failure, captured on pvecube (PVE 9.2.4) and a nested lab node.
var entryConntrackProcfs = Entry{
	ID:    checkConntrackProcfsID,
	Title: "conntrack/NAT reads depend on procfs, which PVE 9 kernels remove",
	FromVersionRange: "PVE <= 8.x (kernel ships CONFIG_NF_CONNTRACK_PROCFS=y; " +
		"/proc/net/nf_conntrack exists whenever nf_conntrack is loaded)",
	ToVersionRange: "PVE >= 9.0 (kernel ships CONFIG_NF_CONNTRACK_PROCFS=n; /proc/net/nf_conntrack " +
		"does not exist, even though the netlink conntrack interface still works)",
	BreaksAt: Version{Major: 9, Minor: 0},
	Affects: "the conntrack/NAT explorer (GET /api/v1/conntrack, internal/host.Reader.Conntrack) " +
		"and T-1004's host-local flow sampler (internal/flow/hostsample)",
	Evidence: []string{
		"planning/tasks/T-3711-conntrack-procfs.md",
		"planning/reports/evidence/T-3711-conntrack-netlink-lab-2026-08-25.txt",
	},
	Remediation: conntrackProcfsRemediation,
	Check:       checkConntrackProcfs,
}

// checkConntrackProcfs probes the actual netlink conntrack capability
// (Facts.ConntrackNetlinkProbed/Err, filled by a real dump attempt in
// cmd/vnproxctl/upgradecheckcmd.go) rather than inferring from a PVE
// version string — T-4004's own instruction ("probe the capability, don't
// infer from kernel version"), and the reason this check would have
// caught T-3711's actual defect: the module being loaded and the
// interface being reachable are two different facts, and only a live dump
// attempt tells them apart.
func checkConntrackProcfs(f Facts) doctor.Result {
	if f.ConntrackPathOverride != "" {
		return doctor.Result{
			Check:  checkConntrackProcfsID,
			Status: doctor.StatusFail,
			Detail: fmt.Sprintf("this host's conntrack read is explicitly overridden to a "+
				"text-format procfs path (%s); PVE 9 kernels ship CONFIG_NF_CONNTRACK_PROCFS=n, "+
				"so that path will not exist after upgrading", f.ConntrackPathOverride),
			Remediation: conntrackProcfsRemediation,
		}
	}
	if !f.ConntrackNetlinkProbed {
		return doctor.Result{
			Check:  checkConntrackProcfsID,
			Status: doctor.StatusSkip,
			Detail: "netlink conntrack capability was not probed on this host",
		}
	}
	if f.ConntrackNetlinkErr != nil {
		return doctor.Result{
			Check:  checkConntrackProcfsID,
			Status: doctor.StatusWarn,
			Detail: fmt.Sprintf("netlink conntrack capability probe failed on this host (%v); "+
				"PVE 9 removes the /proc/net/nf_conntrack fallback that could otherwise mask this, "+
				"so conntrack/NAT reads would stop working entirely after upgrading", f.ConntrackNetlinkErr),
			Remediation: conntrackProcfsRemediation,
		}
	}
	return doctor.Result{
		Check:  checkConntrackProcfsID,
		Status: doctor.StatusPass,
		Detail: "netlink conntrack capability probe succeeded; this host does not depend on " +
			"/proc/net/nf_conntrack (T-3711), so PVE 9's procfs removal will not affect conntrack/NAT reads",
	}
}

// --- entry 2: nftables/iptables firewall compile-engine split (T-3904) -----

const checkNftablesEngineSplitID = "upgrade_nftables_iptables_engine_split"

const nftablesEngineSplitRemediation = "vnprox's compiled-ruleset inspector (GET /firewall/compiled, " +
	"internal/host/nftables.go) only ever reads the nftables engine's tables (`nft -j list ruleset`) " +
	"— it never reads iptables-save. On a default PVE 9+ install the datacenter firewall's `nftables` " +
	"option is off (tech preview), so pve-firewall compiles to iptables and GET /firewall/compiled " +
	"correctly, but confusingly, returns an empty ruleset even while rules are actively enforced. " +
	"Either set `nftables: 1` under this node's host.fw [OPTIONS] to get inspector visibility, or " +
	"cross-check enforcement directly on the node (`iptables-save`, `pve-firewall status`) rather than " +
	"reading an empty compiled-ruleset response as \"no rules\". Background: " +
	"planning/reports/evidence/pve-9.2.4-nftables-firewall-engine-2026-08-28.txt."

// entryNftablesEngineSplit is T-3904's finding, restated as a catalog row:
// PVE 9.2.4 ships two firewall compile engines side by side — the legacy
// Perl/iptables engine (pve-firewall) and a newer Rust/nftables engine
// (proxmox-firewall), selected by a per-host `nftables` boolean in
// host.fw's [OPTIONS] block, default off and labeled "tech preview" by
// PVE's own schema. vnprox's compiled-ruleset inspector reads only
// nftables output, so on the (default) configuration where the firewall
// is on but the nftables option is off, the inspector reports an empty
// ruleset even though iptables is actively enforcing rules — a "looks
// fine, isn't" gap of exactly the shape this card exists to catalog.
//
// Sourced from
// planning/reports/evidence/pve-9.2.4-nftables-firewall-engine-2026-08-28.txt,
// captured read-only against pvecube (PVE 9.2.4): §1 confirms both
// pve-firewall (iptables) and proxmox-firewall (nftables) installed and
// running as independent systemd units; §3 reads PVE::Firewall.pm's own
// source to show `nftables` defaults to 0 and is gated by
// /run/proxmox-nftables-firewall-force-disable, the same flag file this
// entry's Check probes for.
//
// BreaksAt is PVE 9.0 by inference, not direct observation: this
// repository has no PVE 8.x hardware to confirm proxmox-firewall's
// absence there (CLAUDE.md's two-node cluster is both PVE 9.2.4), so the
// boundary rests on proxmox-firewall's own changelog (captured in the
// evidence file's §5), whose earliest entry (1.1.2, Aug 2025) predates no
// PVE 8.x release this project has seen documented. Stated here rather
// than left implicit, per CLAUDE.md's "say so, don't guess" rule.
var entryNftablesEngineSplit = Entry{
	ID:    checkNftablesEngineSplitID,
	Title: "PVE 9 ships a second, nftables-based firewall engine alongside the legacy iptables one",
	FromVersionRange: "PVE <= 8.x (pve-firewall's iptables engine is the only firewall compiler; " +
		"the proxmox-firewall package/engine is not observed to exist — inferred from its changelog, " +
		"not confirmed on real 8.x hardware, see this entry's doc comment)",
	ToVersionRange: "PVE >= 9.0 (proxmox-firewall's nftables engine ships alongside pve-firewall's " +
		"iptables engine; selected by host.fw's `nftables` option, default off / tech preview)",
	BreaksAt: Version{Major: 9, Minor: 0},
	Affects:  "the compiled-ruleset inspector (GET /firewall/compiled, internal/host/nftables.go, internal/api/nftables.go)",
	Evidence: []string{
		"planning/reports/evidence/pve-9.2.4-nftables-firewall-engine-2026-08-28.txt",
	},
	Remediation: nftablesEngineSplitRemediation,
	Check:       checkNftablesEngineSplit,
}

// checkNftablesEngineSplit probes two live signals — the datacenter
// firewall's enable flag and whether the nftables engine is the one that
// would actually compile rules right now (from the force-disable flag
// file's absence/presence, not from a version string) — and fires only in
// the specific combination that is actually misleading: firewall on,
// nftables engine not the active one. Both other combinations (firewall
// off; or firewall on with nftables already active) leave the compiled-
// ruleset inspector's read unambiguous, so they pass rather than warn.
func checkNftablesEngineSplit(f Facts) doctor.Result {
	if !f.FirewallStateProbed || !f.NftablesEngineStateProbed {
		return doctor.Result{
			Check:  checkNftablesEngineSplitID,
			Status: doctor.StatusSkip,
			Detail: "the datacenter firewall's enable state and/or its active compile engine were not probed on this host",
		}
	}
	if !f.FirewallEnabled {
		return doctor.Result{
			Check:  checkNftablesEngineSplitID,
			Status: doctor.StatusPass,
			Detail: "the datacenter firewall is disabled on this host, so the compiled-ruleset inspector's empty result is unambiguous",
		}
	}
	if f.NftablesEngineActive {
		return doctor.Result{
			Check:  checkNftablesEngineSplitID,
			Status: doctor.StatusPass,
			Detail: "this host already runs the nftables tech-preview engine; the compiled-ruleset inspector's read matches what PVE actually enforces",
		}
	}
	return doctor.Result{
		Check:  checkNftablesEngineSplitID,
		Status: doctor.StatusWarn,
		Detail: "the datacenter firewall is enabled and this host compiles to the legacy iptables " +
			"engine (nftables tech preview is off) — the compiled-ruleset inspector (GET " +
			"/firewall/compiled) will report an empty ruleset here even though rules ARE being enforced via iptables",
		Remediation: nftablesEngineSplitRemediation,
	}
}
