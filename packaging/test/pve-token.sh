#!/usr/bin/env bash
# Container test for vnprox-setup's PVE API token provisioning
# (packaging/bin/vnprox-setup step 5) — T-606 acceptance criterion 5:
# "Token/role creation idempotent (re-run installer → no duplicates, no
# errors)".
#
# Real `pveum` only exists on a Proxmox VE node (CLAUDE.md: no live cluster
# available here), so this test drives vnprox-setup against
# packaging/test/fakepveum, a stub that reproduces just enough of pveum's
# documented behavior (role/user/token creation, "already exists" errors)
# for vnprox-setup's own idempotency logic (pve_try_create) to be exercised
# for real — this test asserts vnprox-setup's bash, not fakepveum's
# fidelity to a real PVE node, which needs hardware validation.
#
# Requires: podman.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="${VNPROX_TEST_IMAGE:-debian:12}"

echo ">> testing vnprox-setup PVE token provisioning (fakepveum) in $IMAGE"

podman run --rm --network=host \
	-v "$REPO_ROOT/packaging:/packaging:ro,Z" \
	"$IMAGE" bash -euo pipefail -c '
set -o pipefail
install -d -m 0755 /usr/local/fakebin
install -m 0755 /packaging/test/fakepveum /usr/local/fakebin/pveum
export PATH="/usr/local/fakebin:$PATH"
export FAKEPVEUM_STATE=/tmp/fakepveum-state
mkdir -p "$FAKEPVEUM_STATE"

# vnprox-setup writes /etc/vnprox/vnprox.toml as a fresh file since no
# package conffile is present in this bare container (expected warning,
# not the thing under test); --answers keeps port/lldp non-interactive and
# skips the lldpd apt-get install (no-lldp) to keep this test fast and
# focused on step 5.
cat >/tmp/answers <<EOF
ANSWER_PORT=8007
ANSWER_WITH_LLDP=no
EOF

echo "=== run 1: fresh state — role/user/token all created ==="
OUT1="$(bash /packaging/bin/vnprox-setup --answers /tmp/answers 2>&1)"
echo "$OUT1"
echo "$OUT1" | grep -q "role VnproxAuditor" || { echo "FAIL: role not created"; exit 1; }
echo "$OUT1" | grep -q "user vnprox@pve" || { echo "FAIL: user not created"; exit 1; }
echo "$OUT1" | grep -q "granted VnproxAuditor on / to vnprox@pve" || { echo "FAIL: acl not granted"; exit 1; }
echo "$OUT1" | grep -q "stored token secret at /etc/vnprox/keys/pve-token" || { echo "FAIL: token not stored"; exit 1; }
test -s /etc/vnprox/keys/pve-token
grep -q "^vnprox@pve!daemon=" /etc/vnprox/keys/pve-token
PERMS="$(stat -c %a /etc/vnprox/keys/pve-token)"
[ "$PERMS" = "600" ] || { echo "FAIL: pve-token perms = $PERMS, want 600"; exit 1; }
test -e "$FAKEPVEUM_STATE/roles/VnproxAuditor"
test -e "$FAKEPVEUM_STATE/users/vnprox@pve"
test -e "$FAKEPVEUM_STATE/tokens/vnprox@pve!daemon"
TOKEN_BEFORE="$(cat /etc/vnprox/keys/pve-token)"

echo "=== run 2: token file already present — fully skipped, no duplicates, no errors ==="
OUT2="$(bash /packaging/bin/vnprox-setup --answers /tmp/answers 2>&1)"
echo "$OUT2"
echo "$OUT2" | grep -qi "PVE API token file already present" || { echo "FAIL: re-run did not report the file as already present"; exit 1; }
[ "$(cat /etc/vnprox/keys/pve-token)" = "$TOKEN_BEFORE" ] || { echo "FAIL: token file changed on re-run"; exit 1; }

echo "=== run 3: local token file lost, but PVE-side role/user/token still exist ==="
rm -f /etc/vnprox/keys/pve-token
OUT3="$(bash /packaging/bin/vnprox-setup --answers /tmp/answers 2>&1)"
echo "$OUT3"
echo "$OUT3" | grep -q "role VnproxAuditor .*(already existed)" || { echo "FAIL: role re-creation not tolerated as already-existed"; exit 1; }
echo "$OUT3" | grep -q "user vnprox@pve (already existed)" || { echo "FAIL: user re-creation not tolerated as already-existed"; exit 1; }
echo "$OUT3" | grep -qi "secret cannot be recovered" || { echo "FAIL: missing the unrecoverable-secret warning"; exit 1; }
[ ! -e /etc/vnprox/keys/pve-token ] || { echo "FAIL: a token file reappeared without a real secret"; exit 1; }
# No duplicate marker files: exactly one role, one user, one token, still.
[ "$(find "$FAKEPVEUM_STATE/roles" -type f | wc -l)" = "1" ] || { echo "FAIL: duplicate role"; exit 1; }
[ "$(find "$FAKEPVEUM_STATE/users" -type f | wc -l)" = "1" ] || { echo "FAIL: duplicate user"; exit 1; }
[ "$(find "$FAKEPVEUM_STATE/tokens" -type f | wc -l)" = "1" ] || { echo "FAIL: duplicate token"; exit 1; }

echo "ALL CHECKS PASSED"
'
