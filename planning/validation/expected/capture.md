# Expected outcomes — capture

Backs `planning/validation/harness/capture.sh`. See `planning/validation/README.md` for the table
format.

| id | pointer | op | expected | meaning |
|---|---|---|---|---|
| capture-01 | raw | contains | nf_conntrack_acct = 0 | Accounting (`net.netfilter.nf_conntrack_acct`) is **disabled by default** on this kernel, matching `internal/flow/hostsample/conntrack.go`'s documented assumption — the sampler will produce valid-but-always-zero-byte/-packet Records until an operator enables it, and today nothing surfaces that as a warning (a candidate follow-up bug card, distinct from any parsing divergence). If instead `raw` shows `nf_conntrack_acct = 1`, that's also worth recording (means the default differs from what was assumed), but is not itself a failure. |
| capture-01 | exit_code | equals | 0 | `/proc/net/nf_conntrack` was not readable at all, or `sysctl` failed — confirm the harness ran with enough privilege (root) and that the `nf_conntrack` module is loaded. |
| capture-02 | raw | contains | btf: present | `/sys/kernel/btf/vmlinux` is absent — PVE's shipped kernel doesn't enable `CONFIG_DEBUG_INFO_BTF`, meaning eBPF sampling will always fail on this node/kernel build regardless of capability grants. Worth calling out explicitly in product docs per the checklist item's own text, not just as a probe error string. |
| capture-03 | exit_code | equals | 0 | `corosync-cfgtool`/`corosync-quorumtool` are absent — expected on a genuinely single-node install with no corosync running at all; not a divergence by itself, just means this item needs a real cluster to say anything. |
| capture-03 | raw | contains | RING ID | (Only meaningful when corosync is actually running.) If corosync's knet transport reports `LINK ID`/per-node `link enabled` fields instead of the classic `RING ID` shape `internal/host.ParseCorosyncStatus` parses, that is the checklist's exact open question — capture the full raw output and file a bug card with it attached. |
| capture-04 | raw | contains | packet loss | `ping`'s summary line didn't match the expected `"N% packet loss"` wording even under `LANG=C LC_ALL=C` — this is the exact failure mode `internal/latmesh.parsePingSummary`'s regexes assume never happens; capture the full raw output verbatim, it's the bug report. |
