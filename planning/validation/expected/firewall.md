# Expected outcomes — firewall

Backs `planning/validation/harness/firewall.sh`. See `planning/validation/README.md` for the
table format. As `firewall.sh`'s own header notes, `needs-hardware-validation.md` has no
dedicated "Firewall" heading yet — these rows establish a read-only baseline (does the API
surface `internal/pvemock` models actually exist and shape up the same way on real PVE) ahead of
T-1804's mutating firewall-lockout scenarios.

| id | pointer | op | expected | meaning |
|---|---|---|---|---|
| firewall-01 | exit_code | equals | 0 | Neither `pve-firewall status` nor `systemctl show pve-firewall.service` succeeded — confirm the harness ran with enough privilege and that `pve-firewall` is the real service name on this PVE version. |
| firewall-02 | exit_code | equals | 0 | `/cluster/firewall/options` or `/cluster/firewall/rules` failed outright. |
| firewall-02 | raw | contains | data | Neither cluster-scope firewall route returned the expected `{"data": ...}` envelope — capture the real shape and compare against `internal/pvemock/firewall.go`'s modeled fields. |
| firewall-03 | exit_code | equals | 0 | Node-scope `/nodes/<node>/firewall/rules` failed — confirm the node name resolution (`PVE_NODE` / auto-detected via `hostname -s` under `pvesh`) matched a real node. |
| firewall-04 | exit_code | equals | 0 | One or more of groups/aliases/ipset failed outright. |
| firewall-04 | raw | contains | data | Capture the real shape of security groups/aliases/ipsets and compare field-by-field against `internal/pvemock/firewall.go`; this is the read-only baseline the T-1805/T-1804 mutating scenarios build on. |
