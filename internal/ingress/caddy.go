// caddy.go discovers a Caddy instance via its admin API's own read-only
// `GET /reverse_proxy/upstreams` endpoint (Caddy's documented introspection
// route: the currently active reverse_proxy upstreams and their live
// health/request counters) — a plain HTTP GET against the admin API, never
// a POST to `/load` or any other admin-API mutation route.

package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CaddyDiscoverer implements IngressDiscoverer for Caddy's admin API.
type CaddyDiscoverer struct {
	Client *http.Client
}

const caddyUpstreamsPath = "/reverse_proxy/upstreams"

func (d *CaddyDiscoverer) Discover(ctx context.Context, target Target) (ProxyState, error) {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := newRequest(ctx, target, caddyUpstreamsPath)
	if err != nil {
		return ProxyState{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindCaddy, Reachable: false, Error: err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ProxyState{TargetID: target.ID, Kind: KindCaddy, Reachable: false, Error: fmt.Sprintf("unexpected status %d", resp.StatusCode)}, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDiscoverBodyBytes))
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindCaddy, Reachable: false, Error: err.Error()}, nil
	}
	backends, err := ParseCaddyUpstreams(body)
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindCaddy, Reachable: false, Error: err.Error()}, nil
	}
	return ProxyState{TargetID: target.ID, Kind: KindCaddy, Reachable: true, Backends: backends}, nil
}

// caddyUpstream mirrors Caddy's real `GET /reverse_proxy/upstreams` element
// shape: `{address, num_requests, fails}` per currently-active upstream.
type caddyUpstream struct {
	Address     string `json:"address"`
	NumRequests int    `json:"num_requests"`
	Fails       int    `json:"fails"`
}

// ParseCaddyUpstreams parses a `GET /reverse_proxy/upstreams` JSON array
// response. Caddy's own endpoint carries no route/pool name per upstream
// (unlike nginx Plus/Traefik) — Backend.Route is left empty. An upstream
// is Healthy when Caddy currently reports zero recent failures for it.
func ParseCaddyUpstreams(body []byte) ([]Backend, error) {
	var ups []caddyUpstream
	if err := json.Unmarshal(body, &ups); err != nil {
		return nil, fmt.Errorf("ingress: parsing caddy upstreams: %w", err)
	}
	out := make([]Backend, 0, len(ups))
	for _, u := range ups {
		out = append(out, Backend{Address: u.Address, Healthy: u.Fails == 0})
	}
	return out, nil
}
