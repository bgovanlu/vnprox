// SPDX-License-Identifier: Apache-2.0

// client.go implements Client: a net/http-only, GET-only REST reader
// against a k8s API server's four fixed paths (see doc.go's read-only
// invariant). No other exported method exists on Client, and every one
// that does issues http.MethodGet only — enforced by zerowrite_test.go.

package k8s

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultTimeout bounds a single Client request when no context deadline
// is already set tighter than this — mirrors internal/ingress's per-call
// HTTP client timeout convention (each vendor discoverer/reader owns its
// own outer bound; a caller polling many clusters layers its own overall
// ceiling on top, the same two-level bound ingressDiscoverTimeout applies
// over internal/ingress's own per-vendor client timeouts).
const DefaultTimeout = 10 * time.Second

// Client is a read-only k8s API reader for exactly one cluster/context.
// Construct with NewClient (production: full TLS wiring from a
// ResolvedConfig) or by setting fields directly (tests/k8smock: an
// httptest server's own *http.Client and URL).
type Client struct {
	// HTTPClient issues every request; defaults to a client with
	// DefaultTimeout if nil.
	HTTPClient *http.Client
	// BaseURL is the k8s API server's base URL (ResolvedConfig.Server),
	// no trailing slash required.
	BaseURL string
	// Token is sent as `Authorization: Bearer <Token>` when non-empty;
	// empty when the underlying HTTPClient already authenticates via a
	// client certificate (NewClient wires the cert into the transport's
	// TLS config in that case, not this field).
	Token string
}

// NewClient builds a Client from a resolved kubeconfig context: an
// *http.Client with a TLS config trusting cfg.CAData (or the system pool
// when empty), cfg.InsecureSkipTLSVerify honored verbatim (a kubeconfig
// the operator supplied explicitly requested it — the same trust posture
// `kubectl` itself gives that flag), and a client certificate loaded when
// cfg.ClientCertData/ClientKeyData are set.
func NewClient(cfg ResolvedConfig) (*Client, error) {
	if cfg.Server == "" {
		return nil, ErrNoServer
	}

	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.InsecureSkipTLSVerify} //nolint:gosec // operator-opted-in via kubeconfig, mirrors kubectl's own trust posture
	if len(cfg.CAData) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cfg.CAData) {
			return nil, fmt.Errorf("k8s: certificate-authority-data did not contain a usable PEM certificate")
		}
		tlsCfg.RootCAs = pool
	}
	if len(cfg.ClientCertData) > 0 && len(cfg.ClientKeyData) > 0 {
		cert, err := tls.X509KeyPair(cfg.ClientCertData, cfg.ClientKeyData)
		if err != nil {
			return nil, fmt.Errorf("k8s: loading client certificate/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return &Client{
		HTTPClient: &http.Client{
			Timeout:   DefaultTimeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
		BaseURL: strings.TrimSuffix(cfg.Server, "/"),
		Token:   cfg.Token,
	}, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: DefaultTimeout}
}

// getJSON issues exactly one http.MethodGet request against
// c.BaseURL+path and decodes a JSON response body into out. This is the
// **only** place in this package that constructs an *http.Request — every
// Client method funnels through it, so the read-only invariant reduces to
// "getJSON is never called with anything but http.MethodGet", which it
// always is (the method is hardcoded, not a parameter).
func getJSON(ctx context.Context, c *Client, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("k8s: building request for %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("k8s: requesting %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("k8s: %s returned %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("k8s: decoding response from %s: %w", path, err)
	}
	return nil
}

// Nodes calls GET /api/v1/nodes.
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var list NodeList
	if err := getJSON(ctx, c, "/api/v1/nodes", &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// Pods calls GET /api/v1/pods (cluster-wide).
func (c *Client) Pods(ctx context.Context) ([]Pod, error) {
	var list PodList
	if err := getJSON(ctx, c, "/api/v1/pods", &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// Services calls GET /api/v1/services (cluster-wide).
func (c *Client) Services(ctx context.Context) ([]Service, error) {
	var list ServiceList
	if err := getJSON(ctx, c, "/api/v1/services", &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// KubeSystemDaemonSets calls
// GET /apis/apps/v1/namespaces/kube-system/daemonsets — the fixed,
// single-namespace path this package's CNI detection needs (Calico's
// calico-node / Cilium's cilium DaemonSet both run in kube-system). Not
// parameterized over namespace: this package has no reason to ever read a
// DaemonSet outside kube-system, so it does not expose the capability.
func (c *Client) KubeSystemDaemonSets(ctx context.Context) ([]DaemonSet, error) {
	var list DaemonSetList
	if err := getJSON(ctx, c, "/apis/apps/v1/namespaces/kube-system/daemonsets", &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}
