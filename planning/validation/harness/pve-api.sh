#!/usr/bin/env bash
# planning/validation/harness/pve-api.sh — PVE API behavior section (T-1801).
#
# Covers `planning/reports/needs-hardware-validation.md`'s "## PVE API
# behavior" heading: token auth, ticket-as-password renewal, the
# /access/permissions shape, and the TOTP/TFA login variant. Read-only:
# every call here is a GET or a login (auth is not itself a network
# mutation). The PVE staging/PUT/reload item from that same doc section
# moved to change-engine.sh (MUTATES=1), because exercising it for real
# means writing and reloading a node's network config.
#
# Usage:
#   ssh pvecube 'bash -s' < planning/validation/harness/pve-api.sh > pve-api-evidence.json
# Against pvemock (see README for the full recipe):
#   PVE_API_BASE_URL=http://localhost:8006/api2/json \
#   PVE_API_TOKEN='root@pam!daemon=6b1c0a3e-8f2d-4c11-9a57-0d2f6f3a1b42' \
#     bash planning/validation/harness/pve-api.sh
#
# Env overrides (all optional, defaults match testdata/clusters/single-node.yaml
# so this is exercisable against pvemock out of the box):
#   PVE_API_TOKEN             "user@realm!tokenid=secret" for the
#                              API-token-auth check. If unset, that one item
#                              is recorded as skipped (still schema-valid)
#                              rather than guessed.
#   PVE_TOTP_USER/PASSWORD/CODE   TOTP-required fixture user's credentials.

SECTION="pve-api"
MUTATES=0

# shellcheck source=lib/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/common.sh"

: "${PVE_API_TOKEN:=}"
: "${PVE_TOTP_USER:=totp-user@pve}"
: "${PVE_TOTP_PASSWORD:=with-2fa}"
: "${PVE_TOTP_CODE:=246810}"

NODE="$(harness_local_node)"

# --- pve-api-01: API-token auth (Authorization: PVEAPIToken=...) ----------
harness_item "pve-api-01" \
	"PVE API behavior > API-token auth" \
	'if [ -z "$PVE_API_TOKEN" ]; then
	echo "skipped: PVE_API_TOKEN not configured"
	exit 3
fi
curl -sS ${PVE_API_INSECURE:+-k} -o /dev/null -w "http_status=%{http_code}\n" \
	-H "Authorization: PVEAPIToken=$PVE_API_TOKEN" \
	"$PVE_API_BASE_URL/access/permissions"'

# --- pve-api-02: ticket-as-password renewal -------------------------------
harness_item "pve-api-02" \
	"PVE API behavior > Ticket-as-password renewal" \
	'first="$(curl -sS ${PVE_API_INSECURE:+-k} -d "username=$PVE_API_USER" -d "password=$PVE_API_PASSWORD" "$PVE_API_BASE_URL/access/ticket")"
ticket="$(printf "%s" "$first" | grep -o "\"ticket\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 | sed -E "s/.*:[[:space:]]*\"([^\"]*)\"/\1/")"
if [ -z "$ticket" ]; then
	echo "no ticket returned from first login"
	exit 1
fi
curl -sS ${PVE_API_INSECURE:+-k} -d "username=$PVE_API_USER" -d "password=$ticket" \
	-o /dev/null -w "renewal_http_status=%{http_code}\n" "$PVE_API_BASE_URL/access/ticket"'

# --- pve-api-03: GET /access/permissions shape ----------------------------
harness_item "pve-api-03" \
	"PVE API behavior > GET /access/permissions response shape" \
	'api_get /access/permissions'

# --- pve-api-04: TFA/TOTP flow --------------------------------------------
harness_item "pve-api-04" \
	"PVE API behavior > TFA/TOTP flow" \
	'without="$(curl -sS ${PVE_API_INSECURE:+-k} -o /dev/null -w "%{http_code}" -d "username=$PVE_TOTP_USER" -d "password=$PVE_TOTP_PASSWORD" "$PVE_API_BASE_URL/access/ticket")"
with="$(curl -sS ${PVE_API_INSECURE:+-k} -o /dev/null -w "%{http_code}" -d "username=$PVE_TOTP_USER" -d "password=$PVE_TOTP_PASSWORD" -d "otp=$PVE_TOTP_CODE" "$PVE_API_BASE_URL/access/ticket")"
echo "without_otp_http_status=$without with_otp_http_status=$with"'

# --- pve-api-05: ticket expiry / renewal margin ---------------------------
# This item does not assert anything about the real ~2h ticket lifetime
# (that requires waiting near expiry, out of scope for a single harness
# run) — it records the issuance timestamp so a human/triage step can
# reason about Config.TicketRenewAfter's margin against it.
harness_item "pve-api-05" \
	"PVE API behavior > Ticket expiry" \
	'date -u +%Y-%m-%dT%H:%M:%SZ'

harness_emit
