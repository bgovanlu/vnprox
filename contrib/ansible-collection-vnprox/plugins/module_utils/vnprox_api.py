# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0
"""vnprox_api.py — a minimal HTTP client over vnproxd's documented
/api/v1 surface (docs/api.md in the main vnprox repository), authenticated
exclusively with a T-1104 bearer token — the same mechanism
contrib/terraform-provider-vnprox/internal/provider/client.go uses, and the
same mechanism cmd/vnproxctl/remoteclient.go uses.

THE LOAD-BEARING CONSTRAINT, restated from client.go's own doc comment
because this collection is a second, independent reimplementation of the
identical stage-only contract (this collection is Python, not Go, and lives
in a wholly separate ecosystem from the Terraform provider's Go module — it
cannot import client.go, so the constraint has to be re-established here by
the same discipline, not by sharing code):

    Every mutating method this module exposes stops at
    create-changeset / validate-changeset / update-draft-ops /
    delete-draft. There is no apply_changeset, confirm_changeset, or
    rollback_changeset method here, and there never should be — an Ansible
    module with `state: present/absent` in this collection stages a draft
    changeset and stops; making it live is a human review action inside
    vnprox. See the collection README's "The stage-only contract" section,
    and tests/test_route_lint.py, which greps every module/module_utils
    source file in this collection for forbidden route substrings
    ("/apply", "/confirm", "/rollback") so a future PR that adds one fails a
    named test instead of silently widening this collection's reach.

ROUTES below is deliberately the single source of truth for the routes this
client is allowed to call — it exists as a real Python dict close to the
Ansible route-constant list this card's own acceptance criteria (T-4002 AC2)
that lint rule against, not just as documentation.
"""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import json

from ansible.module_utils.urls import open_url
from ansible.module_utils.six.moves.urllib.error import HTTPError, URLError

# ROUTES: every path this client ever calls. Kept as a flat list of
# "METHOD /path" strings (not just referenced inline in request bodies) so
# tests/test_route_lint.py's grep-based check has one explicit place to
# assert "apply", "confirm", "rollback" never appear as a substring of any
# entry — see this file's module doc comment.
ROUTES = {
    "create_changeset": ("POST", "/changesets"),
    "get_changeset": ("GET", "/changesets/{id}"),
    "update_changeset_ops": ("PUT", "/changesets/{id}"),
    "validate_changeset": ("POST", "/changesets/{id}/validate"),
    "delete_changeset": ("DELETE", "/changesets/{id}"),
    "get_topology": ("GET", "/topology"),
    "get_ports": ("GET", "/ports"),
    "get_inventory": ("GET", "/inventory/{ref}"),
}


class VnproxAPIError(Exception):
    """Raised for any non-2xx response other than 404 (see NotFoundError),
    and for transport/decode failures. Carries the daemon's own error
    envelope (docs/api.md: `{"error": {"code","message","details"}}`) when
    one was returned."""

    def __init__(self, message, code=None, status=None, details=None):
        super(VnproxAPIError, self).__init__(message)
        self.code = code
        self.status = status
        self.details = details


class NotFoundError(VnproxAPIError):
    """Raised by get_* methods on a 404 — the caller (a module's
    idempotency check) distinguishes "entity does not exist live" from
    every other failure, mirroring the Terraform provider's
    isNotFound(err)/notFoundError pattern (client.go)."""


class VnproxAPI(object):
    """Thin bearer-token HTTP client over one vnproxd's /api/v1 base URL.

    Deliberately narrow: it exposes exactly the routes this collection's
    modules and inventory plugin need (ROUTES above), nothing from the
    Auth/Tokens routes — a human (or, in this collection's own test
    harness, a bootstrap step standing in for one) mints the bearer token
    this client is configured with. The client itself never logs in with a
    username and password.
    """

    def __init__(self, base_url, token, validate_certs=True, timeout=30):
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.validate_certs = validate_certs
        self.timeout = timeout

    def _request(self, method, path, body=None):
        url = self.base_url + path
        data = None
        headers = {
            "Authorization": "Bearer %s" % self.token,
            "Accept": "application/json",
        }
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"

        try:
            resp = open_url(
                url,
                method=method,
                data=data,
                headers=headers,
                validate_certs=self.validate_certs,
                timeout=self.timeout,
                follow_redirects="none",
            )
            status = resp.getcode()
            raw = resp.read()
        except HTTPError as e:
            status = e.code
            raw = e.read()
            return self._handle_error(method, path, status, raw)
        except URLError as e:
            raise VnproxAPIError("%s %s: %s" % (method, path, e))

        if not raw:
            return None
        try:
            return json.loads(raw)
        except ValueError as e:
            raise VnproxAPIError(
                "%s %s: decoding response body: %s" % (method, path, e)
            )

    def _handle_error(self, method, path, status, raw):
        code, message, details = "unknown_error", "HTTP %d" % status, None
        if raw:
            try:
                env = json.loads(raw)
                err = env.get("error") or {}
                code = err.get("code", code)
                message = err.get("message", message)
                details = err.get("details")
            except ValueError:
                pass
        if status == 404:
            raise NotFoundError(
                "%s %s: %s: %s" % (method, path, code, message),
                code=code, status=status, details=details,
            )
        raise VnproxAPIError(
            "%s %s: %s: %s" % (method, path, code, message),
            code=code, status=status, details=details,
        )

    # --- Changesets (docs/api.md "Changesets (the only write path)") -----
    #
    # Every method below reaches AT MOST validate. See this file's module
    # doc comment and ROUTES above.

    def create_changeset(self, title, ops):
        return self._request("POST", "/changesets", {"title": title, "ops": ops})

    def validate_changeset(self, changeset_id):
        return self._request("POST", "/changesets/%s/validate" % changeset_id)

    def get_changeset(self, changeset_id):
        return self._request("GET", "/changesets/%s" % changeset_id)

    def update_changeset_ops(self, changeset_id, ops):
        return self._request("PUT", "/changesets/%s" % changeset_id, {"ops": ops})

    def delete_changeset(self, changeset_id):
        return self._request("DELETE", "/changesets/%s" % changeset_id)

    # --- Inventory & topology (read-only) ---------------------------------

    def get_topology(self, layers=None, node=None):
        path = "/topology"
        qs = []
        if layers:
            qs.append("layers=" + ",".join(layers))
        if node:
            qs.append("node=" + node)
        if qs:
            path += "?" + "&".join(qs)
        return self._request("GET", path)

    def get_ports(self):
        return self._request("GET", "/ports")

    def get_inventory(self, ref):
        """Returns the entity's detail dict, or None if it does not
        currently exist live (404) — the Python analogue of the Terraform
        client's EntityExists/GetInventory pair (client.go), used by every
        module's idempotency check (README.md's "Idempotency" section)."""
        try:
            return self._request("GET", "/inventory/%s" % ref)
        except NotFoundError:
            return None


def changeset_editable(status):
    """Mirrors change.Changeset.Editable() in the main Go module and
    changesetEditable() in the Terraform provider (resource_bridge.go) —
    reimplemented here for the same module-boundary reason every wire
    concept in this collection is: a still-draft/validated changeset can be
    revised in place; anything further along gets a NEW changeset instead,
    never a mutation of the one a human already moved forward."""
    return status in ("draft", "validated")


def stage_and_validate(api, title, ops):
    """The shared "Create, then Validate" pair every present-state module
    in this collection uses — the same two-call sequence
    contrib/terraform-provider-vnprox's Create/Update methods make, exposed
    once here rather than duplicated per module. Returns the changeset dict
    (validated, when validation succeeded) and a list of blocking (severity
    == "error") finding dicts, if any — mirroring
    resource_bridge.go's applyChangesetResult/stageBlockingFindingsWarning
    split. A validate-call transport/daemon error is not raised: the
    already-staged draft is kept either way (Create succeeded at staging,
    which is this collection's actual contract, exactly as the Terraform
    provider's own comment on this point states) — it is surfaced back to
    the caller as validate_warning instead.
    """
    cs = api.create_changeset(title, ops)
    validate_warning = None
    try:
        cs = api.validate_changeset(cs["id"])
    except VnproxAPIError as e:
        validate_warning = str(e)

    blocking = [
        f for f in cs.get("findings") or [] if f.get("severity") == "error"
    ]
    return cs, blocking, validate_warning
