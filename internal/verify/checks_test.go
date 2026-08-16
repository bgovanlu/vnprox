package verify

// checks_test.go is T-2501 AC2: every check has a fixture that makes it
// *fail*, asserted individually, and a check with no failing fixture fails
// the build.
//
// The structure is deliberate and worth stating, because the obvious version
// of this test is worthless. A single "run everything and see that it passes"
// test proves only that the fakes agree with the code. A single "break
// something and see that something failed" test cannot tell you *which*
// check noticed — so a check that has never fired still looks covered. Here:
//
//   - TestHealthyClusterPassesEverything is the control. Without it, a check
//     that always fails would sail through every case below.
//   - TestEachCheckFailsOnBrokenInput names, per row, the check that must go
//     to `fail` and asserts on that check specifically, not on the run.
//   - TestEveryCheckHasAFailingFixture re-derives coverage by *running* every
//     mutation and recording which check each one moved to `fail`, rather
//     than trusting the table's own labels. A check nobody broke fails the
//     build with a message naming it.
//
// The last one is the structural guarantee. It is the same shape as
// internal/doctor's TestEveryCheckHasABrokenFixture and Report.Validate's
// remediation rule: a property enforced by construction, not by review.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// mutation is one deliberately broken fixture.
type mutation struct {
	// name is the failure being modelled, in the words an operator would use.
	name string
	// check is the id that must report `fail` (or, for the two rows marked
	// wantSkip, `skip`).
	check string
	// apply breaks the healthy fixture in exactly one way.
	apply func(*Deps)
	// wantDetail is a substring the verdict must contain, so a check that
	// fails for an unrelated reason does not count as covered.
	wantDetail string
	// destructive selects the destructive baseline (consent + write client).
	destructive bool
}

// brokenFixtures is one row per registered check, at least.
//
// Each row is a single mutation away from healthyDeps: that is what makes the
// resulting failure attributable to the thing that was broken rather than to
// a fixture that was never coherent.
func brokenFixtures() []mutation {
	return []mutation{
		{
			// The v4.0.0 defect class T-2901 fixed: the daemon regresses to a
			// CSP whose worker-src refuses the service worker — every API
			// test stays green while the PWA is dead in a real browser.
			name:  "the CSP stops admitting the service worker",
			check: "pwa.servable",
			apply: func(d *Deps) {
				daemonOf(d).rootResponses["/"] = fakeRootResponse{header: map[string]string{
					"Content-Security-Policy": "default-src 'self'; worker-src 'none'; manifest-src 'self'",
				}, body: "<!doctype html>"}
			},
			wantDetail: "worker-src does not allow 'self'",
		},
		{
			// The engine recorded an apply against real PVE whose effect it
			// can no longer describe: the audit story falling over.
			name:  "a committed changeset's diff is gone",
			check: "change.applied_changeset_committed",
			apply: func(d *Deps) {
				daemonOf(d).responses["/changesets/cs-9/diff"] = fakeResponse{status: 404, body: `{"error":{"code":"not_found","message":"gone"}}`}
			},
			wantDetail: "diff is unreadable",
		},
		{
			name:  "the commit-confirm window outlives a PVE ticket",
			check: "commitconfirm.window_within_ticket_lifetime",
			apply: func(d *Deps) {
				daemonOf(d).set("/config", `{"version":"3.0.4","confirmTimeoutDefaultSec":10800}`)
			},
			wantDetail: "lifetime",
		},
		{
			name:  "an 802.3ad bond has negotiated with nobody",
			check: "iface.lacp_partner_observed",
			apply: func(d *Deps) {
				hostOf(d).files["/proc/net/bonding/bond0"] = strings.Replace(healthyBonding, "00:11:22:33:44:55", "00:00:00:00:00:00", 1)
			},
			wantDetail: "nobody on the other end",
		},
		{
			name:  "an external subnet reports a provenance the contract does not define",
			check: "ipam.external_subnet_provenance",
			apply: func(d *Deps) {
				daemonOf(d).set("/ipam/external-subnets", `{"items":[{"id":"ext-1","cidr":"192.0.2.0/24","source":"guessed"}]}`)
			},
			wantDetail: "docs/api.md does not document",
		},
		{
			name:  "a drift finding names a node that is not in the cluster",
			check: "drift.config_vs_live",
			apply: func(d *Deps) {
				daemonOf(d).set("/drift", `{"items":[{"id":"drift:mtu_consistency|vmbr0","check":"mtu_consistency","severity":"warning","detail":"x","nodes":["pve9"]}]}`)
			},
			wantDetail: "not in this cluster",
		},
		{
			name:  "the drift route is unreadable while nodes exist to compare",
			check: "drift.node_vs_node",
			apply: func(d *Deps) {
				daemonOf(d).responses["/drift"] = fakeResponse{status: 500, body: `{"error":{"code":"internal"}}`}
			},
			wantDetail: "could not read drift findings",
		},
		{
			name:  "an ingested flow record carries a source the contract does not define",
			check: "flows.records_ingested",
			apply: func(d *Deps) {
				daemonOf(d).set("/flows", `{"items":[{"at":1,"node":"pve1","srcIp":"10.0.0.1","dstIp":"10.0.0.2","proto":6,"bytes":1,"packets":1,"source":"ebpf-maybe"}]}`)
			},
			wantDetail: "docs/api.md does not document",
		},
		{
			name:  "every finished capture recorded zero packets",
			check: "capture.af_packet_backend",
			apply: func(d *Deps) {
				daemonOf(d).set("/captures", `{"items":[{"id":"cap-1","sessions":[{"node":"pve1","iface":"vmbr0","state":"finished","packets":0,"bytes":0}]}]}`)
			},
			wantDetail: "none of which recorded a single packet",
		},
		{
			name:  "LLDP names a local interface PVE has never heard of",
			check: "lldp.neighbors_match_pve_interfaces",
			apply: func(d *Deps) {
				daemonOf(d).set("/lldp", `{"items":[{"node":"pve1","localIface":"eno99","chassisId":"aa","portId":"Gi1/0/1"}]}`)
			},
			wantDetail: "which PVE does not list",
		},
		{
			name:  "the federation route echoes the credential it sealed on attach",
			check: "federation.credential_never_echoed",
			apply: func(d *Deps) {
				daemonOf(d).set("/federation/clusters", `{"items":[{"id":"fc-1","name":"site-b","apiUrl":"https://pve-b:8007","credential":"root@pam!tok=s3cr3t"}]}`)
			},
			wantDetail: "handed back",
		},
		{
			name:  "a federated read comes back with a hole in it",
			check: "federation.remote_cluster_round_trip",
			apply: func(d *Deps) {
				daemonOf(d).set("/federation/topology", `{"clusters":[{"id":"local","name":"site-a","nodes":[{"name":"pve1"}]}],"partial":true,"failedClusters":["site-b"]}`)
			},
			wantDetail: "came back partial",
		},
		{
			name:  "a cross-cluster conflict names one cluster instead of the pair",
			check: "federation.ipam_conflicts",
			apply: func(d *Deps) {
				daemonOf(d).set("/federation/ipam/conflicts",
					`{"items":[{"type":"cross_cluster_duplicate_subnet","severity":"warning","ips":["10.20.0.0/24"],"message":"m","suggestion":"s","clusters":["site-a"]}],"partial":false,"failedClusters":[]}`)
			},
			wantDetail: "instead of the documented pair",
		},
		{
			name:  "the tunnels route hands back a private key",
			check: "wireguard.private_key_never_returned",
			apply: func(d *Deps) {
				daemonOf(d).set("/wireguard/tunnels",
					`{"items":[{"id":"wg-1","node":"pve1","ifName":"wg0","privateKey":"oops","status":{"interfaceUp":true,"peerCount":1},"peers":[]}]}`)
			},
			wantDetail: "has left it",
		},
		{
			name:  "a tunnel is up with no peer that has ever handshaken",
			check: "wireguard.tunnel_handshake",
			apply: func(d *Deps) {
				daemonOf(d).set("/wireguard/tunnels",
					`{"items":[{"id":"wg-1","node":"pve1","ifName":"wg0","status":{"interfaceUp":true,"peerCount":1},"peers":[{"publicKey":"BBBB","allowedIps":["10.99.0.2/32"],"external":false,"lastHandshakeUnix":0,"rxBytes":0}]}]}`)
			},
			wantDetail: "no peer that handshook",
		},
		{
			name:  "switch push is unlocked with no switch to push to",
			check: "switch.real_device_reachable",
			apply: func(d *Deps) {
				daemonOf(d).set("/lldp", `{"items":[{"node":"pve1","localIface":"eno1","chassisId":"aa","portId":"Gi1/0/1"}]}`)
			},
			wantDetail: "advertises a management address",
		},
		{
			name:  "the NIC advertises SR-IOV and the platform delivers none",
			check: "sriov.vf_capable_nic_present",
			apply: func(d *Deps) {
				hostOf(d).cmds["sh -c for f in /sys/class/net/*/device/sriov_totalvfs; do [ -e \"$f\" ] && echo \"$f=$(cat $f)\"; done; exit 0"] =
					"/sys/class/net/eno1/device/sriov_totalvfs=0\n"
			},
			wantDetail: "not delivering it",
		},
		{
			name:  "HA replication is degraded, so a failover would promote a stale node",
			check: "ha.lease_and_replication",
			apply: func(d *Deps) {
				daemonOf(d).set("/ha/status", `{"role":"active","term":7,"leaseExpiresAt":9999999999,"replicationLag":900,"replicationDegraded":true}`)
			},
			wantDetail: "replication degraded",
		},
		{
			name:  "the backup archive is empty",
			check: "backup.archive_round_trip",
			apply: func(d *Deps) {
				hostOf(d).cmds["vnproxctl backup -o json"] = `{"path":"/tmp/b","bytes":0,"schemaVersion":48,"entries":0,"includesKeyMaterial":false}`
			},
			wantDetail: "empty backup",
		},
		{
			name:  "the support bundle carries the node's real session key",
			check: "supportbundle.contains_no_secret",
			apply: func(d *Deps) {
				hostOf(d).cmds["vnproxctl support-bundle --dry-run -o json"] =
					`{"node":"pve1","entries":["env.txt"],"sessionKey":"` + fixtureSessionKey + `"}`
			},
			wantDetail: "contains this node's real session key",
		},
		{
			// The control half of the same check: a scan that cannot find a
			// string the bundle certainly contains must refuse to conclude
			// anything, rather than reporting a clean bundle.
			name:  "the leak scan cannot find a string it should, so its negatives mean nothing",
			check: "supportbundle.contains_no_secret",
			apply: func(d *Deps) {
				hostOf(d).cmds["vnproxctl support-bundle --dry-run -o json"] = `{"entries":["env.txt"]}`
			},
			wantDetail: "Control failed",
		},
		{
			// Corrected 2026-08-16. This row used to keep `DNS:pve1` and drop
			// only the dial address — but that shape is a PASS under the rule
			// T-2303 actually implements (resolve the dial host to the node
			// name and verify against that), and the check reporting it as a
			// failure produced a false FAIL on real hardware. Breaking it
			// properly means covering NEITHER the node name nor the address,
			// which is the only state that genuinely fails closed on the
			// first peer call.
			name:  "the node's PVE-issued leaf covers neither its node name nor the address peers dial it on",
			check: "peer.ca_pins_real_chain",
			apply: func(d *Deps) {
				hostOf(d).cmds["openssl x509 -in /etc/pve/local/pve-ssl.pem -noout -ext subjectAltName"] =
					"X509v3 Subject Alternative Name:\n    DNS:some-other-node, IP Address:192.168.100.99\n"
			},
			wantDetail: "covers neither",
		},
		{
			name:  "a certificate on the real pmxcfs mount is missing from the inventory",
			check: "certs.inventory_matches_pmxcfs",
			apply: func(d *Deps) {
				daemonOf(d).set("/certs", `{"inventory":{"scannedAt":"2026-08-10T00:00:00Z","certificates":[{"path":"/etc/pve/nodes/pve1/pve-ssl.pem","node":"pve1"}],"errors":[]},"issues":[]}`)
			},
			wantDetail: "absent from the inventory",
		},
		{
			name:  "doctor reports a failing check on the real install",
			check: "cli.daemon_independent_commands",
			apply: func(d *Deps) {
				hostOf(d).cmds["vnproxctl doctor -o json"] =
					`{"results":[{"check":"pmxcfs","status":"fail","detail":"missing","remediation":"start pve-cluster"}],"summary":{"pass":0,"warn":0,"fail":1,"skip":0}}`
			},
			wantDetail: "failing check",
		},

		// --- destructive suite -------------------------------------------------
		{
			name:        "a distributed rollback restores one node and leaves the other",
			check:       "change.multinode_apply_rollback",
			destructive: true,
			apply: func(d *Deps) {
				c := clusterOf(d)
				before := c.ifaces[fixturePeer]
				changed := append([]Iface(nil), before...)
				changed[0].MTU = 9000
				// The peer's post-rollback read differs from its pre-apply one.
				c.ifaces[fixturePeer] = before
				c.afterRollback = map[string][]Iface{fixturePeer: changed}
			},
			wantDetail: "half-restored",
		},
		{
			name:        "the commit-confirm window expires and nothing rolls back",
			check:       "commitconfirm.unattended_rollback_fires",
			destructive: true,
			apply: func(d *Deps) {
				daemonOf(d).set("/changesets/cs-1", `{"id":"cs-1","status":"awaiting_confirm"}`)
			},
			wantDetail: "no unattended rollback fired",
		},
		{
			name:        "a changeset reaches committed without anyone confirming it",
			check:       "commitconfirm.unattended_rollback_fires",
			destructive: true,
			apply: func(d *Deps) {
				daemonOf(d).set("/changesets/cs-1", `{"id":"cs-1","status":"committed"}`)
			},
			wantDetail: "without anyone confirming",
		},
		{
			name:        "a vf.provision is staged and the kernel's VF count never moves",
			check:       "sriov.vf_lifecycle",
			destructive: true,
			apply: func(d *Deps) {
				hostOf(d).fileSeq = map[string][]string{"/sys/class/net/eno1/device/sriov_numvfs": {"0\n"}}
			},
			wantDetail: "nothing reached the hardware",
		},
		{
			name:        "the active daemon is stopped and no standby takes the lease",
			check:       "ha.failover",
			destructive: true,
			apply: func(d *Deps) {
				daemonOf(d).seq = nil // every poll returns the original term
			},
			wantDetail: "no standby promoted",
		},
	}
}

func daemonOf(d *Deps) *fakeDaemon {
	f, _ := d.Daemon.(*fakeDaemon)
	return f
}

func hostOf(d *Deps) *fakeHost {
	f, _ := d.Host.(*fakeHost)
	return f
}

func clusterOf(d *Deps) *fakeCluster {
	f, _ := d.Cluster.(*fakeCluster)
	return f
}

// TestHealthyClusterPassesEverything is the control for every negative case
// below. A check that can never pass would make its "fails on broken input"
// row meaningless, and this is what stops that.
func TestHealthyClusterPassesEverything(t *testing.T) {
	for _, suite := range []Suite{SuiteHardware, SuiteMultinode} {
		deps := healthyDeps()
		report, err := Run(context.Background(), Options{Suite: suite, Version: "test", Logger: discardLog()}, deps)
		if err != nil {
			t.Fatalf("Run(%s): %v", suite, err)
		}
		for _, res := range report.Results {
			if res.Status != StatusPass {
				t.Errorf("suite %s: %s is %s against a healthy cluster, so its failing fixture proves nothing: %s",
					suite, res.ID, res.Status, res.Detail)
			}
		}
		if report.Summary.Passed == 0 {
			t.Errorf("suite %s: nothing passed against the healthy fixture", suite)
		}
	}

	// The destructive suite needs its own baseline: consent, a write client,
	// and the two state transitions it exists to watch for.
	report, err := Run(context.Background(), Options{Suite: SuiteDestructive, Version: "test", Logger: discardLog()}, destructiveDeps())
	if err != nil {
		t.Fatalf("Run(destructive): %v", err)
	}
	for _, res := range report.Results {
		if res.Status != StatusPass {
			t.Errorf("destructive: %s is %s against a healthy fixture: %s", res.ID, res.Status, res.Detail)
		}
	}
}

// TestEachCheckFailsOnBrokenInput is AC2's individual half: one row per
// failure mode, each asserting on the specific check that must notice.
func TestEachCheckFailsOnBrokenInput(t *testing.T) {
	for _, m := range brokenFixtures() {
		t.Run(m.name, func(t *testing.T) {
			deps := healthyDeps()
			if m.destructive {
				deps = destructiveDeps()
			}
			m.apply(&deps)

			res := runCheck(t, m.check, deps)
			if res.Status != StatusFail {
				t.Fatalf("%s reported %s, want fail\n  detail: %s", m.check, res.Status, res.Detail)
			}
			if !strings.Contains(res.Detail, m.wantDetail) {
				t.Errorf("%s failed for the wrong reason:\n  got:  %s\n  want it to mention: %q", m.check, res.Detail, m.wantDetail)
			}
			// A failing verdict with no evidence is an opinion — and
			// Report.Validate would reject the whole report for it, which
			// would turn a real product failure into an internal error.
			if len(res.Evidence) == 0 {
				t.Errorf("%s failed with no evidence attached", m.check)
			}
			if err := res.validate(); err != nil {
				t.Errorf("%s produced a malformed failing result: %v", m.check, err)
			}
		})
	}
}

// TestEveryCheckHasAFailingFixture is AC2's structural half.
//
// It does not read the table's `check` labels. It runs every mutation, records
// which checks each one actually moved to `fail`, and requires the registry to
// be fully covered by what it observed. A check added without a failing
// fixture fails the build here, with a message naming it — which is the whole
// difference between a suite of checks and a suite of decorations.
func TestEveryCheckHasAFailingFixture(t *testing.T) {
	covered := map[string]bool{}
	for _, m := range brokenFixtures() {
		base := healthyDeps
		if m.destructive {
			base = destructiveDeps
		}
		for _, c := range Checks() {
			deps := base()
			m.apply(&deps)
			out := runOne(context.Background(), c, deps, discardLog())
			if out.Status == StatusFail {
				covered[c.ID] = true
			}
		}
	}

	var uncovered []string
	for _, id := range sortedIDs() {
		if !covered[id] {
			uncovered = append(uncovered, id)
		}
	}
	if len(uncovered) > 0 {
		t.Errorf("%d check(s) never reached `fail` under any fixture in brokenFixtures(), so nothing proves they can fail (AC2):\n  - %s\n"+
			"Add a row to brokenFixtures() that breaks each one.", len(uncovered), strings.Join(uncovered, "\n  - "))
	}
}

// TestSkipIsNotPass is AC3 at the check level: with every probe absent, no
// check may report pass.
func TestSkipIsNotPass(t *testing.T) {
	bare := Deps{
		Now:   func() time.Time { return fixtureNow() },
		Wait:  func(context.Context, time.Duration) error { return nil },
		Nodes: fixtureNodes(),
	}
	for _, c := range Checks() {
		out := runOne(context.Background(), c, bare, discardLog())
		if out.Status == StatusPass {
			t.Errorf("%s reported PASS with no probe wired up at all: %s", c.ID, out.Detail)
		}
		if out.Status == StatusSkip && strings.TrimSpace(out.Reason) == "" {
			t.Errorf("%s skipped with no reason", c.ID)
		}
	}
}

// TestSkipReasonsDoNotDiagnose pins the lesson doctor learned on real
// hardware (docs/status-matrix.md §5.10): a skip that asserts a cause it did
// not check is a confident, unverified claim — the exact thing StatusSkip
// exists to prevent. A skip may say what was not observed and what would make
// it observable; it may not say why the world is the way it is.
func TestSkipReasonsDoNotDiagnose(t *testing.T) {
	// Phrases that assert a diagnosis rather than reporting an absence.
	forbidden := []string{
		"is not configured",
		"is misconfigured",
		"is broken",
		"has failed",
		"is not working",
	}
	bare := Deps{
		Now:   func() time.Time { return fixtureNow() },
		Wait:  func(context.Context, time.Duration) error { return nil },
		Nodes: fixtureNodes(),
	}
	for _, c := range Checks() {
		out := runOne(context.Background(), c, bare, discardLog())
		if out.Status != StatusSkip {
			continue
		}
		lower := strings.ToLower(out.Reason)
		for _, phrase := range forbidden {
			if strings.Contains(lower, phrase) {
				t.Errorf("%s's skip reason diagnoses a cause it never checked (%q): %s", c.ID, phrase, out.Reason)
			}
		}
	}
}

// TestDestructiveChecksCannotRunWithoutConsent proves the interlock is
// structural rather than advisory: with consent withheld, every destructive
// check skips naming the flag, and none of them reaches its own body.
func TestDestructiveChecksCannotRunWithoutConsent(t *testing.T) {
	deps := destructiveDeps()
	deps.Consent.Destructive = false
	deps.Mutator = nil

	for _, c := range Checks() {
		if c.Suite != SuiteDestructive {
			continue
		}
		out := runOne(context.Background(), c, deps, discardLog())
		if out.Status != StatusSkip {
			t.Errorf("%s ran without --i-understand (status %s)", c.ID, out.Status)
		}
		if !strings.Contains(out.Reason, "--i-understand") {
			t.Errorf("%s skipped without naming the flag that would enable it: %s", c.ID, out.Reason)
		}
	}
	if daemon := daemonOf(&deps); daemon != nil && len(daemon.posts) > 0 {
		t.Errorf("a destructive check issued %d write(s) without consent: %v", len(daemon.posts), daemon.posts)
	}
}

// TestMultinodeChecksSkipWithTheCountTheySaw: a check that needs two nodes and
// finds one has to say so with the number, not with a shrug. An operator
// reading "needs 2 online node(s); this cluster has 1 (pve1)" knows what to do
// next; one reading "skipped" does not.
func TestMultinodeChecksSkipWithTheCountTheySaw(t *testing.T) {
	// Consent is granted here on purpose: the destructive interlock fires
	// before the node gate, so without it this test would be re-testing the
	// interlock and never reach the gate it is about.
	deps := destructiveDeps()
	deps.Nodes = []Node{{Name: fixtureNode, Address: fixtureNodeA, Online: true, Local: true}}

	var checked int
	for _, c := range Checks() {
		if c.MinNodes < 2 {
			continue
		}
		checked++
		out := runOne(context.Background(), c, deps, discardLog())
		if out.Status != StatusSkip {
			t.Errorf("%s ran on a 1-node cluster despite needing %d", c.ID, c.MinNodes)
			continue
		}
		if !strings.Contains(out.Reason, "this cluster has 1") {
			t.Errorf("%s's skip does not name the node count it saw: %s", c.ID, out.Reason)
		}
		if !strings.Contains(out.Reason, c.Precondition) {
			t.Errorf("%s's skip does not restate its hardware precondition, so the reader is not told what to go and get: %s", c.ID, out.Reason)
		}
	}
	if checked == 0 {
		t.Fatal("no check declares MinNodes >= 2, so the multi-node gate is untested")
	}
}

// TestEveryCheckStatesEvidenceItCanShow guards the shape of a pass: a passing
// check that carries no evidence would be rejected by Report.Validate at run
// time, i.e. a real product pass would surface as an internal error.
func TestEveryCheckStatesEvidenceItCanShow(t *testing.T) {
	for _, suite := range []Suite{SuiteHardware, SuiteMultinode} {
		report, err := Run(context.Background(), Options{Suite: suite, Version: "test", Logger: discardLog()}, healthyDeps())
		if err != nil {
			t.Fatalf("Run(%s): %v", suite, err)
		}
		for _, res := range report.Results {
			if res.Status != StatusPass {
				continue
			}
			if len(res.Evidence) == 0 {
				t.Errorf("%s passed with no evidence", res.ID)
			}
			for _, ev := range res.Evidence {
				if err := ev.validate(); err != nil {
					t.Errorf("%s attached malformed evidence: %v", res.ID, err)
				}
			}
		}
	}
}

// TestEvidenceNeverCarriesTheSecretItSearchedFor.
//
// The report artifact is meant to be attached to a public issue, and it
// carries verbatim command output by design. The support-bundle check reads
// this node's real session key in order to search for it — so that check, of
// all of them, must never put the thing it is holding into its own evidence.
//
// Both branches are covered: the clean one, where the key is absent from the
// bundle, and the leak one, where it is present and the check has to report a
// failure *without* quoting the leaked key back into the report.
func TestEvidenceNeverCarriesTheSecretItSearchedFor(t *testing.T) {
	cases := []struct {
		name       string
		bundleOut  string
		wantStatus Status
	}{
		{
			name:       "a clean bundle",
			bundleOut:  `{"node":"pve1","entries":["env.txt","config.toml"],"redacted":true}`,
			wantStatus: StatusPass,
		},
		{
			name:       "a bundle that leaked the key",
			bundleOut:  `{"node":"pve1","entries":["env.txt"],"sessionKey":"` + fixtureSessionKey + `"}`,
			wantStatus: StatusFail,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := healthyDeps()
			hostOf(&deps).cmds["vnproxctl support-bundle --dry-run -o json"] = tc.bundleOut

			res := runCheck(t, "supportbundle.contains_no_secret", deps)
			if res.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s: %s", res.Status, tc.wantStatus, res.Detail)
			}
			needle := fixtureSessionKey[:16]
			if strings.Contains(res.Detail, needle) {
				t.Errorf("the verdict quotes the session key back into the report: %s", res.Detail)
			}
			for _, ev := range res.Evidence {
				if strings.Contains(ev.Output, needle) {
					t.Errorf("evidence from %s (%s) carries the session key it was searching for", ev.Source, ev.Ref)
				}
			}
		})
	}
}

// TestLACPPartnerParse covers the one piece of real-file parsing in this
// package against the shape a real kernel produces, rather than only through
// the check that uses it.
func TestLACPPartnerParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			// The actor block comes first in a real file and carries a
			// perfectly plausible MAC. A parser that takes the first
			// "system mac address:" it sees reports the node's *own* address
			// as its partner's — which looks exactly like a healthy bond, on
			// a bond that has negotiated with nobody.
			name: "the actor's own MAC is not the partner's",
			in:   healthyBonding,
			want: "00:11:22:33:44:55",
			ok:   true,
		},
		{
			name: "an unnegotiated bond reports the all-zero partner",
			in:   strings.Replace(healthyBonding, "00:11:22:33:44:55", "00:00:00:00:00:00", 1),
			want: "00:00:00:00:00:00",
			ok:   true,
		},
		{name: "no partner block at all", in: "Bonding Mode: balance-rr\n", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := lacpPartnerMAC(tt.in)
			if ok != tt.ok {
				t.Fatalf("lacpPartnerMAC ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("lacpPartnerMAC = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParseSRIOVTotals covers the sysfs listing parse.
func TestParseSRIOVTotals(t *testing.T) {
	got := parseSRIOVTotals("/sys/class/net/eno1/device/sriov_totalvfs=8\n/sys/class/net/eno2/device/sriov_totalvfs=0\nnonsense\n")
	if len(got) != 2 {
		t.Fatalf("parsed %d entries from a 2-NIC listing: %v", len(got), got)
	}
	if got["eno1"] != 8 || got["eno2"] != 0 {
		t.Errorf("parseSRIOVTotals = %v, want eno1:8 eno2:0", got)
	}
}

// TestExtractTOMLSection covers the two-key interlock read.
func TestExtractTOMLSection(t *testing.T) {
	const doc = "[server]\nlisten = \"0.0.0.0:8007\"\nenabled = true\n\n[switches]\n# enabled = true\nenabled = false\n"
	section := extractTOMLSection(doc, "switches")
	if strings.Contains(section, "listen") {
		t.Errorf("[switches] extraction leaked [server]'s keys: %q", section)
	}
	if tomlBoolTrue(section, "enabled") {
		t.Error("enabled = false read as true — a commented-out `# enabled = true` above it must not count")
	}
	if !tomlBoolTrue(extractTOMLSection("[switches]\nenabled = true\n", "switches"), "enabled") {
		t.Error("enabled = true read as false")
	}
}
