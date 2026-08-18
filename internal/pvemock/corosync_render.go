package pvemock

import (
	"fmt"
	"strings"
)

// corosync_render.go renders a node's fixture-declared CorosyncSpec (T-803)
// into the plain-text shape `corosync-cfgtool -s` produces — the same
// "fixture data rendered through the real parser" precedent frr_render.go
// set for FRR/marshalLLDP set for lldpctl.
//
// **Correction, backed by real hardware evidence
// (planning/reports/blocked-validation.md §2.1, a real 2-node PVE 9.2.10
// cluster):** this renders the OLDER "RING ID n" / "id\t=" / "status\t="
// shape, which this comment used to (incorrectly) describe as "knet
// transport" — real knet output (knet has been PVE's default transport
// since 6.x, so this is the common case on any real deployment today) uses
// a "LINK ID n <transport>" header with a nested per-peer "nodeid: N:
// <state>" shape entirely unlike what is rendered below (see
// internal/host.ParseCorosyncStatus's and RingStatus's own doc comments for
// the real captured text, and internal/host/corosync_test.go's
// realKnetCorosyncStatusHealthy, which uses it verbatim as a fixture).
// internal/host.ParseCorosyncStatus now parses both shapes, so this mock
// continuing to render the older one is not a functional bug for the
// parser — but rendering it while calling it "knet" was exactly the kind
// of secondary-source-agreeing-with-itself mismodeling CLAUDE.md's
// evidence-first mandate warns about, so the mislabel is corrected here.
// Rendering the real knet shape instead would need CorosyncSpec/RingSpec to
// model per-peer connection state (which node id is "localhost", which are
// "connected"), not just one Faulty bool per ring — left as a real,
// larger follow-up rather than folded into this correction.
func marshalCorosyncStatus(spec *CorosyncSpec) []byte {
	if spec == nil {
		return nil
	}
	var b strings.Builder
	b.WriteString("Printing ring status.\n")
	b.WriteString("Local node ID 1\n")
	for _, ring := range spec.Rings {
		fmt.Fprintf(&b, "RING ID %d\n", ring.ID)
		fmt.Fprintf(&b, "\tid\t= %s\n", ring.Addr)
		status := ring.StatusText
		if status == "" {
			if ring.Faulty {
				status = fmt.Sprintf("Marking ringid %d interface %s FAULTY - administrative intervention required.", ring.ID, ring.Addr)
			} else {
				status = fmt.Sprintf("ring %d active with no faults", ring.ID)
			}
		}
		fmt.Fprintf(&b, "\tstatus\t= %s\n", status)
	}
	return []byte(b.String())
}
