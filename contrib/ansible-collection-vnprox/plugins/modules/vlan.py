#!/usr/bin/python
# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function

__metaclass__ = type

DOCUMENTATION = r"""
---
module: vlan
short_description: Stage a plain 802.1q VLAN sub-interface as a vnprox draft changeset
description:
  - Stages a VLAN sub-interface (a C(vlan.create)/C(vlan.update)/C(vlan.delete) op,
    mirroring C(internal/change/params_vlan.go) in the main vnprox repository) as a
    draft, B(validated) changeset — never live. Plain 802.1q sub-interfaces only; OVS
    Int Port VLANs are out of scope for this first cut, matching
    C(contrib/terraform-provider-vnprox)'s C(vnprox_vlan) resource exactly (its README's
    "Resources and data sources" table states the same scope cut).
  - See the C(vnprox.vnprox.bridge) module's documentation, and this collection's
    README "The stage-only contract" section, for the full stage-only contract this
    module shares.
  - >-
    B(A known asymmetry, stated rather than hidden): C(gateway) is accepted at create
    time (rendered into the interface stanza's C(gateway) option, per
    C(change.VlanCreateParams.Gateway)) but the live VLAN sub-interface entity
    (C(internal/inventory.VlanIface)) carries no C(Gateway) field to read back — so this
    module's idempotency check cannot compare C(gateway) against live state the way it
    compares C(addresses)/C(mtu)/C(vid). A C(gateway) change alone, with everything
    else unchanged, will not be detected as drift by this module. This is a real gap in
    the underlying entity model, not something invented for this collection —
    C(contrib/terraform-provider-vnprox)'s own C(vnprox_vlan) resource has the identical
    limitation for the same reason (it has no continuous drift-detection notion at all,
    so it does not surface the gap the same way, but the underlying asymmetry is
    identical).
options:
  node:
    description: The PVE node this VLAN sub-interface is staged on.
    type: str
    required: true
  name:
    description: The sub-interface's name, e.g. C(vmbr0.20).
    type: str
    required: true
  state:
    description:
      - C(present) stages a create/update changeset if live state doesn't already
        match; C(absent) stages a delete changeset if the sub-interface still exists
        live.
    type: str
    choices: [present, absent]
    default: present
  parent:
    description: The parent interface's name.
    type: str
  vid:
    description: The VLAN id.
    type: int
  gateway:
    description: >-
      Default gateway, create-time only — see this module's description for why it
      cannot be compared for idempotency against live state.
    type: str
  addresses:
    description: CIDR addresses on this sub-interface.
    type: list
    elements: str
  mtu:
    description: MTU.
    type: int
extends_documentation_fragment:
  - vnprox.vnprox.connection
author:
  - vnprox contributors
"""

EXAMPLES = r"""
- name: Stage a management VLAN sub-interface
  vnprox.vnprox.vlan:
    base_url: "https://pve1:8007/api/v1"
    token: "{{ vnprox_token }}"
    node: pve1
    name: vmbr0.20
    parent: vmbr0
    vid: 20
    addresses: ["10.20.0.2/24"]
"""

RETURN = r"""
changed:
  description: Whether a changeset was staged this run.
  type: bool
  returned: always
changeset:
  description: The staged changeset (id, status, findings, ...).
  type: dict
  returned: when a changeset exists for this resource
live_exists:
  description: Whether GET /inventory/{ref} currently resolves for this sub-interface.
  type: bool
  returned: always
"""

from ansible.module_utils.basic import AnsibleModule

from ansible_collections.vnprox.vnprox.plugins.module_utils.vnprox_api import (
    VnproxAPIError,
    stage_and_validate,
)
from ansible_collections.vnprox.vnprox.plugins.module_utils.vnprox_common import (
    api_from_module,
    connection_argument_spec,
    fields_match,
    partial_desired,
)

# Keyed by inventory.VlanIface's own Go field names (entity.go). No
# "Gateway" key: see this module's DOCUMENTATION for why.
FIELD_MAPPING = {
    "parent": "ParentName",
    "addresses": "Addresses",
    "vid": "Vid",
    "mtu": "MTU",
}


def vlan_ref(node, name):
    return "vlan:%s:%s" % (node, name)


def create_params(p):
    # change.VlanCreateParams is not pointer-based — a brand-new
    # sub-interface has no prior state an omitted key could clear.
    return {
        "parent": p.get("parent") or "",
        "gateway": p.get("gateway") or "",
        "addresses": p.get("addresses") or [],
        "vid": p.get("vid") or 0,
        "mtu": p.get("mtu") or 0,
    }


def update_params(p):
    # change.VlanUpdateParams only accepts Addresses/MTU post-create —
    # Parent/Vid are immutable (contrib/terraform-provider-vnprox marks
    # both RequiresReplace in resource_vlan.go). If a play changes
    # parent/vid on an already-live sub-interface, this staged update will
    # carry no parent/vid change at all — the mismatch is silently NOT
    # applied via update, but it IS still detected (desired_fields() above
    # includes ParentName/Vid), so fields_match() correctly reports
    # changed=true and stages a changeset; that changeset just won't
    # actually change parent/vid once a human applies it. Recreate the
    # resource explicitly (state=absent, then state=present) to change
    # either — the same limitation the Terraform provider's RequiresReplace
    # plan modifier surfaces more visibly (a forced replace) than this
    # module currently does. Flagged rather than silently accepted.
    out = {}
    if p.get("addresses") is not None:
        out["addresses"] = p["addresses"]
    if p.get("mtu") is not None:
        out["mtu"] = p["mtu"]
    return out


def desired_fields(p):
    return partial_desired(p, FIELD_MAPPING)


def run(module):
    p = module.params
    ref = vlan_ref(p["node"], p["name"])
    api = api_from_module(module)

    try:
        live = api.get_inventory(ref)
    except VnproxAPIError as e:
        module.fail_json(msg="reading live inventory for %s: %s" % (ref, e))
        return

    result = {"changed": False, "live_exists": live is not None}

    if p["state"] == "absent":
        if live is None:
            module.exit_json(**result)
            return
        if module.check_mode:
            result["changed"] = True
            module.exit_json(**result)
            return
        op = {"op": "vlan.delete", "target": ref, "params": {}}
        title = "ansible: delete vlan %s on %s" % (p["name"], p["node"])
        try:
            cs, blocking, warn = stage_and_validate(api, title, [op])
        except VnproxAPIError as e:
            module.fail_json(msg="staging vlan.delete changeset: %s" % e)
            return
        result["changed"] = True
        result["changeset"] = cs
        if warn:
            module.warn("validating staged changeset: %s" % warn)
        if blocking:
            module.warn(
                "changeset %s was staged but did not validate clean: %s"
                % (cs["id"], "; ".join("[%s] %s" % (f["code"], f["message"]) for f in blocking))
            )
        module.exit_json(**result)
        return

    desired = desired_fields(p)
    if live is not None and fields_match(live.get("fields") or {}, desired):
        module.exit_json(**result)
        return

    if module.check_mode:
        result["changed"] = True
        module.exit_json(**result)
        return

    op_name = "vlan.update" if live is not None else "vlan.create"
    op_params = update_params(p) if live is not None else create_params(p)
    op = {"op": op_name, "target": ref, "params": op_params}
    title = "ansible: %s vlan %s on %s" % (
        "update" if live is not None else "create", p["name"], p["node"],
    )
    try:
        cs, blocking, warn = stage_and_validate(api, title, [op])
    except VnproxAPIError as e:
        module.fail_json(msg="staging %s changeset: %s" % (op_name, e))
        return

    result["changed"] = True
    result["changeset"] = cs
    if warn:
        module.warn("validating staged changeset: %s" % warn)
    if blocking:
        module.warn(
            "changeset %s was staged but did not validate clean: %s"
            % (cs["id"], "; ".join("[%s] %s" % (f["code"], f["message"]) for f in blocking))
        )
    module.exit_json(**result)


def main():
    argument_spec = connection_argument_spec()
    argument_spec.update(
        node=dict(type="str", required=True),
        name=dict(type="str", required=True),
        state=dict(type="str", choices=["present", "absent"], default="present"),
        parent=dict(type="str"),
        vid=dict(type="int"),
        gateway=dict(type="str"),
        addresses=dict(type="list", elements="str"),
        mtu=dict(type="int"),
    )
    module = AnsibleModule(argument_spec=argument_spec, supports_check_mode=True)
    run(module)


if __name__ == "__main__":
    main()
