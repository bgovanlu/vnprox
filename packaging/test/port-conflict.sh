#!/usr/bin/env bash
# Container test for install.sh's port-8007-conflict detection
# (planning/tasks/phase-0.md#T-006 acceptance criterion 4): a fake listener
# on 8007 must be detected and cause install.sh to fall back to an
# alternative port (8008 non-interactively, or whatever the operator types
# interactively).
#
# Requires: `make deb` already run (a .deb in dist/), and podman.
# Uses --network=host (this sandbox's rootless podman has no pasta/
# slirp4netns for normal bridge networking, and needs real internet access
# for apt-get install netcat/reinstall cycles) — the fake listener binds to
# 127.0.0.1:8007 only, for the container's lifetime, and is torn down with
# the container.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="${VNPROX_TEST_IMAGE:-debian:12}"

# Port preflight (T-1807-bug-02). This test runs --network=host and binds 8007
# itself as a fake listener — that occupied port *is* the test subject — then
# expects install.sh to offer 8008 and honour a typed 8009. All three are host
# binds, and all three are registered in testdata/dev-ports.tsv. If any is
# already held (by `make dev`, the e2e suite's own stacks, or a leftover
# process from a dead session), this test asserts against the wrong fallback
# and fails looking like an install.sh defect.
. "$(dirname "${BASH_SOURCE[0]}")/lib/ports.sh"
ports_require_free 8007 8008 8009 || { echo "port preflight failed — this test needs a quiet machine (T-1807-bug-01); see 'make ports'" >&2; exit 1; }

DEB_FILE="$(ls "$REPO_ROOT"/dist/vnprox_*.deb 2>/dev/null | head -1 || true)"
if [ -z "$DEB_FILE" ]; then
	echo "no .deb found in $REPO_ROOT/dist — run 'make deb' first" >&2
	exit 1
fi
DEB_BASENAME="$(basename "$DEB_FILE")"

echo ">> testing install.sh port-conflict detection in $IMAGE"

podman run --rm --network=host \
	-v "$REPO_ROOT/dist:/dist:ro,Z" \
	-v "$REPO_ROOT/packaging:/packaging:ro,Z" \
	"$IMAGE" bash -euc '
set -o pipefail
apt-get update -qq
apt-get install -y -qq netcat-traditional >/dev/null

echo "=== no conflict: resolves to the default port 8007 ==="
OUT="$(bash /packaging/install.sh --skip-pve-check --yes --offline "/dist/'"$DEB_BASENAME"'" 2>&1)"
echo "$OUT" | grep -q "resolved listen port: 8007" || { echo "FAIL: expected port 8007 with no conflict"; echo "$OUT"; exit 1; }
dpkg -r vnprox >/dev/null 2>&1 || true

echo "=== conflict present (fake listener on 8007): non-interactive falls back to 8008 ==="
(nc -l -p 8007 >/dev/null 2>&1 &)
sleep 1
OUT="$(bash /packaging/install.sh --skip-pve-check --yes --offline "/dist/'"$DEB_BASENAME"'" 2>&1)"
echo "$OUT" | grep -qi "something is already listening on port 8007" || { echo "FAIL: conflict not detected"; echo "$OUT"; exit 1; }
echo "$OUT" | grep -q "resolved listen port: 8008" || { echo "FAIL: expected fallback to 8008"; echo "$OUT"; exit 1; }
dpkg -r vnprox >/dev/null 2>&1 || true

echo "=== conflict present: interactive prompt accepts an operator-typed port ==="
OUT="$(echo "8009" | bash /packaging/install.sh --skip-pve-check --offline "/dist/'"$DEB_BASENAME"'" 2>&1)"
echo "$OUT" | grep -qi "something is already listening on port 8007" || { echo "FAIL: conflict not detected"; echo "$OUT"; exit 1; }
echo "$OUT" | grep -q "resolved listen port: 8009" || { echo "FAIL: expected the typed port 8009"; echo "$OUT"; exit 1; }

echo "ALL CHECKS PASSED"
'
