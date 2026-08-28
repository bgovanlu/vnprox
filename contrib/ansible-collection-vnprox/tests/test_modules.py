# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0
"""test_modules.py — acceptance tests for the bridge/vlan/topology_info
modules, run as REAL `ansible-playbook` invocations (not a direct Python
function call, and not a hand-rolled fake) against a REAL pvemock +
vnproxd stack (tests/harness.py, gated behind ANSIBLE_ACC=1 — see
conftest.py).

Each test writes a small playbook + inventory to a temp dir, runs
`ansible-playbook` as a subprocess with ANSIBLE_COLLECTIONS_PATH pointed at
this collection, and has the playbook dump its `register`ed result to a
JSON file for this test to read back — avoiding any dependence on parsing
ansible-playbook's own console output.
"""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import json
import os
import subprocess
import textwrap
import urllib.request

import pytest

from harness import _insecure_ssl_context


INVENTORY_LOCALHOST = "localhost ansible_connection=local ansible_python_interpreter={}\n".format(
    __import__("sys").executable
)


def _run_playbook(tmp_path, collections_path, tasks_yaml, extra_vars):
    inv_path = tmp_path / "inventory.ini"
    inv_path.write_text(INVENTORY_LOCALHOST)

    playbook_path = tmp_path / "playbook.yml"
    playbook_path.write_text(
        "- hosts: localhost\n  gather_facts: false\n  tasks:\n" + textwrap.indent(tasks_yaml, "    ")
    )

    extra_vars_path = tmp_path / "extravars.json"
    extra_vars_path.write_text(json.dumps(extra_vars))

    env = dict(os.environ)
    env["ANSIBLE_COLLECTIONS_PATH"] = collections_path
    env["ANSIBLE_HOST_KEY_CHECKING"] = "false"
    env["ANSIBLE_RETRY_FILES_ENABLED"] = "false"
    env["ANSIBLE_NOCOWS"] = "1"

    proc = subprocess.run(
        [
            "ansible-playbook", str(playbook_path),
            "-i", str(inv_path),
            "-e", "@%s" % extra_vars_path,
        ],
        cwd=str(tmp_path), env=env, capture_output=True, text=True, timeout=120,
    )
    if proc.returncode != 0:
        raise AssertionError(
            "ansible-playbook failed (rc=%d):\nSTDOUT:\n%s\nSTDERR:\n%s"
            % (proc.returncode, proc.stdout, proc.stderr)
        )
    return proc


def _read_result(tmp_path, name):
    return json.loads((tmp_path / name).read_text())


BRIDGE_TASKS = """
- name: stage bridge
  vnprox.vnprox.bridge:
    base_url: "{{{{ vnprox_base_url }}}}"
    token: "{{{{ vnprox_token }}}}"
    validate_certs: false
    node: pve1
    name: vmbr99
    gateway: 10.99.0.1
    addresses: ["10.99.0.2/24"]
    vlan_aware: true
  register: result
- copy:
    content: "{{{{ result | to_nice_json }}}}"
    dest: "{result_file}"
"""


def test_bridge_present_on_new_bridge_stages_a_draft_changeset_and_reports_changed(
    tmp_path, stack, collections_path,
):
    result_file = str(tmp_path / "result1.json")
    _run_playbook(
        tmp_path, collections_path, BRIDGE_TASKS.format(result_file=result_file),
        {"vnprox_base_url": stack.base_url, "vnprox_token": stack.token},
    )
    result = _read_result(tmp_path, "result1.json")

    assert result["changed"] is True
    assert "changeset" in result
    assert result["changeset"]["status"] in ("draft", "validated")
    changeset_id = result["changeset"]["id"]

    # Independent verification against the daemon's own GET /changesets/{id}
    # — the way an external auditor would check — the same discipline
    # harness_test.go documents for the Terraform provider's acceptance
    # suite (step 4: "a direct GET /changesets/{id} call independent of the
    # provider's own client").
    ctx = _insecure_ssl_context()
    req = urllib.request.Request(
        stack.base_url + "/changesets/" + changeset_id,
        headers={"Authorization": "Bearer " + stack.token},
    )
    with urllib.request.urlopen(req, timeout=10, context=ctx) as resp:
        cs = json.loads(resp.read())
    assert cs["status"] in ("draft", "validated")
    assert cs["status"] != "applied"


def test_bridge_present_second_run_against_unchanged_state_reports_unchanged(
    tmp_path, stack, collections_path,
):
    # T-4002 acceptance criterion 1's second half: "a second run against
    # unchanged live state reports changed: false and stages nothing."
    # The first run above only STAGED a changeset — nothing is live, so a
    # naive re-run would see nothing in GET /inventory/{ref} and stage a
    # SECOND changeset. This module's idempotency contract only covers
    # live state (README.md's "Idempotency" section, and T-4016's framing
    # of why "changed" can't mean "converged" here) — so this test drives
    # the fixture's actual live bridge (vmbr0, from
    # testdata/clusters/single-node.yaml) rather than a staged-but-unapplied
    # one, which is the one case this module CAN truthfully report
    # changed=false for without a human having applied anything.
    result_file = str(tmp_path / "result_noop.json")
    tasks = """
- name: bridge already matching live state
  vnprox.vnprox.bridge:
    base_url: "{{{{ vnprox_base_url }}}}"
    token: "{{{{ vnprox_token }}}}"
    validate_certs: false
    node: pve1
    name: vmbr0
    gateway: 192.168.1.1
    comments: "management bridge"
    vlan_aware: false
    stp: false
  register: result
- copy:
    content: "{{{{ result | to_nice_json }}}}"
    dest: "{result_file}"
""".format(result_file=result_file)
    _run_playbook(
        tmp_path, collections_path, tasks,
        {"vnprox_base_url": stack.base_url, "vnprox_token": stack.token},
    )
    result = _read_result(tmp_path, "result_noop.json")
    assert result["changed"] is False
    assert "changeset" not in result
    assert result["live_exists"] is True


def test_bridge_present_divergent_live_state_stages_an_update_and_reports_changed(
    tmp_path, stack, collections_path,
):
    result_file = str(tmp_path / "result_update.json")
    tasks = """
- name: bridge diverging from live vmbr0 (comments differ)
  vnprox.vnprox.bridge:
    base_url: "{{{{ vnprox_base_url }}}}"
    token: "{{{{ vnprox_token }}}}"
    validate_certs: false
    node: pve1
    name: vmbr0
    gateway: 192.168.1.1
    comments: "renamed by ansible"
    vlan_aware: false
    stp: false
  register: result
- copy:
    content: "{{{{ result | to_nice_json }}}}"
    dest: "{result_file}"
""".format(result_file=result_file)
    _run_playbook(
        tmp_path, collections_path, tasks,
        {"vnprox_base_url": stack.base_url, "vnprox_token": stack.token},
    )
    result = _read_result(tmp_path, "result_update.json")
    assert result["changed"] is True
    assert result["changeset"]["status"] in ("draft", "validated")
    for op in result["changeset"]["ops"]:
        assert op["op"] != "bridge.create"  # vmbr0 already exists live


def test_vlan_absent_on_nonexistent_vlan_reports_unchanged_and_stages_nothing(
    tmp_path, stack, collections_path,
):
    result_file = str(tmp_path / "result_absent.json")
    tasks = """
- name: absent vlan that never existed
  vnprox.vnprox.vlan:
    base_url: "{{{{ vnprox_base_url }}}}"
    token: "{{{{ vnprox_token }}}}"
    validate_certs: false
    node: pve1
    name: vmbr0.999
    state: absent
  register: result
- copy:
    content: "{{{{ result | to_nice_json }}}}"
    dest: "{result_file}"
""".format(result_file=result_file)
    _run_playbook(
        tmp_path, collections_path, tasks,
        {"vnprox_base_url": stack.base_url, "vnprox_token": stack.token},
    )
    result = _read_result(tmp_path, "result_absent.json")
    assert result["changed"] is False
    assert "changeset" not in result


def test_topology_info_reads_the_live_fixture_bridge(tmp_path, stack, collections_path):
    result_file = str(tmp_path / "result_topo.json")
    tasks = """
- name: read topology
  vnprox.vnprox.topology_info:
    base_url: "{{{{ vnprox_base_url }}}}"
    token: "{{{{ vnprox_token }}}}"
    validate_certs: false
  register: result
- copy:
    content: "{{{{ result | to_nice_json }}}}"
    dest: "{result_file}"
""".format(result_file=result_file)
    _run_playbook(
        tmp_path, collections_path, tasks,
        {"vnprox_base_url": stack.base_url, "vnprox_token": stack.token},
    )
    result = _read_result(tmp_path, "result_topo.json")
    assert result["changed"] is False
    node_ids = [n["id"] for n in result["topology"]["nodes"]]
    assert "bridge:pve1:vmbr0" in node_ids
