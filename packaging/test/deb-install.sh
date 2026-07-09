#!/usr/bin/env bash
# Container test for the vnprox .deb: install / apt remove / apt purge
# semantics, run inside a throwaway Debian 12 container.
#
# Verifies planning/tasks/phase-0.md#T-006 acceptance criteria 1-2:
#   1. `apt install ./dist/*.deb` succeeds; the binaries run.
#   2. `apt remove` keeps /etc/vnprox and /var/lib/vnprox; `apt purge`
#      removes both.
#
# Requires: `make deb` already run (a .deb in dist/), and podman.
# `systemctl start vnprox` itself is NOT exercised here: plain `debian:12`
# containers do not run systemd as PID 1, so that half of acceptance
# criterion 1 is out of scope for this script — see the report/README for
# what was checked instead (binary runs manually, `--version` works) and
# the manual `systemd-analyze verify` / directive review done separately.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="${VNPROX_TEST_IMAGE:-debian:12}"

DEB_FILE="$(ls "$REPO_ROOT"/dist/vnprox_*.deb 2>/dev/null | head -1 || true)"
if [ -z "$DEB_FILE" ]; then
	echo "no .deb found in $REPO_ROOT/dist — run 'make deb' first" >&2
	exit 1
fi
DEB_BASENAME="$(basename "$DEB_FILE")"

echo ">> testing $DEB_BASENAME in $IMAGE (podman --network=host; this sandbox's"
echo "   podman has no pasta/slirp4netns rootless networking, so plain bridge"
echo "   networking fails outright — --network=host is what let 'apt-get"
echo "   update' resolve deb.debian.org here at all)"

podman run --rm --network=host -v "$REPO_ROOT/dist:/dist:ro,Z" "$IMAGE" bash -eu -c '
set -o pipefail

echo "--- apt-get update ---"
apt-get update -qq

echo "--- apt install ./'"$DEB_BASENAME"' ---"
apt-get install -y -qq "/dist/'"$DEB_BASENAME"'"

echo "--- binaries run ---"
/usr/bin/vnproxd --version
/usr/bin/vnproxctl --version

echo "--- conffile + directories from postinst ---"
test -f /etc/vnprox/vnprox.toml
test -d /var/lib/vnprox
test -f /etc/vnprox/keys/session.key
KEY_PERMS="$(stat -c %a /etc/vnprox/keys/session.key)"
[ "$KEY_PERMS" = "600" ] || { echo "session.key perms = $KEY_PERMS, want 600" >&2; exit 1; }
test -f /lib/systemd/system/vnprox.service

echo "--- session key is idempotent across reinstall (upgrade path) ---"
KEY_BEFORE="$(sha256sum /etc/vnprox/keys/session.key | cut -d" " -f1)"
apt-get install -y -qq --reinstall "/dist/'"$DEB_BASENAME"'"
KEY_AFTER="$(sha256sum /etc/vnprox/keys/session.key | cut -d" " -f1)"
[ "$KEY_BEFORE" = "$KEY_AFTER" ] || { echo "session key changed across reinstall!" >&2; exit 1; }

echo "--- edit config to simulate a local admin change ---"
echo "# local edit, must survive apt remove" >> /etc/vnprox/vnprox.toml
echo "local marker" > /var/lib/vnprox/marker.txt

echo "--- apt remove keeps config+data ---"
apt-get remove -y -qq vnprox
test -f /etc/vnprox/vnprox.toml || { echo "FAIL: /etc/vnprox/vnprox.toml removed by apt remove" >&2; exit 1; }
test -f /var/lib/vnprox/marker.txt || { echo "FAIL: /var/lib/vnprox removed by apt remove" >&2; exit 1; }
grep -q "local edit" /etc/vnprox/vnprox.toml

echo "--- apt purge removes config+data ---"
apt-get purge -y -qq vnprox
[ ! -e /etc/vnprox ] || { echo "FAIL: /etc/vnprox still present after apt purge" >&2; exit 1; }
[ ! -e /var/lib/vnprox ] || { echo "FAIL: /var/lib/vnprox still present after apt purge" >&2; exit 1; }

echo "ALL CHECKS PASSED"
'
