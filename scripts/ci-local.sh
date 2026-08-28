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
#   * One job, `dco` (T-3803), has no counterpart in either workflow file —
#     it's a local-only DCO sign-off check on commits ahead of the base
#     branch, added here because it can run today while Actions can't.
#   * Another, `reproducible` (T-3806), also has no workflow counterpart: it
#     builds the .deb twice from a clean detached-HEAD git worktree and
#     diffs the sha256sums — see scripts/verify-reproducible.sh.
#   * `terraform-provider` (T-4001) also has no workflow counterpart: it
#     builds/vets/tests contrib/terraform-provider-vnprox — a separate Go
#     module by design (this file's job_terraform_provider comment) — and
#     runs its TF_ACC=1 acceptance suite when a terraform/tofu binary is on
#     $PATH.
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

# GO_VERSION_EXPECTED/NODE_MAJOR: single source of truth in
# scripts/lib/versions.sh (T-3806) — packaging/Makefile's release build path
# reads the same file rather than re-pinning a second literal.
# shellcheck disable=SC1091
. "$REPO_ROOT/scripts/lib/versions.sh"
FUZZTIME="${FUZZTIME:-60s}"
LOG_DIR="${LOG_DIR:-$REPO_ROOT/.ci-local}"

ALL_JOBS=(check e2e cross-arm64 fuzz package packaging-matrix cluster-ssh dco reproducible openapi-client license terraform-provider)

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

# job_dco (T-3803) checks that every commit ahead of the base branch carries a
# `Signed-off-by:` trailer (the DCO — see CONTRIBUTING.md's "Developer
# Certificate of Origin" section). It has no ci.yml counterpart today —
# Actions is disabled (see the "CI" section of docs/development.md) — so this
# is the gate that actually runs, same as every other job in this script.
#
# The comparison base is, in order: $DCO_BASE_REF if set, else the merge-base
# with origin/main, else the merge-base with local main. If none of those
# resolve (e.g. a shallow clone with no origin and no local main — this
# should not happen in ordinary use, but a job that goes silently green on a
# tree it never actually checked would be worse than one that fails loudly),
# the job fails rather than silently passing on zero commits checked.
job_dco() {
    local base="${DCO_BASE_REF:-}"
    if [ -z "$base" ]; then
        base="$(git merge-base HEAD origin/main 2>/dev/null)" || base=""
    fi
    if [ -z "$base" ]; then
        base="$(git merge-base HEAD main 2>/dev/null)" || base=""
    fi
    if [ -z "$base" ]; then
        echo "!! dco: could not resolve a base ref (tried \$DCO_BASE_REF, origin/main, main)" >&2
        echo "!! dco: set DCO_BASE_REF explicitly to the branch this PR targets" >&2
        return 1
    fi

    local range="$base..HEAD"
    local commits
    commits="$(git rev-list "$range" --)"
    if [ -z "$commits" ]; then
        echo "dco: no commits in $range (HEAD is at or behind $base) — nothing to check"
        return 0
    fi

    local rc=0
    local c
    while IFS= read -r c; do
        [ -z "$c" ] && continue
        if git log -1 --format=%B "$c" | grep -qE '^Signed-off-by: .+ <.+>$'; then
            echo "dco: $(git log -1 --format='%h %s' "$c") -- OK"
        else
            echo "dco: $(git log -1 --format='%h %s' "$c") -- MISSING Signed-off-by trailer"
            rc=1
        fi
    done <<< "$commits"

    if [ "$rc" -ne 0 ]; then
        echo "!! dco: one or more commits in $range are missing a DCO sign-off." >&2
        echo "!! dco: run 'git commit --amend -s' (or rebase with '-s') to add it." >&2
    fi
    return "$rc"
}

# job_reproducible (T-3806): builds the .deb twice from a clean, detached
# git worktree at HEAD and asserts byte-identical sha256sums. Delegates to
# scripts/verify-reproducible.sh, which owns worktree creation/cleanup so it
# can also be run standalone. Not run inside this repo's own dirty working
# tree — see that script's header for why (this file's own dirty-tree
# precondition above already guarantees a clean HEAD to build the worktree
# from, but the reproducibility build itself always uses an isolated
# worktree regardless, per T-3806's card: other agents editing this tree
# concurrently must not be measured as "non-reproducible").
job_reproducible() {
    scripts/verify-reproducible.sh
}

# job_openapi_client (T-3809) is the generated-client drift tripwire on top
# of cmd/vnproxd's TestOpenAPI_EveryRouteIsDescribed (T-2405): it generates a
# TypeScript client from the committed docs/openapi.json (dev-only
# `openapi-typescript`, web/tools/openapi-drift/check.mjs), type-checks it,
# and cross-checks every web/src/api/*.ts `apiFetch` call site's method+path
# shape against the spec's own paths. It catches a route whose shape changed
# in the spec without every frontend caller following — it does NOT catch
# request/response BODY drift (docs/openapi.json carries no body schemas;
# see the script's own header comment for the full catches/misses list).
job_openapi_client() {
    ( cd web && npm ci ) || return 1
    ( cd web && npm run check:openapi-drift )
}

# job_license (T-3801) is the license-compatibility gate: go-licenses over
# the Go build graph and license-checker-rseidelsohn over web/'s production
# dependencies, both against an explicit allowed-license set. Neither tool
# is a build dependency — go-licenses runs via a versioned `go run` module
# path that never touches this repo's go.mod/go.sum, and
# license-checker-rseidelsohn is a web/ devDependency only. See
# scripts/check-licenses.sh's header comment for the allowed-license list
# and why each entry is there (including the one narrow copyleft exception,
# EPL-2.0, justified there rather than here).
job_license() {
    ( cd web && npm ci ) || return 1
    scripts/check-licenses.sh
}

# job_terraform_provider (T-4001) builds and tests
# contrib/terraform-provider-vnprox — its own Go module, deliberately outside
# this repository's own go.mod/go.sum (the same structural isolation
# job_reproducible's worktree gets, and commit 34c11588's sigstore-go split
# gets cmd/vnproxctl: a large, unrelated dependency tree stays out of the
# module whose build graph cmd/vnproxd and cmd/vnproxctl are checked
# against, see cmd/vnproxd/tfproviderguard_test.go). Like job_dco/
# job_reproducible/job_openapi_client/job_license, it has no ci.yml
# counterpart today — Actions is unfunded (docs/development.md's "CI"
# section) — so this script is the only place it runs.
#
# Acceptance tests (TF_ACC=1) build and run the REAL cmd/pvemock and
# cmd/vnproxd binaries from THIS module as subprocesses
# (contrib/terraform-provider-vnprox/internal/provider/harness_test.go) and
# need a `terraform` (or `tofu`) binary on $PATH to drive them; when neither
# is present this job still runs the provider's build/vet/unit tests (which
# includes TestClient_HasNoApplyMethod, the stage-only contract's own
# structural guard) and reports the acceptance half skipped rather than
# failing the whole job over an optional local tool.
job_terraform_provider() {
    ( cd contrib/terraform-provider-vnprox && go build ./... ) || return 1
    ( cd contrib/terraform-provider-vnprox && go vet ./... ) || return 1

    if command -v terraform >/dev/null 2>&1 || command -v tofu >/dev/null 2>&1; then
        echo ">> terraform-provider: terraform/tofu CLI found — running unit + acceptance tests (TF_ACC=1)"
        ( cd contrib/terraform-provider-vnprox && TF_ACC=1 go test ./... -timeout 20m )
    else
        echo "!! terraform-provider: no terraform/tofu CLI on \$PATH — running unit tests only; acceptance tests (TF_ACC=1) skipped" >&2
        ( cd contrib/terraform-provider-vnprox && go test ./... )
    fi
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
        dco)              run_job dco              job_dco ;;
        reproducible)     run_job reproducible     job_reproducible ;;
        openapi-client)   run_job openapi-client   job_openapi_client ;;
        license)          run_job license          job_license ;;
        terraform-provider) run_job terraform-provider job_terraform_provider ;;
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
