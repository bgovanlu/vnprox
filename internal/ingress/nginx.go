// nginx.go discovers an nginx instance via its status endpoint. Two real
// nginx status formats exist, and this discoverer auto-detects which one a
// target speaks from the response's Content-Type (both reachable only by a
// plain HTTP GET):
//
//   - Open-source nginx's `stub_status` module: a fixed plain-text block
//     (Active connections / accepts-handled-requests / Reading-Writing-
//     Waiting). It reports aggregate connection counters only — no
//     per-backend list, since stub_status doesn't expose upstream server
//     identity at all. Correlation (chains.go) simply finds no backends
//     for a target that only ever speaks this format.
//   - nginx Plus's status API (`/api/<ver>/http/upstreams`): a JSON object
//     keyed by upstream-pool name, each carrying a `peers` array of
//     `{server, state}` — the shape that actually names backend dial
//     addresses, so this is what T-1406's own correlation examples use.

package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// NginxDiscoverer implements IngressDiscoverer for nginx (open-source
// stub_status or Plus API, auto-detected per-response).
type NginxDiscoverer struct {
	Client *http.Client
}

func (d *NginxDiscoverer) Discover(ctx context.Context, target Target) (ProxyState, error) {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := newRequest(ctx, target, "")
	if err != nil {
		return ProxyState{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindNginx, Reachable: false, Error: err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ProxyState{TargetID: target.ID, Kind: KindNginx, Reachable: false, Error: fmt.Sprintf("unexpected status %d", resp.StatusCode)}, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDiscoverBodyBytes))
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindNginx, Reachable: false, Error: err.Error()}, nil
	}
	backends, err := ParseNginxStatus(body, resp.Header.Get("Content-Type"))
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindNginx, Reachable: false, Error: err.Error()}, nil
	}
	return ProxyState{TargetID: target.ID, Kind: KindNginx, Reachable: true, Backends: backends}, nil
}

// nginxPlusUpstreams is the (deliberately narrowed) subset of nginx Plus's
// real `/api/<ver>/http/upstreams` response shape this parser reads: a map
// of upstream-pool name -> its peer list.
type nginxPlusUpstreams map[string]struct {
	Peers []struct {
		Server string `json:"server"`
		State  string `json:"state"`
	} `json:"peers"`
}

// ParseNginxStatus parses either nginx status format. contentType decides
// which: a JSON content type (or a body that simply starts with '{') is
// parsed as the Plus API upstreams shape; anything else is parsed as
// stub_status plain text (which never yields backends — see this file's
// doc comment).
func ParseNginxStatus(body []byte, contentType string) ([]Backend, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.Contains(contentType, "json") || strings.HasPrefix(trimmed, "{") {
		var ups nginxPlusUpstreams
		if err := json.Unmarshal(body, &ups); err != nil {
			return nil, fmt.Errorf("ingress: parsing nginx plus upstreams: %w", err)
		}
		var out []Backend
		for name, u := range ups {
			for _, p := range u.Peers {
				out = append(out, Backend{
					Route: name, Address: p.Server, Healthy: p.State == "up",
				})
			}
		}
		return out, nil
	}
	// stub_status text — validate it at least looks like the real format
	// before reporting "reachable" (an unparseable body on a non-JSON
	// content type is a real discovery failure, not a silent empty list).
	if !strings.Contains(trimmed, "Active connections:") {
		return nil, fmt.Errorf("ingress: response does not look like nginx stub_status or Plus API output")
	}
	return nil, nil
}
