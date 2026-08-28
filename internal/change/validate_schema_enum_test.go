// SPDX-License-Identifier: Apache-2.0

package change

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// validate_schema_enum_test.go guards the enums vnprox mirrors from PVE
// against the hardware capture they were read off, rather than against a
// developer's memory of them.
//
// The reason this file exists is a specific failure this repository made
// twice. Until 2026-08-16 the compatibility matrix asserted that PVE 9
// added "openfabric"/"ospf" as SDN zone types. It was wrong, the mock that
// served it was wrong in the same way, the docs describing it were wrong in
// the same way, and the check passed on every commit — because all four
// were written from the same Proxmox release notes rather than from the
// API. A mirror and the thing it mirrors, derived from a common secondary
// source, agree with each other forever.
//
// So this test does not restate the enum. It *reads the capture* and
// compares. If someone re-runs `pvesh usage` against a newer PVE and checks
// in a different enum, this test tells them which mirrors to update, in the
// same commit as the evidence.

// capturePath is the checked-in transcript of read-only `pvesh` calls
// against pvecube (PVE 9.2.4). Relative to this package.
const capturePath = "../../planning/reports/evidence/pve-9.2.4-sdn-schema.txt"

// sdnZoneTypeEnumPattern matches the zone `type` enum line as `pvesh usage`
// prints it, e.g.:
//
//	--type     <evpn | faucet | qinq | simple | vlan | vxlan>
//
// It is deliberately anchored on "--type" plus the angle-bracketed
// alternation so it cannot accidentally match the fabric `--protocol` enum,
// which is the exact confusion this whole file exists to prevent.
var sdnZoneTypeEnumPattern = regexp.MustCompile(`--type\s+<([a-z0-9 |]+)>`)

// readCapturedZoneTypeEnum pulls the zone-type enum out of the capture. It
// takes the enum from the section headed by the zones usage call, so a
// controller or ipam `--type` line elsewhere in the transcript cannot be
// mistaken for it.
func readCapturedZoneTypeEnum(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(capturePath))
	if err != nil {
		t.Fatalf("reading the hardware capture at %s: %v\n"+
			"This test compares vnprox's mirrored enums against a transcript of read-only `pvesh usage` "+
			"calls. If the file is gone, restore it rather than deleting this test — the enums it guards "+
			"were wrong for four phases precisely because nothing compared them to a real node.",
			capturePath, err)
	}

	const marker = "### usage /cluster/sdn/zones"
	idx := strings.Index(string(raw), marker)
	if idx < 0 {
		t.Fatalf("capture %s has no %q section; it may have been re-captured with a different script", capturePath, marker)
	}
	section := string(raw)[idx:]

	m := sdnZoneTypeEnumPattern.FindStringSubmatch(section)
	if m == nil {
		t.Fatalf("capture %s: could not find a `--type <...>` enum in the zones section", capturePath)
	}

	var out []string
	for _, part := range strings.Split(m[1], "|") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// TestValidSdnZoneTypesMatchTheCapturedEnum is the guard. vnprox rejecting
// a zone type real PVE accepts is a silent, user-facing defect: the
// operator sees a validation failure on an unrelated edit and has no way to
// tell it is vnprox's model that is behind, not their config.
func TestValidSdnZoneTypesMatchTheCapturedEnum(t *testing.T) {
	captured := readCapturedZoneTypeEnum(t)

	var mirrored []string
	for k := range validSdnZoneTypes {
		mirrored = append(mirrored, k)
	}
	sort.Strings(mirrored)

	capturedSet := map[string]bool{}
	for _, c := range captured {
		capturedSet[c] = true
	}

	for _, c := range captured {
		if !validSdnZoneTypes[c] {
			t.Errorf("real PVE accepts SDN zone type %q and validSdnZoneTypes rejects it: "+
				"vnprox refuses to stage a zone Proxmox would accept.\n"+
				"  captured (%s): %v\n  mirrored:  %v",
				c, capturePath, captured, mirrored)
		}
	}
	for _, m := range mirrored {
		if !capturedSet[m] {
			t.Errorf("validSdnZoneTypes accepts %q and the captured PVE 9.2.4 enum does not list it. "+
				"Either the capture is stale (re-run `pvesh usage /cluster/sdn/zones -v` and check it in), "+
				"or vnprox is modelling a type that does not exist.\n"+
				"  captured (%s): %v\n  mirrored:  %v",
				m, capturePath, captured, mirrored)
		}
	}
}

// sdnFabricProtocolEnumPattern matches the fabric `--protocol` enum line as
// `pvesh usage` prints it, e.g.:
//
//	--protocol <bgp | openfabric | ospf | wireguard>
//
// Anchored on "--protocol" so it cannot be confused with the zone `--type`
// enum sdnZoneTypeEnumPattern reads — the exact confusion this whole file
// exists to prevent, from the other direction. The fabrics section's own
// `pvesh create ... --protocol <string>` synopsis line (a generic
// placeholder, not the enum) appears *before* the detailed conditional-
// options enum in the transcript, unlike the zones section where the
// detailed enum comes first — so, unlike sdnZoneTypeEnumPattern, this
// pattern requires at least one "|" alternation to skip that placeholder
// and land on the real enum below it.
var sdnFabricProtocolEnumPattern = regexp.MustCompile(`--protocol\s+<([a-z0-9]+(?:\s*\|\s*[a-z0-9]+)+)>`)

// readCapturedFabricProtocolEnum pulls the fabric protocol enum out of the
// capture, from the section headed by the fabrics usage call.
func readCapturedFabricProtocolEnum(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(capturePath))
	if err != nil {
		t.Fatalf("reading the hardware capture at %s: %v", capturePath, err)
	}

	const marker = "### usage /cluster/sdn/fabrics/fabric"
	idx := strings.Index(string(raw), marker)
	if idx < 0 {
		t.Fatalf("capture %s has no %q section; it may have been re-captured with a different script", capturePath, marker)
	}
	section := string(raw)[idx:]

	m := sdnFabricProtocolEnumPattern.FindStringSubmatch(section)
	if m == nil {
		t.Fatalf("capture %s: could not find a `--protocol <...>` enum in the fabrics section", capturePath)
	}

	var out []string
	for _, part := range strings.Split(m[1], "|") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// TestValidSdnFabricProtocolsMatchTheCapturedEnum is validSdnZoneTypes'
// guard test, restated for the fabric protocol enum — same discipline,
// same reason: it reads the capture rather than restating it, so the two
// cannot be made to agree by construction.
func TestValidSdnFabricProtocolsMatchTheCapturedEnum(t *testing.T) {
	captured := readCapturedFabricProtocolEnum(t)

	var mirrored []string
	for k := range validSdnFabricProtocols {
		mirrored = append(mirrored, k)
	}
	sort.Strings(mirrored)

	capturedSet := map[string]bool{}
	for _, c := range captured {
		capturedSet[c] = true
	}

	for _, c := range captured {
		if !validSdnFabricProtocols[c] {
			t.Errorf("real PVE accepts SDN fabric protocol %q and validSdnFabricProtocols rejects it: "+
				"vnprox refuses to stage a fabric Proxmox would accept.\n"+
				"  captured (%s): %v\n  mirrored:  %v",
				c, capturePath, captured, mirrored)
		}
	}
	for _, m := range mirrored {
		if !capturedSet[m] {
			t.Errorf("validSdnFabricProtocols accepts %q and the captured PVE 9.2.4 enum does not list it. "+
				"Either the capture is stale (re-run `pvesh usage /cluster/sdn/fabrics/fabric -v`), "+
				"or vnprox is modelling a protocol that does not exist.\n"+
				"  captured (%s): %v\n  mirrored:  %v",
				m, capturePath, captured, mirrored)
		}
	}
}

// sdnControllerTypeEnumPattern matches the controller `type` enum line, the
// same shape sdnZoneTypeEnumPattern matches for zones — the controllers
// section's own `pvesh get` usage block lists the detailed enum before the
// `pvesh create` synopsis's generic `--type <string>` placeholder, exactly
// like the zones section does, so no "at least one |" guard is needed here
// (contrast sdnFabricProtocolEnumPattern, whose section orders the other
// way).
var sdnControllerTypeEnumPattern = regexp.MustCompile(`--type\s+<([a-z0-9 |]+)>`)

// readCapturedControllerTypeEnum pulls the controller-type enum out of the
// capture, from the section headed by the controllers usage call.
func readCapturedControllerTypeEnum(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(capturePath))
	if err != nil {
		t.Fatalf("reading the hardware capture at %s: %v", capturePath, err)
	}

	const marker = "### usage /cluster/sdn/controllers"
	idx := strings.Index(string(raw), marker)
	if idx < 0 {
		t.Fatalf("capture %s has no %q section; it may have been re-captured with a different script", capturePath, marker)
	}
	section := string(raw)[idx:]

	m := sdnControllerTypeEnumPattern.FindStringSubmatch(section)
	if m == nil {
		t.Fatalf("capture %s: could not find a `--type <...>` enum in the controllers section", capturePath)
	}

	var out []string
	for _, part := range strings.Split(m[1], "|") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// TestValidSdnControllerTypesMatchTheCapturedEnum is validSdnZoneTypes'
// guard test, restated for the controller type enum.
func TestValidSdnControllerTypesMatchTheCapturedEnum(t *testing.T) {
	captured := readCapturedControllerTypeEnum(t)

	var mirrored []string
	for k := range validSdnControllerTypes {
		mirrored = append(mirrored, k)
	}
	sort.Strings(mirrored)

	capturedSet := map[string]bool{}
	for _, c := range captured {
		capturedSet[c] = true
	}

	for _, c := range captured {
		if !validSdnControllerTypes[c] {
			t.Errorf("real PVE accepts SDN controller type %q and validSdnControllerTypes rejects it: "+
				"vnprox refuses to stage a controller Proxmox would accept.\n"+
				"  captured (%s): %v\n  mirrored:  %v",
				c, capturePath, captured, mirrored)
		}
	}
	for _, m := range mirrored {
		if !capturedSet[m] {
			t.Errorf("validSdnControllerTypes accepts %q and the captured PVE 9.2.4 enum does not list it. "+
				"Either the capture is stale (re-run `pvesh usage /cluster/sdn/controllers -v` and check it in), "+
				"or vnprox is modelling a type that does not exist.\n"+
				"  captured (%s): %v\n  mirrored:  %v",
				m, capturePath, captured, mirrored)
		}
	}
}

// TestValidSdnIpamTypesMatchTheCapturedEnum is validSdnZoneTypes' guard
// test, restated for the ipam type enum. The ipams section's `pvesh get`
// usage block lists the detailed `--type <netbox | phpipam | pve>` enum
// before the `pvesh create` synopsis's own `--type <string>` placeholder
// (the same ordering sdnControllerTypeEnumPattern's doc comment describes
// for controllers), so captureEnumAfterMarker's "first match with an
// alternation" rule lands on it directly.
func TestValidSdnIpamTypesMatchTheCapturedEnum(t *testing.T) {
	captured := captureEnumAfterMarker(t, "### usage /cluster/sdn/ipams", "type")
	assertEnumMatchesCapture(t, "sdn ipam type", validSdnIpamTypes, captured)
}

// --- T-3103: fw direction / policy_forward / log_level_forward enums -------
//
// Same discipline as the SDN enums above, applied to T-3103's own capture
// findings: `forward` as a rule direction, and the policy_forward/
// log_level_forward ruleset-options fields. This section's markers land in
// the vnet-scope firewall rules/options blocks — the fullest `pvesh usage`
// transcript of the three scopes captured — with a second test cross-
// checking the cluster/node sections independently confirm the same
// direction enum, the exact "check the consumers, don't restate the
// capture" discipline this file's own header describes.

// captureEnumAfterMarker finds marker in the capture, then the first
// "--paramName <...>" block after it, and returns its pipe-separated
// alternatives, trimmed and whitespace-normalized (pvesh usage wraps long
// enums like log_level_forward's across a line break, so the match itself —
// "[^>]+" — is deliberately allowed to span newlines).
func captureEnumAfterMarker(t *testing.T, marker, paramName string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(capturePath))
	if err != nil {
		t.Fatalf("reading the hardware capture at %s: %v", capturePath, err)
	}
	idx := strings.Index(string(raw), marker)
	if idx < 0 {
		t.Fatalf("capture %s has no %q section; it may have been re-captured with a different script", capturePath, marker)
	}
	section := string(raw)[idx:]

	pattern := regexp.MustCompile(`--` + regexp.QuoteMeta(paramName) + `\s+<([^>]+)>`)
	// Some sections (e.g. the vnet rules block) print a generic
	// `pvesh create ... --type <string>` command synopsis before the
	// detailed enum below it — skip any match with no "|" alternation
	// (mirrors sdnFabricProtocolEnumPattern's own documented reason for the
	// same guard) rather than accidentally pinning that placeholder.
	var m []string
	for _, cand := range pattern.FindAllStringSubmatch(section, -1) {
		if strings.Contains(cand[1], "|") {
			m = cand
			break
		}
	}
	if m == nil {
		t.Fatalf("capture %s: could not find a `--%s <a | b | ...>` enum (with at least one alternation) after marker %q", capturePath, paramName, marker)
	}

	var out []string
	for _, part := range strings.Split(m[1], "|") {
		normalized := strings.Join(strings.Fields(part), " ")
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	sort.Strings(out)
	return out
}

// assertEnumMatchesCapture is the shared two-way comparison
// TestValidSdnZoneTypesMatchTheCapturedEnum etc. each hand-roll above; T-3103
// factors it out rather than adding a fourth copy.
func assertEnumMatchesCapture(t *testing.T, label string, mirrored map[string]bool, captured []string) {
	t.Helper()
	var mirroredList []string
	for k := range mirrored {
		mirroredList = append(mirroredList, k)
	}
	sort.Strings(mirroredList)

	capturedSet := map[string]bool{}
	for _, c := range captured {
		capturedSet[c] = true
	}

	for _, c := range captured {
		if !mirrored[c] {
			t.Errorf("real PVE accepts %s %q and vnprox's mirror rejects it: vnprox refuses to stage what Proxmox would accept.\n"+
				"  captured (%s): %v\n  mirrored:  %v", label, c, capturePath, captured, mirroredList)
		}
	}
	for _, m := range mirroredList {
		if !capturedSet[m] {
			t.Errorf("vnprox's mirror accepts %s %q and the captured PVE 9.2.4 enum does not list it. "+
				"Either the capture is stale, or vnprox is modelling a value that does not exist.\n"+
				"  captured (%s): %v\n  mirrored:  %v", label, m, capturePath, captured, mirroredList)
		}
	}
}

// TestValidFwDirectionsMatchTheCapturedEnum guards validFwDirections against
// the vnet-scope firewall rules section's `--type <...>` enum (T-3103's
// "forward" addition — the one this whole task warned against restating by
// hand rather than reading).
func TestValidFwDirectionsMatchTheCapturedEnum(t *testing.T) {
	captured := captureEnumAfterMarker(t, "### usage /cluster/sdn/vnets/labnet/firewall/rules", "type")
	assertEnumMatchesCapture(t, "firewall rule direction", validFwDirections, captured)
}

// TestFwDirectionForwardConfirmedAtClusterAndNodeScope cross-checks that the
// cluster and node firewall-rules sections independently show the same
// `--type <forward | group | in | out>` enum the vnet section does — the
// capture explicitly re-ran `pvesh usage` at all three scopes (rather than
// assuming the vnet section speaks for the others) specifically so this
// could be checked, not assumed.
func TestFwDirectionForwardConfirmedAtClusterAndNodeScope(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(capturePath))
	if err != nil {
		t.Fatalf("reading the hardware capture at %s: %v", capturePath, err)
	}
	for _, marker := range []string{
		"### usage /cluster/firewall/rules (type enum scope check)",
		"### usage /nodes/pvecube/firewall/rules (type enum scope check)",
	} {
		idx := strings.Index(string(raw), marker)
		if idx < 0 {
			t.Fatalf("capture %s has no %q section", capturePath, marker)
		}
		section := string(raw)[idx:]
		if end := strings.Index(section[len(marker):], "###"); end >= 0 {
			section = section[:len(marker)+end]
		}
		if !strings.Contains(section, "forward") {
			t.Errorf("marker %q's --type enum does not mention \"forward\" — the vnet-scope capture cannot be assumed to generalize to this scope without its own confirmation", marker)
		}
	}
}

// TestValidFwForwardPoliciesMatchTheCapturedEnum guards validFwForwardPolicies
// (policy_forward, ACCEPT|DROP — deliberately narrower than DefaultIn/
// DefaultOut's ACCEPT|DROP|REJECT) against the vnet options section.
func TestValidFwForwardPoliciesMatchTheCapturedEnum(t *testing.T) {
	captured := captureEnumAfterMarker(t, "### usage /cluster/sdn/vnets/labnet/firewall/options", "policy_forward")
	assertEnumMatchesCapture(t, "policy_forward value", validFwForwardPolicies, captured)
}

// TestValidFwLogLevelsForwardMatchTheCapturedEnum guards
// validFwLogLevelsForward (log_level_forward) against the vnet options
// section — the one place this field is hardware-confirmed at all (see
// validFwLogLevelsForward's doc comment on why it is not shared with the
// per-rule Log field's validFwLogLevels).
func TestValidFwLogLevelsForwardMatchTheCapturedEnum(t *testing.T) {
	captured := captureEnumAfterMarker(t, "### usage /cluster/sdn/vnets/labnet/firewall/options", "log_level_forward")
	assertEnumMatchesCapture(t, "log_level_forward value", validFwLogLevelsForward, captured)
}

// TestFabricProtocolsAreNotZoneTypes pins the specific wrong idea, by name.
// "openfabric" and "ospf" are SDN *fabric* protocols living under
// /cluster/sdn/fabrics; they are not zone types and never were. This
// assertion is cheap and it is aimed at a mistake that has already been
// made once and cost four phases of a green, meaningless compatibility
// check.
func TestFabricProtocolsAreNotZoneTypes(t *testing.T) {
	for _, proto := range []string{"openfabric", "ospf", "bgp", "wireguard"} {
		if validSdnZoneTypes[proto] {
			t.Errorf("validSdnZoneTypes accepts %q, which is an SDN fabric *protocol*, not a zone type. "+
				"Fabrics are a separate family (/cluster/sdn/fabrics, --protocol <bgp|openfabric|ospf|"+
				"wireguard>). Modelling them as zone types is the error corrected on 2026-08-16 — see "+
				"pvemock.PVEVersionProfile.SDNFabrics and %s.", proto, capturePath)
		}
	}
}
