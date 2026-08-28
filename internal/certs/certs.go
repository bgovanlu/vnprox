// SPDX-License-Identifier: Apache-2.0

// Package certs is vnprox's view of the PVE cluster's TLS certificates.
//
// Why this needs no peer fan-out. Unlike almost every other per-node read in
// this codebase, a cluster-wide certificate inventory is a *local* directory
// walk: /etc/pve is pmxcfs, the cluster's own distributed filesystem, so
// /etc/pve/nodes/<node>/pve-ssl.pem is present and byte-identical on every
// node's local disk (the same property host.DefaultCorosyncConfPath already
// relies on). That is not merely an optimisation — it makes this inventory
// available *precisely when peers are unreachable*, which is exactly when a
// certificate problem is the likely cause. An inventory that needed the peer
// API to diagnose a peer-API TLS failure would be useless at the only moment
// it mattered.
//
// Private keys. /etc/pve/nodes/<node>/ holds pve-ssl.key next to pve-ssl.pem.
// Nothing in this package may read, parse, log, or return key material. The
// scanner is therefore built to only ever open an explicit allowlist of
// certificate filenames — it does not glob a directory and filter afterwards,
// because a filter is something a future edit can loosen, whereas a list of
// three constants is something an edit has to deliberately add to. See
// scan.go's fileKinds and the assertion in certs_test.go that a planted key
// file is never opened.
package certs

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Kind classifies a certificate by the role it plays, which is what decides
// which checks apply to it.
type Kind string

const (
	// KindClusterCA is /etc/pve/pve-root-ca.pem — the cluster's own root CA,
	// and the trust anchor peer-API TLS pins (internal/peer.Trust).
	KindClusterCA Kind = "cluster-ca"
	// KindNodeLeaf is /etc/pve/nodes/<node>/pve-ssl.pem — the per-node
	// certificate issued by the cluster CA, which vnproxd itself serves by
	// default (internal/config.resolveTLSPaths) and which peer TLS verifies.
	KindNodeLeaf Kind = "node-leaf"
	// KindCustom is /etc/pve/nodes/<node>/pveproxy-ssl.pem — the optional
	// operator-supplied or ACME-issued certificate PVE's web proxy prefers
	// when present. Usually issued by a public CA, so it deliberately does
	// NOT get the cluster-CA chain check (see Certificate.expectsClusterCA).
	KindCustom Kind = "custom"
	// KindDaemon is whatever certificate this vnproxd is actually serving,
	// when that is not one of the above (an explicit server.tls_cert
	// override). Reported so an operator is never looking at PVE's
	// certificates while the daemon serves a different one.
	KindDaemon Kind = "daemon"
)

// SAN name types, kept as strings because they cross the API boundary.
const (
	SANDNS = "dns"
	SANIP  = "ip"
)

// SAN is one subject alternative name.
type SAN struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Certificate is one parsed certificate.
//
// Every field here is *derived* from the certificate — there is deliberately
// no field carrying raw file bytes, PEM, or DER. That is what makes "this
// type cannot leak a private key" a property of the type rather than of the
// care taken at each call site.
type Certificate struct {
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	Kind      Kind      `json:"kind"`
	// Node is the PVE node this certificate belongs to; empty for the
	// cluster CA, which belongs to the cluster.
	Node string `json:"node,omitempty"`
	Path string `json:"path"`
	// Subject and Issuer are common names, not full RDN sequences — the CN is
	// what an operator recognises, and the full sequence is noise in a table.
	Subject string `json:"subject"`
	Issuer  string `json:"issuer"`
	// Serial is the certificate serial as an uppercase hex string. Real PVE
	// leaf serials are tiny ("02"); the CA's is a 20-byte random.
	Serial string `json:"serial"`
	// Fingerprint is the SHA-256 of the DER encoding, lowercase hex — the
	// same digest `openssl x509 -fingerprint -sha256` prints.
	Fingerprint        string `json:"fingerprint"`
	KeyAlgorithm       string `json:"keyAlgorithm"`
	SignatureAlgorithm string `json:"signatureAlgorithm"`
	SANs               []SAN  `json:"sans"`
	KeyBits            int    `json:"keyBits"`
	IsCA               bool   `json:"isCA"`
	// SelfSigned reports subject == issuer. True for the PVE cluster CA.
	SelfSigned bool `json:"selfSigned"`
}

// ErrNoCertificate reports a file that existed and was readable but held no
// CERTIFICATE PEM block. Distinguished from a read error because the operator
// actions differ: one is a permissions or mount problem, the other is a file
// that is not what it claims to be.
var ErrNoCertificate = errors.New("certs: no CERTIFICATE block found")

// parse decodes the first CERTIFICATE block in pemBytes.
//
// Only the *first* block, deliberately: pve-ssl.pem is a leaf, and a file that
// also carried intermediates would have them appended after it. Taking the
// first is what makes "the subject of this file" unambiguous. Any non-
// CERTIFICATE block (a key accidentally concatenated into the same file, which
// is a real operator mistake) is skipped without being decoded, so its bytes
// are never parsed and never reach an error message.
func parse(pemBytes []byte, kind Kind, node, path string) (Certificate, error) {
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return Certificate{}, fmt.Errorf("%w in %s", ErrNoCertificate, path)
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		x, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			// The wrapped error is x509's own and describes structure, not
			// content; it cannot echo key bytes because a key block never
			// reaches this branch.
			return Certificate{}, fmt.Errorf("certs: parsing %s: %w", path, err)
		}
		return fromX509(x, kind, node, path), nil
	}
}

func fromX509(x *x509.Certificate, kind Kind, node, path string) Certificate {
	sum := sha256.Sum256(x.Raw)
	keyAlg, keyBits := keyInfo(x)
	return Certificate{
		Kind:               kind,
		Node:               node,
		Path:               path,
		Subject:            x.Subject.CommonName,
		Issuer:             x.Issuer.CommonName,
		Serial:             strings.ToUpper(x.SerialNumber.Text(16)),
		NotBefore:          x.NotBefore.UTC(),
		NotAfter:           x.NotAfter.UTC(),
		Fingerprint:        hex.EncodeToString(sum[:]),
		KeyAlgorithm:       keyAlg,
		KeyBits:            keyBits,
		SignatureAlgorithm: x.SignatureAlgorithm.String(),
		SANs:               sansOf(x),
		IsCA:               x.IsCA,
		SelfSigned:         x.Subject.String() == x.Issuer.String(),
	}
}

// keyInfo reports the public key's algorithm and a comparable strength in
// bits. For ECDSA and Ed25519 the "bits" figure is the curve size, not an
// RSA-equivalent — the weak-key check (checks.go) therefore compares against
// algorithm-specific floors rather than one number.
func keyInfo(x *x509.Certificate) (string, int) {
	switch pub := x.PublicKey.(type) {
	case *rsa.PublicKey:
		return "RSA", pub.N.BitLen()
	case *ecdsa.PublicKey:
		return "ECDSA", pub.Curve.Params().BitSize
	case ed25519.PublicKey:
		return "Ed25519", 256
	default:
		return x.PublicKeyAlgorithm.String(), 0
	}
}

func sansOf(x *x509.Certificate) []SAN {
	out := make([]SAN, 0, len(x.DNSNames)+len(x.IPAddresses))
	for _, d := range x.DNSNames {
		out = append(out, SAN{Type: SANDNS, Value: d})
	}
	for _, ip := range x.IPAddresses {
		out = append(out, SAN{Type: SANIP, Value: ip.String()})
	}
	return out
}

// DNSNames returns just the DNS SANs, in certificate order.
func (c Certificate) DNSNames() []string {
	var out []string
	for _, s := range c.SANs {
		if s.Type == SANDNS {
			out = append(out, s.Value)
		}
	}
	return out
}

// IPAddresses returns just the IP SANs, in certificate order.
func (c Certificate) IPAddresses() []string {
	var out []string
	for _, s := range c.SANs {
		if s.Type == SANIP {
			out = append(out, s.Value)
		}
	}
	return out
}

// Covers reports whether this certificate's SANs authenticate the identity
// `name` — an IP literal or a hostname.
//
// This mirrors what crypto/tls will actually do at handshake time, which is
// the only thing that matters: a helper that were more permissive than
// verification would produce a green check for a connection that then fails.
// Three real-PVE details it therefore has to handle:
//
//   - Trailing-dot FQDNs. Real pve-ssl.pem carries `DNS:pvecube.localdomain.`
//     with a root dot; crypto/x509 strips one trailing dot from both sides
//     before comparing, so `pvecube.localdomain` must match it.
//   - Case. DNS comparison is case-insensitive.
//   - Wildcards. `*.example.com` matches exactly one label, and never matches
//     the bare domain — the same rule crypto/x509 implements.
//
// An IP literal is matched only against IP SANs, never against a DNS SAN that
// happens to look like an address; that is also what crypto/x509 does, and
// conflating them is how a certificate appears to cover an address it does not.
func (c Certificate) Covers(name string) bool {
	if name == "" {
		return false
	}
	if ip := net.ParseIP(name); ip != nil {
		for _, s := range c.SANs {
			if s.Type == SANIP && net.ParseIP(s.Value) != nil && net.ParseIP(s.Value).Equal(ip) {
				return true
			}
		}
		return false
	}
	host := normalizeHost(name)
	for _, s := range c.SANs {
		if s.Type != SANDNS {
			continue
		}
		if matchHost(normalizeHost(s.Value), host) {
			return true
		}
	}
	return false
}

// normalizeHost lowercases and strips a single trailing dot, matching
// crypto/x509's own comparison preparation.
func normalizeHost(h string) string {
	h = strings.ToLower(h)
	return strings.TrimSuffix(h, ".")
}

// matchHost compares a (already normalized) SAN pattern against a host.
func matchHost(pattern, host string) bool {
	if pattern == host {
		return true
	}
	rest, ok := strings.CutPrefix(pattern, "*.")
	if !ok {
		return false
	}
	// A wildcard covers exactly one label and never the bare domain:
	// "*.example.com" matches "a.example.com" but not "example.com" and not
	// "a.b.example.com".
	idx := strings.IndexByte(host, '.')
	if idx < 0 {
		return false
	}
	return host[idx+1:] == rest
}

// ExpiresIn reports how long until this certificate expires, relative to now.
// Negative for an already-expired certificate.
func (c Certificate) ExpiresIn(now time.Time) time.Duration {
	return c.NotAfter.Sub(now)
}

// expectsClusterCA reports whether this certificate is one the PVE cluster CA
// is supposed to have issued.
//
// A KindCustom certificate (pveproxy-ssl.pem) is normally issued by a public
// CA or by ACME — checking it against the cluster CA would flag every
// correctly-configured Let's Encrypt deployment as broken, which is how a
// check trains operators to ignore it.
func (c Certificate) expectsClusterCA() bool { return c.Kind == KindNodeLeaf }
