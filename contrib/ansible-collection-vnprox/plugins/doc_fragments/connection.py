# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function

__metaclass__ = type


class ModuleDocFragment(object):
    DOCUMENTATION = r"""
options:
  base_url:
    description:
      - The vnprox daemon's API base URL, e.g. C(https://pve1:8007/api/v1).
      - Falls back to the C(VNPROX_URL) environment variable — the same variable
        name C(contrib/terraform-provider-vnprox) uses.
    type: str
    required: true
  token:
    description:
      - A T-1104 bearer token minted via C(POST /tokens). This module never logs in
        with a username and password — see this collection's README, "Authentication".
      - Falls back to the C(VNPROX_TOKEN) environment variable.
    type: str
    required: true
  validate_certs:
    description:
      - Whether to verify the daemon's TLS certificate.
      - Falls back to the C(VNPROX_VALIDATE_CERTS) environment variable.
    type: bool
    default: true
  timeout:
    description: Per-request timeout, in seconds.
    type: int
    default: 30
"""
