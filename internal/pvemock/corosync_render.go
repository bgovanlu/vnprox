package pvemock

import (
	"fmt"
	"strings"
)

// corosync_render.go renders a node's fixture-declared CorosyncSpec (T-803)
// into the plain-text shape `corosync-cfgtool -s` produces — the same
// "fixture data rendered through the real parser" precedent frr_render.go
// set for FRR/marshalLLDP set for lldpctl. Real corosync-cfgtool output is
// not JSON, and its exact wording/format varies across corosync versions
// (see planning/reports/needs-hardware-validation.md); this renders one
// commonly-observed shape (knet transport, "Printing ring status." header,
// one "RING ID n" block per ring with "id"/"status" fields) that
// internal/host.ParseCorosyncStatus is written to tolerate, not a verified
// byte-exact capture from a real cluster.
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
