# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0
"""conftest.py — shared pytest fixtures for this collection's test suite.

Acceptance tests (anything using the `stack` or `collection_env` fixtures
below) build and run the real cmd/pvemock + cmd/vnproxd binaries as
subprocesses (harness.py) and are gated behind ANSIBLE_ACC=1 — the same
"only pay this cost when opted in" discipline
contrib/terraform-provider-vnprox's TF_ACC gate uses (see its README's
"Running the acceptance tests" section), so a bare `pytest` here never
builds Go binaries. scripts/ci-local.sh's ansible-collection job always sets
ANSIBLE_ACC=1.
"""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import os
import shutil
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from harness import AcceptanceStack  # noqa: E402


def _acceptance_enabled():
    return os.environ.get("ANSIBLE_ACC") == "1"


@pytest.fixture(scope="session")
def stack():
    if not _acceptance_enabled():
        pytest.skip("acceptance test: set ANSIBLE_ACC=1 to run (builds and starts real pvemock+vnproxd)")
    with AcceptanceStack() as s:
        yield s


@pytest.fixture(scope="session")
def collection_root():
    """The collection's own directory (contrib/ansible-collection-vnprox)."""
    return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


@pytest.fixture(scope="session")
def collections_path(tmp_path_factory, collection_root):
    """Builds a throwaway ANSIBLE_COLLECTIONS_PATH root containing this
    collection at the namespace/name path ansible-core's collection loader
    requires (ansible_collections/vnprox/vnprox), via a symlink to the real
    source tree rather than a copy, so edits during development are picked
    up without re-syncing."""
    root = tmp_path_factory.mktemp("collections_path")
    ns_dir = root / "ansible_collections" / "vnprox"
    ns_dir.mkdir(parents=True)
    link_path = ns_dir / "vnprox"
    if not link_path.exists():
        try:
            os.symlink(collection_root, str(link_path), target_is_directory=True)
        except OSError:
            shutil.copytree(collection_root, str(link_path))
    return str(root)
