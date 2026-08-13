// Package hub is T-1705's client for an opt-in public registry of T-1107
// signed blueprint bundles and T-1702 SDK plugins. It is a browse/install
// *client* and the registry-index *contract* only — this repository hosts no
// registry service (mirrors T-1106's "the provider/collection source lives in
// a separate repo" boundary; see the T-1705 report for where the registry
// service is expected to live).
//
// The security model is inherited wholesale, never re-implemented here:
//
//   - This package fetches and parses bytes. It performs NO signature or trust
//     decision of its own — a downloaded blueprint bundle is verified and
//     trust-gated by T-1107's exact import path (internal/api's importBundleCore
//     -> blueprint.VerifyBundle + the TrustStore), and a downloaded plugin
//     manifest by blueprint.VerifySignature (the same Ed25519 primitive) + the
//     same TrustStore, before internal/plugin's capability-scoped registry ever
//     installs it. The hub adds no implicit-trust path.
//   - The "vetted" tier (VettedSet) is purely informational: a badge meaning
//     "this signer fingerprint is in the hub's own recognized list". It never
//     substitutes for, or short-circuits, the per-installation trust decision.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/blueprint"
)

// EntryType is the kind of artifact a registry-index entry describes.
type EntryType string

const (
	// TypeBlueprint is a T-1107 signed blueprint bundle.
	TypeBlueprint EntryType = "blueprint"
	// TypePlugin is a T-1702 SDK plugin.
	TypePlugin EntryType = "plugin"
)

// ValidType reports whether t is a recognized entry type.
func ValidType(t EntryType) bool { return t == TypeBlueprint || t == TypePlugin }

// CurrentIndexSchema is the only registry-index schema version this client
// understands; a newer index fails loudly rather than being silently
// misread (mirrors blueprint.CurrentBundleVersion's own convention).
const CurrentIndexSchema = 1

// maxIndexBytes and maxArtifactBytes bound the registry response bodies this
// client will read — generous headroom, not a realistic limit, matching the
// 4 MiB ceiling internal/api already applies to a bundle import body.
const (
	maxIndexBytes    = 4 << 20
	maxArtifactBytes = 4 << 20
)

// ErrUnsupportedSchema is returned when a fetched index's SchemaVersion is not
// CurrentIndexSchema.
var ErrUnsupportedSchema = errors.New("hub: unsupported registry index schema version")

// ErrForeignArtifact is returned when an entry's artifact URL resolves to a
// host other than the configured registry's own host. The index is fetched
// over HTTP and is not signed as a whole, so an artifact URL is never allowed
// to redirect an install to an arbitrary third-party host — the artifact must
// live on the same origin the operator configured. (The artifact's own
// Ed25519 signature is still the authoritative gate; this is defence in depth
// against the index steering a fetch off-origin.)
var ErrForeignArtifact = errors.New("hub: entry artifact URL is not on the registry host")

// Index is the documented `GET <registry>/index.json` contract this card
// specifies but does not host.
type Index struct {
	Entries       []Entry `json:"entries"`
	SchemaVersion int     `json:"schemaVersion"`
}

// Entry is one catalog item. Signature carries the publisher's Ed25519 signer
// identity (for display and the vetted-badge lookup); the authoritative gate
// is the downloaded artifact's own signature, verified at install. The
// plugin-only fields (Capabilities, ExtensionPoints, Transport) let a browse
// UI surface a plugin's declared capability scope for review *before* the
// operator confirms an install (T-1705 AC4) without downloading it first.
type Entry struct {
	Signature       *blueprint.BundleSignature `json:"signature,omitempty"`
	Type            EntryType                  `json:"type"`
	ID              string                     `json:"id"`
	Name            string                     `json:"name"`
	Version         string                     `json:"version"`
	Publisher       string                     `json:"publisher,omitempty"`
	Description     string                     `json:"description,omitempty"`
	ArtifactURL     string                     `json:"artifactUrl"`
	Transport       string                     `json:"transport,omitempty"`
	Capabilities    []string                   `json:"capabilities,omitempty"`
	ExtensionPoints []string                   `json:"extensionPoints,omitempty"`
}

// SignerFingerprint returns the entry's publisher signer fingerprint, or "" if
// the entry is unsigned.
func (e Entry) SignerFingerprint() string {
	if e.Signature == nil {
		return ""
	}
	return e.Signature.PublicKeyFingerprint
}

// CapabilityMismatch compares a catalog entry's advertised plugin
// Capabilities/ExtensionPoints (T-1705 AC4's pre-install review surface —
// what GET /hub/index shows an operator before they click install) against
// the manifest of the artifact that would actually be installed, and reports
// a human-readable description of the first disagreement, or "" if they
// agree.
//
// This exists because the index entry and the artifact are two separate
// pieces of registry-served data: the entry is catalog metadata a reviewer
// or a compromised/buggy registry controls, while the artifact's manifest
// (downloaded and, when signed, cryptographically verified) is what
// plugin.Registry.Install actually grants a scope from. Nothing fetched from
// a registry is trusted merely because the registry served it — so a hub
// plugin install must not silently install a wider (or merely different)
// capability/extension-point scope than what the catalog showed the operator
// before they consented (T-2104 AC2: "a mismatch fails a test"). The
// comparison is order-independent (a set, not a sequence) so a reviewer
// tool's re-ordering can never itself be flagged as a mismatch.
func CapabilityMismatch(entry Entry, m PluginManifest) string {
	if !sameStringSet(entry.Capabilities, m.Capabilities) {
		return fmt.Sprintf("catalog capabilities %v disagree with the artifact manifest's own %v", entry.Capabilities, m.Capabilities)
	}
	if !sameStringSet(entry.ExtensionPoints, m.ExtensionPoints) {
		return fmt.Sprintf("catalog extensionPoints %v disagree with the artifact manifest's own %v", entry.ExtensionPoints, m.ExtensionPoints)
	}
	return ""
}

// sameStringSet reports whether a and b contain the same strings, ignoring
// order and nil-vs-empty.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

// PluginManifest is the plugin artifact's self-description — the exact field
// set internal/plugin.Manifest carries, as a stable JSON wire shape. A plugin
// artifact is `{manifest, signature}` where the signature is an Ed25519
// signature over CanonicalManifestBytes(manifest); the api layer converts a
// verified PluginManifest into an internal/plugin.Manifest and installs it
// through T-1702's capability-scoped registry (which independently re-validates
// the capability scope — the hub never bypasses that check).
type PluginManifest struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	APIVersion      string   `json:"apiVersion"`
	Transport       string   `json:"transport"`
	Endpoint        string   `json:"endpoint,omitempty"`
	ExtensionPoints []string `json:"extensionPoints"`
	Capabilities    []string `json:"capabilities"`
}

// PluginArtifact is the downloadable plugin bundle: a manifest plus an optional
// Ed25519 signature over its canonical bytes.
type PluginArtifact struct {
	Signature *blueprint.BundleSignature `json:"signature,omitempty"`
	Manifest  PluginManifest             `json:"manifest"`
}

// CanonicalManifestBytes returns the exact byte sequence a plugin artifact's
// signature is computed over: encoding/json's deterministic marshaling of the
// manifest alone. Struct field order is fixed by PluginManifest's definition,
// so two calls with equal values always produce identical bytes — the property
// signature verification depends on.
func CanonicalManifestBytes(m PluginManifest) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("hub: encoding plugin manifest for signature: %w", err)
	}
	return b, nil
}

// VettedSet is the hub's own recognized-signer allowlist ([hub] vetted_signers)
// — distinct from T-1107's per-admin trust store. Membership drives only the
// informational "vetted" badge; it is never consulted by any install gate.
type VettedSet struct {
	fingerprints map[string]struct{}
}

// NewVettedSet builds a VettedSet from a list of hex signer fingerprints.
// Fingerprints are compared case-insensitively (lowercased), matching
// blueprint.Fingerprint's lowercase-hex output.
func NewVettedSet(fingerprints []string) VettedSet {
	set := make(map[string]struct{}, len(fingerprints))
	for _, fp := range fingerprints {
		fp = strings.ToLower(strings.TrimSpace(fp))
		if fp == "" {
			continue
		}
		set[fp] = struct{}{}
	}
	return VettedSet{fingerprints: set}
}

// IsVetted reports whether fingerprint is in the hub's recognized-signer list.
// An empty fingerprint (an unsigned entry) is never vetted.
func (v VettedSet) IsVetted(fingerprint string) bool {
	if fingerprint == "" {
		return false
	}
	_, ok := v.fingerprints[strings.ToLower(fingerprint)]
	return ok
}

// httpDoer is the minimal HTTP seam Client needs — *http.Client satisfies it.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client fetches (and briefly caches) a registry's index and downloads its
// artifacts. It holds no state that must survive a restart.
type Client struct {
	cachedAt time.Time
	http     httpDoer
	base     *url.URL
	cached   Index
	cacheTTL time.Duration
	mu       sync.Mutex
	hasCache bool
}

// DefaultCacheTTL is how long a fetched index is served from memory before the
// registry is re-polled. Short by design: the hub is a browse surface, not a
// source of truth, so a slightly stale catalog is fine but a long cache would
// hide freshly published/withdrawn entries.
const DefaultCacheTTL = 60 * time.Second

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the HTTP client (tests inject a double).
func WithHTTPClient(h httpDoer) Option {
	return func(c *Client) { c.http = h }
}

// WithCacheTTL overrides the index cache TTL (a non-positive value disables
// caching — every Index call re-fetches).
func WithCacheTTL(d time.Duration) Option {
	return func(c *Client) { c.cacheTTL = d }
}

// NewClient constructs a Client for the registry at registryURL. registryURL
// is the base the index and artifacts are fetched from and against which
// relative artifact URLs resolve.
func NewClient(registryURL string, opts ...Option) (*Client, error) {
	base, err := url.Parse(strings.TrimSpace(registryURL))
	if err != nil {
		return nil, fmt.Errorf("hub: parsing registry URL %q: %w", registryURL, err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("hub: registry URL %q must be an absolute http(s) URL", registryURL)
	}
	c := &Client{
		http:     &http.Client{Timeout: 15 * time.Second},
		base:     base,
		cacheTTL: DefaultCacheTTL,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// indexURL is the base URL with index.json appended to its path.
func (c *Client) indexURL() string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/index.json"
	return u.String()
}

// Index fetches (or returns a cached copy of) the registry index. The result
// is a defensive copy — a caller may filter/annotate it freely.
func (c *Client) Index(ctx context.Context) (Index, error) {
	c.mu.Lock()
	if c.hasCache && c.cacheTTL > 0 && time.Since(c.cachedAt) < c.cacheTTL {
		idx := c.cached.clone()
		c.mu.Unlock()
		return idx, nil
	}
	c.mu.Unlock()

	var idx Index
	if err := c.getJSON(ctx, c.indexURL(), maxIndexBytes, &idx); err != nil {
		return Index{}, err
	}
	if idx.SchemaVersion != CurrentIndexSchema {
		return Index{}, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedSchema, idx.SchemaVersion, CurrentIndexSchema)
	}

	c.mu.Lock()
	c.cached = idx.clone()
	c.cachedAt = time.Now()
	c.hasCache = true
	c.mu.Unlock()
	return idx, nil
}

// FetchBlueprintBundle downloads the blueprint bundle an entry names. The
// returned bundle carries its own embedded signature (if any); this method
// does NOT verify it — internal/api's importBundleCore does, through T-1107's
// exact path.
func (c *Client) FetchBlueprintBundle(ctx context.Context, entry Entry) (blueprint.Bundle, error) {
	artifactURL, err := c.resolveArtifact(entry.ArtifactURL)
	if err != nil {
		return blueprint.Bundle{}, err
	}
	var bundle blueprint.Bundle
	if err := c.getJSON(ctx, artifactURL, maxArtifactBytes, &bundle); err != nil {
		return blueprint.Bundle{}, err
	}
	return bundle, nil
}

// FetchPluginArtifact downloads the plugin artifact an entry names (a manifest
// plus its optional signature). This method does NOT verify the signature —
// internal/api verifies it via blueprint.VerifySignature + the trust store
// before installing through T-1702's registry.
func (c *Client) FetchPluginArtifact(ctx context.Context, entry Entry) (PluginArtifact, error) {
	artifactURL, err := c.resolveArtifact(entry.ArtifactURL)
	if err != nil {
		return PluginArtifact{}, err
	}
	var art PluginArtifact
	if err := c.getJSON(ctx, artifactURL, maxArtifactBytes, &art); err != nil {
		return PluginArtifact{}, err
	}
	return art, nil
}

// resolveArtifact resolves raw against the registry base and requires the
// result to stay on the registry host (ErrForeignArtifact otherwise).
func (c *Client) resolveArtifact(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("hub: entry has no artifact URL")
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("hub: parsing artifact URL %q: %w", raw, err)
	}
	resolved := c.base.ResolveReference(ref)
	if !strings.EqualFold(resolved.Host, c.base.Host) {
		return "", fmt.Errorf("%w: %s (registry host %s)", ErrForeignArtifact, resolved.Host, c.base.Host)
	}
	return resolved.String(), nil
}

// getJSON GETs url and decodes a JSON body no larger than maxBytes into out.
func (c *Client) getJSON(ctx context.Context, url string, maxBytes int64, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("hub: building request for %s: %w", url, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hub: fetching %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hub: fetching %s: registry returned %d", url, resp.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxBytes))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("hub: decoding %s: %w", url, err)
	}
	return nil
}

// clone deep-copies an Index so a cached copy can never be mutated by a caller.
func (i Index) clone() Index {
	out := Index{SchemaVersion: i.SchemaVersion}
	if i.Entries == nil {
		return out
	}
	out.Entries = make([]Entry, len(i.Entries))
	for idx, e := range i.Entries {
		ce := e
		if e.Signature != nil {
			sig := *e.Signature
			ce.Signature = &sig
		}
		ce.Capabilities = append([]string(nil), e.Capabilities...)
		ce.ExtensionPoints = append([]string(nil), e.ExtensionPoints...)
		out.Entries[idx] = ce
	}
	return out
}
