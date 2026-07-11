// Package fwlog implements T-505's cluster-wide pve-firewall log viewer:
// parsing pve-firewall's own log line format (ParseLine/ParseAll), a
// best-effort correlator that maps a parsed line back to the configured
// rule that (most likely) produced it (Correlate), a bounded ring buffer
// with a rate cap for log-storm handling (RingBuffer, Service.Tick), and
// the Source seam abstracting "read this node's firewall log" over both a
// real file (FileSource) and an in-memory fixture/test double
// (MemorySource).
//
// # Log format (grounded in upstream pve-firewall, not guessed)
//
// docs/features/firewall.md §4 says pve-firewall logs "include rule
// references in most drop/reject cases", but the actual upstream source
// (proxmox/pve-firewall, src/pvefw-logger.c and src/PVE/Firewall.pm,
// checked against the real repository during this task) tells a more
// specific — and more limited — story:
//
//   - Every log line's format is documented in pvefw-logger.c verbatim as:
//     "<VMID> <LOGLEVEL> <CHAIN> <TIME> <TIMEZONE> <MSG>", e.g.
//     "117 6 tap117i0-IN 14/Mar/2014:12:47:07 +0100 policy REJECT: IN=vmbr1 ...".
//   - The NFLOG prefix every LOG rule carries is built by
//     get_log_rule_base() as literally ":$vmid:$loglevel:$chain: $msg" —
//     $msg is either "policy $policy: " for a default-policy fallthrough,
//     or just "$rule->{action}: " (e.g. "DROP: ") for an explicit rule
//     match. Neither form embeds the rule's position/index in the chain.
//
// So real pve-firewall log lines identify a *chain* (which, for a guest,
// encodes vmid/nic/direction: "tap<vmid>i<nic>-IN"/"-OUT") and an *action*
// (ACCEPT/DROP/REJECT/...), never an explicit rule number. Correlate's
// heuristic — find the enabled rule(s) in that guest's resolved evaluation
// order (internal/fw.Resolve) whose direction and action match, narrowed by
// protocol/port when the rule specifies them — is the closest a log line
// can honestly get to "the matching rule" given what the log actually
// contains. When more than one rule remains after narrowing, or none do,
// or the chain isn't a recognized guest chain, or the guest has no
// observed firewall data yet, Correlate says so plainly (CorrelationStatus)
// rather than guessing — the same "no silent approximation" ethos
// docs/features/firewall.md §6 states for the path simulator.
//
// This is flagged as a deviation worth a second look once real hardware is
// available (CLAUDE.md/docs/development.md's "needs hardware validation"
// convention): the product spec's premise of embedded rule references
// does not match upstream pve-firewall's actual, checked source as of this
// writing. If a future PVE version changes pvefw-logger's log format to
// include rule positions, Correlate should be revisited to use them
// directly instead of the heuristic.
//
// # Scope: guest chains only
//
// Only guest tap/veth chains are correlated to a specific rule. Node
// (host) and cluster (forward) scope chain-naming was not confidently
// grounded during this task (no live PVE cluster available to confirm
// against — docs/development.md's "Real PVE access" note applies), so
// non-guest chains are labeled CorrelationStatusUnknownChain rather than
// guessed at. Node/cluster-scope log correlation is explicitly "not
// evaluated" — a future task with hardware access should confirm the real
// host-chain naming convention and extend Correlate accordingly.
package fwlog
