// SPDX-License-Identifier: Apache-2.0

package explain

import "github.com/bgovanlu/vnprox/internal/findings"

// findingRegistry is the per-check template table: one entry per check name
// findings.AllCheckNames() can emit (internal/findings/catalog.go), grouped
// by the same producing-package sections that catalog.go itself uses, in
// the same order, so a reader diffing the two files can follow along line
// for line. TestCoverage_AllCatalogChecksHaveExplainerOrExemption in
// findings_test.go is the gate: every entry in catalog.go's allCheckNames
// must appear as a key here or in exemptions.
//
// SOURCING (2026-08-28, per CLAUDE.md's rule against inventing plausible-
// looking behavior): every What/Why/Remedy below is drawn from the
// producing file's own doc comment or check logic — internal/findings'
// health_*.go/adapt_*.go files, internal/drift/{types,reconcile,doc}.go,
// internal/certs/checks.go, internal/gitsync/service.go,
// internal/capacity/{types,forecast}.go, internal/ipam/merge.go,
// internal/topology/vlancheck.go, internal/k8s/nodeport.go, and
// cmd/vnproxd/{tenant,automation,findings}.go for the three
// composition-root literal checks — never guessed from the check's name
// alone.
//
// Three checks (orphan_vnet, fw_rule_unused, trunk_unused_vlans) leave
// Remedy empty on purpose: internal/runbook/catalog.go already carries a
// built-in runbook for each, and Explain (findings.go) points at the
// runbook by name/title instead — see that file's doc comment for why
// restating the runbook's remediation here would be the exact duplication
// the task card forbids.
//
//nolint:gochecknoglobals // a read-only template table, the same shape internal/findings.allCheckNames and internal/runbook.Runbooks() already are
var findingRegistry = map[string]findingTemplate{
	// --- internal/ipam (literal-declared, mirroring catalog.go) ----------
	"allocated_dark": {
		What: "PVE's IPAM records this address as reserved for a guest, but no currently-known guest (by VMID or MAC) claims it — the allocation has gone dark.",
		Why:  "A reservation nobody is using still blocks the address from being handed out, silently shrinking the usable pool.",
		Remedy: "Release the address if the guest was decommissioned, or verify the guest still exists under a different VMID/MAC " +
			"before reclaiming it.",
	},
	"duplicate_ip": {
		What:   "Two or more guests are independently using the same IP address, or one guest's IPAM allocation record disagrees with what's actually observed on the wire for that address.",
		Why:    "A shared address means at least one guest is unreachable, or is silently receiving traffic meant for the other.",
		Remedy: "Reassign all but one guest to its own address, then reserve this one for the guest that should keep it.",
	},
	"observed_unallocated": {
		What:   "An address is actively in use on the network but has no matching IPAM reservation.",
		Why:    "IPAM no longer reflects reality, so the next allocation could hand this same address to a different guest.",
		Remedy: "Reserve the address for the guest currently using it to bring IPAM back in sync with reality.",
	},

	// --- literal-declared at the composition root ------------------------
	"approval_pending": {
		What:   "A tenant's requested changeset is waiting on one of its designated approvers before it can proceed.",
		Why:    "An approval nobody has seen blocks a legitimate change from moving forward.",
		Remedy: "One of the named approvers (this finding's Nodes) should review and approve or reject the referenced changeset.",
	},
	"k8s_nodeport_exposed_without_fw_rule": {
		What: "A Kubernetes NodePort or LoadBalancer service's port has no explicit, enabled PVE firewall allow rule covering it " +
			"on the backing guest's own ruleset or the cluster ruleset.",
		Why: "Whether the port is actually reachable depends on PVE's own default policy, which this check doesn't evaluate — " +
			"what it flags is that nothing documents an intentional opening for it.",
		Remedy: "Add an explicit firewall rule for the port if it's meant to be reachable, or close it at the Kubernetes Service " +
			"level if it isn't.",
	},
	"sim_divergence": {
		What: "The path simulator's computed verdict for a source/destination/protocol/port tuple disagrees with what was " +
			"actually observed on the wire.",
		Why: "A simulated allow that's actually unreachable, or a simulated deny that's actually reachable, means the firewall " +
			"model vnprox reasons from doesn't match production.",
		Remedy: "Open the path simulator at this finding's link to re-trace the exact tuple and determine which side — the " +
			"simulated verdict or the live observation — is wrong.",
	},
	"webhook_unhealthy": {
		What: "A configured webhook has failed several consecutive delivery attempts.",
		Why:  "Whatever automation depends on that webhook firing is silently not happening.",
		Remedy: "Check the webhook's target endpoint and its last recorded delivery error, then fix the endpoint or the webhook " +
			"configuration — the finding clears automatically on the next successful delivery.",
	},

	// --- constant-declared (guarded by findings_test.go's coverage gate) -
	findings.CheckArpSpoofSuspected: {
		What: "An IP address has been seen claimed by more than one MAC address within a short trailing window — classic ARP/ND spoofing behavior.",
		Why:  "This is a live security event: something may be intercepting or hijacking traffic addressed to that IP.",
		Remedy: "Investigate immediately — identify which MAC is legitimate and which device is spoofing, then isolate or block " +
			"the rogue device at the switch.",
	},
	findings.CheckBondSlaveDown: {
		What: "A member interface of a bond has lost link (MII/carrier down).",
		Why:  "The bond is running with reduced, or zero, redundancy until the member comes back.",
		Remedy: "Check the cable, the NIC, and the switch port for the down member — this is a physical/administrative fix " +
			"vnprox cannot compute or apply.",
	},
	"bridge_divergence": {
		What: "The same-named bridge is configured differently — VLAN-awareness, VID set, or another property — on different " +
			"nodes in the cluster.",
		Why: "A guest or service migrating between nodes can silently land on a bridge that behaves differently from the one it left.",
		Remedy: "vnprox may be able to propose a fixing changeset that harmonizes the bridge's properties across nodes — check " +
			"this finding's fix action before editing the mismatched nodes by hand.",
	},
	findings.CheckBridgeNoCarrier: {
		What: "A bridge declares one or more uplink ports — directly, or through an enslaving bond — and every one of them " +
			"currently reports no carrier.",
		Why: "The bridge itself is up, but it has no working path off the node; anything attached to it is isolated. A bridge " +
			"with no configured uplink at all is never flagged, so this always reflects a real link problem.",
		Remedy: "Check the uplink NIC(s) and their cabling/switch ports.",
	},
	"capacity_ipam_forecast": {
		What: "The linear trend fitted over an IPAM pool's recent daily utilization projects it crossing 100% within the " +
			"forecast horizon.",
		Why:    "Running out of addresses in a pool blocks new guest provisioning on that subnet, with no other warning.",
		Remedy: "Grow the subnet or reclaim unused allocations before the projected exhaustion date.",
	},
	"capacity_link_forecast": {
		What: "The linear trend fitted over a link's recent daily peak utilization projects it crossing 100% within the " +
			"forecast horizon.",
		Why: "A link running out of headroom degrades every guest and service riding it; this exists so that's planned rather " +
			"than discovered as an outage.",
		Remedy: "Plan additional capacity — a bigger uplink, a second bond member, or traffic rebalancing — before the projected date.",
	},
	"cert_ca_mismatch": {
		What: "A node's certificate was issued by a different CA than the rest of the cluster expects.",
		Why:  "Peer/cluster trust assumes one CA; a mismatched CA breaks that assumption silently until something tries to validate it.",
		Remedy: "Reissue the certificate from the cluster's own CA — vnprox detects certificate problems but does not renew or " +
			"reissue certificates itself.",
	},
	"cert_expired": {
		What: "A certificate has passed its expiry date.",
		Why:  "An expired certificate breaks TLS verification for anything that dials this endpoint.",
		Remedy: "Reissue and install a new certificate — vnprox detects certificate problems but does not renew or reissue " +
			"certificates itself.",
	},
	"cert_expiring": {
		What:   "A certificate will expire within the configured warning window.",
		Why:    "The same failure as cert_expired, caught early enough to fix before it becomes an outage.",
		Remedy: "Reissue and install a replacement certificate before it expires.",
	},
	"cert_missing": {
		What:   "A cluster member vnprox expected to have a certificate on record does not have one.",
		Why:    "Missing coverage means that node's TLS posture cannot be verified at all.",
		Remedy: "Issue and install a certificate for the node.",
	},
	"cert_not_chained": {
		What:   "A certificate does not chain to the cluster's trusted CA.",
		Why:    "Anything validating against the pinned CA will reject the connection.",
		Remedy: "Install a certificate issued by, or chained to, the cluster CA.",
	},
	"cert_san_mismatch": {
		What:   "A certificate's Subject Alternative Names don't cover the address it's actually being served on.",
		Why:    "A client that validates the hostname will refuse the connection.",
		Remedy: "Reissue the certificate with the correct SANs, or fix the address being dialed to match an existing SAN.",
	},
	"cert_unreadable": {
		What:   "A certificate file exists but could not be parsed or read.",
		Why:    "An unreadable certificate is as good as missing for verification purposes, and usually indicates corruption or a permissions problem.",
		Remedy: "Check the certificate file's permissions and contents; reissue it if it's corrupted.",
	},
	"cert_weak_key": {
		What:   "A certificate's key is below the minimum accepted strength (RSA-2048, or ECDSA P-256).",
		Why:    "A weak key is crackable and shouldn't be trusted for cluster communication.",
		Remedy: "Reissue the certificate with a key at or above the minimum strength.",
	},
	findings.CheckBreakGlass: {
		What: "An emergency break-glass override let a changeset in a protected operation class apply without its required " +
			"number of distinct approvers.",
		Why: "This bypassed the multi-approver safety gate — every use has to be visibly reviewed, which is why this finding " +
			"cannot be acknowledged for 24 hours after it fires.",
		Remedy: "Review who invoked the override, why, and what the changeset did. The 24-hour ack lock is deliberate: the " +
			"person who invoked it cannot silence this finding on their way out.",
	},
	findings.CheckCorosyncLinkDegraded: {
		What: "A corosync ring is reporting faulty or no-link status right now, even though its configured address is correct.",
		Why:  "A degraded ring reduces cluster-communication redundancy; if every ring fails, the cluster loses quorum communication.",
		Remedy: "Check the ring's cable, switch port, and knet transport — this is a physical/administrative fix vnprox " +
			"cannot compute.",
	},
	findings.CheckDualstackDrift: {
		What:   "An SDN VNet with both an IPv4 and an IPv6 subnet allows outbound reachability on one address family but not the other.",
		Why:    "This is the classic silent dual-stack failure: users on the working family notice nothing wrong while users on the broken one are quietly cut off.",
		Remedy: "Compare the two families' SNAT/exit-node/gateway configuration for this VNet and fix whichever one diverges.",
	},
	findings.CheckErrorDropRate: {
		What:   "An interface's sampled error or drop rate has crossed the configured threshold and held.",
		Why:    "Sustained errors/drops degrade throughput and can indicate a failing NIC, a duplex mismatch, or a saturated link.",
		Remedy: "Check the interface's physical layer (cabling, duplex/speed negotiation) and whether the link is oversubscribed.",
	},
	findings.CheckEvpnGwInconsistency: {
		What:   "An EVPN zone's anycast subnet gateway is realized on some of the zone's member nodes but not others (or with a different address).",
		Why:    "A guest attached on a node missing the gateway loses its default route out of the segment.",
		Remedy: "Reapply or investigate the SDN configuration on the nodes missing the gateway.",
	},
	"file_runtime_divergence": {
		What: "The interfaces file (what will apply on the next reload) and the live kernel state (netlink — what's running " +
			"right now) disagree.",
		Why: "Usually means someone edited the file by hand or ran `ip` commands directly, outside vnprox — the two will snap " +
			"back into sync on the next reload, in whichever direction is wrong.",
		Remedy: "Decide which side is correct, then either stage a changeset to match the live state, or reload networking " +
			"to match the file.",
	},
	findings.CheckFwRuleUnused: {
		What: "An enabled firewall rule, reachable in some known guest's current resolved evaluation order, has recorded " +
			"zero hits over the configured window (default 30 days).",
		Why: "A rule that never matches is either dead weight or a sign the intended traffic isn't flowing the way it should.",
	},
	"gitsync_commit_unsigned": {
		What:   "The commit at the git sync target has no signature.",
		Why:    "Unsigned commits break the chain of trust the GitOps sync is meant to provide.",
		Remedy: "Require signed commits on the branch/tag vnprox syncs from, and push a signed commit.",
	},
	"gitsync_divergence": {
		What: "The live cluster state disagrees with the pinned declarative spec.",
		Why:  "Something changed the network outside the spec-driven flow, or the spec hasn't caught up with an intentional change.",
		Remedy: "Reconcile: stage a changeset to bring live state back to the spec, or update and commit the spec to match an " +
			"intentional live change.",
	},
	"gitsync_signature_unverifiable": {
		What:   "The sync target's commit is signed, but the signature could not be verified against a trusted key.",
		Why:    "An unverifiable signature carries the same risk as no signature — vnprox cannot confirm who actually wrote the commit.",
		Remedy: "Make sure the signer's key is available to the verification keyring, or re-sign the commit with a trusted key.",
	},
	"gitsync_spec_unparseable": {
		What:   "The document at the pinned commit does not parse as a valid spec.",
		Why:    "A broken spec document blocks reconciliation from advancing past it.",
		Remedy: "Fix the spec document at the source and push a corrected commit.",
	},
	"gitsync_unreachable": {
		What:   "The configured git remote for the declarative spec could not be reached.",
		Why:    "Spec sync is stalled until the remote is reachable again; the last-known-good pin keeps being used in the meantime.",
		Remedy: "Check network connectivity and credentials to the git remote.",
	},
	findings.CheckHAReplicationDegraded: {
		What: "The active daemon's state replication to its HA standby failed on the last push, or the standby is lagging " +
			"past the configured threshold.",
		Why:    "A standby that's out of sync would be stale if promoted, undermining the whole point of having one.",
		Remedy: "Investigate the replication link and the standby's own health; there is no single automated fix.",
	},
	findings.CheckLACPPartnerMismatch: {
		What: "An 802.3ad bond's members disagree about the LACP partner's system ID/key, or one member's actor state isn't " +
			"fully negotiated (synchronized+collecting+distributing).",
		Why: "This is split-brain aggregation: the members may actually be talking to different physical switches, which can " +
			"silently blackhole or duplicate traffic.",
		Remedy: "Check the switch-side LACP configuration for each bond member's port — this is a physical/administrative " +
			"fix vnprox cannot compute.",
	},
	findings.CheckMgmtSinglePath: {
		What: "A node's management path — the interface(s) ultimately carrying its management IP or corosync links — has " +
			"fewer than two link-up physical NICs backing it.",
		Why:    "A single point of failure on the management path means one link loss cuts vnprox, and possibly corosync, off from that node.",
		Remedy: "Use the management-redundancy wizard to add a second physical uplink to the management bridge/bond.",
	},
	"mtu_consistency": {
		What: "MTU is inconsistent along an L2 path (NIC→bond→bridge) on one node, or across same-named bridges cluster-wide.",
		Why:  "An MTU mismatch causes silent packet drops or fragmentation for traffic that happens to hit the smaller MTU.",
		Remedy: "vnprox may be able to propose a fixing changeset that aligns the mismatched MTU — check this finding's fix " +
			"action before editing the mismatched interfaces by hand.",
	},
	findings.CheckNeighborBindingFlap: {
		What: "A MAC-to-IP binding has changed repeatedly — IP churn on one MAC, or one MAC claiming many IPs — over the " +
			"persisted binding history.",
		Why: "Frequent rebinding can indicate a misconfigured DHCP client, a moving VM, or address spoofing; the persisted " +
			"history catches patterns a single live poll would miss.",
		Remedy: "Review the binding history for the affected MAC/IP and confirm whether the churn is expected (DHCP renewal, " +
			"live migration) or not.",
	},
	findings.CheckNewPort: {
		What:   "Recent flow traffic includes a destination port for this guest/segment that its learned baseline has never seen before.",
		Why:    "A new listening port can be a legitimate service change — or a sign of compromise or misconfiguration.",
		Remedy: "Confirm the new port corresponds to an intentional service change; investigate the guest if it doesn't.",
	},
	findings.CheckNewSubnet: {
		What:   "Recent flow traffic touches a subnet this guest/segment's learned baseline has never seen before.",
		Why:    "A new destination outside the established pattern is worth a second look, especially for a segment meant to be isolated.",
		Remedy: "Confirm the new destination is expected; investigate the guest if it isn't.",
	},
	findings.CheckOrphanVnet: {
		What: "An SDN VNet's zone no longer resolves to any known SDN zone — the zone was deleted out from under it.",
		Why:  "The VNet cannot realize on any node while its zone is gone, and anything still attached to it is silently broken.",
	},
	findings.CheckPtrMismatch: {
		What: "A forward (A/AAAA) DNS record's address has a PTR record in the corresponding reverse zone, but the PTR's " +
			"target does not match the forward record's own FQDN.",
		Why: "A stale or dangling PTR means reverse lookups for this address resolve to the wrong name — tools and logs that " +
			"rely on reverse DNS will misidentify what's at this address.",
		Remedy: "Update the PTR record to point at the forward record's current FQDN, or remove it if the address no longer " +
			"belongs to that name.",
	},
	findings.CheckPtrMissing: {
		What: "A forward (A/AAAA) DNS record has no matching PTR record at all in its corresponding reverse zone — a zone " +
			"vnprox does manage a config entry for.",
		Why:    "Missing reverse DNS breaks anything that relies on reverse lookups (logging, some auth checks, troubleshooting).",
		Remedy: "Add a PTR record for the address pointing at the forward record's FQDN.",
	},
	findings.CheckPtrZoneUnreadable: {
		What: "A reverse DNS zone vnprox has a config entry for, and that at least one known forward record's address falls " +
			"inside, contributed zero records on the last poll — indistinguishable from \"genuinely empty\" without a real read.",
		Why: "This is deliberately reported as \"unknown\" rather than as a missing-PTR finding: collapsing it into " +
			"ptr_missing would be a false finding the moment the real cause is a transient PowerDNS read failure, not an " +
			"actually-empty zone.",
		Remedy: "Check connectivity and credentials to the PowerDNS instance backing this reverse zone, then wait for the " +
			"next poll to confirm whether records are actually present.",
	},
	findings.CheckPathLatencyDegraded: {
		What:   "A node-to-node link's rolling RTT has crossed the configured latency threshold and held.",
		Why:    "Sustained elevated latency degrades anything sensitive to it — corosync, live migration, replicated storage.",
		Remedy: "Investigate the physical/network path between the two nodes for congestion or a degraded link.",
	},
	findings.CheckPathLoss: {
		What:   "A node-to-node link's rolling packet loss has crossed the configured threshold and held.",
		Why:    "Sustained loss causes retransmits and can push cluster services (corosync, HA) toward instability.",
		Remedy: "Investigate the physical/network path between the two nodes for a failing link or congestion.",
	},
	findings.CheckPeerTrustDegraded: {
		What: "This daemon's own peer-API TLS trust posture is weaker than the pinned default — an escape hatch is " +
			"configured, or the pinned CA is unreadable.",
		Why: "A weakened trust posture on the peer API — which carries cross-node changeset application and rollback " +
			"timers — increases the blast radius of a compromised or misbehaving peer.",
		Remedy: "Review why the escape hatch is configured and remove it once no longer needed; fix the pinned CA if unreadable.",
	},
	findings.CheckPeerUnreachable: {
		What: "Nothing answered on the peer API for a cluster member.",
		Why:  "vnprox cannot coordinate cross-node changes with a peer it cannot reach.",
		Remedy: "Check that node's vnproxd process and network path — a single missed poll doesn't fire this, so it reflects " +
			"a sustained outage.",
	},
	findings.CheckPeerUntrusted: {
		What:   "Something answered on the peer API but failed certificate verification against the pinned cluster CA.",
		Why:    "This is a security event: either a misconfigured peer, or something impersonating one.",
		Remedy: "Investigate immediately — verify the peer's certificate and whether the pinned CA is still correct before trusting it.",
	},
	"pending_interfaces": {
		What:   "A staged interfaces.new edit exists that has not yet been applied or reloaded.",
		Why:    "An uncommitted staged edit sitting around is easy to forget about.",
		Remedy: "Review the pending edit and either apply it or discard it.",
	},
	findings.CheckRogueDHCPServer: {
		What:   "A DHCP server is responding on the network that vnprox does not recognize as an authorized one.",
		Why:    "A rogue DHCP server can hand out the wrong address/gateway or intercept traffic — an active security event.",
		Remedy: "Investigate and disable the unauthorized DHCP server immediately.",
	},
	findings.CheckScheduleMissed: {
		What:   "A changeset's scheduled maintenance window elapsed entirely without ever firing.",
		Why:    "The intended change never happened, silently, unless someone notices this finding.",
		Remedy: "Decide whether to reschedule the changeset or apply it now; there is no single correct automated response.",
	},
	"sdn_realization": {
		What:   "An SDN zone's declared node membership doesn't match which nodes actually realize the zone's bridge.",
		Why:    "A node missing the realized bridge can't host guests on that zone the way the configuration says it should.",
		Remedy: "Reapply the SDN configuration on the affected nodes, or correct the zone's declared membership if that's the one that's wrong.",
	},
	"sdn_zone_status": {
		What: "PVE itself reports a zone's live per-node realization status as something other than ok.",
		Why: "This is PVE's own authoritative signal that the zone isn't cleanly applied on that node, distinct from " +
			"vnprox's own membership comparison (sdn_realization).",
		Remedy: "Check PVE's own SDN status output for the node and zone named in this finding for the specific error.",
	},
	findings.CheckServiceDown: {
		What: "A network-relevant systemd service this node has previously reported (dnsmasq for SDN DHCP, or frr for SDN " +
			"EVPN/routing) is now down.",
		Why:    "SDN DHCP or EVPN routing depends on these services; their absence breaks whatever they back.",
		Remedy: "vnprox may offer to restart the service directly as a remediation action; otherwise investigate why the unit stopped.",
	},
	findings.CheckServiceTrafficOnWrongNetwork: {
		What: "Traffic classified as belonging to a specific service (migration, backup, ceph-public, ceph-cluster, " +
			"corosync) was observed on a VLAN outside that service's declared network.",
		Why: "Storage or cluster traffic bleeding onto the wrong — often guest-facing — VLAN can saturate it and expose " +
			"sensitive traffic to the wrong segment.",
		Remedy: "Confirm the service's actual network configuration matches its declared one, and correct whichever is wrong.",
	},
	"spec_drift": {
		What: "The live network state has diverged from the pinned declarative spec — the GitOps reconciler's own reference.",
		Why:  "Whatever changed outside the spec-driven flow won't survive the next reconciliation the way anyone expects until this is resolved.",
		Remedy: "Reconcile: stage a changeset to bring live state back to the spec, or update and commit the spec to match an " +
			"intentional live change.",
	},
	"spec_reconciliation": {
		What: "An entity the spec declares has three positions — the spec document, the interfaces file, and the running " +
			"kernel — that don't all agree.",
		Why: "Naming exactly which of the three is out of step (rather than two independent two-way comparisons) is what " +
			"actually tells an operator what to fix and in which direction.",
		Remedy: "Compare the three positions named in this finding and reconcile toward whichever is correct.",
	},
	findings.CheckStalePendingInterfaces: {
		What:   "A staged interfaces.new edit has been sitting unapplied for over an hour.",
		Why:    "Unlike a routine pending-edit reminder, an edit stale this long usually means it was forgotten, not just under review.",
		Remedy: "Apply or discard the stale pending edit.",
	},
	findings.CheckStoreNearCapacity: {
		What: "vnproxd's own SQLite app store (vnprox.db plus its WAL/SHM sidecars) is approaching its configured size threshold.",
		Why:  "A full root filesystem caused by vnprox's own data would be an outage caused by the tool meant to prevent one.",
		Remedy: "Review what's consuming space (changesets, snapshots, audit history) and prune or archive it, or raise the " +
			"configured threshold if the growth is expected.",
	},
	findings.CheckSTPTopologyBurst: {
		What: "A bridge's forwarding port set has changed repeatedly within a short window — a functional proxy for STP " +
			"topology-change bursts.",
		Why: "Frequent reconvergence usually means a flapping link or a redundant path fighting itself, both of which cause " +
			"brief traffic interruptions on every change.",
		Remedy: "Identify the flapping link or redundant path causing the churn and stabilize it.",
	},
	findings.CheckTrunkUnusedVlans: {
		What: "A VLAN-aware bridge's trunked VID set contains a VID no guest NIC on that bridge/VNet actually uses.",
		Why:  "It's a tidy-up opportunity, not a correctness problem — the trunked VID may be reserved for planned capacity or an external device.",
	},
	findings.CheckTunnelDownPeerUnreachable: {
		What: "A federated cluster that's reachable only through a specific WireGuard tunnel has a stale tunnel handshake.",
		Why: "This is the single named signal that replaces noisy per-surface unreachability across topology, audit, and " +
			"IPAM reads for a federated cluster whose tunnel is down.",
		Remedy: "Check the WireGuard tunnel's configuration and connectivity to the federated cluster.",
	},
	findings.CheckUnexpectedRA: {
		What:   "An IPv6 Router Advertisement was observed from a source that isn't an expected/authorized router.",
		Why:    "An unexpected RA can silently redirect IPv6 traffic through an unauthorized gateway — an active security event.",
		Remedy: "Investigate and stop the unauthorized RA source immediately.",
	},
	findings.CheckUnknownMacProtectedSegment: {
		What:   "A MAC address not known to the inventory graph was observed on a segment marked protected.",
		Why:    "An unrecognized device on a segment meant to be tightly controlled is worth confirming isn't unauthorized access.",
		Remedy: "Identify the device behind the MAC address and confirm it's authorized on this segment.",
	},
	"vf_spoofcheck_mismatch": {
		What: "A live SR-IOV virtual function's VLAN/spoof-check setting no longer matches its physical function's bridge's " +
			"own VLAN-awareness/VID-set policy.",
		Why:    "A VF that's drifted from its PF's policy can allow VLAN hopping or spoofed traffic the bridge policy was meant to prevent.",
		Remedy: "Re-provision the VF (there is no vf.update — always a fresh vf.provision) to match the PF bridge's current policy.",
	},
	"vlan_cross_check_missing_on_bridge": {
		What:   "The switch port an LLDP neighbor advertises carries VLANs the bridge/bond is not configured to expect.",
		Why:    "Traffic the switch is willing to send on those VLANs has nowhere to go on this bridge — likely a bridge configuration gap.",
		Remedy: "Add the missing VIDs to the bridge's trunk if they're meant to be used here, or correct the switch port if they're not.",
	},
	"vlan_cross_check_missing_on_switch": {
		What:   "The bridge/bond expects VLANs that the LLDP-advertised switch port does not carry.",
		Why:    "Traffic tagged for those VLANs will be dropped at the switch — a mismatch between what vnprox configured and what the physical switch actually allows.",
		Remedy: "Add the missing VLANs to the switch port's trunk configuration, or narrow the bridge's expected VID set if they're not actually needed.",
	},
	findings.CheckVolumeSpike: {
		What:   "A guest/segment's recent traffic volume deviates sharply from its learned baseline.",
		Why:    "A sudden volume spike can be a legitimate load event, or a sign of a runaway process, exfiltration, or abuse.",
		Remedy: "Compare the spike against the baseline and observed values named in the finding, and investigate if it isn't an expected event.",
	},
	findings.CheckVxlanUnderlayMTU: {
		What: "The observed underlay path MTU (from live interface data) no longer has enough headroom for VXLAN " +
			"encapsulation overhead — unlike the changeset-validate-time check, which only checks an assumed default.",
		Why: "Insufficient underlay MTU headroom causes silent fragmentation or drops for VXLAN-encapsulated traffic; this " +
			"check catches degradation that happens after apply, outside any changeset.",
		Remedy: "Increase the underlay path's MTU, or reduce the VXLAN zone's effective MTU to fit within the available headroom.",
	},
	findings.CheckWanDegraded: {
		What:   "A node's configured external reference target is showing elevated rolling packet loss, up to and including full unreachability.",
		Why:    "This isolates \"it's the WAN/ISP, not the cluster\" from other findings, so an operator doesn't chase a cluster-side cause for an upstream problem.",
		Remedy: "Check the node's WAN/uplink connectivity to the reference target; this may be outside vnprox's, or even the cluster's, control.",
	},
	findings.CheckWgEndpointDrift: {
		What:   "A WireGuard peer's live observed endpoint no longer matches its configured one — typically a NAT rebind on the peer's side.",
		Why:    "An outdated configured endpoint can mean the tunnel silently stops reconnecting if the live endpoint changes again.",
		Remedy: "Update the peer's configured endpoint to match the live one if the drift is expected (a dynamic IP), or investigate if it isn't.",
	},
	findings.CheckWgHandshakeStale: {
		What:   "A WireGuard peer's last handshake is older than the configured staleness threshold.",
		Why:    "A stale handshake usually means the tunnel is down or the peer is unreachable, even though the interface itself still looks configured.",
		Remedy: "Check connectivity to the peer's endpoint and whether the peer's own WireGuard service is running.",
	},
}
