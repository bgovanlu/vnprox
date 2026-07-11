#!/usr/bin/env bash
# Container test for T-606 acceptance criterion 1's "upgrade from previous
# tag → still serves with data intact" half.
#
# This repository has cut no release tags yet (`git tag -l` is empty), so
# there is no real "previous tag" .deb to upgrade from — this test is
# honest about that rather than faking one: it builds the CURRENT source
# tree twice, under two different synthetic version strings
# (VNPROX_TEST_OLD_VERSION / VNPROX_TEST_NEW_VERSION), and has dpkg upgrade
# from the "old" one to the "new" one. This exercises the real packaging
# upgrade mechanics dpkg only runs on a genuine version bump (the `upgrade`
# maintainer-script argument, not `configure` alone) — session-key/config
# idempotency across that path, and that app data placed before the
# upgrade survives it untouched. Forward SQLite schema migration itself
# (the other thing "upgrade" implies) is exhaustively unit-tested per prior
# schema version in internal/store/migrate_fromeach_test.go, not
# re-proven here at the shell level; once this project cuts real tags,
# swap this script's "old" build for `git worktree`-checking-out the
# latest existing tag instead of a synthetic version bump, and this test's
# assertions keep meaning what they say.
#
# Requires: podman, make (this script builds two .debs itself, into a
# scratch directory — it does not touch dist/).

set -euo pipefail

PROG=$(basename "$0")
log() { printf '>> %s: %s\n' "$PROG" "$*" >&2; }
die() {
	printf '%s: error: %s\n' "$PROG" "$*" >&2
	exit 1
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="${VNPROX_TEST_IMAGE:-debian:12}"
OLD_VERSION="${VNPROX_TEST_OLD_VERSION:-0.9.0}"
NEW_VERSION="${VNPROX_TEST_NEW_VERSION:-1.0.0}"

SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

log "building $OLD_VERSION and $NEW_VERSION .debs from the current source tree into $SCRATCH"
make -C "$REPO_ROOT/packaging" deb DIST_DIR="$SCRATCH" VERSION="$OLD_VERSION" >/dev/null
make -C "$REPO_ROOT/packaging" deb DIST_DIR="$SCRATCH" VERSION="$NEW_VERSION" >/dev/null

OLD_DEB="$SCRATCH/vnprox_${OLD_VERSION}_amd64.deb"
NEW_DEB="$SCRATCH/vnprox_${NEW_VERSION}_amd64.deb"
[ -f "$OLD_DEB" ] || die "expected $OLD_DEB to exist"
[ -f "$NEW_DEB" ] || die "expected $NEW_DEB to exist"

echo ">> testing upgrade $OLD_VERSION -> $NEW_VERSION in $IMAGE"

podman run --rm --network=host -v "$SCRATCH:/debs:ro,Z" "$IMAGE" bash -euo pipefail -c '
set -o pipefail
apt-get update -qq

echo "--- install '"$OLD_VERSION"' ---"
apt-get install -y -qq "/debs/vnprox_'"$OLD_VERSION"'_amd64.deb"
/usr/bin/vnproxd --version | grep -q "'"$OLD_VERSION"'"

echo "--- seed app-owned data that must survive the upgrade ---"
echo "# local admin edit, must survive upgrade" >> /etc/vnprox/vnprox.toml
echo "pre-upgrade marker" > /var/lib/vnprox/marker.txt
SESSION_KEY_BEFORE="$(sha256sum /etc/vnprox/keys/session.key | cut -d" " -f1)"

echo "--- upgrade to '"$NEW_VERSION"' (real dpkg upgrade action, not a reinstall) ---"
apt-get install -y -qq "/debs/vnprox_'"$NEW_VERSION"'_amd64.deb"
/usr/bin/vnproxd --version | grep -q "'"$NEW_VERSION"'"

echo "--- data intact after upgrade ---"
test -f /var/lib/vnprox/marker.txt || { echo "FAIL: /var/lib/vnprox/marker.txt lost across upgrade"; exit 1; }
grep -q "pre-upgrade marker" /var/lib/vnprox/marker.txt
grep -q "local admin edit" /etc/vnprox/vnprox.toml || { echo "FAIL: local config edit lost across upgrade"; exit 1; }
SESSION_KEY_AFTER="$(sha256sum /etc/vnprox/keys/session.key | cut -d" " -f1)"
[ "$SESSION_KEY_BEFORE" = "$SESSION_KEY_AFTER" ] || { echo "FAIL: session key changed across upgrade"; exit 1; }

echo "--- vnproxctl (daemon-independent, T-206) opens the store post-upgrade cleanly ---"
# store.Open (called by vnproxctl snapshots/rollback-now, and by vnproxd on
# startup) runs the real forward-only migration on whatever schema version
# it finds — exhaustively unit-tested per prior version in
# internal/store/migrate_fromeach_test.go. This just confirms the
# packaged vnproxctl binary can open+migrate the app database at all
# post-upgrade, without needing a PVE certificate/token (which vnproxd
# itself would require to bind its HTTPS listener, out of scope for a
# generic non-PVE container).
/usr/bin/vnproxctl snapshots list --config /etc/vnprox/vnprox.toml
test -f /var/lib/vnprox/vnprox.db || { echo "FAIL: vnprox.db was not created/migrated"; exit 1; }

echo "ALL CHECKS PASSED"
'
