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

## 7. Beyond one cluster (v2.0)

Everything above works the same whether you run one cluster or ten. The features in this section only appear when you use them — if you run a single cluster and never attach a second, vnprox looks and behaves exactly as it did before v2.0.

### 7.1 Federation — many clusters, one pane

An administrator attaches other PVE clusters under **Settings → Clusters** (name, API URL, and a read credential for that cluster). Each cluster's credential is sealed at rest with the same encryption vnprox uses for Proxmox tickets, and is never shown again.

Once a **second** cluster is attached, the map gains a **Global** view at its outermost zoom: one capsule per cluster showing its name, open-findings count, drift status, and a greyed-out "unreachable" indicator if a cluster is momentarily unreachable (its capsule degrades; the others are unaffected — you never lose the whole view because one cluster is down). Click a capsule to drill into that cluster's ordinary topology, unchanged from the single-cluster experience.

Search and the command palette (`/`, `⌘K`) span every attached cluster: results are grouped by cluster and namespaced, and the palette gains a **"switch to cluster X"** action to change context. The **Audit** view merges rows across clusters newest-first, each tagged with its cluster (with the same "partial results" indicator you already know from unreachable peers).

**What federation does *not* do:** it never changes another cluster's config for you. Each cluster stays the source of truth for its own network; a changeset always belongs to exactly one cluster and is rejected if an edit would reach across the boundary. Federation federates *views and workflows*, not ownership.

### 7.2 Cross-cluster IPAM & external subnets

**IPAM → Cross-cluster** surfaces any subnet (or overlapping CIDR) allocated in two attached clusters as a **conflict finding** naming both clusters — the "we used 10.20.0.0/24 in two places" problem is finally visible.

You can also record **external subnets** — office LANs, upstream transit, colo ranges that PVE itself doesn't manage — as first-class IPAM records (**IPAM → External subnets → Add**), so your address plan is complete, not just the PVE-managed part. External subnets are plain records; they are never staged as PVE SDN changes.

If you run NetBox or phpIPAM, the bridge is now **two-way**. **IPAM → External sync → Preview** shows a dry run of what would change on either side (additions, removals, conflicts) and writes nothing. Only **Apply** (with an explicit confirm) performs the sync, and every write is recorded in the audit log with before/after. A disagreement between vnprox and the external system on a specific address shows up as a finding, not a silent overwrite.

### 7.3 DNS management

If your SDN uses the DNS plugin (PowerDNS), **SDN → DNS** now shows your zones and records — both the authoritative config and, where reachable, what PowerDNS is actually serving (the same config-vs-live duality you see for DHCP reservations vs. leases). Guests whose IP matches a DNS record get a **name badge** on the map.

Editing a record or zone is an ordinary change: it collects in the change drawer and goes through the same review → apply → confirm flow as any SDN edit (deleting a zone that still has records makes you remove the records in the same change). Nothing here is a separate, un-audited mechanism.

### 7.4 Switch config push (guarded, opt-in)

This is the one read-write step onto your physical switches, and it is deliberately the most guarded feature in the product. It is **off by default** and must be enabled twice: once at the daemon level, and again for each specific switch you register (**Settings → Switches**). Until both are on, no switch write is possible.

When enabled, vnprox can push **only** to switch ports that LLDP confirms are facing your PVE nodes, and **only** VLAN membership, port descriptions, and LACP settings — never a full-config push. Every push is an ordinary changeset with a diff and a confirm step, and immediately before each write vnprox re-checks that the port's neighbor is still the PVE node it expects (if a cable moved, the push aborts).

Read the residual-risk note before you enable it: unlike a node-side change, a switch that a bad push makes unreachable **cannot be rolled back remotely** — there is no vnprox agent living on the switch. vnprox extends its management-path guardrails onto the uplink port carrying a node's management VLAN (a push that would strip it is hard-blocked, no override), and if it can't reach a switch to revert a change, it marks that changeset "rollback incomplete — needs manual intervention" rather than pretending it rolled back.

### 7.5 Backup network awareness (PBS)

Proxmox Backup Server hosts now appear on the map with their interfaces, and the **Backup path** paint mode lights up the node → PBS traffic path for nodes with a backup job targeting that storage. The inspector adds a plain-English datastore-network sizing hint (based on your backup schedule/volume and the resolved link speed). This is entirely read-only — vnprox stores no PBS credentials and makes no changes here.

### 7.6 Single sign-on (OIDC)

For larger, multi-cluster setups, an administrator can enable **OIDC login** alongside the normal Proxmox login (**Settings → Authentication**). You log in through your identity provider, and your group memberships map to a vnprox role.

Note the boundary: OIDC signs you in to vnprox, but your **Proxmox permissions still decide what you can do in each cluster**. An OIDC role never grants more than your real PVE ACLs allow, and if there's no PVE linkage for a cluster, you can read it (subject to that cluster's rules) but hold no write capability there from the OIDC role alone.
