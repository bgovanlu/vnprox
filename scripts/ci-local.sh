#!/usr/bin/env bash
# ci-local.sh — run everything GitHub Actions would run, on this host.
#
# GitHub Actions is out of funds for this repository, so this script is the
# gate that matters. It reproduces every job in .github/workflows/ci.yml and
# .github/workflows/packaging-matrix.yml, in the same order, with the same
# commands, and reports one line per job at the end.
#
# Differences from the hosted runner, stated rather than discovered later:
#
#   * Node comes from nvm (pinned to the same major the workflows pin) rather
#     than actions/setup-node. If nvm is absent the script FAILS rather than
#     silently running the system Node — a green run on a different Node major
#     than CI pins is not evidence about CI.
#   * `actions/upload-artifact` has no local equivalent; artifacts are left in
#     place (dist/*.deb, web/test-results/) and their paths reported.
#   * The `e2e` job is a 4-way shard MATRIX on the hosted runner — one shard per
#     2-4 core runner — plus a required `e2e-gate` job that reads all four
#     reports. `make e2e` here runs all four shards CONCURRENTLY on this
#     32-core box and then gates, so the verdict is the same but the timing is
#     not comparable. Do not quote a local shard wall-clock as a CI number
#     (T-2505-input-02, planning/tasks/phase-25.md).
#   * The hosted runner starts from a clean checkout every time. This host does
#     not, so the script refuses to run with a dirty tree unless ALLOW_DIRTY=1.
#
# Usage:
#   scripts/ci-local.sh                # every job
#   scripts/ci-local.sh check e2e      # only the named jobs
#   FUZZTIME=10s scripts/ci-local.sh fuzz
#   ALLOW_DIRTY=1 scripts/ci-local.sh check
#
# Exit status is non-zero if any job failed.

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

NODE_MAJOR="${NODE_MAJOR:-22}"
GO_VERSION_EXPECTED="${GO_VERSION_EXPECTED:-1.26.5}"
FUZZTIME="${FUZZTIME:-60s}"
LOG_DIR="${LOG_DIR:-$REPO_ROOT/.ci-local}"

ALL_JOBS=(check e2e cross-arm64 fuzz package packaging-matrix cluster-ssh)

# --- job selection ---------------------------------------------------------

if [ "$#" -gt 0 ]; then
    JOBS=("$@")
else
    JOBS=("${ALL_JOBS[@]}")
fi

for j in "${JOBS[@]}"; do
    found=0
    for known in "${ALL_JOBS[@]}"; do
        [ "$j" = "$known" ] && found=1
    done
    if [ "$found" -eq 0 ]; then
        echo "unknown job: $j" >&2
        echo "known jobs: ${ALL_JOBS[*]}" >&2
        exit 2
    fi
done

mkdir -p "$LOG_DIR"

# --- preconditions ---------------------------------------------------------
#
# Each of these is a thing that, if wrong, makes the whole run report something
# other than what CI would have reported. They are checked up front so a
# 40-minute run does not end in a misleading verdict.

fail_precondition() {
    echo "!! precondition failed: $*" >&2
    exit 2
}

if [ -z "${ALLOW_DIRTY:-}" ] && [ -n "$(git status --porcelain)" ]; then
    fail_precondition "working tree is dirty; CI runs a clean checkout. Set ALLOW_DIRTY=1 to override."
fi

GO_VERSION_ACTUAL="$(go version 2>/dev/null | awk '{print $3}' | sed 's/^go//')"
if [ "$GO_VERSION_ACTUAL" != "$GO_VERSION_EXPECTED" ]; then
    echo "!! go $GO_VERSION_ACTUAL, workflows pin $GO_VERSION_EXPECTED — results may differ from CI" >&2
fi

# Node: load nvm and select the pinned major. `nvm use` must NOT be piped —
# a pipe runs it in a subshell and the PATH change is silently discarded,
# leaving the system Node in place while the output claims otherwise.
export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
if [ ! -s "$NVM_DIR/nvm.sh" ]; then
    fail_precondition "nvm not found at $NVM_DIR; install it, or CI's pinned Node $NODE_MAJOR cannot be reproduced"
fi
# shellcheck disable=SC1091
. "$NVM_DIR/nvm.sh"
nvm use --delete-prefix "$NODE_MAJOR" >/dev/null 2>&1 || fail_precondition "nvm has no Node $NODE_MAJOR installed (nvm install $NODE_MAJOR)"

NODE_ACTUAL="$(node --version)"
case "$NODE_ACTUAL" in
    v"$NODE_MAJOR".*) : ;;
    *) fail_precondition "node is $NODE_ACTUAL after nvm use; expected v$NODE_MAJOR.x" ;;
esac

needs_podman=0
for j in "${JOBS[@]}"; do
    case "$j" in packaging-matrix|cluster-ssh) needs_podman=1 ;; esac
done
if [ "$needs_podman" -eq 1 ] && ! command -v podman >/dev/null 2>&1; then
    fail_precondition "podman is required by the packaging jobs"
fi

echo ">> ci-local: go $GO_VERSION_ACTUAL, node $NODE_ACTUAL, logs in $LOG_DIR"
echo ">> ci-local: jobs: ${JOBS[*]}"
echo

# --- job runner ------------------------------------------------------------

declare -A RESULT
declare -A DURATION

run_job() {
    local name="$1"
    shift
    local log="$LOG_DIR/$name.log"
    local start
    start=$(date +%s)

    echo ">> [$name] starting ($(date '+%H:%M:%S'))"
    if ( "$@" ) >"$log" 2>&1; then
        RESULT[$name]="PASS"
    else
        RESULT[$name]="FAIL"
    fi
    DURATION[$name]=$(( $(date +%s) - start ))
    echo ">> [$name] ${RESULT[$name]} in ${DURATION[$name]}s — $log"
}

# --- the jobs, one function each, mirroring the workflow steps -------------

job_check() {
    ( cd web && npm ci ) || return 1
    make check
}

job_e2e() {
    ( cd web && npm ci ) || return 1
    ( cd web && npx playwright install --with-deps chromium ) || \
        ( cd web && npx playwright install chromium ) || return 1
    make e2e
}

job_cross_arm64() {
    GOOS=linux GOARCH=arm64 go build ./...
}

job_fuzz() {
    go test -run='^$' -fuzz='^FuzzParse$'            -fuzztime="$FUZZTIME" ./internal/host/  || return 1
    go test -run='^$' -fuzz='^FuzzPeerAuth$'         -fuzztime="$FUZZTIME" ./internal/peer/  || return 1
    go test -run='^$' -fuzz='^FuzzParseBGPSummary$'  -fuzztime="$FUZZTIME" ./internal/host/  || return 1
    go test -run='^$' -fuzz='^FuzzParseEVPNVNI$'     -fuzztime="$FUZZTIME" ./internal/host/  || return 1
    go test -run='^$' -fuzz='^FuzzParseLLDP$'        -fuzztime="$FUZZTIME" ./internal/host/  || return 1
    go test -run='^$' -fuzz='^FuzzParseDHCPLeases$'  -fuzztime="$FUZZTIME" ./internal/host/  || return 1
    go test -run='^$' -fuzz='^FuzzParseAll$'         -fuzztime="$FUZZTIME" ./internal/fwlog/
}

job_package() {
    make build || return 1
    make deb || return 1
    # upload-artifact's `if-no-files-found: error` half, which is the only
    # part of that step that can actually fail a build.
    if ! ls dist/*.deb >/dev/null 2>&1; then
        echo "no .deb produced in dist/ — the artifact step would have failed the job"
        return 1
    fi
    ls -la dist/*.deb
}

job_packaging_matrix() {
    make build || return 1
    make deb || return 1
    bash packaging/test/lib/sigpipe-guard.sh || return 1
    local rc=0
    for image in debian:12 debian:13; do
        echo "=============== VNPROX_TEST_IMAGE=$image ==============="
        for script in deb-install port-conflict pve-token answers-parity upgrade; do
            echo "--- $script.sh ($image) ---"
            if ! VNPROX_TEST_IMAGE="$image" bash "packaging/test/$script.sh"; then
                echo "!! $script.sh FAILED on $image"
                rc=1
            fi
        done
    done
    return $rc
}

job_cluster_ssh() {
    make build || return 1
    make deb || return 1
    VNPROX_TEST_IMAGE=debian:12 bash packaging/test/cluster-ssh.sh
}

# --- dispatch --------------------------------------------------------------

for j in "${JOBS[@]}"; do
    case "$j" in
        check)            run_job check            job_check ;;
        e2e)              run_job e2e              job_e2e ;;
        cross-arm64)      run_job cross-arm64      job_cross_arm64 ;;
        fuzz)             run_job fuzz             job_fuzz ;;
        package)          run_job package          job_package ;;
        packaging-matrix) run_job packaging-matrix job_packaging_matrix ;;
        cluster-ssh)      run_job cluster-ssh      job_cluster_ssh ;;
    esac
done

# --- report ----------------------------------------------------------------

echo
echo "================ ci-local summary ================"
printf '%-20s %-6s %10s\n' "JOB" "RESULT" "SECONDS"
overall=0
for j in "${JOBS[@]}"; do
    printf '%-20s %-6s %10s\n' "$j" "${RESULT[$j]}" "${DURATION[$j]}"
    [ "${RESULT[$j]}" = "FAIL" ] && overall=1
done
echo "=================================================="
if [ "$overall" -eq 0 ]; then
    echo "ALL PASS (go $GO_VERSION_ACTUAL, node $NODE_ACTUAL)"
else
    echo "FAILURES ABOVE — logs in $LOG_DIR"
fi
exit "$overall"
