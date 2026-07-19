// haproxy.go discovers an HAProxy instance via its classic HTTP stats page
// CSV export (`stats enable` + `stats uri ...` in haproxy.cfg — appending
// `;csv` to that URI, e.g. `http://host:8404/stats;csv`, is HAProxy's own
// documented read-only machine-readable stats format). This is a plain
// HTTP GET — never the admin/runtime unix socket, and never a POST to any
// HAProxy endpoint.

package ingress

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HAProxyDiscoverer implements IngressDiscoverer for HAProxy's HTTP stats
// CSV export.
type HAProxyDiscoverer struct {
	Client *http.Client
}

// haproxyCSVURL rewrites target.Address into its own `;csv` stats URL,
// per HAProxy's own documented convention (the `;csv` request modifier
// applies to the stats page's *path*, not simply concatenated onto a bare
// "host:port" — done via url.URL field assignment rather than string
// concatenation so a path-less Address like "http://host:8404" doesn't
// produce an invalid "http://host:8404;csv" — see net/url's own "invalid
// port after host" parse error for what naive concatenation would hit
// there). Already-`;csv`-suffixed addresses pass through unchanged.
func haproxyCSVURL(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("ingress: parsing haproxy target address %q: %w", address, err)
	}
	// Match the `;csv` modifier on the path specifically, not a bare "csv"
	// substring anywhere in the URL — a host or path merely containing "csv"
	// (e.g. "haproxy-csv.lan") must still get the `;csv` suffix appended
	// (review-T-1406 correctness fix).
	if strings.Contains(u.Path, ";csv") {
		return address, nil
	}
	switch u.Path {
	case "", "/":
		u.Path = "/;csv"
	default:
		u.Path += ";csv"
	}
	return u.String(), nil
}

// Discover fetches target.Address's HAProxy stats CSV and parses it into a
// ProxyState. Unreachable/malformed responses report Reachable: false with
// a descriptive Error, never an error return — a down proxy is an ordinary,
// expected GET /ingress/status outcome, not a route failure.
func (d *HAProxyDiscoverer) Discover(ctx context.Context, target Target) (ProxyState, error) {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	csvURL, err := haproxyCSVURL(target.Address)
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindHAProxy, Reachable: false, Error: err.Error()}, nil
	}
	req, err := newRequest(ctx, Target{ID: target.ID, Kind: target.Kind, Address: csvURL, Credential: target.Credential}, "")
	if err != nil {
		return ProxyState{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindHAProxy, Reachable: false, Error: err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ProxyState{TargetID: target.ID, Kind: KindHAProxy, Reachable: false, Error: fmt.Sprintf("unexpected status %d", resp.StatusCode)}, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDiscoverBodyBytes))
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindHAProxy, Reachable: false, Error: err.Error()}, nil
	}
	backends, err := ParseHAProxyCSV(body)
	if err != nil {
		return ProxyState{TargetID: target.ID, Kind: KindHAProxy, Reachable: false, Error: err.Error()}, nil
	}
	return ProxyState{TargetID: target.ID, Kind: KindHAProxy, Reachable: true, Backends: backends}, nil
}

// haproxyAggregateRows are HAProxy stats CSV's own two per-proxy summary
// rows (as opposed to one row per actual server) — never a discoverable
// backend.
var haproxyAggregateRows = map[string]bool{"FRONTEND": true, "BACKEND": true}

// ParseHAProxyCSV parses an HAProxy stats CSV export (its own documented
// `# pxname,svname,...` header line followed by one data row per
// frontend/backend/server) into the server rows only, skipping the
// FRONTEND/BACKEND aggregate rows. Column lookup is by header name, not
// fixed position — HAProxy's real stats CSV carries dozens of columns
// across versions; this parser only reads pxname/svname/status/addr, so it
// tolerates any superset of columns a real deployment sends. `addr`
// (server address:port, present since HAProxy 2.0) becomes Backend.Address
// when non-empty/non-"-"; older HAProxy without that column falls back to
// the server name (svname) — real, but not a dial address, so chain
// correlation (chains.go) simply finds nothing for it.
func ParseHAProxyCSV(data []byte) ([]Backend, error) {
	text := strings.TrimPrefix(strings.TrimSpace(string(data)), "# ")
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("ingress: parsing haproxy csv: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("ingress: haproxy csv has no header row")
	}
	idx := make(map[string]int, len(records[0]))
	for i, col := range records[0] {
		idx[strings.TrimSpace(col)] = i
	}
	pxI, pxOK := idx["pxname"]
	svI, svOK := idx["svname"]
	if !pxOK || !svOK {
		return nil, fmt.Errorf("ingress: haproxy csv missing pxname/svname columns")
	}
	statusI, hasStatus := idx["status"]
	addrI, hasAddr := idx["addr"]

	var out []Backend
	for _, row := range records[1:] {
		if svI >= len(row) || pxI >= len(row) {
			continue
		}
		sv := row[svI]
		if haproxyAggregateRows[sv] {
			continue
		}
		b := Backend{Route: row[pxI], Address: sv, Detail: sv}
		if hasAddr && addrI < len(row) && row[addrI] != "" && row[addrI] != "-" {
			b.Address = row[addrI]
		}
		if hasStatus && statusI < len(row) {
			b.Healthy = row[statusI] == "UP"
		}
		out = append(out, b)
	}
	return out, nil
}
