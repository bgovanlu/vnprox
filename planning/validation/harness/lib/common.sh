#!/usr/bin/env bash
# planning/validation/harness/lib/common.sh — shared library for every
# planning/validation/harness/<section>.sh script (T-1801).
#
# Sourced, never executed directly:
#   . "$(dirname "$0")/lib/common.sh"
#
# Contract a section script follows (see planning/validation/README.md for
# the human-facing version of this):
#   1. Set SECTION="<name>" and MUTATES=0 (or MUTATES=1 — see below) near
#      the top of the file, before sourcing this library.
#   2. If MUTATES=1, call `harness_require_mutation_flag "$@"` before doing
#      anything that touches live state. It refuses to continue unless the
#      script was invoked with --i-understand-this-mutates.
#   3. Call `harness_item <id> <checklist_ref> <command> [verdict_extra]`
#      once per checklist observation. <command> is a single string passed
#      to `eval` — write it as you would type it in a shell. Output
#      (stdout+stderr) is captured verbatim, redacted, and stored as that
#      item's "raw" field; the item's exit code is captured automatically.
#      <verdict_extra>, if given, is a fragment of already-valid JSON object
#      *members* (no surrounding braces), e.g. '"http_status":200', merged
#      into that item's verdict_inputs alongside exit_code.
#   4. Call `harness_emit` exactly once at the end. It prints the complete
#      evidence blob (and nothing else) to stdout.
#
# The script never decides pass/fail. verdict_inputs are raw signals for a
# human/agent triage step (planning/validation/expected/<section>.md,
# internal/validation's triage.go) to compare against a declared expected
# outcome — see docs/roadmap-proven.md D7.
#
# Redaction happens *before* a raw field is ever assembled into the blob,
# per the design guidance in T-1801's task card: treat every blob as
# something that will be pasted into a public chat transcript. See
# `redact()` below for the secret shapes scrubbed.

set -u
set -o pipefail

HARNESS_SCHEMA_VERSION="1.0"
HARNESS_VERSION="1.0.0"

: "${SECTION:?common.sh: SECTION must be set before sourcing}"
: "${MUTATES:?common.sh: MUTATES must be set to 0 or 1 before sourcing}"

# --- PVE API access, dual-mode --------------------------------------------
#
# Every real PVE node ships `pvesh`, the local CLI that talks to pveproxy
# over a root-trusted local socket — no credentials to manage, no curl
# dependency, "nothing on the target but a stock PVE install" (the card's
# words). Prefer it whenever present.
#
# internal/pvemock has no `pvesh` binary (it is a plain HTTP server), so
# when pvesh is absent — true of any dev machine or CI runner, which is
# exactly AC1's "no hardware" case — api_get falls back to ticket-based
# HTTP against PVE_API_BASE_URL. This is what lets every script here be
# exercised against pvemock without ever touching a real node.
#
#   PVE_API_BASE_URL   default: http://localhost:8006/api2/json
#   PVE_API_USER       default: root@pam
#   PVE_API_PASSWORD   default: vnprox-mock   (single-node.yaml's root user)
#   PVE_API_INSECURE   default: 1 (pass curl -k; real deployments behind a
#                                  valid pveproxy cert should set this to 0)
#   PVE_NODE           default: "" (auto: `hostname -s` under pvesh mode,
#                                  "pve1" under HTTP/mock mode)

: "${PVE_API_BASE_URL:=http://localhost:8006/api2/json}"
: "${PVE_API_USER:=root@pam}"
: "${PVE_API_PASSWORD:=vnprox-mock}"
: "${PVE_API_INSECURE:=1}"
: "${PVE_NODE:=}"

_PVE_TICKET=""
_PVE_CSRF=""

harness_local_node() {
	if [ -n "$PVE_NODE" ]; then
		printf '%s' "$PVE_NODE"
		return 0
	fi
	if command -v pvesh >/dev/null 2>&1; then
		hostname -s 2>/dev/null || hostname
		return 0
	fi
	printf 'pve1'
}

_api_http_login() {
	[ -n "$_PVE_TICKET" ] && return 0
	local curl_opts=()
	[ "$PVE_API_INSECURE" = "1" ] && curl_opts+=(-k)
	local resp
	resp="$(curl -sS "${curl_opts[@]}" \
		-d "username=${PVE_API_USER}" -d "password=${PVE_API_PASSWORD}" \
		"${PVE_API_BASE_URL}/access/ticket" 2>&1)" || { printf '%s' "$resp" >&2; return 1; }
	_PVE_TICKET="$(printf '%s' "$resp" | grep -o '"ticket"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed -E 's/.*:[[:space:]]*"([^"]*)"/\1/')"
	_PVE_CSRF="$(printf '%s' "$resp" | grep -o '"CSRFPreventionToken"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed -E 's/.*:[[:space:]]*"([^"]*)"/\1/')"
	[ -n "$_PVE_TICKET" ]
}

# api_get <path> — GETs a pvesh-style path (no /api2/json prefix, e.g.
# "/cluster/status"). Prints raw response body (JSON) to stdout, mixed
# stderr on failure so the caller's harness_item still captures something
# useful. Never redacts on its own — callers pass the output through
# harness_item, which redacts unconditionally.
api_get() {
	local path="$1"
	if command -v pvesh >/dev/null 2>&1; then
		pvesh get "$path" --output-format json 2>&1
		return
	fi
	if ! _api_http_login; then
		printf 'error: pve api login failed\n'
		return 1
	fi
	local curl_opts=()
	[ "$PVE_API_INSECURE" = "1" ] && curl_opts+=(-k)
	curl -sS "${curl_opts[@]}" -b "PVEAuthCookie=${_PVE_TICKET}" \
		"${PVE_API_BASE_URL}${path}" 2>&1
}

# --- redaction -------------------------------------------------------------
#
# Scrubs known secret shapes from stdin, writes to stdout. Design guidance
# (T-1801 task card): prefer over-redaction to under-redaction. One pattern
# per secret class; keep this list in sync with
# internal/validation/redact_test.go's table.
redact() {
	sed -E \
		-e 's#PVE:[^][()<>{}"'"'"'[:space:],]+#PVE:[REDACTED-TICKET]#g' \
		-e 's#(PVEAPIToken=[A-Za-z0-9._-]+![A-Za-z0-9._-]+=)[A-Za-z0-9._-]+#\1[REDACTED-SECRET]#g' \
		-e 's#([A-Za-z0-9._-]+@[A-Za-z0-9_-]+![A-Za-z0-9._-]+=)[A-Za-z0-9._-]+#\1[REDACTED-SECRET]#g' \
		-e 's#^([[:space:]]*[Aa]uthorization[[:space:]]*:).*#\1 [REDACTED-HEADER]#g' \
		-e 's#^([[:space:]]*[Cc]ookie[[:space:]]*:).*#\1 [REDACTED-HEADER]#g' \
		-e 's#\b[A-Za-z0-9+/]{42,43}=#[REDACTED-WG-KEY]#g'
}

# --- JSON assembly -----------------------------------------------------
#
# No jq dependency (not guaranteed present on a stock PVE install). Hand-
# rolled escaping is sufficient because every value we embed is either our
# own literal text or command output already forced through redact() and
# control-char sanitization below.

_sanitize_control_chars() {
	# Keep newline and tab (escaped below); drop everything else below
	# 0x20 and the DEL byte, which would otherwise produce invalid JSON.
	LC_ALL=C tr -d '\000-\010\013\014\016-\037\177'
}

json_escape() {
	local s=$1
	s=${s//\\/\\\\}
	s=${s//\"/\\\"}
	s=${s//$'\n'/\\n}
	s=${s//$'\t'/\\t}
	s=${s//$'\r'/\\r}
	printf '%s' "$s"
}

ITEMS=()

# harness_item <id> <checklist_ref> <command> [verdict_extra]
harness_item() {
	local id="$1" ref="$2" cmd="$3" verdict_extra="${4:-}"
	local raw ec
	raw="$(eval "$cmd" 2>&1)"
	ec=$?
	raw="$(printf '%s' "$raw" | redact | _sanitize_control_chars)"
	local verdict_obj="{\"exit_code\":${ec}"
	if [ -n "$verdict_extra" ]; then
		verdict_obj="${verdict_obj},${verdict_extra}"
	fi
	verdict_obj="${verdict_obj}}"
	local item
	item="$(printf '{"id":"%s","checklist_ref":"%s","command":"%s","raw":"%s","exit_code":%d,"verdict_inputs":%s}' \
		"$(json_escape "$id")" "$(json_escape "$ref")" "$(json_escape "$cmd")" \
		"$(json_escape "$raw")" "$ec" "$verdict_obj")"
	ITEMS+=("$item")
}

harness_require_mutation_flag() {
	if [ "${1:-}" != "--i-understand-this-mutates" ]; then
		{
			echo "error: $SECTION.sh mutates state (MUTATES=1) and refuses to run"
			echo "without --i-understand-this-mutates. Read planning/validation/README.md"
			echo "and the script's own header comment before passing that flag."
		} >&2
		exit 2
	fi
}

harness_pve_version() {
	if command -v pveversion >/dev/null 2>&1; then
		printf 'pveversion|%s' "$(pveversion 2>/dev/null | head -1)"
		return
	fi
	printf 'unknown|unknown'
}

harness_node_identity() {
	hostname -f 2>/dev/null || hostname 2>/dev/null || printf 'unknown'
}

# harness_emit — prints the complete evidence blob to stdout. Must be the
# last thing a section script does, and the only thing it has printed to
# stdout (diagnostics go to stderr via the functions above).
harness_emit() {
	local generated_at hostname pve_raw pve_source pve_value items_json mutates_json
	generated_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	hostname="$(harness_node_identity)"
	pve_raw="$(harness_pve_version)"
	pve_source="${pve_raw%%|*}"
	pve_value="${pve_raw#*|}"
	if [ "$MUTATES" = "1" ]; then mutates_json=true; else mutates_json=false; fi
	items_json=""
	if [ "${#ITEMS[@]}" -gt 0 ]; then
		local IFS=,
		items_json="${ITEMS[*]}"
	fi
	printf '{"schema_version":"%s","harness_version":"%s","section":"%s","generated_at":"%s","mutates":%s,"node":{"hostname":"%s","identity":"%s"},"pve_version":{"source":"%s","raw":"%s"},"items":[%s]}\n' \
		"$HARNESS_SCHEMA_VERSION" "$HARNESS_VERSION" "$(json_escape "$SECTION")" "$generated_at" "$mutates_json" \
		"$(json_escape "$hostname")" "$(json_escape "$hostname")" \
		"$(json_escape "$pve_source")" "$(json_escape "$pve_value")" \
		"$items_json"
}
