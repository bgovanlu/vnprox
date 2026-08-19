// tokens.go implements T-1104's automation-token machinery: raw-token
// generation/hashing, scope parsing/validation against caps.go's
// capability vocabulary, and building the Capabilities a bearer-token-
// authenticated request context carries. internal/api's POST/GET/DELETE
// /tokens handlers (docs/api.md's Tokens section) call ParseScopes and
// Identity.CanGrantScope to enforce "a token's scopes can never exceed the
// creating user's own derived capabilities at creation time"; the bearer
// middleware (middleware.go) calls GenerateAPIToken's counterpart
// HashAPIToken and CapabilitiesFromScopes on every request.

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// apiTokenRawBytes is a minted token's raw entropy before hex encoding
// (256 bits), matching the session id / cluster secret / metrics scrape
// token convention elsewhere in this codebase.
const apiTokenRawBytes = 32

// apiTokenPrefix marks a value as a vnprox automation token at a glance
// (in logs, in a pasted `Authorization: Bearer` header, in a CI secret
// name) — purely cosmetic, not parsed or validated by this package.
const apiTokenPrefix = "vnpx_"

// ErrInvalidScope is returned by ParseScopes when a requested scope name is
// not one of AllCaps' documented values.
var ErrInvalidScope = errors.New("auth: invalid token scope")

// ErrScopeExceedsCapabilities is returned when a requested token scope is
// not one the minting user's own session capabilities grant (docs/api.md's
// Tokens section: "a token's scopes can never exceed the creating user's
// own derived capabilities at creation time").
var ErrScopeExceedsCapabilities = errors.New("auth: requested scope exceeds the minting user's own capabilities")

// GenerateAPIToken returns a new random bearer token value (the one-time
// POST /tokens reveals verbatim) and the hex-encoded SHA-256 hash that is
// the only form ever persisted (api_tokens.token_hash). Unlike the session
// AES key/metrics scrape token (store.GenerateHexTokenFile), this is a
// per-mint value with no on-disk file of its own — it lives only in the
// caller's hands and this table's hash column.
func GenerateAPIToken() (raw, hash string, err error) {
	b := make([]byte, apiTokenRawBytes)
	if _, rerr := rand.Read(b); rerr != nil {
		return "", "", fmt.Errorf("auth: generating api token: %w", rerr)
	}
	raw = apiTokenPrefix + hex.EncodeToString(b)
	return raw, HashAPIToken(raw), nil
}

// HashAPIToken returns the hex-encoded SHA-256 hash of raw, the value
// compared against api_tokens.token_hash on every bearer-authenticated
// request. A cryptographic hash (rather than reversible encryption, unlike
// sessions.pve_ticket_enc) is enough here: nothing ever needs to recover
// the raw token from storage, only prove a presented value hashes to a
// live row — the same one-way property GitHub/GitLab personal access
// tokens rely on.
func HashAPIToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// capVocabulary is the set of scope names ParseScopes accepts, built once
// from AllCaps (caps.go's single source of truth for the capability-flag
// vocabulary, extended with CapAutomation).
var capVocabulary = func() map[Cap]bool {
	m := make(map[Cap]bool, len(AllCaps))
	for _, c := range AllCaps {
		m[c] = true
	}
	return m
}()

// ParseScopes validates raw scope-name strings (a POST /tokens request
// body's `scopes` field) against AllCaps, de-duplicating and returning the
// typed, order-preserving []Cap. An unrecognized name fails closed
// (ErrInvalidScope) rather than being silently dropped — docs/api.md's
// Tokens section documents scopes as "drawn from the exact capability set
// internal/auth/caps.go already defines, plus automation", so a caller
// typo (or forward-compatibility request for a not-yet-existing scope)
// must be a validation error, not a quietly-narrower token.
func ParseScopes(raw []string) ([]Cap, error) {
	seen := make(map[Cap]bool, len(raw))
	out := make([]Cap, 0, len(raw))
	for _, s := range raw {
		c := Cap(s)
		if !capVocabulary[c] {
			return nil, fmt.Errorf("%w: %q", ErrInvalidScope, s)
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

// CanGrantScope reports whether id's session may mint a token carrying
// scope, per docs/api.md's Tokens section: CapAutomation and
// CapAutomationWrite are always grantable (neither is derived from any PVE
// privilege — see Capabilities.Automation/AutomationWrite's doc comments —
// so there is nothing for either to "exceed"; both are inherent to holding
// an authenticated vnprox session at all, the same way only a logged-in
// user can reach POST /tokens in the first place). Every other scope must
// be a capability id's own derived Capabilities grant on at least one node
// — Identity.HasCap("", scope)'s cluster-wide "any node" check, since a
// minted token itself carries no node granularity (CapabilitiesFromScopes
// below always builds a single cluster-wide entry).
func (id Identity) CanGrantScope(scope Cap) bool {
	if scope == CapAutomation || scope == CapAutomationWrite {
		return true
	}
	return id.HasCap("", scope)
}

// ValidateScopeGrant checks every scope against id.CanGrantScope, returning
// ErrScopeExceedsCapabilities (naming the first offending scope) the
// moment one doesn't qualify — the enforcement point for "a token's scopes
// can never exceed the creating user's own derived capabilities at
// creation time".
func (id Identity) ValidateScopeGrant(scopes []Cap) error {
	for _, s := range scopes {
		if !id.CanGrantScope(s) {
			return fmt.Errorf("%w: %s", ErrScopeExceedsCapabilities, s)
		}
	}
	return nil
}

// CapabilitiesFromScopes builds the single cluster-wide Capabilities value
// (keyed "" in the Identity.Caps map — the same "no per-node granularity"
// fallback BuildCapabilities documents for a ticket that couldn't enumerate
// the cluster's node list) that a bearer-token-authenticated request's
// Identity carries, from a minted token's stored scopes. A token is
// inherently cluster-wide/non-node-scoped: docs/api.md's Tokens section
// doesn't document any per-node scoping, and RequireCap's node-scoped
// lookup already falls back to "any node in the map" when no
// node-specific entry exists, so a single "" entry is both correct and
// sufficient.
func CapabilitiesFromScopes(scopes []Cap) Capabilities {
	var c Capabilities
	for _, s := range scopes {
		switch s {
		case CapNetRead:
			c.NetRead = true
		case CapNetWrite:
			c.NetWrite = true
		case CapSDNRead:
			c.SDNRead = true
		case CapSDNWrite:
			c.SDNWrite = true
		case CapFWRead:
			c.FWRead = true
		case CapFWWrite:
			c.FWWrite = true
		case CapGuestNet:
			c.GuestNet = true
		case CapAudit:
			c.Audit = true
		case CapAutomation:
			c.Automation = true
		case CapAutomationWrite:
			c.AutomationWrite = true
		case CapCapture:
			c.Capture = true
		}
	}
	return c
}

// ScopeStrings converts scopes back to plain strings for api_tokens'
// scopes_json column / the POST /tokens response body.
func ScopeStrings(scopes []Cap) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}
