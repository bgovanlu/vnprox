#!/usr/bin/env bash
# Container test for T-1807 acceptance criterion 4's "the service comes back
# up" half of the package-upgrade guarantee, which upgrade.sh's own comment
# explicitly flags as out of scope ("systemctl start vnprox itself is NOT
# exercised here: plain debian:12 containers do not run systemd as PID 1")
# and deb-install.sh's comment flags the same way. This script closes that
# gap for real, rather than asserting around it: it builds a systemd-capable
# variant of the target image, boots it with systemd as PID 1
# (`podman run --systemd=always ... /sbin/init`), and drives the actual
# `apt install` upgrade path against a REAL running `vnprox.service` —
# proving with `systemctl is-active` (not a proxy like "the binary didn't
# crash when invoked manually") that postinst's "restart the service on
# upgrade when it was running" logic (packaging/debian/postinst) really
# leaves the daemon serving afterward, still bound to the same port it was
# serving on before.
#
# Port choice: this test does NOT use vnprox's shipped default port (8007).
# vnprox's own test tooling assumes exclusive use of the machine in several
# places — this repo's e2e specs alone claim the whole "N8006/N8007" family
# (8006-8008, 18006-18008, 28006-28007, 38006-38007, 48006-48007,
# 58006-58007 — grep web/e2e/*.spec.ts and web/playwright.config.ts) for
# their own concurrently-running vnproxd/pvemock/k8smock fixtures, and
# `make dev` binds 8007 too. This script's own container additionally runs
# with `--network=host` (required: this sandbox's rootless podman has no
# pasta/slirp4netns for `podman run`'s own network stack, so a normal bridge
# network can't reach deb.debian.org for apt-get — see port-conflict.sh's
# identical note), which means this container's listening sockets land
# directly in the HOST's port namespace, not an isolated one. Binding 8007
# here would collide with any of the above running concurrently — confirmed
# the hard way while building this script: a concurrent Playwright run's own
# 127.0.0.1:8007 vnproxd made this test's vnproxd fail to bind at all.
# TEST_PORT below is chosen well outside the entire "N8006/N8007" family
# (not just a different member of it) so a future fixture pair following the
# same naming pattern can't collide with it either; the preflight check
# below fails fast and loudly if it is ever occupied anyway, rather than
# producing a confusing failure deeper in the script.
#
# Separately (not a port collision, found once the above was fixed):
# vnprox.service is Type=simple, so systemd marks it active(running) the
# instant the process forks/execs — well before vnproxd has actually
# finished its own startup work (migrations, cluster-secret generation) and
# bound its HTTPS listener. Treating "systemctl is-active" as proof the
# listener is already up is a race; see the dedicated poll loops around each
# `ss` check below, separate from the is-active poll loops.
#
# Preserving `/etc/vnprox/keys` (the session encryption key, in particular)
# across the upgrade is the single most important assertion in this script:
# losing it makes every AES-256-GCM-sealed credential already in the store
# (PVE tickets, WireGuard private keys, cluster/OIDC/switch credentials —
# see docs/security.md) permanently unreadable, which is a strictly worse
# outcome for an operator than the upgrade simply failing loudly. It is
# checked BOTH by content hash (the key material itself must not change)
# and by directory/file permissions (0700 root:root / 0600 root:root per
# postinst) — a permissions regression would silently widen the credential
# attack surface without deleting anything, so a content-hash check alone
# would miss it.
#
# vnproxd starts and binds its HTTPS listener even with no reachable PVE API
# (it logs the collector-init failure and keeps serving; confirmed empirically
# building this test) as long as it has a resolvable TLS certificate/key —
# this script supplies a throwaway self-signed one via `[server] tls_cert` /
# `tls_key` overrides (the shipped default reuses PVE's own node certificate,
# which does not exist in a bare container) so the real systemd unit can
# actually reach the "active (running)" state rather than crash-looping on a
# missing cert, which would make this test indistinguishable from a real
# upgrade regression.
#
# Same "no real previous tag" honesty note as upgrade.sh: this repository has
# cut no release tags, so "old" and "new" are the current source tree built
# under two synthetic version strings — this exercises real dpkg upgrade
# mechanics (the `upgrade` maintainer-script argument) and postinst's restart
# logic for real, even though the schema itself does not actually change
# between them (forward SQL migration itself is exhaustively unit-tested per
# prior schema version in internal/store/migrate_fromeach_test.go, not
# re-proven here).
#
# Requires: podman, make. Needs a rootful (or rootless-with-cgroupsv2)
# podman that can run systemd as PID 1 — confirmed working against this
# repo's own dev sandbox (podman 5.x, rootless, cgroups v2) via
# `podman run --systemd=always`.

set -euo pipefail

PROG=$(basename "$0")
log() { printf '>> %s: %s\n' "$PROG" "$*" >&2; }
die() {
	printf '%s: error: %s\n' "$PROG" "$*" >&2
	exit 1
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BASE_IMAGE="${VNPROX_TEST_IMAGE:-debian:12}"
OLD_VERSION="${VNPROX_TEST_OLD_VERSION:-0.9.0}"
NEW_VERSION="${VNPROX_TEST_NEW_VERSION:-1.0.0}"
# Deliberately outside the whole "N8006/N8007" family (8006-8008,
# 18006-18008, 28006-28007, 38006-38007, 48006-48007, 58006-58007) — see the
# port-choice note above.
TEST_PORT="${VNPROX_TEST_SERVICE_PORT:-61007}"
CONTAINER_NAME="vnprox-upgrade-service-test-$$"
SYSTEMD_IMAGE_TAG="vnprox-upgrade-service-test-image-$$"

SCRATCH="$(mktemp -d)"
cleanup() {
	podman rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
	podman rmi "$SYSTEMD_IMAGE_TAG" >/dev/null 2>&1 || true
	rm -rf "$SCRATCH"
}
trap cleanup EXIT

# Fail fast and clearly (not as a confusing service-start failure — see the
# port-choice note above) if something on this machine already holds
# TEST_PORT. Best-effort: ss may not be installed on every dev host, so a
# missing ss is not itself fatal.
if command -v ss >/dev/null 2>&1 && ss -tln 2>/dev/null | grep -q ":${TEST_PORT} "; then
	die "port ${TEST_PORT} is already in use on this host — set VNPROX_TEST_SERVICE_PORT to a free port and retry (this test uses --network=host, so container and host ports are the same namespace; see this script's port-choice comment)"
fi

log "building $OLD_VERSION and $NEW_VERSION .debs from the current source tree into $SCRATCH"
make -C "$REPO_ROOT/packaging" deb DIST_DIR="$SCRATCH/debs" VERSION="$OLD_VERSION" >/dev/null
make -C "$REPO_ROOT/packaging" deb DIST_DIR="$SCRATCH/debs" VERSION="$NEW_VERSION" >/dev/null

OLD_DEB="$SCRATCH/debs/vnprox_${OLD_VERSION}_amd64.deb"
NEW_DEB="$SCRATCH/debs/vnprox_${NEW_VERSION}_amd64.deb"
[ -f "$OLD_DEB" ] || die "expected $OLD_DEB to exist"
[ -f "$NEW_DEB" ] || die "expected $NEW_DEB to exist"

log "building a systemd-capable variant of $BASE_IMAGE ($SYSTEMD_IMAGE_TAG)"
cat >"$SCRATCH/Containerfile" <<EOF
FROM $BASE_IMAGE
RUN apt-get update -qq && apt-get install -y -qq systemd systemd-sysv openssl >/dev/null && apt-get clean
CMD ["/sbin/init"]
EOF
podman build -q -t "$SYSTEMD_IMAGE_TAG" -f "$SCRATCH/Containerfile" "$SCRATCH" >/dev/null

log "booting $SYSTEMD_IMAGE_TAG with systemd as PID 1"
podman run -d --name "$CONTAINER_NAME" --systemd=always --network=host "$SYSTEMD_IMAGE_TAG" >/dev/null

# Give systemd a moment to reach a steady state before driving anything.
for _ in 1 2 3 4 5 6 7 8 9 10; do
	state="$(podman exec "$CONTAINER_NAME" systemctl is-system-running 2>/dev/null || true)"
	case "$state" in
	running | degraded) break ;;
	esac
	sleep 1
done

podman cp "$OLD_DEB" "$CONTAINER_NAME:/tmp/old.deb"
podman cp "$NEW_DEB" "$CONTAINER_NAME:/tmp/new.deb"

podman exec "$CONTAINER_NAME" bash -euo pipefail -c '
set -o pipefail
apt-get update -qq

echo "--- install '"$OLD_VERSION"' ---"
apt-get install -y -qq /tmp/old.deb
/usr/bin/vnproxd --version | grep -q "'"$OLD_VERSION"'"

echo "--- give vnproxd a resolvable TLS cert (the shipped default reuses PVE'"'"'s own node cert, absent in this bare container) so the real systemd unit can actually reach active(running) instead of crash-looping ---"
mkdir -p /etc/vnprox/tls
openssl req -x509 -newkey rsa:2048 -nodes -keyout /etc/vnprox/tls/vnprox.key -out /etc/vnprox/tls/vnprox.pem -days 1 -subj "/CN=vnprox-test" 2>/dev/null
sed -i "/^\[server\]/a tls_cert = \"/etc/vnprox/tls/vnprox.pem\"\ntls_key = \"/etc/vnprox/tls/vnprox.key\"" /etc/vnprox/vnprox.toml

echo "--- rebind off the shared default port 8007 (this sandbox runs several other vnproxd/pvemock fixtures concurrently on 8006-8008/18006-58007; --network=host means this container'"'"'s listener lands in the HOST port namespace, not an isolated one) ---"
sed -i "s/^listen = \"0.0.0.0:8007\"/listen = \"0.0.0.0:'"$TEST_PORT"'\"/" /etc/vnprox/vnprox.toml
grep -q "^listen = \"0.0.0.0:'"$TEST_PORT"'\"" /etc/vnprox/vnprox.toml || { echo "FAIL: could not rebind vnprox.toml'"'"'s listen address to '"$TEST_PORT"'"; cat /etc/vnprox/vnprox.toml; exit 1; }

echo "--- seed app-owned data that must survive the upgrade ---"
echo "# local admin edit, must survive upgrade" >> /etc/vnprox/vnprox.toml
echo "pre-upgrade marker" > /var/lib/vnprox/marker.txt
KEYS_DIR_PERMS_BEFORE="$(stat -c %a /etc/vnprox/keys)"
SESSION_KEY_PERMS_BEFORE="$(stat -c %a /etc/vnprox/keys/session.key)"
SESSION_KEY_BEFORE="$(sha256sum /etc/vnprox/keys/session.key | cut -d" " -f1)"

echo "--- start the REAL systemd service (not a manual binary invocation) and confirm it reaches active(running) ---"
systemctl enable --now vnprox
for _ in 1 2 3 4 5 6 7 8 9 10; do
	systemctl is-active --quiet vnprox && break
	sleep 1
done
systemctl is-active --quiet vnprox || { echo "FAIL: vnprox.service did not become active on '"$OLD_VERSION"'"; journalctl -u vnprox --no-pager | tail -30; exit 1; }
# vnprox.service is Type=simple: systemd marks it active(running) the
# instant the process forks/execs, well before vnproxd has finished its own
# startup sequence (migrations, cluster-secret generation, etc.) and bound
# its HTTPS listener — so the listener needs its own poll, separate from
# is-active above, rather than assuming "active" already means "ready".
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
	ss -tln 2>/dev/null | grep -q ":'"$TEST_PORT"'" && break
	sleep 1
done
ss -tln 2>/dev/null | grep -q ":'"$TEST_PORT"'" || { echo "FAIL: vnprox not listening on :'"$TEST_PORT"' on '"$OLD_VERSION"'"; echo "--- systemctl status ---"; systemctl status vnprox --no-pager || true; echo "--- journalctl ---"; journalctl -u vnprox --no-pager | tail -40; echo "--- ss -tlnp ---"; ss -tlnp 2>&1 || true; echo "--- vnprox.toml ---"; cat /etc/vnprox/vnprox.toml; exit 1; }
PID_BEFORE="$(systemctl show -p MainPID --value vnprox)"

echo "--- upgrade to '"$NEW_VERSION"' (real dpkg upgrade action, not a reinstall) ---"
apt-get install -y -qq /tmp/new.deb
/usr/bin/vnproxd --version | grep -q "'"$NEW_VERSION"'"

echo "--- the service comes back up on its own (postinst restarts it because it was running) ---"
for _ in 1 2 3 4 5 6 7 8 9 10; do
	systemctl is-active --quiet vnprox && break
	sleep 1
done
systemctl is-active --quiet vnprox || { echo "FAIL: vnprox.service is not active after upgrade"; journalctl -u vnprox --no-pager | tail -30; exit 1; }
PID_AFTER="$(systemctl show -p MainPID --value vnprox)"
[ "$PID_AFTER" != "0" ] || { echo "FAIL: vnprox.service main PID is 0 after upgrade (not actually running)"; exit 1; }
[ "$PID_AFTER" != "$PID_BEFORE" ] || { echo "FAIL: vnprox.service PID unchanged across upgrade — the process was never actually restarted onto the new binary"; exit 1; }
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
	ss -tln 2>/dev/null | grep -q ":'"$TEST_PORT"'" && break
	sleep 1
done
ss -tln 2>/dev/null | grep -q ":'"$TEST_PORT"'" || { echo "FAIL: vnprox not listening on :'"$TEST_PORT"' after upgrade"; echo "--- systemctl status ---"; systemctl status vnprox --no-pager || true; echo "--- journalctl ---"; journalctl -u vnprox --no-pager | tail -40; exit 1; }

echo "--- app-owned data + /etc/vnprox/keys survived, byte-for-byte and permission-for-permission ---"
test -f /var/lib/vnprox/marker.txt || { echo "FAIL: /var/lib/vnprox/marker.txt lost across upgrade"; exit 1; }
grep -q "pre-upgrade marker" /var/lib/vnprox/marker.txt
grep -q "local admin edit" /etc/vnprox/vnprox.toml || { echo "FAIL: local config edit lost across upgrade"; exit 1; }
KEYS_DIR_PERMS_AFTER="$(stat -c %a /etc/vnprox/keys)"
SESSION_KEY_PERMS_AFTER="$(stat -c %a /etc/vnprox/keys/session.key)"
SESSION_KEY_AFTER="$(sha256sum /etc/vnprox/keys/session.key | cut -d" " -f1)"
[ "$SESSION_KEY_BEFORE" = "$SESSION_KEY_AFTER" ] || { echo "FAIL: session key CONTENT changed across upgrade — every sealed credential in the store is now unreadable"; exit 1; }
[ "$KEYS_DIR_PERMS_BEFORE" = "$KEYS_DIR_PERMS_AFTER" ] || { echo "FAIL: /etc/vnprox/keys permissions changed across upgrade ($KEYS_DIR_PERMS_BEFORE -> $KEYS_DIR_PERMS_AFTER)"; exit 1; }
[ "$KEYS_DIR_PERMS_AFTER" = "700" ] || { echo "FAIL: /etc/vnprox/keys perms = $KEYS_DIR_PERMS_AFTER, want 700"; exit 1; }
[ "$SESSION_KEY_PERMS_BEFORE" = "$SESSION_KEY_PERMS_AFTER" ] || { echo "FAIL: session.key permissions changed across upgrade ($SESSION_KEY_PERMS_BEFORE -> $SESSION_KEY_PERMS_AFTER)"; exit 1; }
[ "$SESSION_KEY_PERMS_AFTER" = "600" ] || { echo "FAIL: session.key perms = $SESSION_KEY_PERMS_AFTER, want 600"; exit 1; }

echo "--- vnproxctl (daemon-independent, T-206) opens the store post-upgrade cleanly ---"
/usr/bin/vnproxctl snapshots list --config /etc/vnprox/vnprox.toml
test -f /var/lib/vnprox/vnprox.db || { echo "FAIL: vnprox.db was not created/migrated"; exit 1; }

systemctl stop vnprox

echo "ALL CHECKS PASSED"
'
