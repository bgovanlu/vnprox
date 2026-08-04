#!/usr/bin/env bash
# planning/validation/harness/capture.sh — capture section (T-1801).
# Read-only.
#
# Covers the host-kernel-facing items behind T-1002/T-1004/T-1301/T-1303's
# checklist entries: `/proc/net/nf_conntrack` table shape and accounting
# availability (Host-local flow sampling, T-1004), eBPF capability/BTF
# preconditions (same section), corosync ring-status output shape (Health-
# check pack 2, T-803), and the host ping(1) summary-line locale/wording
# assumption (Latency & loss mesh, T-1303). Grouped together per T-1802's
# "capture" area even though the source checklist spreads them across
# several headings, because they are all "read a kernel/host fact and
# compare its shape to what a parser assumes" checks of the same kind.
#
# Usage:
#   ssh pvecube 'bash -s' < planning/validation/harness/capture.sh > capture-evidence.json
# Against a dev machine (no PVE, no pvemock needed — every check here is
# host/kernel introspection, not a PVE API call):
#   bash planning/validation/harness/capture.sh

SECTION="capture"
MUTATES=0

# shellcheck source=lib/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/common.sh"

# --- capture-01: conntrack table shape + accounting ------------------------
harness_item "capture-01" \
	"Host-local flow sampling (T-1004) > /proc/net/nf_conntrack table format + accounting default" \
	'if [ -r /proc/net/nf_conntrack ]; then
	echo "--- first 5 lines ---"
	head -5 /proc/net/nf_conntrack 2>&1
	echo "--- total entries ---"
	wc -l < /proc/net/nf_conntrack
else
	echo "/proc/net/nf_conntrack not readable (module not loaded, or insufficient privilege)"
fi
if command -v sysctl >/dev/null 2>&1; then
	sysctl net.netfilter.nf_conntrack_acct 2>&1
else
	echo "sysctl not present on this host"
fi'

# --- capture-02: eBPF capability/BTF preconditions -------------------------
harness_item "capture-02" \
	"Host-local flow sampling (T-1004) > CAP_BPF/CAP_PERFMON availability + BTF presence" \
	'if [ -r /proc/self/status ]; then
	grep -E "^Cap(Eff|Bnd)" /proc/self/status 2>&1
else
	echo "/proc/self/status not readable"
fi
if [ -e /sys/kernel/btf/vmlinux ]; then
	echo "btf: present"
else
	echo "btf: /sys/kernel/btf/vmlinux absent (CONFIG_DEBUG_INFO_BTF not enabled on this kernel)"
fi
uname -r 2>&1'

# --- capture-03: corosync ring status output shape --------------------------
harness_item "capture-03" \
	"Health-check pack 2 (T-803) > exact corosync-cfgtool -s output format/version" \
	'if command -v corosync-cfgtool >/dev/null 2>&1; then
	corosync-cfgtool -s 2>&1
	echo "--- corosync-quorumtool -s ---"
	corosync-quorumtool -s 2>&1
else
	echo "corosync-cfgtool not present on this host"
fi'

# --- capture-04: ping(1) summary line wording/locale -----------------------
harness_item "capture-04" \
	"Latency & loss mesh (T-1303) > ping summary-line wording/format across real PVE node builds" \
	'echo "LANG=$LANG LC_ALL=$LC_ALL"
if command -v ping >/dev/null 2>&1; then
	LANG=C LC_ALL=C ping -c 3 -W 1 127.0.0.1 2>&1
else
	echo "ping(8) not present on this host"
fi'

harness_emit
