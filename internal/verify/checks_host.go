package verify

// checks_host.go holds the checks that read the node itself — the kernel, the
// pmxcfs mount, the packaged CLI. These are the ones no mock can answer even
// in principle: /proc/net/bonding's partner MAC, /sys/class/net's SR-IOV
// counters, and a certificate chain PVE generated rather than one a test built
// in-process with crypto/x509.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// checkLACPPartner is the cross-kernel check row 6's matrix note asks for.
//
// PVE says a bond is 802.3ad; the kernel says who it negotiated with. An
// all-zero partner system ID means the bond is in LACP mode and has no LACP
// partner — the link is up, traffic may even flow on one slave, and the
// aggregation everyone believes is there is not. No fixture produces that,
// because a fixture writes down what it was told the file looks like.
func checkLACPPartner(ctx context.Context, d Deps) Outcome {
	if d.Cluster == nil {
		return skipNoCluster("finding this node's 802.3ad bonds")
	}
	if d.Host == nil {
		return skipNoHost("reading /proc/net/bonding for the negotiated LACP partner")
	}
	node := localNode(d.Nodes)
	if node == "" {
		return skipNoCluster("reading a node's bonds needs a named cluster node")
	}

	ifaces, err := d.Cluster.Interfaces(ctx, node)
	if err != nil {
		return Fail(fmt.Sprintf("could not read %s's interfaces from PVE: %v", node, err), NewEvidence(SourcePVEAPI, fmt.Sprintf("GET /nodes/%s/network", node), err.Error()))
	}
	pveEv := NewEvidence(SourcePVEAPI, fmt.Sprintf("GET /nodes/%s/network", node), describeIfaces(ifaces))

	var lacp []string
	for _, i := range ifaces {
		if i.Type == "bond" && i.BondMode == "802.3ad" {
			lacp = append(lacp, i.Name)
		}
	}
	if len(lacp) == 0 {
		return Skip(fmt.Sprintf("%s has no 802.3ad bond, so no LACP negotiation happened to observe. Configure an LACP bond against a switch port-channel and re-run", node), pveEv)
	}

	evidence := []Evidence{pveEv}
	var broken []string
	for _, name := range lacp {
		path := "/proc/net/bonding/" + name
		raw, readErr := d.Host.ReadFile(ctx, node, path)
		if readErr != nil {
			return Fail(fmt.Sprintf("PVE reports %s as an 802.3ad bond on %s but the kernel has no %s: %v", name, node, path, readErr),
				append(evidence, NewEvidence(SourceFile, path, readErr.Error()))...)
		}
		text := string(raw)
		evidence = append(evidence, NewEvidence(SourceFile, path, text))

		partner, ok := lacpPartnerMAC(text)
		switch {
		case !ok:
			broken = append(broken, fmt.Sprintf("%s: %s reports no partner MAC at all", name, path))
		case isZeroMAC(partner):
			broken = append(broken, fmt.Sprintf("%s: partner system id is %s — the bond is in LACP mode with nobody on the other end", name, partner))
		}
	}
	if len(broken) > 0 {
		return Fail(fmt.Sprintf("%d of %d 802.3ad bond(s) on %s have not negotiated with a switch: %s", len(broken), len(lacp), node, strings.Join(broken, "; ")), evidence...)
	}
	return Pass(fmt.Sprintf("%d 802.3ad bond(s) on %s, every one reporting a real negotiated LACP partner", len(lacp), node), evidence...)
}

// lacpPartnerMAC pulls the partner system id out of a real
// /proc/net/bonding/<bond>. The file lists it once per slave, under
// "details partner lacp pdu"; the first occurrence is enough, because a bond
// whose slaves disagree about the partner is already a separate finding
// (internal/findings' lacp_partner_mismatch).
func lacpPartnerMAC(text string) (string, bool) {
	const key = "system mac address:"
	var inPartner bool
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "details partner lacp pdu") {
			inPartner = true
			continue
		}
		if strings.HasPrefix(lower, "details actor lacp pdu") {
			inPartner = false
			continue
		}
		if !inPartner || !strings.HasPrefix(lower, key) {
			continue
		}
		return strings.TrimSpace(lower[len(key):]), true
	}
	return "", false
}

func isZeroMAC(mac string) bool {
	return strings.TrimSpace(mac) == "00:00:00:00:00:00"
}

// checkSRIOVCapableNIC answers the question row 48 has never been able to:
// is there a NIC here that can do this at all?
func checkSRIOVCapableNIC(ctx context.Context, d Deps) Outcome {
	if d.Host == nil {
		return skipNoHost("reading /sys/class/net for SR-IOV capability")
	}
	node := localNode(d.Nodes)

	// The trailing `exit 0` is load-bearing, not tidiness.
	//
	// A `for` loop's exit status is its last command's, and the last command
	// here is `[ -e "$f" ] && echo ...`. On a host where NO NIC exposes
	// sriov_totalvfs the glob stays literal, the test fails, and the shell
	// exits 1 — so this check reported "could not enumerate SR-IOV capability"
	// on every such host, when the truth is the far more useful "there is no
	// SR-IOV-capable NIC here", which the len(total)==0 branch below already
	// says properly and could never reach. Observed on pvecube 2026-08-16.
	// (The same status leak could fire on a mixed host whose last glob entry
	// happens not to exist, so this is not only about the empty case.)
	//
	// A non-zero status now means what it should: the shell itself could not
	// run, which IS an unknown. An empty listing means a definite negative.
	// Collapsing those two into one message is this arc's recurring bug —
	// see planning/tasks/phase-29.md's wave-4 record.
	listing, err := d.Host.Run(ctx, node, "sh", "-c", "for f in /sys/class/net/*/device/sriov_totalvfs; do [ -e \"$f\" ] && echo \"$f=$(cat $f)\"; done; exit 0")
	if err != nil {
		return Skip(fmt.Sprintf("could not enumerate SR-IOV capability on %s: %v. This needs a shell on the node itself", node, err),
			NewEvidence(SourceCommand, "for f in /sys/class/net/*/device/sriov_totalvfs; ...", err.Error()))
	}
	ev := NewEvidence(SourceCommand, "cat /sys/class/net/*/device/sriov_totalvfs", listing)

	total := parseSRIOVTotals(listing)
	if len(total) == 0 {
		return Skip(fmt.Sprintf("no NIC on %s exposes sriov_totalvfs: this host has no SR-IOV-capable NIC, or IOMMU is not enabled in firmware/kernel. This is the hardware row 48 has always been blocked on", node), ev)
	}

	var capable []string
	for name, n := range total {
		if n > 0 {
			capable = append(capable, fmt.Sprintf("%s (%d VFs)", name, n))
		}
	}
	if len(capable) == 0 {
		return Fail(fmt.Sprintf("%d NIC(s) on %s expose sriov_totalvfs but every one reports 0 usable VFs: the driver advertises SR-IOV and the platform is not delivering it (usually IOMMU/VT-d off in firmware)", len(total), node), ev)
	}
	return Pass(fmt.Sprintf("%s has real SR-IOV-capable NIC(s): %s", node, strings.Join(capable, ", ")), ev)
}

// parseSRIOVTotals reads the "path=value" lines the listing command emits.
func parseSRIOVTotals(listing string) map[string]int {
	out := map[string]int{}
	for _, line := range strings.Split(listing, "\n") {
		line = strings.TrimSpace(line)
		eq := strings.LastIndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		path, value := line[:eq], strings.TrimSpace(line[eq+1:])
		name := path
		if trimmed := strings.TrimPrefix(path, "/sys/class/net/"); trimmed != path {
			if slash := strings.IndexByte(trimmed, '/'); slash > 0 {
				name = trimmed[:slash]
			}
		}
		var n int
		if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
			continue
		}
		out[name] = n
	}
	return out
}

// checkSwitchReachable reports on row 44's two-key interlock and, when it is
// unlocked, on whether the thing behind it is real.
func checkSwitchReachable(ctx context.Context, d Deps) Outcome {
	if d.Host == nil {
		return skipNoHost("reading [switches] from this node's vnprox.toml")
	}
	node := localNode(d.Nodes)
	const configPath = "/etc/vnprox/vnprox.toml"
	raw, err := d.Host.ReadFile(ctx, node, configPath)
	if err != nil {
		return Skip(fmt.Sprintf("could not read %s on %s: %v. Run this on a node with vnprox installed", configPath, node, err),
			NewEvidence(SourceFile, configPath, err.Error()))
	}
	// The config carries no secret in [switches] itself (per-switch
	// credentials live in the store), but the file as a whole does, so only
	// the section is attached as evidence.
	section := extractTOMLSection(string(raw), "switches")
	ev := NewEvidence(SourceFile, configPath+" [switches]", section)

	if !strings.Contains(section, "enabled") || !tomlBoolTrue(section, "enabled") {
		return Skip(fmt.Sprintf("switch push is dark on %s: [switches] enabled is not true, which is the daemon-level half of the two-key interlock. The driver has therefore only ever run against internal/switchmock — set it, register a real switch, and re-run", node), ev)
	}

	// Second key: a registered switch that answers. LLDP is the independent
	// witness that a real switch is on the other end of a cable.
	var lldp struct {
		Items []struct {
			ChassisName string   `json:"chassisName"`
			ChassisID   string   `json:"chassisId"`
			MgmtIPs     []string `json:"mgmtIps"`
		} `json:"items"`
	}
	lldpEv, lldpErr := daemonJSON(ctx, d, "/lldp", &lldp)
	if lldpErr != nil {
		return Fail(fmt.Sprintf("switch push is enabled on %s but no switch could be identified: %v", node, lldpErr), ev, lldpEv)
	}
	var withMgmt int
	for _, n := range lldp.Items {
		if len(n.MgmtIPs) > 0 {
			withMgmt++
		}
	}
	if withMgmt == 0 {
		return Fail(fmt.Sprintf("switch push is enabled on %s but not one of the %d LLDP neighbour(s) advertises a management address to push to", node, len(lldp.Items)), ev, lldpEv)
	}
	return Pass(fmt.Sprintf("switch push is enabled on %s and %d of %d LLDP neighbour(s) advertise a real management address", node, withMgmt, len(lldp.Items)), ev, lldpEv)
}

// checkBackupRoundTrip runs the command that validated this row on real
// hardware on 2026-08-05, and asserts on what it reported.
func checkBackupRoundTrip(ctx context.Context, d Deps) Outcome {
	if d.Host == nil {
		return skipNoHost("running vnproxctl backup against the real store")
	}
	node := localNode(d.Nodes)
	out, err := d.Host.Run(ctx, node, "vnproxctl", "backup", "-o", "json")
	ev := NewEvidence(SourceCommand, "vnproxctl backup -o json", out)
	if err != nil {
		return Fail(fmt.Sprintf("vnproxctl backup failed on %s: %v", node, err), NewEvidence(SourceCommand, "vnproxctl backup -o json", out+"\n"+err.Error()))
	}

	// These names are `vnproxctl backup -o json`'s actual keys
	// (cmd/vnproxctl/backupcmd.go), pinned by the shared golden at
	// testdata/vnproxctl-backup.json — see backupContractGolden in
	// checks_host_test.go for why this file exists at all.
	//
	// The struct originally read "sizeBytes" and "includedKeys", which the
	// CLI has never emitted. json.Unmarshal ignores absent fields, so
	// SizeBytes was always 0 and this check reported "wrote a 0-byte
	// archive: an empty backup of a live store" on every healthy node —
	// while IncludedKeys, always false, made the key-material assertion
	// permanently dead. Found 2026-08-16 on pvecube, where a hand-run
	// `vnproxctl backup` wrote 720 KiB at the same moment this check called
	// it empty. Its unit fixture had invented the same wrong names, so the
	// check and its test agreed with each other and both disagreed with the
	// program.
	var body struct {
		Path         string `json:"path"`
		SizeBytes    int64  `json:"bytes"`
		SchemaVer    int    `json:"schemaVersion"`
		Entries      int    `json:"entries"`
		IncludedKeys bool   `json:"includesKeyMaterial"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return Fail(fmt.Sprintf("vnproxctl backup on %s produced output that is not the documented JSON: %v", node, err), ev)
	}
	switch {
	case body.SizeBytes <= 0:
		return Fail(fmt.Sprintf("vnproxctl backup on %s wrote a %d-byte archive: an empty backup of a live store", node, body.SizeBytes), ev)
	case body.SchemaVer <= 0:
		return Fail(fmt.Sprintf("vnproxctl backup on %s recorded no schema version, so the archive cannot be checked for forward-only restore", node), ev)
	case body.IncludedKeys:
		// --include-keys was not passed, so a true here means the default
		// changed under someone.
		return Fail(fmt.Sprintf("vnproxctl backup on %s included key material without --include-keys", node), ev)
	default:
		return Pass(fmt.Sprintf("vnproxctl backup wrote %d bytes at schema %d from %s's real store, with no key material", body.SizeBytes, body.SchemaVer, node), ev)
	}
}

// checkSupportBundleNoSecret is row 62's real-credential test, and it carries
// its own control.
//
// A leak scan that finds nothing proves nothing until you have shown it can
// find something. The 2026-08-05 hardware run established this by checking
// that the same scan *did* find the node's hostname; this check does the
// same, in the same run, so a negative result is never reported on its own.
func checkSupportBundleNoSecret(ctx context.Context, d Deps) Outcome {
	if d.Host == nil {
		return skipNoHost("running vnproxctl support-bundle against real credentials")
	}
	node := localNode(d.Nodes)

	keyBytes, keyErr := d.Host.ReadFile(ctx, node, "/etc/vnprox/keys/session.key")
	if keyErr != nil {
		return Skip(fmt.Sprintf("could not read this node's session key to scan for it: %v. Run this as root on a node where vnprox has started at least once", keyErr),
			NewEvidence(SourceFile, "/etc/vnprox/keys/session.key", keyErr.Error()))
	}
	// Never the whole key, and never in the evidence: a prefix is enough to
	// search for and useless to an attacker who reads this report.
	needle := strings.TrimSpace(string(keyBytes))
	if len(needle) < 16 {
		return Fail(fmt.Sprintf("this node's session key is %d characters, which is too short to be a real key", len(needle)),
			NewEvidence(SourceFile, "/etc/vnprox/keys/session.key", fmt.Sprintf("(%d characters — not printed)", len(needle))))
	}
	needle = needle[:16]

	out, err := d.Host.Run(ctx, node, "vnproxctl", "support-bundle", "--dry-run", "-o", "json")
	// Every piece of evidence this check attaches is redacted of the needle
	// before it is attached.
	//
	// This is not belt-and-braces. The verify report is designed to be sent to
	// strangers — that is the whole point of the artifact — and this check is
	// the one holding a live session key. On the branch that matters, the one
	// where the bundle *did* leak the key, attaching the raw output verbatim
	// would republish the leak into the report written to prove there wasn't
	// one. Caught by TestEvidenceNeverCarriesTheSecretItSearchedFor.
	evidenceFor := func(text string) Evidence {
		return NewEvidence(SourceCommand, "vnproxctl support-bundle --dry-run -o json", redactNeedle(text, needle))
	}
	ev := evidenceFor(out)
	if err != nil {
		return Fail(fmt.Sprintf("vnproxctl support-bundle --dry-run failed on %s: %v", node, err),
			evidenceFor(out+"\n"+err.Error()))
	}

	// The control first, so its failure is reported as a broken scan rather
	// than as a clean bundle.
	if !strings.Contains(out, node) {
		return Fail(fmt.Sprintf("the leak scan could not find %q — a string the bundle certainly contains — so a negative result from it would mean nothing. Control failed; no conclusion drawn about secrets", node), ev)
	}
	if strings.Contains(out, needle) {
		return Fail(fmt.Sprintf("the support bundle from %s contains this node's real session key", node), ev)
	}
	return Pass(fmt.Sprintf("the support bundle from %s does not contain this node's real session key, and the same scan does find %q — so the negative means something", node, node), ev)
}

// redactNeedle removes a known secret from text, replacing it with a marker
// that says a redaction happened. A silently-removed secret would leave
// evidence a reader could not tell had been edited.
func redactNeedle(text, needle string) string {
	if needle == "" {
		return text
	}
	return strings.ReplaceAll(text, needle, "[REDACTED: this node's session key]")
}

// checkPeerCAPinsRealChain is the item planning/reports/needs-hardware-validation.md
// has carried since T-1906: every test CA and certificate in this repository
// is built in-process with crypto/x509, so what PVE actually issues has never
// been checked.
//
// It also encodes T-1906-bug-01, which a real node found and no fixture would
// have: pvecube's leaf carried a stale IP SAN, so a peer dialled by IP would
// have failed pinned hostname verification while every test passed.
func checkPeerCAPinsRealChain(ctx context.Context, d Deps) Outcome {
	if d.Host == nil {
		return skipNoHost("reading the real pve-root-ca.pem and this node's leaf certificate")
	}
	node := localNode(d.Nodes)
	const (
		caPath   = "/etc/pve/pve-root-ca.pem"
		leafPath = "/etc/pve/local/pve-ssl.pem"
	)

	if _, err := d.Host.ReadFile(ctx, node, caPath); err != nil {
		return Fail(fmt.Sprintf("%s is not readable on %s: %v. internal/peer pins this file as its sole trust anchor and fails closed without it", caPath, node, err),
			NewEvidence(SourceFile, caPath, err.Error()))
	}

	verifyOut, verifyErr := d.Host.Run(ctx, node, "openssl", "verify", "-CAfile", caPath, leafPath)
	verifyEv := NewEvidence(SourceCommand, fmt.Sprintf("openssl verify -CAfile %s %s", caPath, leafPath), verifyOut)
	if verifyErr != nil || !strings.Contains(verifyOut, "OK") {
		return Fail(fmt.Sprintf("%s does not verify against the cluster CA on %s: %s", leafPath, node, firstLine(verifyOut)),
			NewEvidence(SourceCommand, fmt.Sprintf("openssl verify -CAfile %s %s", caPath, leafPath), fmt.Sprintf("%s\nerror: %v", verifyOut, verifyErr)))
	}

	sanOut, sanErr := d.Host.Run(ctx, node, "openssl", "x509", "-in", leafPath, "-noout", "-ext", "subjectAltName")
	sanEv := NewEvidence(SourceCommand, fmt.Sprintf("openssl x509 -in %s -noout -ext subjectAltName", leafPath), sanOut)
	if sanErr != nil {
		return Fail(fmt.Sprintf("the leaf on %s verifies but its SAN list could not be read: %v", node, sanErr), verifyEv, sanEv)
	}

	// What must be covered is a name internal/peer can actually verify
	// against — NOT necessarily the dial address.
	//
	// This assertion was wrong until 2026-08-16, and wrong in the direction
	// that costs the most: it demanded the dial address appear in the SAN
	// list, which is the pre-T-2303 rule. T-2303 changed peer verification
	// precisely so a certificate that covers the node name works even when
	// no IP SAN matches (certs.ResolveVerifyName, rules 1 and 2; the trust
	// layer then sets ServerName to the resolved name — internal/peer/trust.go).
	// On pvecube, whose real PVE leaf carries `DNS:pvecube`,
	// `DNS:pvecube.localdomain.` and an IP SAN for a different interface than
	// the one the operator reaches it on, this check reported a FAIL against
	// a node the product handles correctly. A hardware suite that cries wolf
	// gets ignored, which costs more than the check was ever worth.
	//
	// So: the node name (or an FQDN rooted at it) is the primary covered
	// name, and the dial address is an accepted alternative — the same
	// precedence ResolveVerifyName itself applies.
	sanList := strings.TrimSpace(sanOut)
	addr := nodeAddress(d.Nodes, node)
	coversNodeName := sanCovers(sanOut, node)
	coversAddr := addr != "" && sanCovers(sanOut, addr)

	switch {
	case coversNodeName:
		detail := fmt.Sprintf("%s's PVE-issued leaf verifies against the real cluster CA and its SAN list covers the node name %q, which is what internal/peer resolves a peer's dial host to (T-2303)", node, node)
		if addr != "" && !coversAddr {
			detail += fmt.Sprintf(". The dial address %s is NOT in the SAN list, and that is fine by design — this is exactly the T-1906-bug-01 shape T-2303 stopped failing closed", addr)
		}
		return Pass(detail, verifyEv, sanEv)
	case coversAddr:
		return Pass(fmt.Sprintf("%s's PVE-issued leaf verifies against the real cluster CA and its SAN list covers the dial address %s (rule 3 of certs.ResolveVerifyName). It does not cover the node name, which is unusual for PVE-issued certificates but valid", node, addr), verifyEv, sanEv)
	default:
		return Fail(fmt.Sprintf("%s's leaf verifies against the cluster CA but its SAN list covers neither the node name %q nor the dial address %q, so certs.ResolveVerifyName reports covered=false and the first peer call fails closed. Full SAN list: %s",
			node, node, addr, sanList), verifyEv, sanEv)
	}
}

// sanCovers reports whether an `openssl x509 -ext subjectAltName` dump names
// the given host.
//
// It exists because the surrounding check used to report its evidence through
// firstLine(), and openssl prints the header on line 1 with every SAN on the
// line *after* it — so a failing verdict rendered as
// "X509v3 Subject Alternative Name:" with nothing after the colon, which reads
// as "this certificate has no SANs" for a certificate carrying six. In a
// report designed to be sent to strangers, that is worse than saying nothing.
//
// Matching is substring-based on purpose: the openssl dump interleaves
// `DNS:` and `IP Address:` prefixes and PVE emits a root-dotted FQDN
// ("pvecube.localdomain."), so parsing it into a typed list would add a second
// place for the format to be wrong. The comparison is case-insensitive because
// DNS names are.
func sanCovers(sanDump, host string) bool {
	if host == "" {
		return false
	}
	return strings.Contains(strings.ToLower(sanDump), strings.ToLower(host))
}

func nodeAddress(nodes []Node, name string) string {
	for _, n := range nodes {
		if n.Name == name {
			return n.Address
		}
	}
	return ""
}

// checkCertsInventory cross-checks the daemon's certificate inventory against
// the pmxcfs directory it is built from — the same two-source shape as the
// LLDP check, and for the same reason.
func checkCertsInventory(ctx context.Context, d Deps) Outcome {
	if d.Host == nil {
		return skipNoHost("listing this cluster's certificates under /etc/pve/nodes")
	}
	node := localNode(d.Nodes)

	listing, err := d.Host.Run(ctx, node, "sh", "-c", "ls -1 /etc/pve/nodes/*/pve-ssl.pem 2>/dev/null")
	if err != nil {
		return Skip(fmt.Sprintf("could not list /etc/pve/nodes/*/pve-ssl.pem on %s: %v. This needs a real pmxcfs mount", node, err),
			NewEvidence(SourceCommand, "ls -1 /etc/pve/nodes/*/pve-ssl.pem", err.Error()))
	}
	diskEv := NewEvidence(SourceCommand, "ls -1 /etc/pve/nodes/*/pve-ssl.pem", listing)
	onDisk := nonEmptyLines(listing)
	if len(onDisk) == 0 {
		return Skip(fmt.Sprintf("no per-node certificate is present under /etc/pve/nodes on %s, so there was nothing for the inventory to have found. This needs a real pmxcfs mount", node), diskEv)
	}

	var body struct {
		Inventory struct {
			ScannedAt    string `json:"scannedAt"`
			Certificates []struct {
				Path string `json:"path"`
				Node string `json:"node"`
			} `json:"certificates"`
		} `json:"inventory"`
	}
	apiEv, err := daemonJSON(ctx, d, "/certs", &body)
	if err != nil {
		if errorIsNoDaemon(err) {
			return skipNoDaemon("the certificate inventory")
		}
		return Fail(fmt.Sprintf("%d certificate(s) on disk but the inventory could not be read: %v", len(onDisk), err), diskEv, apiEv)
	}

	// A private key must never appear in an inventory response. This is
	// internal/certs' central invariant and it is worth re-checking against a
	// response built from real files rather than fixtures.
	if strings.Contains(apiEv.Output, "PRIVATE KEY") {
		return Fail("the certificate inventory response carries PEM private-key material", diskEv, apiEv)
	}

	known := make([]string, 0, len(body.Inventory.Certificates))
	for _, c := range body.Inventory.Certificates {
		known = append(known, c.Path)
	}
	var missing []string
	for _, p := range onDisk {
		if !containsString(known, p) {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return Fail(fmt.Sprintf("%d of %d certificate(s) present on this real pmxcfs mount are absent from the inventory: %s", len(missing), len(onDisk), strings.Join(missing, ", ")), diskEv, apiEv)
	}
	return Pass(fmt.Sprintf("every one of the %d certificate(s) on this real pmxcfs mount appears in the inventory, and no private key does", len(onDisk)), diskEv, apiEv)
}

// checkCLIDaemonIndependent runs the operator CLI's own self-check on the
// real install. Row 74 is marked `V` because `certs`, `backup` and
// `support-bundle` were run on pvecube; this keeps that true by running one
// of them on every hardware pass rather than trusting a dated note.
func checkCLIDaemonIndependent(ctx context.Context, d Deps) Outcome {
	if d.Host == nil {
		return skipNoHost("running vnproxctl on the node")
	}
	node := localNode(d.Nodes)

	versionOut, err := d.Host.Run(ctx, node, "vnproxctl", "--version")
	versionEv := NewEvidence(SourceCommand, "vnproxctl --version", versionOut)
	if err != nil {
		return Fail(fmt.Sprintf("vnproxctl is not runnable on %s: %v", node, err), NewEvidence(SourceCommand, "vnproxctl --version", versionOut+"\n"+err.Error()))
	}

	doctorOut, doctorErr := d.Host.Run(ctx, node, "vnproxctl", "doctor", "-o", "json")
	doctorEv := NewEvidence(SourceCommand, "vnproxctl doctor -o json", doctorOut)

	var report struct {
		Results []struct {
			Check  string `json:"check"`
			Status string `json:"status"`
			// `vnproxctl doctor -o json` emits "detail", not "remediation".
			// The old "remediation" field decoded to "" on every real run;
			// nothing read it, so unlike the backup mismatch above this was
			// harmless — removed rather than corrected because the check has
			// no use for it.
		} `json:"results"`
		Summary struct {
			Pass int `json:"pass"`
			Warn int `json:"warn"`
			Fail int `json:"fail"`
			Skip int `json:"skip"`
		} `json:"summary"`
	}
	// doctor exits non-zero when a check fails, so a non-nil error here is
	// expected on an unhealthy install and the body still parses.
	if jsonErr := json.Unmarshal([]byte(doctorOut), &report); jsonErr != nil {
		return Fail(fmt.Sprintf("vnproxctl doctor on %s did not produce its documented JSON (%v): %v", node, doctorErr, jsonErr), versionEv, doctorEv)
	}
	if len(report.Results) == 0 {
		return Fail(fmt.Sprintf("vnproxctl doctor on %s reported no checks at all", node), versionEv, doctorEv)
	}
	if report.Summary.Fail > 0 {
		var failed []string
		for _, r := range report.Results {
			if r.Status == "fail" {
				failed = append(failed, r.Check)
			}
		}
		return Fail(fmt.Sprintf("vnproxctl doctor reports %d failing check(s) on this real install: %s", report.Summary.Fail, strings.Join(failed, ", ")), versionEv, doctorEv)
	}
	return Pass(fmt.Sprintf("%s runs the daemon-independent CLI: doctor reports %d passed, %d warned, %d skipped, 0 failed on the real install",
		firstLine(versionOut), report.Summary.Pass, report.Summary.Warn, report.Summary.Skip), versionEv, doctorEv)
}

// --- helpers -----------------------------------------------------------------

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// extractTOMLSection returns the lines of one top-level TOML table. It is a
// deliberately small reader rather than a TOML parse: the only question asked
// is "is this flag set", and pulling internal/config's whole loader (and its
// secret-bearing fields) into a validation check to answer it would be worse.
func extractTOMLSection(text, name string) string {
	var out []string
	var inSection bool
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inSection = trimmed == "["+name+"]"
			if inSection {
				out = append(out, trimmed)
			}
			continue
		}
		if inSection {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return fmt.Sprintf("(no [%s] section)", name)
	}
	return strings.Join(out, "\n")
}

func tomlBoolTrue(section, key string) bool {
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq <= 0 || strings.TrimSpace(trimmed[:eq]) != key {
			continue
		}
		return strings.TrimSpace(trimmed[eq+1:]) == "true"
	}
	return false
}

// errorIsNoDaemon keeps the sentinel comparison in one place for the checks
// that wrap daemonJSON's error before inspecting it.
func errorIsNoDaemon(err error) bool {
	return err != nil && strings.Contains(err.Error(), errNoDaemon.Error())
}
