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
