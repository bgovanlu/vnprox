package ipam

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ExternalHTTPConfig is the connection config a NetBox/phpIPAM write client
// needs — exactly the fields item 2's `sdn.ipam.*` op family stores on a
// configured PVE ipam plugin instance (internal/pve.IPAM: URL/Token/
// Fingerprint), so cmd/vnproxd's wiring constructs one of these straight
// from a live `netbox`/`phpipam`-type entry in `GET /cluster/sdn/ipams`
// rather than inventing a second, vnprox-side settings surface (T-3104
// item 3's own scoping: "reuse PVE's own credential storage"). Token is
// deliberately the caller's problem to supply fresh: internal/pve/
// sdn_ipam.go's package doc comment explains why a read of the PVE object
// never carries it back — see NewNetBoxClient's/NewPhpIPAMClient's own doc
// comment for the operational consequence.
type ExternalHTTPConfig struct {
	HTTPClient  *http.Client
	BaseURL     string
	Token       string
	Fingerprint string
	Timeout     time.Duration
}

// httpClient returns cfg.HTTPClient if set, else a client built from
// cfg.Fingerprint/cfg.Timeout — fingerprint-pinned via VerifyPeerCertificate
// when a fingerprint is configured (crypto/tls's InsecureSkipVerify plus a
// manual chain check, the standard Go pattern for pinning a single leaf
// certificate's fingerprint rather than trusting a CA), otherwise the
// default system trust store.
func (cfg ExternalHTTPConfig) httpClient() (*http.Client, error) {
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient, nil
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	if cfg.Fingerprint == "" {
		return &http.Client{Timeout: timeout}, nil
	}
	want, err := normalizeFingerprint(cfg.Fingerprint)
	if err != nil {
		return nil, fmt.Errorf("ipam: parsing external IPAM certificate fingerprint: %w", err)
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // deliberate: VerifyPeerCertificate below pins the exact leaf fingerprint instead of chain trust, the same model PVE's own --fingerprint gives a self-signed endpoint.
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return fmt.Errorf("ipam: external IPAM TLS handshake presented no certificate")
				}
				got := sha256.Sum256(rawCerts[0])
				if fmt.Sprintf("%x", got) != want {
					return fmt.Errorf("ipam: external IPAM certificate fingerprint mismatch (configured fingerprint does not match the presented certificate)")
				}
				return nil
			},
		},
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// normalizeFingerprint turns PVE's captured "AA:BB:...:FF" colon-separated
// hex form into the bare lowercase hex string sha256.Sum256 comparisons use.
func normalizeFingerprint(fp string) (string, error) {
	fp = strings.ToLower(strings.ReplaceAll(fp, ":", ""))
	if len(fp) != sha256.Size*2 {
		return "", fmt.Errorf("fingerprint %q is not a 32-byte SHA-256 hex string", fp)
	}
	return fp, nil
}

// doExternalRequest is a tiny shared do-request-and-check-status helper
// both netbox.go and phpipam.go build their own request bodies/paths around
// — neither system's request/response shape is similar enough to share more
// than this (NetBox is a flat REST collection; phpIPAM nests addresses
// under subnets), so this stays deliberately thin rather than forcing a
// shared "generic external IPAM request" abstraction neither API actually
// has in common.
func doExternalRequest(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("ipam: external IPAM request %s %s: %w", req.Method, req.URL.Path, err)
	}
	return resp, nil
}
