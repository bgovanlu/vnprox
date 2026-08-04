# Expected outcomes — sdn

Backs `planning/validation/harness/sdn.sh`. See `planning/validation/README.md` for the table
format. Only meaningful on a node/cluster with SDN actually configured — an empty
zones/vnets list on a plain single-node install is not itself a divergence, just means this
section needs a richer cluster to say anything.

| id | pointer | op | expected | meaning |
|---|---|---|---|---|
| sdn-01 | exit_code | equals | 0 | `/cluster/sdn/zones` (or a per-zone `/status` call) failed outright. |
| sdn-01 | raw | contains | zone | If SDN zones are configured on this cluster and none appear here, the zones list's field name/shape differs from `internal/pvemock/sdn.go`'s `"zone"` key — capture the real shape. For the per-zone status calls: this is the "EVPN anycast-gateway realization when the gateway is absent" and "exact rejection point" checklist items' data source — a human/triage pass should read the actual status text, not just confirm the call succeeded. |
| sdn-02 | exit_code | equals | 0 | `/cluster/sdn/vnets` failed outright. |
| sdn-03 | exit_code | equals | 0 | A vnet's `/subnets` call failed. |
| sdn-03 | raw | contains | gateway | (Only meaningful when a subnet with a gateway is configured.) If a configured gateway doesn't appear here with the expected `0`/`1` int marker (not a bool), that's the checklist's "gateway 0/1 int" wire-shape question — capture the real value's JSON type. |
| sdn-04 | exit_code | equals | 0 | `/cluster/sdn` (cluster-wide realization status) failed. |
