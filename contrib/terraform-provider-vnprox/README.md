# terraform-provider-vnprox

A Terraform/OpenTofu provider for [vnprox](../../README.md) — a visual networking add-on for
Proxmox VE.

## The stage-only contract — read this before writing a resource block

**`terraform apply` on any resource in this provider never makes a network change live.** It
stages a draft changeset, validates it, and stops. Making the change live — actually creating the
bridge, actually deleting the VLAN — is a **human review action inside vnprox**, exactly the same
way a change staged through vnprox's own UI, its MCP (AI-operator) server, or a plugin works.

This is not a limitation of the provider to work around. It is vnprox's core safety guarantee
(`docs/architecture.md` §13, decision D4: *the change engine is the sole mutation path*). Every
integration seam into vnprox — `internal/plugin.Stager`, `internal/mcp.ChangesetStager`, and now
this provider — is required to reuse that same "stage, never apply" boundary rather than invent a
new one. Concretely, for this provider:

- **Data sources** (`vnprox_topology`, `vnprox_inventory`) read freely — they call vnprox's
  ordinary read routes (`GET /topology`, `GET /inventory/{ref}`) and stage nothing.
- **Resources** (`vnprox_bridge`, `vnprox_vlan`) map every `Create`/`Update`/`Delete` to exactly
  one changeset staged via `POST /changesets` and validated via
  `POST /changesets/{id}/validate` — **never** `POST /changesets/{id}/apply`,
  `.../confirm`, or `.../rollback`. Those three verbs do not exist anywhere in this codebase (see
  `internal/provider/client.go`'s package doc comment, and
  `internal/provider/client_test.go`'s `TestClient_HasNoApplyMethod`, a reflection check that
  fails a named test if a future change ever adds one).

This will feel wrong if you come from a provider where `apply` means convergence. Sit with that
tension for a second: a Terraform run against vnprox always "succeeds" in the sense of staging and
validating a changeset, but the actual network is untouched until a person looks at the diff inside
vnprox and clicks Apply. `terraform plan`/`apply` here is closer to "open a pull request" than
"merge and deploy."

**A worked example, ending where every `terraform apply` against this provider ends:**

```hcl
terraform {
  required_providers {
    vnprox = {
      source = "bgovanlu/vnprox"
    }
  }
}

provider "vnprox" {
  base_url = "https://pve1:8007/api/v1"
  token    = var.vnprox_token # a T-1104 bearer token — see "Authentication" below
}

resource "vnprox_bridge" "lab" {
  node       = "pve1"
  name       = "vmbr99"
  gateway    = "10.99.0.1"
  addresses  = ["10.99.0.2/24"]
  vlan_aware = true
}

output "changeset_id" {
  value = vnprox_bridge.lab.changeset_id
}
```

```
$ terraform apply
...
vnprox_bridge.lab: Creating...
vnprox_bridge.lab: Creation complete after 1s [id=bridge:pve1:vmbr99]

Apply complete! Resources: 1 added, 0 changed, 0 destroyed.

Outputs:

changeset_id = "01M13H..."
```

**Nothing changed on `pve1` yet.** A draft changeset named `terraform: create bridge vmbr99 on
pve1` now exists, already validated. **Now go review the changeset** at your vnprox instance's
Changesets screen (`https://pve1:8007/#/changesets/01M13H...`, or `GET /changesets/01M13H...`) and
apply it from there when you're satisfied with the diff.

### Making a CI pipeline honest (ADR-0012's gate pattern)

Everything above is about a human reading output. A pipeline does not read READMEs — it reads
`terraform plan -detailed-exitcode`, which reports **0 (no changes)** after a stage-only apply,
because from Terraform's point of view the resource exists and matches config. **It does, and the
network has not changed.**

`docs/adr/0012-stage-only-integrations-never-imply-liveness.md` is the decision behind that: a
stage-only integration reports *intent recorded*, never *network converged*, and publishes a gate
so a pipeline can tell the difference. This is that gate.

```hcl
resource "vnprox_bridge" "example" {
  node = "pve1"
  name = "vmbr99"
  mtu  = 9000
}

# Fails the plan/apply until a human has reviewed and applied the changeset
# in vnprox. Terraform >= 1.5.
check "bridge_is_live" {
  assert {
    condition     = vnprox_bridge.example.changeset_status == "applied"
    error_message = <<-EOT
      Changeset ${vnprox_bridge.example.changeset_id} is still
      "${vnprox_bridge.example.changeset_status}" — staged, not applied.
      Nothing has changed on ${vnprox_bridge.example.node}. Review and apply it
      in vnprox, then re-run.
    EOT
  }
}
```

`changeset_status` is re-read from the daemon on every `Read`, so `terraform plan` re-evaluates the
check against the changeset's *current* status — a value frozen at creation would make this gate
report a truth that had expired.

The Ansible collection's equivalent is `result.changeset.status`, which every changed run returns:

```yaml
- name: Stage the bridge
  vnprox.vnprox.bridge: { node: pve1, name: vmbr99, mtu: 9000 }
  register: staged

- name: Fail the play until a human has applied it
  ansible.builtin.assert:
    that: staged.changeset.status == "applied"
    fail_msg: >-
      Changeset {{ staged.changeset.id }} is still
      {{ staged.changeset.status }} — staged, not applied.
```

**The gate is opt-in, and that is a deliberate, accepted cost.** A pipeline that adopts neither
gets a green run over a network that has not changed. ADR-0012 records why the alternatives were
worse — chiefly that a "permanent diff until applied" model is structurally unreachable for
Ansible, which has no state file to hold the changeset id it would need to check.

### What happens after a human applies

Once a human applies the changeset outside Terraform, this resource's `changeset_status` in state
still refers to that same changeset — but the changeset is no longer editable (vnprox refuses
`PUT`/`DELETE` on anything past `draft`/`validated`). A later `terraform apply` that changes this
resource's config, or a `terraform destroy`, does **not** try to mutate the already-applied
changeset. Instead it stages a **new** changeset — a `bridge.update` or `bridge.delete` op against
the same entity — and stops, exactly like the first one did. `terraform destroy` still removes the
resource from Terraform's own state (that part of Terraform's contract is unchanged), but the live
bridge is untouched until a human reviews and applies the new deletion changeset. The resource's
`live_exists` computed attribute (from `GET /inventory/{ref}`) tells you, informationally, whether
the entity has actually shown up in live inventory yet — Terraform's own plan/apply cycle for the
resource is driven by the changeset, never by this value.

### `terraform plan` and `GET /changesets/{id}/diff`

vnprox's own changeset diff view (`GET /changesets/{id}/diff`) is the right place to see the
rendered before/after of a staged change — visit it from the vnprox UI, not from `terraform plan`.
`terraform plan`'s own diff is an ordinary Terraform attribute diff (declared config vs. last-known
state), which is a different, complementary view: it tells you what *Terraform* will send, not what
vnprox's validator concluded about the resulting network.

## Module boundary

This directory is **its own Go module** (`go.mod` here has module path
`github.com/bgovanlu/terraform-provider-vnprox`, distinct from the main repository's
`github.com/bgovanlu/vnprox`) — deliberately outside `cmd/vnproxd`'s and `cmd/vnproxctl`'s build
graphs. `terraform-plugin-framework`/`terraform-plugin-go` are a large dependency tree (a plugin
handshake protocol, `go-plugin`'s subprocess machinery, gRPC, OpenTelemetry) that must never end up
linked into the daemon that controls host networking, or into the CLI. This is the same structural
isolation commit `34c11588` gives `sigstore-go` (kept out of `vnproxd`, scoped to `vnproxctl`
alone) — except here the isolation is a genuine separate module, not merely an unimported
subpackage, because a Terraform provider is its own compiled binary that `vnproxd`/`vnproxctl` never
link.

Two things enforce this boundary, not just this README:

- **Go's own internal-package visibility rule.** This module's import path
  (`github.com/bgovanlu/terraform-provider-vnprox`) does not share the main module's
  `github.com/bgovanlu/vnprox` prefix, so nothing in this directory can import
  `github.com/bgovanlu/vnprox/internal/...` even with a local `replace` directive pointing at the
  same files on disk. Every wire type this provider needs (`internal/provider/client.go`'s `op`,
  `changeset`, `topologyResponse`, `entityDetail`, …) is reimplemented field-for-field against
  `docs/api.md`/`docs/data-model.md`'s documented shapes instead of imported — this provider is an
  ordinary external API consumer, the same as a hand-written Terraform config author would be.
- **`cmd/vnproxd/tfproviderguard_test.go`** (in the main module) runs `go list -deps` against both
  `cmd/vnproxd` and `cmd/vnproxctl` and fails if either transitively imports any
  `terraform-plugin-*` package — mirroring `cmd/vnproxd/sigstoreguard_test.go`'s exact pattern for
  the sigstore-go/vnproxd split.

## Authentication

This provider authenticates **exclusively** with a T-1104 bearer token — the same mechanism
`vnproxctl remote`/`vnproxctl apply` use (`cmd/vnproxctl/remoteclient.go` in the main repository).
It never logs in with a PVE username and password, and never will (see `internal/provider/client.go`'s
and `provider.go`'s doc comments) — `docs/security.md`'s "No vnprox-local accounts" is about
*login*; a bearer token is a delegated, capability-scoped credential a logged-in user (or an
existing automation flow) mints via `POST /tokens`, distinct from the PVE ticket bridge.

To mint one:

1. Log in to your vnprox instance (the SPA, or `POST /auth/login`) as a user with `netWrite`.
2. `POST /tokens` with `{"name": "terraform", "scopes": ["netRead", "netWrite"]}` (session cookie +
   CSRF header). The response's one-time `token` field is what you pass to this provider — it is
   never retrievable again after this call.
3. Set it as `provider.token` or the `VNPROX_TOKEN` environment variable.

### Provider configuration

| Attribute | Env var fallback | Default | Notes |
|---|---|---|---|
| `base_url` | `VNPROX_URL` | — (required) | e.g. `"https://pve1:8007/api/v1"` |
| `token` | `VNPROX_TOKEN` | — (required) | A T-1104 bearer token; sensitive |
| `insecure` | `VNPROX_INSECURE` (`"true"`/`"1"`) | `false` | Skip TLS verification |
| `timeout_seconds` | — | `30` | Per-request timeout |

`insecure` defaults to `false` here, **unlike** `vnproxctl`'s own `--insecure` flag (which defaults
to `true` for interactive dev convenience). Terraform state and CI runs are a worse place to
silently trust an unverified endpoint than an interactive CLI session is, so this provider verifies
by default; set `insecure = true` explicitly against a dev instance using `testdata/certs`' throwaway
self-signed cert.

## Resources and data sources

| Name | Kind | Maps to |
|---|---|---|
| `vnprox_bridge` | resource | `bridge.create`/`bridge.update`/`bridge.delete` ops (`internal/change/params_bridge.go`) |
| `vnprox_vlan` | resource | `vlan.create`/`vlan.update`/`vlan.delete` ops, plain 802.1q sub-interfaces only (`internal/change/params_vlan.go`) — OVS int-port VLANs are out of scope for this first cut |
| `vnprox_topology` | data source | `GET /topology` |
| `vnprox_inventory` | data source | `GET /inventory/{ref}` |

Two resources and two data sources is a deliberate scope: this card's job is to fix the stage-only
wire contract exactly right for T-4002 (Ansible) and T-4003 (runbooks) to reuse, not to cover
vnprox's whole entity surface (bonds, SDN zones/vnets, firewall rules, …) in one pass. Extending
either resource file's pattern to a new op family is mechanical once the contract is right.

Every resource attribute is documented inline in its schema (`terraform providers schema` after
`terraform init`, or read `internal/provider/resource_bridge.go`/`resource_vlan.go` directly).

## Install (local development)

This provider is not yet published to a registry. Point Terraform at a local build with a
[`dev_overrides`](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers)
block:

```
go install .
```

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "bgovanlu/vnprox" = "/absolute/path/to/your/GOBIN"
  }
  direct {}
}
```

With that in place, `terraform init` is skipped entirely for this provider and `terraform plan`/
`apply` use the locally built binary.

## Running the acceptance tests

```
cd contrib/terraform-provider-vnprox
TF_ACC=1 go test ./... -v -timeout 20m
```

Requires a `terraform` (or `tofu`) binary on `$PATH` — `terraform-plugin-testing` shells out to it.
Everything else is self-contained: `internal/provider/harness_test.go`

1. builds the **real** `cmd/pvemock` and `cmd/vnproxd` binaries from the main vnprox module (found
   by walking up from this file's own directory — no assumption about the caller's working
   directory),
2. starts `cmd/pvemock` against `testdata/clusters/single-node.yaml` and `cmd/vnproxd` against a
   rewritten copy of `testdata/dev.toml` (ephemeral ports, temp storage — the same rewrite
   `cmd/vnproxd/devconfig_test.go`'s `TestRunDaemon_DevConfigServesHealth` does in the main module,
   duplicated here because this module cannot import that test file's helper across the module
   boundary),
3. logs in as the fixture's built-in `root@pam`/`vnprox-mock` user and mints a
   `netRead`+`netWrite` bearer token via `POST /tokens` — the *only* place in this entire module
   that touches a username/password; the provider itself never does,
4. runs each acceptance test's `terraform apply` (and, for the two resources, a direct
   `GET /changesets/{id}` call independent of the provider's own client — the way an external
   auditor would check) against that real daemon.

This is exactly the "acceptance-style tests against `cmd/pvemock` + a real `vnproxd`, not a
hand-rolled fake" T-4001's card asks for — the same discipline
`internal/apicontract/harness_test.go` uses in the main module for its own conformance suite,
adapted to a subprocess boundary because this provider cannot import `internal/api` directly (see
"Module boundary" above).

Without `TF_ACC=1`, `go test ./...` runs only the fast unit tests
(`TestClient_HasNoApplyMethod`, `TestChangesetEditable`) and skips every acceptance test — the
standard `terraform-plugin-testing` convention.

`scripts/ci-local.sh terraform-provider` (in the main repository) runs this module's build/vet/
unit tests always, and the acceptance suite whenever a `terraform`/`tofu` binary is available.

## What Terraform's convergence model does not get from this provider

Worth stating plainly, since T-4002 (Ansible) and T-4003 (runbooks) inherit the identical
tension: Terraform's whole mental model is "my config describes desired state; `apply` converges
reality to match it, and the next `plan` shows zero diff if nothing external changed." This
provider cannot honestly offer that. A `vnprox_bridge` resource's "existence" in Terraform state is
really "a changeset requesting this bridge exists, staged." Whether the bridge *actually* exists
depends on a human clicking Apply inside vnprox, on their own schedule — which could be seconds or
weeks after `terraform apply` returns. `terraform plan` immediately after `terraform apply` here
will faithfully show "no changes" (the changeset was staged and recorded in state), even though
nothing on the wire has moved yet. There is no way to make Terraform's plan/apply cycle mean
"converged and live" without giving this provider an apply path of its own — which is exactly the
one thing `docs/architecture.md`'s decision D4 rules out. Treat this provider as a way to *propose*
network changes as code and get them into vnprox's review queue, not as a drop-in replacement for
providers where `apply` means done.
