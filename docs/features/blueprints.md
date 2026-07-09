# Feature spec — Blueprints & onboarding

## 1. Blueprints

A blueprint is a parameterized, reusable network topology template stored as JSON: entities to create (bonds, bridges, VLANs, SDN objects, firewall groups) with `{{param}}` placeholders and per-node expansion rules ("on every node", "on nodes matching selector").

- **Create**: author from scratch in a form editor, or **capture from current state** ("blueprint-ify this node's network, parameterizing addresses").
- **Instantiate**: pick blueprint → fill parameters (with validation + IPAM-aware address suggestions) → vnprox expands to a changeset draft → normal review/apply flow. Idempotent re-instantiation: entities that already match are skipped, divergent ones produce update ops (shown as such in the diff).
- **Ship with starters** (bundled, read-only, copy-to-edit): "Single NIC homelab (VLAN-aware bridge + guest VLANs)", "Dual NIC: mgmt + trunk", "2-port LACP bond + storage VLAN", "3-node cluster with VXLAN overlay", "EVPN datacenter starter". Each starter carries a description of when to use it and a preview diagram.
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
