# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0
"""test_route_lint.py — T-4002 acceptance criterion 2: "No module in the
collection calls an apply/confirm/rollback route — grep-checkable against
the collection's own route-constant list, documented as a lint rule in its
CI."

This is that grep, run as a fast, always-on (no server, no ANSIBLE_ACC)
pytest test — wired into scripts/ci-local.sh's ansible-collection job like
every other test in this suite, so a future PR that adds a
POST /changesets/{id}/apply (or /confirm, or /rollback) call anywhere in
plugins/ fails a named test instead of silently widening this collection's
reach past the stage-only contract documented in the README.
"""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import os
import re

FORBIDDEN_SUBSTRINGS = ("/apply", "/confirm", "/rollback")

PLUGINS_DIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "plugins")

# Matches the second (path) argument of every self._request(METHOD, PATH, ...)
# call site in this collection's source — the ONE place an HTTP path literal
# is ever allowed to appear outside ROUTES itself (vnprox_api.py's private
# transport method). Every public method on VnproxAPI (create_changeset,
# get_inventory, ...) calls through here, so scanning call sites (not prose)
# is what actually proves no path bypasses the typed API with a hand-rolled
# route string.
_REQUEST_CALL_PATH = re.compile(
    r"""_request\(\s*["']\w+["']\s*,\s*["']([^"']*)["']"""
)


def _all_python_files(root):
    for dirpath, _dirnames, filenames in os.walk(root):
        for name in filenames:
            if name.endswith(".py"):
                yield os.path.join(dirpath, name)


def test_no_request_call_site_uses_a_forbidden_path():
    """Scans actual `self._request(...)` call sites — not prose/comments,
    which legitimately name "/apply"/"/confirm"/"/rollback" when explaining
    what this collection refuses to call — for a path literal containing a
    forbidden route substring. This is the check that would catch a future
    module bypassing the typed VnproxAPI methods with a hand-rolled path."""
    offenders = []
    for path in _all_python_files(PLUGINS_DIR):
        with open(path, "r", encoding="utf-8") as f:
            text = f.read()
        for match in _REQUEST_CALL_PATH.finditer(text):
            route_path = match.group(1)
            for needle in FORBIDDEN_SUBSTRINGS:
                if needle in route_path:
                    offenders.append((path, route_path, needle))
    assert offenders == [], (
        "found a _request() call site using a forbidden apply/confirm/rollback path — "
        "this collection's stage-only contract (README.md \"The stage-only contract\") "
        "requires every mutating call to stop at create/validate/update-draft/delete-draft: "
        "%r" % (offenders,)
    )


def test_vnprox_api_routes_table_has_no_apply_confirm_rollback():
    """A second, narrower check directly against the ROUTES table in
    module_utils/vnprox_api.py (this collection's single source of truth
    for routes the API client is allowed to call) — belt-and-suspenders
    with the whole-tree grep above, and the one that would catch a future
    route added to ROUTES under an unexpected key name."""
    import sys

    sys.path.insert(0, os.path.join(PLUGINS_DIR, "module_utils"))
    # vnprox_api imports from ansible.module_utils.* — available because
    # ansible-core is a dev-time test dependency of this collection (see
    # README.md's "Running the tests" section), never a runtime dependency
    # of anything in the main Go module.
    import vnprox_api  # noqa: E402

    for name, (method, path) in vnprox_api.ROUTES.items():
        for needle in FORBIDDEN_SUBSTRINGS:
            assert needle not in path, "ROUTES[%r] = %s %s contains forbidden substring %r" % (
                name, method, path, needle,
            )
