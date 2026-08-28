# -*- coding: utf-8 -*-
# SPDX-License-Identifier: Apache-2.0
"""harness.py — builds and starts the REAL cmd/pvemock and cmd/vnproxd
binaries from the main vnprox module as subprocesses, then bootstraps a
T-1104 bearer token against the real daemon over HTTP, exactly the
discipline contrib/terraform-provider-vnprox/internal/provider/harness_test.go
uses for its own acceptance suite (see that file's doc comment) and this
card's own instruction: "Tests that run against the real cmd/pvemock +
vnproxd binaries... not against a hand-rolled fake."

This file has no test functions of its own — it is imported by
test_modules.py / test_inventory_plugin.py, which are the actual pytest
entry points. Kept separate so both test files share exactly one running
stack per test session rather than each building/starting their own.
"""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import json
import os
import shutil
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from http.cookiejar import CookieJar


class AcceptanceStack(object):
    """Owns one pvemock + vnproxd process pair, and the bearer token minted
    against it. Call start(), use .base_url/.token, call stop() when done
    (or use as a context manager)."""

    def __init__(self):
        self.repo_root = find_repo_root()
        self.tmpdir = tempfile.mkdtemp(prefix="vnprox-ansible-acc-")
        self.pvemock_proc = None
        self.vnproxd_proc = None
        self.base_url = None
        self.token = None

    def __enter__(self):
        self.start()
        return self

    def __exit__(self, exc_type, exc, tb):
        self.stop()

    def start(self):
        pvemock_bin = os.path.join(self.tmpdir, "pvemock")
        vnproxd_bin = os.path.join(self.tmpdir, "vnproxd")
        build_binary(self.repo_root, pvemock_bin, "./cmd/pvemock")
        build_binary(self.repo_root, vnproxd_bin, "./cmd/vnproxd")

        pvemock_port = reserve_port()
        self.pvemock_proc = subprocess.Popen(
            [
                pvemock_bin,
                "--addr", ":%d" % pvemock_port,
                "--fixture", os.path.join(self.repo_root, "testdata", "clusters", "single-node.yaml"),
            ],
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        )
        wait_tcp_up("127.0.0.1", pvemock_port, 10)

        vnproxd_port = reserve_port()
        cfg_path = rewrite_dev_config(self.repo_root, self.tmpdir, vnproxd_port, pvemock_port)
        self.vnproxd_proc = subprocess.Popen(
            [vnproxd_bin, "--config", cfg_path],
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        )

        self.base_url = "https://127.0.0.1:%d/api/v1" % vnproxd_port
        wait_healthy(self.base_url + "/health", 15)
        self.token = mint_bearer_token(self.base_url)

    def stop(self):
        for proc in (self.vnproxd_proc, self.pvemock_proc):
            if proc is not None and proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
        shutil.rmtree(self.tmpdir, ignore_errors=True)


def find_repo_root():
    """Walks upward from this file's own directory until it finds the main
    vnprox module root — identified by cmd/vnproxd, cmd/pvemock,
    testdata/dev.toml, mirroring harness_test.go's findRepoRoot exactly
    (same three markers, same fail-closed behavior)."""
    d = os.path.dirname(os.path.abspath(__file__))
    for _ in range(8):
        if (
            os.path.isdir(os.path.join(d, "cmd", "vnproxd"))
            and os.path.isdir(os.path.join(d, "cmd", "pvemock"))
            and os.path.isfile(os.path.join(d, "testdata", "dev.toml"))
        ):
            return d
        parent = os.path.dirname(d)
        if parent == d:
            break
        d = parent
    raise RuntimeError(
        "harness: could not find the main vnprox module root walking up from %s"
        % os.path.dirname(os.path.abspath(__file__))
    )


def reserve_port():
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def build_binary(repo_root, out, pkg):
    subprocess.run(
        ["go", "build", "-o", out, pkg], cwd=repo_root, check=True,
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
    )


def wait_tcp_up(host, port, timeout):
    deadline = time.time() + timeout
    last_err = None
    while time.time() < deadline:
        try:
            with socket.create_connection((host, port), timeout=0.5):
                return
        except OSError as e:
            last_err = e
            time.sleep(0.1)
    raise RuntimeError("harness: %s:%d never accepted a TCP connection: %s" % (host, port, last_err))


def _insecure_ssl_context():
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    return ctx


def wait_healthy(url, timeout):
    deadline = time.time() + timeout
    last_err = None
    ctx = _insecure_ssl_context()
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2, context=ctx) as resp:
                if resp.getcode() == 200:
                    return
        except (urllib.error.URLError, ConnectionError, OSError) as e:
            last_err = e
        time.sleep(0.1)
    raise RuntimeError("harness: %s never became healthy: %s" % (url, last_err))


def rewrite_dev_config(repo_root, tmpdir, vnproxd_port, pvemock_port):
    """A Python re-implementation of harness_test.go's rewriteDevConfig —
    same replacement key set, plus [pve] api_url pointed at the mock's own
    ephemeral port. Duplicated here rather than shared with the Go harness
    for the same reason contrib/terraform-provider-vnprox's own copy is
    duplicated: this is a separate ecosystem (Python, not Go) that cannot
    import that file's helper across the language boundary."""
    with open(os.path.join(repo_root, "testdata", "dev.toml")) as f:
        lines = f.read().splitlines()

    replacements = {
        "listen": "127.0.0.1:%d" % vnproxd_port,
        "tls_cert": os.path.join(repo_root, "testdata", "certs", "dev-cert.pem"),
        "tls_key": os.path.join(repo_root, "testdata", "certs", "dev-key.pem"),
        "db_path": os.path.join(tmpdir, "vnprox.db"),
        "session_key_file": os.path.join(tmpdir, "session.key"),
        "protected_path": os.path.join(tmpdir, "protected.json"),
        "dev_interfaces_dir": os.path.join(tmpdir, "dev-host"),
        "secret_path": os.path.join(tmpdir, "cluster.secret"),
        "key_file": os.path.join(tmpdir, "metrics.key"),
        "signing_key_file": os.path.join(tmpdir, "blueprint-signing.key"),
        "trusted_signers_dir": os.path.join(tmpdir, "trusted-signers"),
        "api_url": "http://127.0.0.1:%d" % pvemock_port,
    }
    replaced = set()
    out_lines = []
    for line in lines:
        stripped = line.strip()
        matched = False
        for key, value in replacements.items():
            if stripped.startswith(key + " ") or stripped.startswith(key + "="):
                out_lines.append('%s = %s' % (key, json.dumps(value)))
                replaced.add(key)
                matched = True
                break
        if not matched:
            out_lines.append(line)

    missing = set(replacements) - replaced
    if missing:
        raise RuntimeError(
            "harness: testdata/dev.toml has no %s key(s) to rewrite; update this harness "
            "to match its current shape" % sorted(missing)
        )

    cfg_path = os.path.join(tmpdir, "dev.toml")
    with open(cfg_path, "w") as f:
        f.write("\n".join(out_lines) + "\n")
    return cfg_path


def mint_bearer_token(base_url):
    """The ordinary "log in, mint an automation token" ceremony — the only
    place in this test suite that touches a username/password; the
    collection's own modules/plugin never do. Mirrors
    harness_test.go's mintBearerToken."""
    ctx = _insecure_ssl_context()
    jar = CookieJar()
    opener = urllib.request.build_opener(
        urllib.request.HTTPCookieProcessor(jar), urllib.request.HTTPSHandler(context=ctx),
    )

    login_body = json.dumps({"username": "root@pam", "password": "vnprox-mock", "realm": "pam"}).encode()
    req = urllib.request.Request(
        base_url + "/auth/login", data=login_body, headers={"Content-Type": "application/json"},
    )
    with opener.open(req, timeout=10) as resp:
        if resp.getcode() != 200:
            raise RuntimeError("harness: POST /auth/login: HTTP %d" % resp.getcode())

    csrf = None
    for cookie in jar:
        if cookie.name == "vnprox_csrf":
            csrf = cookie.value
    if not csrf:
        raise RuntimeError("harness: POST /auth/login did not set a vnprox_csrf cookie")

    token_body = json.dumps(
        {"name": "ansible-collection-acceptance-test", "scopes": ["netRead", "netWrite"]}
    ).encode()
    req = urllib.request.Request(
        base_url + "/tokens", data=token_body,
        headers={"Content-Type": "application/json", "X-VNPROX-CSRF": csrf},
    )
    with opener.open(req, timeout=10) as resp:
        if resp.getcode() != 201:
            raise RuntimeError("harness: POST /tokens: HTTP %d" % resp.getcode())
        out = json.loads(resp.read())
    token = out.get("token")
    if not token:
        raise RuntimeError("harness: POST /tokens returned an empty token")
    return token
