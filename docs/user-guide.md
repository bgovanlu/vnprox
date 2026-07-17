# User guide

This guide is written for the shipped product; during development it doubles as the UX specification — implementation agents build what this document describes.

## 1. First login

Browse to `https://<any-node>:8007`. Log in with your **Proxmox credentials** (same username, password, realm, and second factor as the PVE UI — vnprox has no separate accounts).

On first login vnprox walks you through a short review:

1. **What we found** — your cluster's network, drawn. Nothing was changed; vnprox only read.
2. **Protected interfaces** — vnprox detected which interfaces carry each node's management IP and corosync traffic. Confirm these; vnprox will refuse changes that would cut them off.
3. **Physical discovery** — if `lldpd` isn't running, vnprox offers to enable it so the map can show real switch names and ports.
4. **Health findings** — anything inconsistent it noticed (MTU mismatches, half-applied configs, drift between nodes).

If you'd rather look before you touch, an administrator can set `read_only = true` in the config — the full UI works, editing is disabled.

## 2. Reading the map

The **Topology** view is home, and it lands on **Switch view** — a faceplate rendering of your real gear, one appliance per Linux/OVS bridge, grouped per cluster node:

- **Uplink bay**: the bridge's physical NICs/bonds, with LACP/MII state and the LLDP neighbor on the far end of each wire. Red = link down.
- **VLAN strip**: the bridge's VLAN sub-interfaces.
- **Guest access-port grid**: one port per guest NIC attached to the bridge (VMID as the port number, guest name, VLAN tag), collapsible per bridge.
- **VNet strip**: any SDN VNets realized on that bridge.
- **Unattached ports**: NICs/bonds not wired into any bridge surface in their own panel.

A `Switch | Graph` toggle in the header switches to the classic **Graph view** — a pan/zoom node-link canvas with four layer bands (Physical/L2/SDN/Guests) stacked per cluster node. Reach for Graph view when you need its spatial features: drag-and-drop editing, the path-simulator map overlay, traffic paint mode, or hover-chain highlight. The toggle is a per-session preference, not saved layout.

Things to try first:

- **Hover** any VM — its whole path to the physical switch lights up (Graph view).
- **Press `/`** and type a VM name, MAC, or IP — jump straight to it.
- Enter a **VLAN ID** in the filter box — see exactly where that VLAN lives and where it's trunked. Works in both views.
- **Click** anything — the inspector shows every detail, live status, and the raw config behind it.

## 3. Making changes safely

vnprox never applies anything as you click. Edits collect in the **change drawer** (bottom right):

1. Make edits anywhere — drag a NIC into a bond, edit a bridge, retag a guest NIC. Each becomes a line in the drawer.
2. Hit **Review & apply**. You'll see a plain summary, the exact file diffs per node, and the ordered plan.
3. Apply. A **countdown banner** appears (default 2 minutes). vnprox has applied your change — now it wants proof you still have connectivity.
4. Click **Confirm** — done. If you *can't* click (the change cut you off), vnprox automatically rolls everything back at the deadline. Reconnect and read what happened.

Every applied change is snapshotted. **Time machine** (History → Snapshots) lets you diff any two points and restore any of them — restores go through the same review flow.

### Common tasks (each has a wizard or guided form)

| Task | Where |
|---|---|
| Create a LACP bond from two NICs | Map → select NICs → "Create bond", or Node → Bonds → New |
| Make a bridge VLAN-aware and trunk 10–30 | Click bridge → Edit → VLAN aware toggle |
| Move 12 VMs to another bridge | Guests view → filter by bridge → select all → Reattach |
| Create an isolated test network on all nodes | SDN → New zone → Simple → wizard |
| Stretch a network across nodes with VXLAN | SDN → New zone → VXLAN → wizard (it does the MTU math) |
| Reserve an IP for a VM | IPAM → subnet → grid → click a free cell → Reserve |
| Allow only web traffic to a VM | Firewall → guest → builder row (macro: HTTP/HTTPS) |
| "Why can't VM A reach VM B?" | Tools → Path simulator — it names the blocking rule or missing link |

## 4. The escape hatches

- **Raw editor**: Tools → Raw interfaces editor → pick a node. Full editor with linting; saving still goes through review + rollback protection. (Corrected, T-607: previously said "Node → Advanced," which is not where this lives in the shipped UI — it's under Tools with a node-select dropdown, not a per-node "Advanced" tab.)
- **CLI on the node**: `vnproxctl` can list/restore snapshots and trigger rollbacks even when the UI is unreachable (`docs/deployment.md` §Troubleshooting).
- **vnprox down?** Your network keeps working — Proxmox owns the config; vnprox is only a (very good) way of editing it.

## 5. Permissions

You see and can do exactly what your Proxmox permissions allow. Read-only PVE users get a read-only vnprox; users without SDN privileges see the SDN cockpit disabled with a tooltip naming the missing privilege. Everything anyone changes is in the **Audit** view.

## 6. Keyboard reference

`/` search · `1–4` toggle layers · `f` VLAN filter · `g` then `t/s/f/i` go to Topology/SDN/Firewall/IPAM · `⌘K`/`Ctrl+K` command palette · `?` full list.

**Command palette (`⌘K`/`Ctrl+K`)**: one dialog, reachable from any page, merging the same fuzzy entity search `/` opens with every action the current page(s) have registered — "edit vmbr0", "new VLAN zone", "open drafts", "simulate path from <entity>", and more as pages add their own verbs. Arrow keys move through the merged list; Enter/click runs the highlighted entry. On the topology map itself, arrow keys also move focus between entities (roving focus, in on-screen left-to-right/top-to-bottom order) once an entity has focus; Enter activates the focused one exactly like a click.
