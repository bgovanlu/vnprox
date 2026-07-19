// traefik.go discovers a Traefik instance via its API's own read-only
// `GET /api/http/services` endpoint (Traefik's documented introspection
// route: every currently configured HTTP service, its load-balancer
// servers, and its enabled/disabled status) — a plain HTTP GET, never a
// call into Traefik's dynamic-configuration provider API.

package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// TraefikDiscoverer implements IngressDiscoverer for Traefik's API.
type TraefikDiscoverer struct {
	Client *http.Client
}

const traefikServicesPath = "/api/http/services"

func (d *TraefikDiscoverer) Discover(ctx context.Context, target Target) (ProxyState, error) {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := newRequest(ctx, target, traefikServicesPath)
	if err != nil {
		return ProxyState{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindTraefik, Reachable: false, Error: err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ProxyState{TargetID: target.ID, Kind: KindTraefik, Reachable: false, Error: fmt.Sprintf("unexpected status %d", resp.StatusCode)}, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDiscoverBodyBytes))
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindTraefik, Reachable: false, Error: err.Error()}, nil
	}
	backends, err := ParseTraefikServices(body)
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindTraefik, Reachable: false, Error: err.Error()}, nil
	}
	return ProxyState{TargetID: target.ID, Kind: KindTraefik, Reachable: true, Backends: backends}, nil
}

// traefikService mirrors Traefik's real `GET /api/http/services` element
// shape, narrowed to the fields this discoverer needs.
type traefikService struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	LoadBalancer struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
	} `json:"loadBalancer"`
}

// ParseTraefikServices parses a `GET /api/http/services` JSON array
// response. Traefik reports health at the service level, not per
// individual server, so every server under an "enabled" service is
// reported Healthy — a documented simplification, not a per-backend health
// probe.
func ParseTraefikServices(body []byte) ([]Backend, error) {
	var services []traefikService
	if err := json.Unmarshal(body, &services); err != nil {
		return nil, fmt.Errorf("ingress: parsing traefik services: %w", err)
	}
	var out []Backend
	for _, svc := range services {
		healthy := strings.EqualFold(svc.Status, "enabled")
		for _, srv := range svc.LoadBalancer.Servers {
			out = append(out, Backend{Route: svc.Name, Address: stripURLScheme(srv.URL), Healthy: healthy})
		}
	}
	return out, nil
}

// stripURLScheme trims a leading "scheme://" from a Traefik server URL
// (e.g. "http://10.0.0.8:5000" -> "10.0.0.8:5000") so Backend.Address is a
// bare host:port like every other vendor's, for chains.go's HostOnly to
// compare uniformly.
func stripURLScheme(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		return u[i+3:]
	}
	return u
}
