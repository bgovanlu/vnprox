#!/usr/bin/env bash
#
# T-2505: run the Playwright e2e suite as N concurrent shards and gate the
# result.
#
# Each shard is a separate Playwright process with its OWN pvemock/vnproxd
# stacks, its own SQLite store and its own interfaces sandbox (web/e2e/
# shards.ts decides which specs and which ports). So the suite's wall clock is
# the slowest shard rather than the sum of every spec, and a spec that corrupts
# global state can only corrupt its own shard's.
#
# The shards' exit codes are NOT the verdict. cmd/e2egate reads every shard's
# JSON report and applies the quarantine, the expiry rule and the run-history
# trend; it is what decides pass or fail.
#
#   scripts/e2e-shards.sh                    every shard, concurrently
#   scripts/e2e-shards.sh shard-2 shard-3    only those
#   E2E_JOBS=2 scripts/e2e-shards.sh         at most two shards at a time
#   E2E_ARGS="--repeat-each=2" scripts/e2e-shards.sh
#
# On a 2-4 core CI runner, run ONE shard per job (a matrix leg sets
# VNPROX_E2E_SHARD itself) and hand the collected reports to `e2egate gate`.
# Four concurrent shards is a 32-core developer machine's arrangement, and the
# hosted runner failing two specs this host passes (T-2505-input-02) is what
# that distinction is for.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$PWD"

REPORT_DIR="web/test-results/shards"
BIN_DIR="web/test-results/e2e-bin"
LOG_DIR="web/test-results/shard-logs"

# --- which shards -----------------------------------------------------------
# Read from web/e2e/shards.json, the same file web/e2e/shards.ts reads. A list
# hard-coded here would drift from the manifest the moment a shard is added.
ALL_SHARDS=$(node -e '
  const s = JSON.parse(require("fs").readFileSync("web/e2e/shards.json", "utf8"));
  process.stdout.write(s.map((x) => x.name).join(" "));
')

if [ "$#" -gt 0 ]; then
  SHARDS="$*"
  COMPLETE=0
else
  SHARDS="$ALL_SHARDS"
  COMPLETE=1
fi

JOBS="${E2E_JOBS:-0}" # 0 = all at once
EXTRA_ARGS="${E2E_ARGS:-}"

echo ">> e2e: shards: $SHARDS"

# --- preflight --------------------------------------------------------------
# The registry knows every port this repo's tooling binds; a shard whose stack
# cannot bind fails 90 seconds later with a message about a health check, not
# about the port.
# shellcheck source=/dev/null
. packaging/test/lib/ports.sh
E2E_PORTS=$(awk -F'\t' '$1 !~ /^#/ && NF >= 4 && ($4 ~ /^web\//  || $4 ~ /^testdata\/dev/) && $2 == "tcp" { print $1 }' testdata/dev-ports.tsv)
# shellcheck disable=SC2086 # deliberate word splitting: one argument per port.
if ! ports_require_free $E2E_PORTS; then
  echo ">> e2e: refusing to start — something is holding a port this suite needs (see 'make ports')" >&2
  exit 2
fi

rm -rf "$REPORT_DIR" "$LOG_DIR"
mkdir -p "$REPORT_DIR" "$LOG_DIR" "$BIN_DIR"

# Every shard's daemons and mocks live under here; each shard clears its own
# subdirectory at config load, but a wholesale wipe also removes the
# directories of shards that are not in this run.
rm -rf var/e2e-shards var/e2e-canary

# Build once. Without this, a full run pays `go run`'s link cost on each of the
# ~20 servers four shards start.
echo ">> e2e: building vnproxd, pvemock and k8smock once for every shard"
for cmd in vnproxd pvemock k8smock; do
  go build -o "$BIN_DIR/$cmd" "./cmd/$cmd"
done

# --- run --------------------------------------------------------------------
run_shard() {
  local shard="$1"
  local log="$LOG_DIR/$shard.log"
  local start
  start=$(date +%s)
  if (
    cd web
    VNPROX_E2E_SHARD="$shard" \
    VNPROX_E2E_ALL_SHARDS="$COMPLETE" \
      npx playwright test $EXTRA_ARGS
  ) >"$log" 2>&1; then
    echo ">> e2e: $shard finished green in $(($(date +%s) - start))s"
  else
    echo ">> e2e: $shard exited non-zero after $(($(date +%s) - start))s (see $log; the gate decides)"
  fi
}

SUITE_START=$(date +%s)
running=0
for shard in $SHARDS; do
  run_shard "$shard" &
  running=$((running + 1))
  if [ "$JOBS" -gt 0 ] && [ "$running" -ge "$JOBS" ]; then
    wait -n
    running=$((running - 1))
  fi
done
wait
SUITE_ELAPSED=$(($(date +%s) - SUITE_START))

echo ">> e2e: all shards done in ${SUITE_ELAPSED}s ($(awk "BEGIN{printf \"%.1f\", $SUITE_ELAPSED/60}") min)"
for shard in $SHARDS; do
  tail -n 3 "$LOG_DIR/$shard.log" | sed "s/^/   [$shard] /"
done

# --- gate -------------------------------------------------------------------
# A CI matrix leg runs one shard on its own runner and has nothing to gate: the
# verdict needs every shard's report, which only the collecting job has. It sets
# E2E_NO_GATE=1 and uploads web/test-results/shards/ instead.
if [ "${E2E_NO_GATE:-0}" = "1" ]; then
  echo ">> e2e: E2E_NO_GATE=1 — reports are in $REPORT_DIR; run 'e2egate gate' where they are collected"
  exit 0
fi

GATE_ARGS=(gate --reports "$REPORT_DIR" --shards "$(echo "$SHARDS" | tr ' ' ',')")
if [ "$COMPLETE" -eq 0 ]; then
  GATE_ARGS+=(--complete=false)
fi
exec go run ./cmd/e2egate "${GATE_ARGS[@]}"
