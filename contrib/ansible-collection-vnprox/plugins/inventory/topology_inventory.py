# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function

__metaclass__ = type

DOCUMENTATION = r"""
---
name: topology_inventory
plugin_type: inventory
short_description: vnprox dynamic inventory sourced from GET /topology and GET /ports
description:
  - Builds an Ansible inventory from a running vnproxd's live, projected topology —
    C(GET /topology) (every bridge/bond/VLAN/physical NIC/guest the cluster map
    renders) and, optionally, C(GET /ports) (the flat LLDP-derived
    node/nic/switch/port table) — so C(ansible-inventory) can enumerate
    vnprox-managed infrastructure without a second, hand-maintained source of truth.
  - Read-only. This plugin calls no changeset route and stages nothing — it is a pure
    consumer of vnprox's inventory read surface, the same routes
    C(contrib/terraform-provider-vnprox)'s C(vnprox_topology) data source reads.
  - >-
    Every topology node becomes one inventory host, named by its vnprox C(Ref) triplet
    (e.g. C(bridge:pve1:vmbr0)) — stable and globally unique, the same identifier
    C(GET /inventory/{ref}) and this collection's C(bridge)/C(vlan) modules key on.
  - >-
    Hosts are grouped two ways: by B(kind) — a group named exactly the topology node's
    C(kind) value (C(bridge), C(bond), C(vlan), C(physnic), C(guest), ...) — and by
    B(node) — a group named for the owning PVE cluster node (the topology node's
    C(nodeGroup) field), when non-empty (cluster-scoped entities like SDN vnets have no
    single owning node and are grouped by kind only). A top-level C(vnprox) group
    contains every host this plugin produces.
  - >-
    When C(include_ports) is true (the default), C(GET /ports) rows are matched to
    their C(physnic:<node>:<nic>) host (when that ref exists in the topology) and
    added as host vars (C(vnprox_switch), C(vnprox_switch_port), C(vnprox_pvid),
    C(vnprox_tagged_vlans), C(vnprox_speed_mbps), C(vnprox_ports_stale)) — the
    physical-port-to-switch mapping the topology graph alone does not carry.
  - This is a standard-shaped Ansible inventory plugin (C(compose)/C(groups)/
    C(keyed_groups)/C(strict) all work as documented in
    U(https://docs.ansible.com/ansible/latest/collections/ansible/builtin/constructed_inventory.html)).
extends_documentation_fragment:
  - vnprox.vnprox.connection
  - constructed
options:
  plugin:
    description: Must be C(vnprox.vnprox.topology_inventory).
    required: true
    type: str
    choices: [vnprox.vnprox.topology_inventory]
  layers:
    description: Optional layer filter passed to C(GET /topology?layers=).
    type: list
    elements: str
  node:
    description: Optional node filter passed to C(GET /topology?node=).
    type: str
  include_ports:
    description: Whether to enrich physnic hosts with C(GET /ports) data.
    type: bool
    default: true
author:
  - vnprox contributors
"""

EXAMPLES = r"""
# inventory.vnprox_topology.yml
plugin: vnprox.vnprox.topology_inventory
base_url: https://pve1:8007/api/v1
token: "{{ lookup('env', 'VNPROX_TOKEN') }}"
validate_certs: true
"""

from ansible.errors import AnsibleParserError
from ansible.plugins.inventory import BaseInventoryPlugin, Constructable

from ansible_collections.vnprox.vnprox.plugins.module_utils.vnprox_api import (
    VnproxAPI,
    VnproxAPIError,
)

# Kind values that carry a genuinely meaningful "kind" group — mirrors the
# vocabulary internal/inventory/ref.go's Kind constants define in the main
# repo (bridge/bond/ovs_bridge/ovs_bond/vlan/physnic/guest/guest-nic/...).
# This plugin does not hardcode the list: every kind GET /topology reports is
# grouped, whatever it is, so a kind added to the daemon later needs no
# change here.


class InventoryModule(BaseInventoryPlugin, Constructable):
    NAME = "vnprox.vnprox.topology_inventory"

    def verify_file(self, path):
        if not super(InventoryModule, self).verify_file(path):
            return False
        return path.endswith(("vnprox_topology.yml", "vnprox_topology.yaml"))

    def _client(self):
        base_url = self.get_option("base_url")
        token = self.get_option("token")
        validate_certs = self.get_option("validate_certs")
        timeout = self.get_option("timeout")
        return VnproxAPI(
            base_url=base_url, token=token, validate_certs=validate_certs, timeout=timeout,
        )

    def _kind_group(self, kind):
        # Sanitized to a valid Ansible group name (kind values are already
        # simple lowercase tokens like "bridge"/"ovs_bond", but this stays
        # defensive rather than assuming that never changes upstream).
        name = "".join(c if (c.isalnum() or c == "_") else "_" for c in kind)
        return name or "unknown_kind"

    def _node_group(self, node_group):
        name = "".join(c if (c.isalnum() or c == "_") else "_" for c in node_group)
        return "node_" + name if name else None

    def parse(self, inventory, loader, path, cache=True):
        super(InventoryModule, self).parse(inventory, loader, path, cache=cache)
        self._read_config_data(path)

        client = self._client()

        try:
            topo = client.get_topology(
                layers=self.get_option("layers"), node=self.get_option("node"),
            )
        except VnproxAPIError as e:
            raise AnsibleParserError("vnprox topology_inventory: GET /topology: %s" % e)

        self.inventory.add_group("vnprox")

        nodes = topo.get("nodes") or []
        refs_present = set()

        for n in nodes:
            hostname = n.get("id")
            if not hostname:
                continue
            refs_present.add(hostname)

            self.inventory.add_host(hostname)
            self.inventory.add_child("vnprox", hostname)

            kind = n.get("kind") or ""
            self.inventory.set_variable(hostname, "vnprox_ref", hostname)
            self.inventory.set_variable(hostname, "vnprox_kind", kind)
            self.inventory.set_variable(hostname, "vnprox_label", n.get("label"))
            self.inventory.set_variable(hostname, "vnprox_layer", n.get("layer"))
            self.inventory.set_variable(hostname, "vnprox_node", n.get("nodeGroup"))
            self.inventory.set_variable(hostname, "vnprox_status", n.get("status"))

            if kind:
                kind_group = self._kind_group(kind)
                self.inventory.add_group(kind_group)
                self.inventory.add_child(kind_group, hostname)

            node_group = self._node_group(n.get("nodeGroup") or "")
            if node_group:
                self.inventory.add_group(node_group)
                self.inventory.add_child(node_group, hostname)

            # Standard constructed-inventory hooks — compose/groups/keyed_groups
            # all read this host's variables exactly as any other inventory
            # plugin's do.
            variables = self.inventory.get_host(hostname).get_vars()
            self._set_composite_vars(
                self.get_option("compose"), variables, hostname, strict=self.get_option("strict"),
            )
            self._add_host_to_composed_groups(
                self.get_option("groups"), variables, hostname, strict=self.get_option("strict"),
            )
            self._add_host_to_keyed_groups(
                self.get_option("keyed_groups"), variables, hostname, strict=self.get_option("strict"),
            )

        if self.get_option("include_ports"):
            try:
                ports = client.get_ports()
            except VnproxAPIError as e:
                raise AnsibleParserError("vnprox topology_inventory: GET /ports: %s" % e)

            for row in (ports.get("items") or []):
                ref = "physnic:%s:%s" % (row.get("node"), row.get("nic"))
                if ref not in refs_present:
                    # This NIC either has no topology node (e.g. filtered by
                    # ?layers=/?node=) or vnprox has not resolved it as a
                    # physnic yet — skip rather than inventing a host for a
                    # row this plugin cannot otherwise corroborate.
                    continue
                self.inventory.set_variable(ref, "vnprox_switch", row.get("switch"))
                self.inventory.set_variable(ref, "vnprox_switch_port", row.get("port"))
                self.inventory.set_variable(ref, "vnprox_speed_mbps", row.get("speedMbps"))
                self.inventory.set_variable(ref, "vnprox_pvid", row.get("pvid"))
                self.inventory.set_variable(ref, "vnprox_tagged_vlans", row.get("taggedVlans"))
                self.inventory.set_variable(ref, "vnprox_ports_stale", row.get("stale"))
