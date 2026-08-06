#!/usr/bin/env bash
# Container test for T-606 acceptance criterion 4: "--answers file produces
# identical results to the interactive flow (diff of resulting system
# state)". Runs vnprox-setup twice in separate containers — once answering
# the interactive prompts via stdin, once via --answers — with the same
# effective choices (port 8007, no lldpd — lldpd is skipped in both to keep
# this test independent of container network/apt-get flakiness, which is
# irrelevant to what's being compared here: config/answers-file plumbing,
# not the lldpd install step itself, which port-conflict.sh /
# deb-install.sh's own runs already exercise), and diffs the resulting
# on-disk state.
#
# Requires: podman.

set -euo pipefail

PROG=$(basename "$0")
die() {
	printf '%s: error: %s\n' "$PROG" "$*" >&2
	exit 1
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="${VNPROX_TEST_IMAGE:-debian:12}"

# Port preflight (T-1807-bug-02). Runs --network=host. This test's interactive
# branch depends on 8007 being free — see its own comment below: "No port
# conflict in a clean container, so resolve_port() never prompts". A busy host
# 8007 makes that false, resolve_port() prompts, and the two branches stop
# being comparable — a parity failure caused entirely by the machine.
. "$(dirname "${BASH_SOURCE[0]}")/lib/ports.sh"
ports_require_free 8007 || die "port preflight failed — this test needs a quiet machine (T-1807-bug-01); see 'make ports'"

echo ">> testing vnprox-setup --answers vs. interactive parity in $IMAGE"

run_setup() {
	mode="$1" # "interactive" or "answers"
	podman run --rm --network=host \
		-v "$REPO_ROOT/packaging:/packaging:ro,Z" \
		"$IMAGE" bash -euo pipefail -c '
set -o pipefail
if [ "'"$mode"'" = "answers" ]; then
	cat >/tmp/answers <<EOF
ANSWER_PORT=8007
ANSWER_WITH_LLDP=no
EOF
	bash /packaging/bin/vnprox-setup --answers /tmp/answers >/tmp/setup.log 2>&1
else
	# No port conflict in a clean container, so resolve_port() never
	# prompts (docs/deployment.md: only asked "if needed") — the only
	# interactive prompt reached is lldpd.
	printf "n\n" | bash /packaging/bin/vnprox-setup >/tmp/setup.log 2>&1
fi
echo "--- vnprox.toml ---"
cat /etc/vnprox/vnprox.toml
echo "--- session key perms ---"
stat -c "%a" /etc/vnprox/keys/session.key
echo "--- directory listing ---"
find /etc/vnprox -maxdepth 2 | sort
'
}

# set +e/-e: a run_setup failure must not abort this script via set -e
# before its output (needed for debugging) is captured and printed.
set +e
INTERACTIVE_OUT="$(run_setup interactive)"
INTERACTIVE_RC=$?
ANSWERS_OUT="$(run_setup answers)"
ANSWERS_RC=$?
set -e
[ "$INTERACTIVE_RC" -eq 0 ] || { echo "$INTERACTIVE_OUT"; die "interactive vnprox-setup run failed (exit $INTERACTIVE_RC)"; }
[ "$ANSWERS_RC" -eq 0 ] || { echo "$ANSWERS_OUT"; die "--answers vnprox-setup run failed (exit $ANSWERS_RC)"; }

echo "=== interactive ==="
echo "$INTERACTIVE_OUT"
echo "=== answers ==="
echo "$ANSWERS_OUT"

if [ "$INTERACTIVE_OUT" != "$ANSWERS_OUT" ]; then
	echo "FAIL: interactive and --answers produced different resulting state:" >&2
	diff <(echo "$INTERACTIVE_OUT") <(echo "$ANSWERS_OUT") >&2 || true
	exit 1
fi

echo "ALL CHECKS PASSED (interactive and --answers system state are identical)"
