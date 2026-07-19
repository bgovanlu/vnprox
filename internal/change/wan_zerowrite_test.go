package change

import (
	"strings"
	"testing"
)

// TestNoWanFailoverOpType is T-1405 acceptance criterion 5: no changeset op
// type exists anywhere in the v1 op vocabulary that could write a WAN
// failover / uplink-switching mutation — WAN & upstream health
// (GET /wan/status, the probe loop, GET/PUT /wan/targets) is visibility and
// configured-reference-target management only, per this task's own package
// doc comment ("no WAN failover automation anywhere in this package").
// GET/PUT /wan/targets themselves are outside the changeset system
// entirely (an ordinary netWrite+CSRF route, like alert_rules or protected-
// interfaces, not a `nat.*`/`route.static.*`-style op group) — this test
// pins that no op type was ever introduced for WAN failover specifically,
// the same structural check TestNoDHCPv6PDOpType (T-1404) already performs
// for its own analogous read-only-surface guarantee.
func TestNoWanFailoverOpType(t *testing.T) {
	for opType := range paramFactories {
		lower := strings.ToLower(string(opType))
		if strings.Contains(lower, "wan") || strings.Contains(lower, "failover") || strings.Contains(lower, "uplink") {
			t.Errorf("op type %q suggests a WAN failover/uplink-switching write surface — T-1405 must never introduce one", opType)
		}
	}
}
