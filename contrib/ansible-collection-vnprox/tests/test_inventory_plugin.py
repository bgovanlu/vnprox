# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0
"""test_inventory_plugin.py — T-4002 acceptance criterion 3:
"ansible-inventory --list against a live vnproxd/pvemock returns
node/bridge/bond groups that match GET /topology's content."

Runs the REAL `ansible-inventory` CLI against a REAL pvemock + vnproxd
stack (harness.py), and cross-checks its output against an independent
direct call to GET /topology — the same "independent verification, not the
plugin checking its own homework" discipline test_modules.py's changeset
check uses.
"""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import json
import os
import subprocess
import urllib.request

from harness import _insecure_ssl_context


def _unwrap(value):
    # ansible-inventory --list JSON-serializes AnsibleUnsafeText values (a
    # security wrapper newer ansible-core applies to values sourced from an
    # inventory plugin, since they could otherwise be re-templated) as
    # {"__ansible_unsafe": "<value>"} rather than a bare string. Unwrap it
    # so this test asserts against the actual string content.
    if isinstance(value, dict) and "__ansible_unsafe" in value:
        return value["__ansible_unsafe"]
    return value


def _kind_group_name(kind):
    # Mirrors plugins/inventory/topology_inventory.py's InventoryModule._kind_group
    # sanitization (non-alnum/underscore -> "_") — kept in sync deliberately rather
    # than importing the plugin module, so this test asserts the DOCUMENTED
    # behavior (README.md's "grouped two ways: by kind...") rather than merely
    # mirroring the implementation by construction.
    return "".join(c if (c.isalnum() or c == "_") else "_" for c in kind) or "unknown_kind"


def _write_inventory_config(tmp_path, base_url, token):
    cfg = tmp_path / "inventory.vnprox_topology.yml"
    cfg.write_text(
        "plugin: vnprox.vnprox.topology_inventory\n"
        "base_url: %s\n"
        "token: %s\n"
        "validate_certs: false\n" % (json.dumps(base_url), json.dumps(token))
    )
    return cfg


def _run_ansible_inventory(tmp_path, collections_path, cfg_path):
    env = dict(os.environ)
    env["ANSIBLE_COLLECTIONS_PATH"] = collections_path
    env["ANSIBLE_INVENTORY_ENABLED"] = "vnprox.vnprox.topology_inventory"
    proc = subprocess.run(
        ["ansible-inventory", "-i", str(cfg_path), "--list"],
        cwd=str(tmp_path), env=env, capture_output=True, text=True, timeout=60,
    )
    if proc.returncode != 0:
        raise AssertionError(
            "ansible-inventory failed (rc=%d):\nSTDOUT:\n%s\nSTDERR:\n%s"
            % (proc.returncode, proc.stdout, proc.stderr)
        )
    return json.loads(proc.stdout)


def _get_topology_directly(base_url, token):
    ctx = _insecure_ssl_context()
    req = urllib.request.Request(
        base_url + "/topology", headers={"Authorization": "Bearer " + token},
    )
    with urllib.request.urlopen(req, timeout=10, context=ctx) as resp:
        return json.loads(resp.read())


def test_inventory_list_groups_match_topology_content(tmp_path, stack, collections_path):
    cfg_path = _write_inventory_config(tmp_path, stack.base_url, stack.token)
    inv = _run_ansible_inventory(tmp_path, collections_path, cfg_path)
    topo = _get_topology_directly(stack.base_url, stack.token)

    topo_nodes = topo.get("nodes") or []
    topo_ids_by_kind = {}
    topo_node_groups = set()
    for n in topo_nodes:
        topo_ids_by_kind.setdefault(n["kind"], set()).add(n["id"])
        if n.get("nodeGroup"):
            topo_node_groups.add(n["nodeGroup"])

    assert topo_ids_by_kind, "fixture topology unexpectedly reported no nodes at all"

    # The plugin's top-level "vnprox" group must contain every topology node.
    assert "vnprox" in inv
    vnprox_hosts = set(inv["vnprox"]["hosts"])
    all_topo_ids = set()
    for ids in topo_ids_by_kind.values():
        all_topo_ids |= ids
    assert vnprox_hosts == all_topo_ids

    # A group named for each observed kind exists and contains exactly that
    # kind's refs — this is the "bridge"/"bond" grouping T-4002's AC3 names
    # directly.
    for kind, ids in topo_ids_by_kind.items():
        group_name = _kind_group_name(kind)
        assert group_name in inv, "expected a %r group in ansible-inventory --list output" % group_name
        assert set(inv[group_name]["hosts"]) == ids

    assert "bridge" in topo_ids_by_kind, "fixture (single-node.yaml) is expected to have vmbr0"
    assert "bridge:pve1:vmbr0" in topo_ids_by_kind["bridge"]

    # Node grouping: every observed nodeGroup produces a "node_<name>" group.
    for node_group in topo_node_groups:
        group_name = "node_" + node_group
        assert group_name in inv
        assert set(inv[group_name]["hosts"]).issubset(all_topo_ids)

    # Hostvars carry the fields the README documents.
    hostvars = inv.get("_meta", {}).get("hostvars", {})
    bridge_vars = hostvars.get("bridge:pve1:vmbr0")
    assert bridge_vars is not None
    assert _unwrap(bridge_vars["vnprox_kind"]) == "bridge"
    assert _unwrap(bridge_vars["vnprox_node"]) == "pve1"
