#!/usr/bin/python
# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function

__metaclass__ = type

DOCUMENTATION = r"""
---
module: bridge
short_description: Stage a Linux bridge as a vnprox draft changeset
description:
  - Stages a Linux bridge (a C(bridge.create)/C(bridge.update)/C(bridge.delete) op,
    mirroring C(internal/change/params_bridge.go) in the main vnprox repository) as a
    draft, B(validated) changeset — never live.
  - >-
    B(This module never makes a network change live.) C(state=present)/C(state=absent)
    stage a changeset and stop; making it live is a human review action inside vnprox,
    exactly the same "stage, never apply" boundary
    C(contrib/terraform-provider-vnprox) enforces for Terraform. See this collection's
    top-level README, section "The stage-only contract", before writing a playbook
    against this module — in particular its C(changed) semantics caveat and the
    reference to C(planning/tasks/T-4016-stage-only-convergence-semantics.md), the
    still-open decision this module deliberately does not get ahead of.
  - >-
    Idempotency: a run against live state that already matches this module's
    parameters reports C(changed=false) and stages nothing (see the README's
    "Idempotency" section) — it does B(not) mean the resource is "applied", only
    that nothing further needs staging right now.
options:
  node:
    description: The PVE node this bridge is staged on.
    type: str
    required: true
  name:
    description: The bridge's interface name, e.g. C(vmbr99).
    type: str
    required: true
  state:
    description:
      - C(present) stages a create/update changeset if live state doesn't already
        match; C(absent) stages a delete changeset if the bridge still exists live.
    type: str
    choices: [present, absent]
    default: present
  gateway:
    description: Default gateway (rendered as the stanza's C(gateway) option).
    type: str
  comments:
    description: Interface comment text.
    type: str
  addresses:
    description: CIDR addresses on this bridge.
    type: list
    elements: str
  mtu:
    description: MTU.
    type: int
  vlan_aware:
    description:
      - Whether the bridge is VLAN-aware.
      - Unset (the default) means "no opinion" — this module never compares or
        touches C(vlan_aware) on an already-live bridge unless you set it explicitly.
        On creation, unset is treated as C(false) (a plain, non-VLAN-aware bridge).
    type: bool
  stp:
    description:
      - Whether STP is enabled. Same "no opinion when unset" behavior as C(vlan_aware).
    type: bool
extends_documentation_fragment:
  - vnprox.vnprox.connection
author:
  - vnprox contributors
"""

EXAMPLES = r"""
- name: Stage a lab bridge
  vnprox.vnprox.bridge:
    base_url: "https://pve1:8007/api/v1"
    token: "{{ vnprox_token }}"
    node: pve1
    name: vmbr99
    gateway: 10.99.0.1
    addresses: ["10.99.0.2/24"]
    vlan_aware: true
  register: result

# result.changeset.id now names a DRAFT, VALIDATED changeset.
# Nothing on pve1 changed yet. Now go review it inside vnprox.
"""

RETURN = r"""
changed:
  description: Whether a changeset was staged this run.
  type: bool
  returned: always
changeset:
  description: >-
    The staged/current changeset (id, status, findings, ...), or the changeset most
    recently associated with this resource when nothing new was staged. Absent when
    state=absent and the bridge did not exist live (nothing to report).
  type: dict
  returned: when a changeset exists for this resource
live_exists:
  description: >-
    Whether GET /inventory/{ref} currently resolves for this bridge — informational
    only, exactly like the Terraform provider's live_exists attribute; this module's
    own present/absent contract is driven by the diff against live state, not by this
    value.
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

# Maps this module's params to inventory.Bridge's own Go field names
# (internal/inventory/entity.go), used both to build desired_fields() (only
# for params the caller actually specified — see partial_desired's doc
# comment) and, implicitly, to document the exact correspondence.
FIELD_MAPPING = {
    "gateway": "Gateway",
    "comments": "Comments",
    "addresses": "Addresses",
    "mtu": "MTU",
    "vlan_aware": "VlanAware",
    "stp": "STP",
}


def bridge_ref(node, name):
    return "bridge:%s:%s" % (node, name)


def create_params(p):
    # change.BridgeCreateParams is NOT pointer-based (unlike Update below) —
    # a brand-new entity has no prior state an omitted key could
    # accidentally clear, so every field defaults to its zero value here.
    return {
        "gateway": p.get("gateway") or "",
        "comments": p.get("comments") or "",
        "addresses": p.get("addresses") or [],
        "mtu": p.get("mtu") or 0,
        "vlanAware": bool(p.get("vlan_aware")),
        "stp": bool(p.get("stp")),
    }


# update_op's wire-name mapping — deliberately the OP PARAM name (camelCase,
# change.BridgeUpdateParams' JSON tag), not the Go struct field name
# FIELD_MAPPING above uses for live-state comparison.
UPDATE_PARAM_MAPPING = {
    "gateway": "gateway",
    "comments": "comments",
    "addresses": "addresses",
    "mtu": "mtu",
    "vlan_aware": "vlanAware",
    "stp": "stp",
}


def update_params(p):
    # change.BridgeUpdateParams IS pointer-based server-side (every field
    # `*T`, see contrib/terraform-provider-vnprox/internal/provider/
    # resource_bridge.go's bridgeUpdateParams doc comment): a JSON key
    # that's simply ABSENT from the body leaves that field untouched, while
    # a key present with a zero value (e.g. "mtu": 0) explicitly clears it.
    # This module's own contract is "only manage the subset of attributes
    # the play actually specifies" (see vnprox_common.partial_desired's doc
    # comment) — so, unlike create_params above, an update op must omit
    # every key the caller didn't provide, not default it to zero, or a
    # play that only sets `comments` would silently wipe this bridge's
    # gateway/addresses/mtu/vlan_aware/stp the moment a human applies it.
    out = {}
    for py_key, wire_key in UPDATE_PARAM_MAPPING.items():
        val = p.get(py_key)
        if val is not None:
            out[wire_key] = val
    return out


def desired_fields(p):
    return partial_desired(p, FIELD_MAPPING)


def run(module):
    p = module.params
    ref = bridge_ref(p["node"], p["name"])
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
        op = {
            "op": "bridge.delete",
            "target": ref,
            "params": {},
        }
        title = "ansible: delete bridge %s on %s" % (p["name"], p["node"])
        try:
            cs, blocking, warn = stage_and_validate(api, title, [op])
        except VnproxAPIError as e:
            module.fail_json(msg="staging bridge.delete changeset: %s" % e)
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

    # state == present
    desired = desired_fields(p)
    if live is not None and fields_match(live.get("fields") or {}, desired):
        module.exit_json(**result)
        return

    if module.check_mode:
        result["changed"] = True
        module.exit_json(**result)
        return

    op_name = "bridge.update" if live is not None else "bridge.create"
    op_params = update_params(p) if live is not None else create_params(p)
    op = {"op": op_name, "target": ref, "params": op_params}
    title = "ansible: %s bridge %s on %s" % (
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
        gateway=dict(type="str"),
        comments=dict(type="str"),
        addresses=dict(type="list", elements="str"),
        mtu=dict(type="int"),
        # No `default=` on either boolean: see vnprox_common.partial_desired's
        # doc comment for why a default here would make "not mentioned" and
        # "explicitly set to the default" indistinguishable, breaking
        # idempotency against an already-live bridge with the non-default
        # value. create_params()/update_params() each supply their own
        # not-specified fallback appropriately for create vs. update.
        vlan_aware=dict(type="bool"),
        stp=dict(type="bool"),
    )
    module = AnsibleModule(argument_spec=argument_spec, supports_check_mode=True)
    run(module)


if __name__ == "__main__":
    main()
