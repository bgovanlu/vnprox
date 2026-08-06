#!/usr/bin/env bash
# packaging/test/lib/ports.sh — port preflight for this repo's test tooling.
#
# T-1807-bug-02. Sourced, never executed directly:
#   . "$(dirname "$0")/lib/ports.sh"
#   ports_require_free "$TEST_PORT"
#
# Reads testdata/dev-ports.tsv, the single source of truth (docs/testing/
# port-registry.md explains the policy; internal/devports enforces it in
# `make check`).
#
# What this buys, concretely. On 2026-08-06 eight orphaned `pvemock`/`k8smock`
# processes from a dead session held 8006/8008/18006/28006/38006/48006/58006/
# 61006 for three hours. Anything reaching for those ports would have failed
# with a bind error naming no culprit — the same "systemd says active but
# nothing is listening" confusion T-1807-bug-01 spent an agent-hour
# root-causing by hand. ports_require_free names the holding PID and command
# line in its failure message, so the next occurrence costs one line of output
# instead of an investigation.

set -u

# ports_registry_path — absolute path to the registry, located by walking up
# from this file to the module root.
ports_registry_path() {
	local dir
	dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
	while [ "$dir" != "/" ]; do
		if [ -f "$dir/go.mod" ]; then
			printf '%s\n' "$dir/testdata/dev-ports.tsv"
			return 0
		fi
		dir="$(dirname "$dir")"
	done
	return 1
}

# ports_owner <port> — print "owner (binder)" for a registered port, or the
# empty string. Used to make failure messages say who the port belongs to.
ports_owner() {
	local port="$1" registry
	registry="$(ports_registry_path)" || return 0
	[ -f "$registry" ] || return 0
	awk -F'\t' -v p="$port" '
		/^#/ || /^[[:space:]]*$/ { next }
		$1 == p { printf "%s (%s)", $3, $4; found=1; exit }
	' "$registry"
}

# ports_holder <port> — print "pid N: <cmdline>" for whatever is listening on
# <port>, or the empty string. Best effort: ss may not report a PID for a
# process owned by another user, in which case the port is still reported busy.
ports_holder() {
	local port="$1" pid
	command -v ss >/dev/null 2>&1 || return 0
	pid="$(ss -tlnpH "sport = :${port}" 2>/dev/null | grep -oE 'pid=[0-9]+' | head -1 | cut -d= -f2)"
	if [ -z "$pid" ]; then
		pid="$(ss -ulnpH "sport = :${port}" 2>/dev/null | grep -oE 'pid=[0-9]+' | head -1 | cut -d= -f2)"
	fi
	[ -n "$pid" ] || return 0
	# Collapse NULs *and* newlines: a cmdline with an embedded newline (a `sh -c`
	# with a multi-line script, which is exactly how the e2e stacks are spawned)
	# would otherwise wrap the failure message across a dozen lines.
	printf 'pid %s: %s' "$pid" "$(tr '\0\n\t' '   ' < "/proc/${pid}/cmdline" 2>/dev/null | cut -c1-160)"
}

# ports_in_use <port> — 0 if something is listening on <port>, 1 otherwise.
ports_in_use() {
	local port="$1"
	command -v ss >/dev/null 2>&1 || return 1
	ss -tlnH "sport = :${port}" 2>/dev/null | grep -q . && return 0
	ss -ulnH "sport = :${port}" 2>/dev/null | grep -q . && return 0
	return 1
}

# ports_require_free <port>... — fail with an actionable message if any port is
# already bound. Call before starting anything that binds.
ports_require_free() {
	local port failed=0 owner holder
	for port in "$@"; do
		if ports_in_use "$port"; then
			failed=1
			owner="$(ports_owner "$port")"
			holder="$(ports_holder "$port")"
			echo "FAIL: port ${port} is already in use on this host." >&2
			[ -n "$owner" ] && echo "      registered to: ${owner}" >&2
			if [ -n "$holder" ]; then
				echo "      held by:       ${holder}" >&2
			else
				echo "      held by:       unknown (no PID visible — another user, or a container)" >&2
			fi
			echo "      This repo's tooling assumes a quiet machine (T-1807-bug-01). Stop the" >&2
			echo "      holder, or run 'make ports' to see every port this repo claims." >&2
		fi
	done
	return "$failed"
}

# ports_report — print the registry as an aligned table alongside live status.
# Backs `make ports`.
ports_report() {
	local registry
	registry="$(ports_registry_path)" || { echo "could not locate testdata/dev-ports.tsv" >&2; return 1; }
	printf '%-7s %-5s %-26s %-8s %s\n' PORT PROTO OWNER STATUS BINDER
	while IFS=$'\t' read -r port proto owner binder _purpose; do
		case "$port" in ''|'#'*) continue ;; esac
		local status="free" holder
		if ports_in_use "$port"; then
			status="IN USE"
			holder="$(ports_holder "$port")"
		else
			holder=""
		fi
		printf '%-7s %-5s %-26s %-8s %s\n' "$port" "$proto" "$owner" "$status" "$binder"
		if [ -n "$holder" ]; then
			printf '%-7s %-5s %-26s %-8s %s\n' "" "" "" "" "  ^ $holder"
		fi
	done < "$registry"
	# Explicit: without this the loop's last conditional decides the function's
	# exit status, so a clean report of all-free ports would `make ports` fail.
	return 0
}
