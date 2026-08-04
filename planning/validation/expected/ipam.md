# Expected outcomes — ipam

Backs `planning/validation/harness/ipam.sh`. See `planning/validation/README.md` for the table
format.

| id | pointer | op | expected | meaning |
|---|---|---|---|---|
| ipam-01 | exit_code | equals | 0 | `/cluster/sdn/ipams` failed outright. |
| ipam-02 | exit_code | equals | 0 | `/cluster/sdn/ipams/pve` failed. If this 404s on a cluster with **zero** explicit `ipams.cfg` entries, that directly answers the checklist's "is the built-in `pve` IPAM reachable with zero configured entries, or must a zone explicitly set `ipam: pve` first" question — `internal/pvemock/ipam.go`'s `effectiveIpams`/`defaultIpamID` currently assume it is always reachable; a 404 here is a real divergence, file it as a bug card against those functions. |
| ipam-03 | exit_code | equals | 0 | `/cluster/sdn/ipams/pve/status` failed. |
| ipam-03 | raw | contains | gateway | (Only meaningful when a gateway allocation exists.) Confirm the `gateway` marker's JSON type is a `0`/`1` int as `internal/pvemock/ipam.go` models, not a bool or string — capture the exact value if it differs. Also check `vmid` typing (int vs. string) against the same allocation entries. |
