package ipam

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// PhpIPAMClient implements ExternalIPAMClient against phpIPAM's REST API
// (modeled against phpIPAM's documented `/api/{app_id}/` surface —
// https://phpipam.net/api/api_documentation/ — as of the 1.4/1.5 API,
// which has been stable for several major releases). There is no real
// phpIPAM instance on pvecube to verify this against — see
// planning/reports/needs-hardware-validation.md's T-3104 entry for the two
// points flagged below, both more consequential than NetBoxClient's own
// flagged gap:
//
//  1. **Authentication.** phpIPAM's documented flow is a session-token
//     exchange: POST /api/{app}/user/ with HTTP Basic auth (an
//     app-scoped username/password) returns a short-lived token, which
//     every subsequent request then carries as a `token` header, and which
//     eventually expires and must be re-exchanged. internal/pve.IPAM (the
//     config this client is built from) carries exactly one opaque string
//     (Token) — not a username/password pair — so this client instead
//     sends that single value directly as the `token` header on every
//     request, the same static-token model NetBoxClient uses. Some phpIPAM
//     deployments do support a long-lived static "App API token" configured
//     this way (bypassing the session exchange), but this is this client's
//     own assumption, not a confirmed behavior — if a real deployment's
//     App API is configured session-exchange-only, every call here would
//     fail authentication and this client needs the exchange flow added.
//  2. **No flat "list every address" endpoint.** Unlike NetBox's single
//     `/ip-addresses/` collection, phpIPAM's documented API organizes
//     addresses under their owning subnet
//     (`/api/{app}/subnets/{id}/addresses/`) — there is no cluster-wide
//     address list. ListRecords therefore lists every subnet first
//     (`/api/{app}/subnets/`) and fans out one addresses request per
//     subnet, aggregating the results — correct per the documented API
//     shape, but unverified against how a real deployment's app scoping
//     (which subnets a given App API token can actually see) behaves.
type PhpIPAMClient struct {
	client  *http.Client
	baseURL string
	appID   string
	token   string
}

// NewPhpIPAMClient builds a PhpIPAMClient from cfg and appID (phpIPAM's own
// per-application API identifier, configured in phpIPAM itself — not
// carried by internal/pve.IPAM, since PVE's ipam config has no field for
// it; cmd/vnproxd's wiring must supply it separately, e.g. from the daemon
// config alongside the still-undetermined token-supply mechanism this
// file's package doc comment on NewNetBoxClient flags for the token
// itself). See PhpIPAMClient's own doc comment for the two flagged,
// hardware-unverified points this client's behavior depends on.
func NewPhpIPAMClient(cfg ExternalHTTPConfig, appID string) (*PhpIPAMClient, error) {
	client, err := cfg.httpClient()
	if err != nil {
		return nil, err
	}
	return &PhpIPAMClient{baseURL: strings.TrimRight(cfg.BaseURL, "/"), appID: appID, token: cfg.Token, client: client}, nil
}

type phpIPAMSubnet struct {
	ID     string `json:"id"`
	Subnet string `json:"subnet"` // network address, e.g. "10.0.0.0"
	Mask   string `json:"mask"`   // prefix length, e.g. "24"
}

type phpIPAMAddress struct {
	ID       string `json:"id"`
	SubnetID string `json:"subnetId"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
}

type phpIPAMEnvelope[T any] struct {
	Data    T      `json:"data"`
	Message string `json:"message,omitempty"`
	Success bool   `json:"success"`
}

func (c *PhpIPAMClient) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("ipam: encoding phpIPAM request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/"+c.appID+path, reader)
	if err != nil {
		return nil, fmt.Errorf("ipam: building phpIPAM request: %w", err)
	}
	// See this type's doc comment (flagged point 1): sent as a static app
	// token, not phpIPAM's documented session-exchange result.
	req.Header.Set("token", c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// listSubnets calls GET /api/{app}/subnets/.
func (c *PhpIPAMClient) listSubnets(ctx context.Context) ([]phpIPAMSubnet, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/subnets/", nil)
	if err != nil {
		return nil, err
	}
	resp, err := doExternalRequest(ctx, c.client, req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ipam: phpIPAM GET subnets: status %d", resp.StatusCode)
	}
	var env phpIPAMEnvelope[[]phpIPAMSubnet]
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("ipam: decoding phpIPAM subnets response: %w", err)
	}
	return env.Data, nil
}

// ListRecords aggregates every subnet's address list — see this type's own
// doc comment (flagged point 2) on why there is no single endpoint for
// this.
func (c *PhpIPAMClient) ListRecords(ctx context.Context) ([]ExternalRecord, error) {
	subnets, err := c.listSubnets(ctx)
	if err != nil {
		return nil, err
	}
	var out []ExternalRecord
	for _, s := range subnets {
		req, err := c.newRequest(ctx, http.MethodGet, "/subnets/"+s.ID+"/addresses/", nil)
		if err != nil {
			return nil, err
		}
		resp, err := doExternalRequest(ctx, c.client, req)
		if err != nil {
			return nil, err
		}
		var env phpIPAMEnvelope[[]phpIPAMAddress]
		decErr := json.NewDecoder(resp.Body).Decode(&env)
		closeErr := resp.Body.Close()
		// phpIPAM answers an empty subnet with success:false ("no addresses
		// found") rather than an empty array — not an error condition for
		// this aggregation.
		if resp.StatusCode == http.StatusOK && decErr == nil {
			for _, a := range env.Data {
				out = append(out, ExternalRecord{IP: a.IP, Hostname: a.Hostname})
			}
		}
		if closeErr != nil {
			return nil, fmt.Errorf("ipam: reading phpIPAM addresses response for subnet %s: %w", s.ID, closeErr)
		}
	}
	return out, nil
}

// subnetContaining finds which of subnets contains ip (client-side prefix
// containment check against each subnet's own network+mask) — phpIPAM's
// create call requires an explicit subnetId; there is no "just take this IP"
// create form.
func subnetContaining(subnets []phpIPAMSubnet, ip string) (phpIPAMSubnet, bool) {
	target := net.ParseIP(ip)
	if target == nil {
		return phpIPAMSubnet{}, false
	}
	for _, s := range subnets {
		_, ipnet, err := net.ParseCIDR(s.Subnet + "/" + s.Mask)
		if err != nil {
			continue
		}
		if ipnet.Contains(target) {
			return s, true
		}
	}
	return phpIPAMSubnet{}, false
}

// CreateRecord resolves rec.IP's containing subnet, then
// POST /api/{app}/addresses/.
func (c *PhpIPAMClient) CreateRecord(ctx context.Context, rec ExternalRecord) error {
	subnets, err := c.listSubnets(ctx)
	if err != nil {
		return err
	}
	subnet, ok := subnetContaining(subnets, rec.IP)
	if !ok {
		return fmt.Errorf("ipam: no phpIPAM subnet contains %s; create the containing subnet in phpIPAM first", rec.IP)
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/addresses/", phpIPAMAddress{
		SubnetID: subnet.ID, IP: rec.IP, Hostname: rec.Hostname,
	})
	if err != nil {
		return err
	}
	resp, err := doExternalRequest(ctx, c.client, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ipam: phpIPAM POST addresses: status %d", resp.StatusCode)
	}
	return nil
}

// lookupAddress calls GET /api/{app}/addresses/search/{ip}/, phpIPAM's
// documented cross-subnet address search.
func (c *PhpIPAMClient) lookupAddress(ctx context.Context, ip string) (phpIPAMAddress, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/addresses/search/"+ip+"/", nil)
	if err != nil {
		return phpIPAMAddress{}, err
	}
	resp, err := doExternalRequest(ctx, c.client, req)
	if err != nil {
		return phpIPAMAddress{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return phpIPAMAddress{}, fmt.Errorf("ipam: phpIPAM has no address record for %s (status %d; nothing to update/delete)", ip, resp.StatusCode)
	}
	var env phpIPAMEnvelope[[]phpIPAMAddress]
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return phpIPAMAddress{}, fmt.Errorf("ipam: decoding phpIPAM search response: %w", err)
	}
	if len(env.Data) == 0 {
		return phpIPAMAddress{}, fmt.Errorf("ipam: phpIPAM has no address record for %s (nothing to update/delete)", ip)
	}
	return env.Data[0], nil
}

// UpdateRecord resolves rec.IP's address id then
// PATCH /api/{app}/addresses/{id}/.
func (c *PhpIPAMClient) UpdateRecord(ctx context.Context, rec ExternalRecord) error {
	addr, err := c.lookupAddress(ctx, rec.IP)
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPatch, "/addresses/"+addr.ID+"/", phpIPAMAddress{Hostname: rec.Hostname})
	if err != nil {
		return err
	}
	resp, err := doExternalRequest(ctx, c.client, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ipam: phpIPAM PATCH addresses/%s: status %d", addr.ID, resp.StatusCode)
	}
	return nil
}

// DeleteRecord resolves ip's address id then
// DELETE /api/{app}/addresses/{id}/.
func (c *PhpIPAMClient) DeleteRecord(ctx context.Context, ip string) error {
	addr, err := c.lookupAddress(ctx, ip)
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodDelete, "/addresses/"+addr.ID+"/", nil)
	if err != nil {
		return err
	}
	resp, err := doExternalRequest(ctx, c.client, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ipam: phpIPAM DELETE addresses/%s: status %d", addr.ID, resp.StatusCode)
	}
	return nil
}

var _ ExternalIPAMClient = (*PhpIPAMClient)(nil)
