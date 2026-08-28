# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0
"""vnprox_common.py — the connection argument spec every module in this
collection shares (base_url/token/validate_certs/timeout), plus small
helpers for building a VnproxAPI client from module params and for the
"diff-before-stage" idempotency comparison every present/absent module
performs.

Env var fallback mirrors contrib/terraform-provider-vnprox's own
VNPROX_URL/VNPROX_TOKEN provider-config fallback (README.md's "Provider
configuration" table) — the same two environment variables work for both
integrations, so an operator who has already set them up for Terraform does
not need a second set of names for Ansible.
"""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

from ansible.module_utils.basic import env_fallback

from ansible_collections.vnprox.vnprox.plugins.module_utils.vnprox_api import VnproxAPI


def connection_argument_spec():
    return dict(
        base_url=dict(
            type="str",
            required=True,
            fallback=(env_fallback, ["VNPROX_URL"]),
        ),
        token=dict(
            type="str",
            required=True,
            no_log=True,
            fallback=(env_fallback, ["VNPROX_TOKEN"]),
        ),
        validate_certs=dict(
            type="bool",
            default=True,
            fallback=(env_fallback, ["VNPROX_VALIDATE_CERTS"]),
        ),
        timeout=dict(type="int", default=30),
    )


def api_from_module(module):
    return VnproxAPI(
        base_url=module.params["base_url"],
        token=module.params["token"],
        validate_certs=module.params["validate_certs"],
        timeout=module.params["timeout"],
    )


def partial_desired(params, mapping):
    """Builds the comparison dict fields_match() takes, from only the
    module params the caller actually specified (params[key] is not
    None) — an unspecified optional attribute is left alone: not
    compared, not enforced, and never forced back to a zero value on an
    already-live entity. This is the same "declared subset" semantics
    most Ansible present-state modules use for partial-update resources,
    and it is why a module's boolean options (e.g. bridge.py's
    vlan_aware/stp) must NOT carry a `default=` in their argument_spec —
    a default makes "the caller didn't mention it" indistinguishable
    from "the caller wants it set to the default", which would make a
    second run against a live bridge that already has vlan_aware=true
    report changed=true forever, having never been asked to touch it.
    """
    out = {}
    for py_key, go_key in mapping.items():
        val = params.get(py_key)
        if val is not None:
            out[go_key] = val
    return out


def fields_match(live_fields, desired):
    """Compares a subset of GET /inventory/{ref}'s `fields` map (an
    entity's own exported Go struct fields, flattened to JSON — see
    internal/topology/detail.go's Detail() doc comment in the main repo:
    "Fields is the entity's own exported Go struct fields... complete and
    mechanically kept in sync with entity.go by construction") against a
    module's desired-state dict, keyed identically (this collection's
    modules deliberately use the live entity's own Go field names —
    "Gateway", "Addresses", "MTU", ... — as their internal comparison keys,
    not the wire-shape camelCase op-params names, precisely so this
    function needs no translation table between the two).

    This is the "matching -> noop" half of internal/spec.Import's
    absent->create / divergent->update / matching->noop discipline
    (docs/api.md's spec_drift paragraph; T-4002's card cites the same
    pattern by name) — the reason a second run against unchanged live state
    reports changed: false and stages nothing (T-4002 AC1).

    List-valued fields (Addresses, Trunks) are compared order-independent:
    the live NIC/route table order is not something a playbook author's
    YAML list should have to match exactly.
    """
    for key, want in desired.items():
        have = live_fields.get(key)
        if isinstance(want, list):
            if sorted(have or []) != sorted(want or []):
                return False
        elif isinstance(want, bool):
            if bool(have) != want:
                return False
        elif isinstance(want, int) and not isinstance(want, bool):
            if int(have or 0) != want:
                return False
        else:
            if (have or "") != (want or ""):
                return False
    return True
