#!/usr/bin/python
# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function

__metaclass__ = type

DOCUMENTATION = r"""
---
module: topology_info
short_description: Read vnprox's projected topology (GET /topology)
description:
  - Read-only. Calls C(GET /topology) and returns it verbatim (nodes, edges,
    layers, generatedAt). Never stages a changeset — there is no C(state) option
    and no write path anywhere in this module's code, unlike
    C(vnprox.vnprox.bridge)/C(vnprox.vnprox.vlan).
  - The same data this collection's C(vnprox.vnprox.topology_inventory) dynamic
    inventory plugin sources from, exposed here as an ordinary fact-gathering module
    for use inside a playbook (e.g. picking a node/bridge ref before staging a
    resource) rather than only at inventory-build time.
options:
  layers:
    description: >-
      Optional layer filter (C(phys), C(l2), C(sdn), C(guest)) — passed through to
      C(GET /topology)'s C(?layers=) query parameter unchanged.
    type: list
    elements: str
  node:
    description: Optional node filter, passed through to C(?node=).
    type: str
extends_documentation_fragment:
  - vnprox.vnprox.connection
author:
  - vnprox contributors
"""

EXAMPLES = r"""
- name: Read the cluster topology
  vnprox.vnprox.topology_info:
    base_url: "https://pve1:8007/api/v1"
    token: "{{ vnprox_token }}"
  register: topo

- name: Show every bridge ref
  debug:
    msg: "{{ topo.topology.nodes | selectattr('kind', 'equalto', 'bridge') | map(attribute='id') | list }}"
"""

RETURN = r"""
topology:
  description: The full GET /topology response, unmodified.
  type: dict
  returned: always
"""

from ansible.module_utils.basic import AnsibleModule

from ansible_collections.vnprox.vnprox.plugins.module_utils.vnprox_api import VnproxAPIError
from ansible_collections.vnprox.vnprox.plugins.module_utils.vnprox_common import (
    api_from_module,
    connection_argument_spec,
)


def run(module):
    api = api_from_module(module)
    try:
        topo = api.get_topology(layers=module.params.get("layers"), node=module.params.get("node"))
    except VnproxAPIError as e:
        module.fail_json(msg="reading topology: %s" % e)
        return
    module.exit_json(changed=False, topology=topo)


def main():
    argument_spec = connection_argument_spec()
    argument_spec.update(
        layers=dict(type="list", elements="str"),
        node=dict(type="str"),
    )
    module = AnsibleModule(argument_spec=argument_spec, supports_check_mode=True)
    run(module)


if __name__ == "__main__":
    main()
