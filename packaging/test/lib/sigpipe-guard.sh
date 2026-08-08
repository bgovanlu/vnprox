#!/usr/bin/env bash
# T-2410: a build-time guard against the `echo "$OUT" | grep -q` pattern in
# packaging test scripts.
#
# WHY THIS EXISTS. `echo "$OUT" | grep -q PATTERN || die` fails **when the
# pattern MATCHES**, if `$OUT` exceeds the pipe buffer and the match occurs
# early: `grep -q` exits at the first match, `echo` still has bytes to write,
# the write returns EPIPE, `set -o pipefail` turns that into a failed pipeline,
# and `|| die` fires on a successful match.
#
# That is not a theory. GitHub Actions job 93012069994 (2026-08-07) printed the
# expected `URL: https://pve1:8008` line, then `echo: write error: Broken pipe`
# on the asserting line, then the die message. The content was right and the
# assertion failed anyway. Two separate `cluster-ssh` failures — one at the
# cluster-detection assert, one at the port-fallback assert — are the same
# mechanism at two different greps.
#
# WHY IT NEVER REPRODUCED LOCALLY (three attempts, recorded on T-1806-bug-02):
# bash reports `write error: Broken pipe` from a builtin only when SIGPIPE is
# **ignored** rather than fatal. The Actions runner is a Node process, Node sets
# SIGPIPE to SIG_IGN, and every step's shell inherits that disposition. On a
# developer workstation SIGPIPE is fatal, so the same race kills `echo` silently
# — and at the sizes previously tested it never triggered at all.
#
# The fix is a here-string, which removes the pipe entirely and so cannot
# exhibit the failure under ANY SIGPIPE disposition. This guard keeps the
# pattern from coming back, because the next person to write it will not know
# any of the above.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEST_DIR="$(dirname "$SCRIPT_DIR")"

fail=0
while IFS= read -r hit; do
  echo "sigpipe-guard: $hit"
  fail=1
done < <(grep -nE 'echo +"\$[A-Za-z_]+" *\| *grep' "$TEST_DIR"/*.sh || true)

if [ "$fail" -ne 0 ]; then
  cat >&2 <<'MSG'

sigpipe-guard: found `echo "$VAR" | grep` in a packaging test script.

That pipeline fails when the pattern MATCHES, if the output is larger than the
pipe buffer and the match is early — see this file's header for the runner
evidence (T-2410). Use a here-string instead, which has no pipe:

    grep -q "PATTERN" <<<"$OUT" || die "..."

MSG
  exit 1
fi

echo "sigpipe-guard: no \`echo \"\$VAR\" | grep\` pipelines in $TEST_DIR"
