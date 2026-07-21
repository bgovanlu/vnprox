# Feature spec — Blueprints & onboarding

## 1. Blueprints

A blueprint is a parameterized, reusable network topology template stored as JSON: entities to create (bonds, bridges, VLANs, SDN objects, firewall groups) with `{{param}}` placeholders and per-node expansion rules ("on every node", "on nodes matching selector").

- **Create**: author from scratch in a form editor, or **capture from current state** ("blueprint-ify this node's network, parameterizing addresses").
- **Instantiate**: pick blueprint → fill parameters (with validation + IPAM-aware address suggestions) → vnprox expands to a changeset draft → normal review/apply flow. Idempotent re-instantiation: entities that already match are skipped, divergent ones produce update ops (shown as such in the diff).
- **Ship with starters** (bundled, read-only, copy-to-edit): "Single NIC homelab (VLAN-aware bridge + guest VLANs)", "Dual NIC: mgmt + trunk", "2-port LACP bond + storage VLAN", "3-node cluster with VXLAN overlay", "EVPN datacenter starter". Each starter carries a description of when to use it and a preview diagram. **Caveat (flagged, T-607):** the "EVPN datacenter starter" is partial — there is no `sdn.controller.create` changeset op (`internal/change/op.go` has zone/vnet/subnet/apply ops only), so this starter can reference an already-existing EVPN controller but cannot provision one from scratch. See `planning/reports/T-603.md` §3.
- Format is versioned (`"blueprintVersion": 1`) and export/importable as files — shareable in the community.

## 2. Cluster-wide consistency application

The flagship use: define the node network *once*, apply to N nodes. Blueprint instantiation with node selectors generates per-node ops in one changeset, and the drift checker (topology spec §6) subsequently treats the blueprint as a desired-state reference for those nodes (P1: "pin nodes to blueprint" → drift against it).

## 3. First-run onboarding (P0)

On first login vnprox must be immediately valuable on a brownfield cluster:

1. **Import scan** — collectors populate everything automatically; nothing to configure.
2. **Guided health review** — a one-time walkthrough of what was found: topology summary, detected issues (drift/health findings), LLDP availability check with setup offer, and confirmation of detected management interfaces + corosync links (these seed the safety interlocks; user confirms or corrects, stored in `/etc/pve/vnprox/protected.json`).
3. **Read-only mode toggle** — admins can run vnprox observe-only (config `read_only = true`) until they trust it; all write UI renders disabled with explanatory tooltips.

## 4. Config documentation export (P1)

One click → Markdown/HTML document of the cluster network: rendered topology (SVG), per-node interface tables, VLAN matrix, SDN inventory, firewall summaries, LLDP wiring table. Timestamped — the "as-built doc" that never gets written manually.

**Posture report extension (T-1607).** The same export machinery (`internal/docexport`'s dual-format Markdown/HTML renderer, not a parallel one) also renders the **network posture report** via `GET /export/posture?format=md|html` (docs/api.md's "Posture score & report" section): the periodically-computed security/resilience score, its named-factor table (SPOF resilience, segmentation coverage, exposed ports, anomaly rate, drift hygiene — each factor's weight/value/contribution shown independently, never an opaque single number), and a trend sparkline over the bounded score history. It is the management-legible progress artifact that turns the findings stream into a trend line an operator can show someone else. The report is **honest about uncertainty**: a factor that could not be assessed (a cold-start anomaly rate with no learned baselines, a SPOF score resting on failsim dimensions it could not evaluate) is rendered as a partial/qualified score with its caveat, never silently shown as a clean 100 — the same "no silent approximation" contract the simulators in this codebase follow.

## 5. Blueprint sharing bundles (T-1107)

A community layer on top of blueprints v2 (`internal/blueprint`, `docs/api.md`'s Blueprints section for the `Blueprint` shape): a signed envelope one installation can export and hand to another, with a trust model that never imports something unverified without an explicit human decision.

**Bundle format.** `{bundleVersion: 1, blueprint: Blueprint, signature?: {alg, publicKeyFingerprint, publicKey, sig}}`. `signature` is absent for an unsigned bundle. The signature covers `encoding/json`'s canonical marshaling of `blueprint` alone (struct field order is fixed and Go sorts `map[string]any` keys, so this is deterministic and independent of the enclosing envelope's own byte layout) — any edit to `blueprint` after signing invalidates it. `publicKey` (base64, the raw 32-byte Ed25519 public key) travels alongside `publicKeyFingerprint` (hex SHA-256 of that key) so a signature can be verified standalone, without first consulting any trust store — the fingerprint alone is a one-way digest, not key material.

**Signing identity.** Each installation generates its own Ed25519 keypair at first use: `/etc/vnprox/keys/blueprint-signing.key` (the private seed, base64, `root:root 0600` — the same "generate if absent, never silently overwrite" handling `docs/security.md`'s session key and metrics scrape token already use). The public half is exportable via `GET /blueprints/signing-key`, so a receiving admin can pin it as a trusted signer ahead of time (or trust it inline at import time — see below).

**Trust store.** `/etc/vnprox/keys/trusted-signers/` holds one small JSON file per pinned signer (`<fingerprint>.json`: `{fingerprint, publicKey, label?, addedBy?, addedAt}`), managed via `GET/POST/DELETE /blueprint-signers` (`docs/api.md`). This is filesystem, app-owned state, not a shadow copy of any PVE config — a signer either is or isn't pinned, and there is nothing versioned about it that would justify a SQLite migration.

**Import trust decision** (`POST /blueprints/import`, `docs/api.md`). Every import is one of four outcomes:

- **`imported`** — the bundle is unsigned and the caller passed `trustUnsigned: true`, or it's signed by an already-trusted (or newly-trusted-this-request) key. The blueprint is saved (always as a *new* saved blueprint — the shared bundle's `id` is never trusted to mean "overwrite my local blueprint with that id").
- **`unsigned`** — no signature at all, and `trustUnsigned` wasn't set. Never imported by default.
- **`untrustedSignature`** — the signature verifies, but against a key not in the trust store, and `trustNewKey` wasn't set. The response's `signer` field carries the fingerprint + public key so a UI can offer "trust this signer" without a second round trip. Setting `trustNewKey: true` on a retry both pins the key and imports in the same request.
- **`invalidSignature`** — the signature doesn't verify at all (malformed, or the blueprint content was altered after signing). No trust flag can make this importable — there is nothing to trust, only tampered or corrupt data.

Both explicit-trust import paths (`trustUnsigned`, `trustNewKey`) — and every rejected attempt — are audited as `blueprint.import`, with the trust decision (`alreadyTrusted` / `trustUnsigned` / `trustNewKey` / absent-for-a-rejection) and signer fingerprint (when known) recorded in the entry's detail. Pinning/un-pinning a signer via `POST`/`DELETE /blueprint-signers` is audited as `blueprint.signer.add`/`blueprint.signer.delete`.

**UI.** The import dialog (`web/src/blueprints/BlueprintImportDialog.tsx`) surfaces all three non-`imported` outcomes distinctly and requires the explicit trust checkbox before its "Import anyway" action enables for `unsigned`/`untrustedSignature`; a verified-and-trusted bundle imports with no prompt at all, and `invalidSignature` offers no import action whatsoever.

## 6. Guided dual-stack IPv6 rollout wizard (T-1404)

`web/src/ipv6/DualStackWizard.tsx` is a purpose-built instantiate flow narrower than the general Instantiate step in §1: it fixes both the blueprint (a single, well-known one this wizard creates on first use via an ordinary `POST /blueprints` upsert-by-id, then reuses on every subsequent run) and the target entity kind (`sdn-subnet`), and lets the operator only pick which existing VLAN/VNet to add IPv6 addressing to and its CIDR/gateway/SNAT choice — reusing the same `ParamForm` component and `useInstantiateBlueprintMutation` hook §1's flow already provides, not a parallel implementation. Every mutation is the ordinary `sdn.subnet.create` op inside one reviewable changeset draft; nothing is applied until the operator reviews and confirms it through the normal changeset flow. Idempotent re-instantiation (§1's own contract) means re-running the wizard against a VNet whose v6 subnet already matches produces a zero-op changeset, rendered as "already up to date" rather than a duplicate draft — see `docs/features/ipam.md` §5 and `docs/api.md`'s IPv6 section for the read side (`GET /ipv6/segments`, the `dualstack_drift` finding) this wizard's own success is ultimately validated against.
