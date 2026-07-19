package host

// IPv6RAObservation is one interface's bounded, host-local IPv6 Router
// Advertisement / DHCPv6 observation (T-1404, docs/features/sdn.md §6).
//
// DHCPv6ServerPresence limitation (documented, not guessed past): unlike
// RAPresent/ManagedFlag/OtherFlag/Prefixes/RouterLifetimeSec — all of which
// come directly off an observed Router Advertisement — this package has no
// independent way to confirm a DHCPv6 server actually answers on the
// segment without completing a real DHCPv6 SOLICIT/ADVERTISE exchange
// (out of scope for a bounded, read-only observation — the same "read,
// never write/probe-as-a-client" boundary this task's card draws for the
// WAN/upstream and reverse-proxy surfaces). DHCPv6ServerPresent is
// therefore *inferred* from the RA's own Managed (M) flag — RFC 4861 §4.2:
// M=1 means "hosts should use DHCPv6 for address configuration", which in
// practice means a DHCPv6 server is expected to exist and answer — and
// InferredFromRA is always true when DHCPv6ServerPresent is true, so a
// caller can render "expected (from RA M-flag)" rather than a bare
// confirmed/not-confirmed boolean. **Needs hardware validation**
// (planning/reports/needs-hardware-validation.md): a real cluster could
// legitimately advertise M=1 while transiently having no DHCPv6 server
// reachable (a misconfiguration this field would then under-report).
type IPv6RAObservation struct {
	Iface string
	// Prefixes are the RA's advertised on-link prefixes, in CIDR form.
	Prefixes             []string
	RouterLifetimeSec    int
	Vlan                 int // 0 when the interface has no VLAN tag of its own
	RAPresent            bool
	ManagedFlag          bool
	OtherFlag            bool
	DHCPv6ServerPresent  bool
	DHCPv6InferredFromRA bool
}
