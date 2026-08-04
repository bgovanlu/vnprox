#!/usr/bin/env bash
# planning/validation/harness/ipam.sh — IPAM section (T-1801). Read-only.
#
# Covers the IPAM-specific bullets under
# `planning/reports/needs-hardware-validation.md`'s "## PVE API behavior"
# heading ("IPAM wire shapes") and "## SDN subnet gateway (T-701)"'s
# built-in-`pve`-IPAM-default question.
#
# Usage:
#   ssh pvecube 'bash -s' < planning/validation/harness/ipam.sh > ipam-evidence.json
# Against pvemock:
#   PVE_API_BASE_URL=http://localhost:8006/api2/json \
#     bash planning/validation/harness/ipam.sh

SECTION="ipam"
MUTATES=0

# shellcheck source=lib/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/common.sh"

# --- ipam-01: configured IPAM plugin instances -----------------------------
harness_item "ipam-01" \
	"PVE API behavior > IPAM wire shapes (GET /cluster/sdn/ipams)" \
	'api_get /cluster/sdn/ipams'

# --- ipam-02: built-in pve IPAM reachable with zero explicit config --------
harness_item "ipam-02" \
	"SDN subnet gateway (T-701) > built-in pve IPAM default-instance question" \
	'api_get /cluster/sdn/ipams/pve'

# --- ipam-03: allocation entries + gateway marker typing -------------------
harness_item "ipam-03" \
	"PVE API behavior > IPAM wire shapes (gateway 0/1 int, vmid typing)" \
	'api_get /cluster/sdn/ipams/pve/status'

harness_emit
