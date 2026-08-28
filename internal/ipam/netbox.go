// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// NetBoxClient implements ExternalIPAMClient against NetBox's IPAM REST API
// (modeled against NetBox's documented `/api/ipam/ip-addresses/` collection
// as of NetBox 3.x/4.x — https://netbox.readthedocs.io/en/stable/rest-api/
// — the API surface has been stable across that range). There is no real
// NetBox instance on pvecube to verify this against (CLAUDE.md's
// capture-from-hardware discipline only covers PVE itself, not a
// third-party system vnprox merely talks to) — see
// planning/reports/needs-hardware-validation.md's T-3104 entry for the
// specific points flagged below.
//
// NetBox always stores an address with a prefix length (its own data
// model has no bare-host-address concept); this client writes every record
// as a /32 (docs/features/ipam.md's ExternalRecord carries no prefix of its
// own — vnprox's sync target is "this host address exists", not "this
// subnet exists in NetBox too"). **Flagged, unconfirmed against a real
// instance**: whether a real NetBox deployment already holds these same
// addresses under a different prefix length (e.g. as part of an existing
// /24 aggregate) — in which case this client's own reads (ListRecords)
// would still find them (address matching in NetBox does not require an
// exact prefix-length match for GET), but a Create for an address NetBox's
// operator expects at a different prefix could produce a duplicate-looking
// entry rather than reusing the existing one. Update/Delete resolve NetBox's
// internal numeric id by filtering on the same /32-qualified address this
// client's own Create writes, which also could miss a manually-created
// record at a different prefix length — the same flagged gap.
type NetBoxClient struct {
	client  *http.Client
	baseURL string
	token   string
}

// NewNetBoxClient builds a NetBoxClient from cfg. cfg.Token must be
// supplied fresh at construction time — see this package's
// ExternalHTTPConfig doc comment: a `netbox`-type SdnIpam entry read back
// from PVE never carries its own token (write-only field), so cmd/vnproxd's
// wiring cannot simply "read it back" at daemon start the way it does
// URL/Fingerprint. **This is the honest, currently-unresolved consequence,
// flagged rather than worked around**: production wiring needs a still-
// undetermined mechanism to supply the token at daemon start (e.g. a
// daemon-local secret store keyed by ipam id, populated once at
// sdn.ipam.create time and never round-tripped through PVE) — see
// cmd/vnproxd/server.go's wiring comment and
// planning/reports/needs-hardware-validation.md.
func NewNetBoxClient(cfg ExternalHTTPConfig) (*NetBoxClient, error) {
	client, err := cfg.httpClient()
	if err != nil {
		return nil, err
	}
	return &NetBoxClient{baseURL: strings.TrimRight(cfg.BaseURL, "/"), token: cfg.Token, client: client}, nil
}

// netboxIPAddress mirrors the fields of NetBox's ip-address object this
// client reads/writes — a small subset of NetBox's actual (much larger)
// object; every other field is left untouched by omitting it from request
// bodies (NetBox's PATCH is a partial update).
type netboxIPAddress struct {
	Address string `json:"address"`
	DNSName string `json:"dns_name,omitempty"`
	ID      int    `json:"id,omitempty"`
}

type netboxListResponse struct {
	Next    *string           `json:"next"`
	Results []netboxIPAddress `json:"results"`
	Count   int               `json:"count"`
}

func (c *NetBoxClient) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("ipam: encoding NetBox request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("ipam: building NetBox request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// ListRecords calls GET /api/ipam/ip-addresses/, following NetBox's own
// cursor-style pagination (`next`) until exhausted. Gateway/broadcast-only
// filtering is not applied here — every address NetBox returns becomes an
// ExternalRecord; diffRecords (sync.go) is what decides which ones matter
// to vnprox's own allocation set.
func (c *NetBoxClient) ListRecords(ctx context.Context) ([]ExternalRecord, error) {
	var out []ExternalRecord
	path := "/api/ipam/ip-addresses/?limit=1000"
	for path != "" {
		req, err := c.newRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		resp, err := doExternalRequest(ctx, c.client, req)
		if err != nil {
			return nil, err
		}
		var page netboxListResponse
		decErr := json.NewDecoder(resp.Body).Decode(&page)
		closeErr := resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("ipam: NetBox GET %s: status %d", path, resp.StatusCode)
		}
		if decErr != nil {
			return nil, fmt.Errorf("ipam: decoding NetBox response: %w", decErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("ipam: reading NetBox response: %w", closeErr)
		}
		for _, r := range page.Results {
			out = append(out, ExternalRecord{IP: bareIP(r.Address), Hostname: r.DNSName})
		}
		if page.Next == nil {
			break
		}
		next, perr := url.Parse(*page.Next)
		if perr != nil {
			return nil, fmt.Errorf("ipam: parsing NetBox pagination link: %w", perr)
		}
		path = next.RequestURI()
	}
	return out, nil
}

// CreateRecord calls POST /api/ipam/ip-addresses/ — see this type's doc
// comment on the /32 convention.
func (c *NetBoxClient) CreateRecord(ctx context.Context, rec ExternalRecord) error {
	req, err := c.newRequest(ctx, http.MethodPost, "/api/ipam/ip-addresses/", netboxIPAddress{
		Address: rec.IP + "/32", DNSName: rec.Hostname,
	})
	if err != nil {
		return err
	}
	resp, err := doExternalRequest(ctx, c.client, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("ipam: NetBox POST ip-addresses: status %d", resp.StatusCode)
	}
	return nil
}

// UpdateRecord resolves the record's NetBox id (a GET filtered by address)
// then PATCHes its dns_name.
func (c *NetBoxClient) UpdateRecord(ctx context.Context, rec ExternalRecord) error {
	id, err := c.lookupID(ctx, rec.IP)
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/ipam/ip-addresses/%d/", id), netboxIPAddress{DNSName: rec.Hostname})
	if err != nil {
		return err
	}
	resp, err := doExternalRequest(ctx, c.client, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ipam: NetBox PATCH ip-addresses/%d: status %d", id, resp.StatusCode)
	}
	return nil
}

// DeleteRecord resolves the record's NetBox id then DELETEs it.
func (c *NetBoxClient) DeleteRecord(ctx context.Context, ip string) error {
	id, err := c.lookupID(ctx, ip)
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/ipam/ip-addresses/%d/", id), nil)
	if err != nil {
		return err
	}
	resp, err := doExternalRequest(ctx, c.client, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("ipam: NetBox DELETE ip-addresses/%d: status %d", id, resp.StatusCode)
	}
	return nil
}

// lookupID finds ip's NetBox object id by filtering on the exact /32
// address this client's own Create writes — see this type's doc comment on
// why a record NetBox already holds at a different prefix length would be
// missed here.
func (c *NetBoxClient) lookupID(ctx context.Context, ip string) (int, error) {
	q := url.Values{"address": {ip + "/32"}}
	req, err := c.newRequest(ctx, http.MethodGet, "/api/ipam/ip-addresses/?"+q.Encode(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := doExternalRequest(ctx, c.client, req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ipam: NetBox GET ip-addresses?address=%s: status %d", ip, resp.StatusCode)
	}
	var page netboxListResponse
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return 0, fmt.Errorf("ipam: decoding NetBox lookup response: %w", err)
	}
	if len(page.Results) == 0 {
		return 0, fmt.Errorf("ipam: NetBox has no ip-address record for %s (nothing to update/delete)", ip)
	}
	return page.Results[0].ID, nil
}

// bareIP strips a NetBox "address/prefixlen" value down to the bare IP
// ExternalRecord carries.
func bareIP(cidr string) string {
	if i := strings.IndexByte(cidr, '/'); i >= 0 {
		return cidr[:i]
	}
	return cidr
}

var _ ExternalIPAMClient = (*NetBoxClient)(nil)
