# vnprox.vnprox — Ansible collection for vnprox

Stage-only Ansible modules and a dynamic inventory plugin for
[vnprox](../../README.md) — a visual networking add-on for Proxmox VE.

## The stage-only contract — read this before writing a playbook

**No module in this collection ever makes a network change live.**
`state: present`/`state: absent` on `vnprox.vnprox.bridge` or `vnprox.vnprox.vlan`
stages a draft changeset, validates it, and stops. Making the change live —
actually creating the bridge, actually deleting the VLAN — is a **human review
action inside vnprox**, exactly the same boundary
[`contrib/terraform-provider-vnprox`](../terraform-provider-vnprox/README.md)
enforces for Terraform (T-4001, the card this collection reuses the wire contract
from), and the same "stage, never apply" boundary `internal/plugin.Stager` and
`internal/mcp.ChangesetStager` enforce in-process inside the daemon itself.

This is not a limitation to work around. It is vnprox's core safety guarantee
(`docs/architecture.md` §13, decision D4: *the change engine is the sole mutation
path*). Concretely, for this collection:

- **`vnprox.vnprox.topology_info`** (read-only) and the
  **`vnprox.vnprox.topology_inventory`** dynamic inventory plugin call only
  `GET /topology` / `GET /ports` / `GET /inventory/{ref}` — they stage nothing.
- **`vnprox.vnprox.bridge`** / **`vnprox.vnprox.vlan`** map every `state: present`/
  `state: absent` run that needs to do anything to exactly one changeset staged via
  `POST /changesets` and validated via `POST /changesets/{id}/validate` — **never**
  `POST /changesets/{id}/apply`, `.../confirm`, or `.../rollback`. Those three verbs
  are not called anywhere in this collection's code —
  `tests/test_route_lint.py` greps every module/module_utils source file for
  `/apply`, `/confirm`, `/rollback` route literals and fails a named test if one is
  ever added (T-4002 acceptance criterion 2).

**A worked example, ending where every changed run of this collection ends:**

```yaml
# site.yml
- hosts: localhost
  gather_facts: false
  tasks:
    - name: stage a lab bridge
      vnprox.vnprox.bridge:
        base_url: "https://pve1:8007/api/v1"
        token: "{{ vnprox_token }}"   # a T-1104 bearer token — see "Authentication" below
        node: pve1
        name: vmbr99
        gateway: 10.99.0.1
        addresses: ["10.99.0.2/24"]
        vlan_aware: true
      register: result

    - debug:
        msg: "staged {{ result.changeset.id }}, status={{ result.changeset.status }}"
```

```
$ ansible-playbook site.yml
...
TASK [stage a lab bridge] *****************************************************
changed: [localhost]

TASK [debug] *******************************************************************
ok: [localhost] => {
    "msg": "staged 01M13H..., status=validated"
}

PLAY RECAP **********************************************************************
localhost : ok=2  changed=1  unreachable=0  failed=0
```

**Nothing changed on `pve1` yet.** A draft, validated changeset named
`ansible: create bridge vmbr99 on pve1` now exists. **Now go review the changeset**
at your vnprox instance's Changesets screen
(`https://pve1:8007/#/changesets/01M13H...`, or `GET /changesets/01M13H...`) and
apply it from there when you're satisfied with the diff.

## The `changed` semantics caveat — read this before gating a pipeline on it

Ansible's whole idempotency contract is *"a second run against unchanged reality
reports `changed: false`."* This collection's modules can honestly deliver that
**only against live state** — `changed: false` means "nothing further needs
staging right now to make live reality match what this task declares," never
"this task's change is live." A play that stages a brand-new bridge always reports
`changed: true`; re-running that exact same play immediately after will report
`changed: true` **again**, because nothing about live reality changed — the
changeset is still sitting in `draft`/`validated`, waiting on a human. A CI
pipeline that treats a green, `changed: false` Ansible run as "the network now
matches my playbook" will draw the same wrong conclusion T-4016 documents for
Terraform's `plan -detailed-exitcode`.

This is the identical tension
[`planning/tasks/T-4016-stage-only-convergence-semantics.md`](../../planning/tasks/T-4016-stage-only-convergence-semantics.md)
names for every stage-only vnprox integration, not something invented for this
collection. **That task is the place this decision gets made once, for every
integration — this README does not re-derive an answer.** As of this writing
T-4016 is still open (three options, no ADR yet); this collection follows
T-4001's current interim answer — accept the limitation and document it loudly,
option 3 in that task's framing — and will adopt whichever option T-4016 settles
on once it lands, the same way T-4001 will.

**What this collection's own experience adds to that decision (see this task's
report for the fuller version):** Ansible's `changed` flag is coarser than
Terraform's plan/state model — there is no persistent per-resource "last known
changeset id" an idempotent re-run can check the status of (Ansible has no state
file; every run starts from nothing but live inventory). That makes **option 1
("permanent diff until applied")** structurally *unreachable* for this collection
without inventing a state store Ansible doesn't have — worse than Terraform's
"never reaches a clean state on its own" cost, because there's no state to even
be dirty in. **Option 2 ("a status attribute plus a documented gate")** fits this
collection more naturally than option 1 would: `result.changeset.status` is
already returned on every changed run (see "Return values" below), so an
Ansible-side gate is just `until: result.changeset.status == "applied"` inside a
`vnprox.vnprox.changeset_info`-shaped follow-up lookup — a pattern this
collection's design does not block, whichever way T-4016 ultimately decides.

## Idempotency

Every `state: present` module compares only the attributes you actually specify
against vnprox's live inventory (`GET /inventory/{ref}`) — the same
absent→create / divergent→update / matching→noop discipline
`internal/spec.Import` already uses server-side (see
`vnprox_common.partial_desired`'s doc comment in this collection's source):

- **Absent from live inventory** → stages a `*.create` changeset, `changed: true`.
- **Present, but diverges** on an attribute you specified → stages a `*.update`
  changeset carrying only that subset, `changed: true`.
- **Present, and matches** every attribute you specified → `changed: false`,
  nothing staged.

An attribute you don't mention in your task is never compared and never
enforced — "no opinion," not "must be unset." This matters in particular for
`bridge`'s `vlan_aware`/`stp` booleans: they carry **no** `default:` in this
module's argument spec on purpose, so a play that never mentions `vlan_aware`
against an already-live, VLAN-aware bridge reports `changed: false` forever,
rather than trying to turn VLAN-awareness off because "unset" and "false"
would otherwise be indistinguishable.

**A known asymmetry** (see `vnprox.vnprox.vlan`'s own module documentation for
the full explanation): `gateway` is accepted at create time but the live VLAN
sub-interface entity carries no `Gateway` field to read back, so this module's
idempotency check cannot detect gateway-only drift on a VLAN sub-interface. The
same underlying entity-model gap exists in `contrib/terraform-provider-vnprox`'s
`vnprox_vlan` resource, for the identical reason — it is a real gap in
`internal/inventory.VlanIface`, not something this collection invented.

## Authentication

Exclusively a T-1104 bearer token — the same mechanism
`contrib/terraform-provider-vnprox` and `cmd/vnproxctl/remoteclient.go` use. No
module or plugin in this collection ever logs in with a PVE username/password.

To mint one:

1. Log in to your vnprox instance (the SPA, or `POST /auth/login`) as a user with
   `netWrite`.
2. `POST /tokens` with `{"name": "ansible", "scopes": ["netRead", "netWrite"]}`
   (session cookie + CSRF header). The response's one-time `token` field is what
   you pass to this collection — it is never retrievable again.
3. Pass it as each task's `token:` parameter, or set the `VNPROX_TOKEN`
   environment variable (and `VNPROX_URL` for `base_url`) — the same two
   variable names `contrib/terraform-provider-vnprox` falls back to, so one
   token-minting step serves both integrations.

| Parameter | Env var fallback | Notes |
|---|---|---|
| `base_url` | `VNPROX_URL` | e.g. `https://pve1:8007/api/v1` |
| `token` | `VNPROX_TOKEN` | A T-1104 bearer token; `no_log: true` |
| `validate_certs` | `VNPROX_VALIDATE_CERTS` | Default `true` |
| `timeout` | — | Default `30` seconds |

## Modules

| Module | Kind | Maps to |
|---|---|---|
| `vnprox.vnprox.bridge` | resource | `bridge.create`/`bridge.update`/`bridge.delete` ops (`internal/change/params_bridge.go`) |
| `vnprox.vnprox.vlan` | resource | `vlan.create`/`vlan.update`/`vlan.delete` ops, plain 802.1q sub-interfaces only (`internal/change/params_vlan.go`) — OVS Int Port VLANs are out of scope, matching `contrib/terraform-provider-vnprox`'s identical cut |
| `vnprox.vnprox.topology_info` | info (read-only) | `GET /topology` |

Two resource modules and one info module is a deliberate scope, matching
T-4001's own "prove the contract, not the whole entity surface" cut (bonds, SDN
zones/vnets, firewall rules are out of scope for this first pass — extending
either resource module's pattern to a new op family is mechanical once the
contract is right, exactly as `contrib/terraform-provider-vnprox`'s README says
about its own two resources).

`ansible-doc vnprox.vnprox.bridge` (etc.) documents every parameter inline.

## Dynamic inventory: `vnprox.vnprox.topology_inventory`

Sourced from a running vnproxd's `GET /topology` (every bridge/bond/VLAN/
physical NIC/guest the cluster map renders) and, optionally, `GET /ports` (the
flat LLDP-derived node/nic/switch/port table) — so `ansible-inventory` enumerates
vnprox-managed infrastructure without a second, hand-maintained source of truth.
Read-only: this plugin calls no changeset route.

```yaml
# inventory.vnprox_topology.yml  (the filename suffix is required)
plugin: vnprox.vnprox.topology_inventory
base_url: https://pve1:8007/api/v1
token: "{{ lookup('env', 'VNPROX_TOKEN') }}"
```

```
$ ansible-inventory -i inventory.vnprox_topology.yml --graph
@all:
  |--@ungrouped:
  |--@vnprox:
  |  |--bridge:pve1:vmbr0
  |  |--physnic:pve1:eno1
  |  |--physnic:pve1:eno2
  |--@bridge:
  |  |--bridge:pve1:vmbr0
  |--@physnic:
  |  |--physnic:pve1:eno1
  |  |--physnic:pve1:eno2
  |--@node_pve1:
  |  |--bridge:pve1:vmbr0
  |  |--physnic:pve1:eno1
  |  |--physnic:pve1:eno2
```

Every topology node becomes one inventory host, named by its vnprox `Ref` triplet
(e.g. `bridge:pve1:vmbr0`) — the same identifier `GET /inventory/{ref}` and this
collection's `bridge`/`vlan` modules key on. Hosts are grouped two ways:

- **By kind** — a group named for the topology node's `kind` (`bridge`, `bond`,
  `vlan`, `physnic`, `guest`, ...; non-alphanumeric characters, e.g. the hyphen in
  `guest-nic`, are folded to `_`).
- **By node** — a group named `node_<name>` for the owning PVE cluster node (the
  topology node's `nodeGroup` field); cluster-scoped entities (SDN vnets) have no
  single owning node and are grouped by kind only.

Host vars: `vnprox_ref`, `vnprox_kind`, `vnprox_label`, `vnprox_layer`,
`vnprox_node`, `vnprox_status` — straight from the `GET /topology` node shape —
plus, when `include_ports: true` (the default) and the row corresponds to a
`physnic:<node>:<nic>` host already in the topology, `vnprox_switch`,
`vnprox_switch_port`, `vnprox_speed_mbps`, `vnprox_pvid`, `vnprox_tagged_vlans`,
`vnprox_ports_stale` from `GET /ports` — the physical-port-to-switch mapping the
topology graph alone does not carry.

This is a standard-shaped inventory plugin: `compose`/`groups`/`keyed_groups`/
`strict` all work as documented for
[`constructed`](https://docs.ansible.com/ansible/latest/collections/ansible/builtin/constructed_inventory.html).
It does **not** implement caching (`cache: true` is accepted by no option here —
every run re-reads `GET /topology` live), a stated scope limit rather than a
silent gap.

## Module boundary

This directory is structurally isolated from the main Go module the same way
`contrib/terraform-provider-vnprox` is (that provider's README's "Module
boundary" section explains the reasoning in full) — except here there is no
Go dependency graph for a guard test to check, since Python/YAML have no way to
end up linked into `vnproxd`/`vnproxctl` in the first place. The isolation is
structural instead: this collection lives entirely under
`contrib/ansible-collection-vnprox/`, is never imported by anything under
`cmd/` or `internal/`, and every wire type it needs (`op`, `changeset`,
`entityDetail`, ...) is reimplemented against `docs/api.md`'s documented shapes
in `plugins/module_utils/vnprox_api.py`, the same "ordinary external API
consumer" posture the Terraform provider's `client.go` takes.

Python tooling used by this collection's tests (`pytest`, `ansible-core` itself)
is a **dev-time-only** dependency — nothing under `plugins/` requires anything
beyond what `ansible-core` already bundles (`ansible.module_utils.urls`,
`ansible.module_utils.basic`), and nothing here ever enters
`go.mod`/`go.sum`.

## Running the tests

```
cd contrib/ansible-collection-vnprox
python3 -m pytest tests/test_route_lint.py -v      # fast, no server, always runs
ANSIBLE_ACC=1 python3 -m pytest tests/ -v           # builds + starts real pvemock/vnproxd
```

`ANSIBLE_ACC=1` gates the acceptance suite (`tests/test_modules.py`,
`tests/test_inventory_plugin.py`) behind an opt-in env var — the same
"only pay this cost when opted in" discipline
`contrib/terraform-provider-vnprox`'s `TF_ACC=1` gate uses — so a bare
`pytest` never builds Go binaries. `tests/harness.py`:

1. builds the **real** `cmd/pvemock` and `cmd/vnproxd` binaries from the main
   vnprox module (found by walking up from this file's own directory, the same
   `findRepoRoot` pattern `contrib/terraform-provider-vnprox`'s
   `harness_test.go` uses),
2. starts `cmd/pvemock` against `testdata/clusters/single-node.yaml` and
   `cmd/vnproxd` against a rewritten copy of `testdata/dev.toml` (ephemeral
   ports, temp storage),
3. logs in as the fixture's built-in `root@pam`/`vnprox-mock` user and mints a
   `netRead`+`netWrite` bearer token via `POST /tokens` — the *only* place in
   this whole test suite that touches a username/password,
4. runs each acceptance test as a **real** `ansible-playbook`/`ansible-inventory`
   subprocess invocation (not a direct Python function call) against that real
   daemon, and, for the changeset-staging tests, independently re-reads
   `GET /changesets/{id}` outside the module's own client — the way an external
   auditor would check.

`scripts/ci-local.sh ansible-collection` (in the main repository) runs the fast
lint test always, and the full acceptance suite whenever `go` is on `$PATH` (it
always is in this repo's own dev environment).

## Installing this collection locally

```
ansible-galaxy collection install --force contrib/ansible-collection-vnprox
```

or, for iterative development, point `ANSIBLE_COLLECTIONS_PATH` at a directory
containing `ansible_collections/vnprox/vnprox` symlinked to this directory (the
same approach `tests/conftest.py`'s `collections_path` fixture uses).
