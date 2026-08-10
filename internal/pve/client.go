package pve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// maxResponseBody caps how much of a response body this client will read
// into memory. PVE API responses (config listings, firewall rulesets) are
// small; this is a defensive bound against a misbehaving or malicious
// endpoint, not a real-world limit.
const maxResponseBody = 16 << 20 // 16 MiB

// Client is a typed PVE API client. Construct with New. A Client is safe
// for concurrent use.
type Client struct {
	baseURL *url.URL
	httpc   *http.Client
	auth    authenticator
	log     *slog.Logger
	// rec is non-nil only in record mode (T-2502, VNPROX_PVE_RECORD). See
	// record.go: it observes responses on the way past and writes
	// cassettes, and a failed write fails the request.
	rec *recorder
}

// endpointURL builds the full URL for a PVE API path (e.g.
// "/cluster/status"), which is always rooted at "/api2/json".
func (c *Client) endpointURL(path string) *url.URL {
	u := *c.baseURL
	u.Path = c.baseURL.Path + "/api2/json" + path
	return &u
}

// pveEnvelope mirrors the {"data": ...} shape every successful PVE API
// response uses.
type pveEnvelope struct {
	Data json.RawMessage `json:"data"`
}

// requestParams bundles a request body (marshaled to JSON) and/or a query
// string for a single call.
type requestParams struct {
	query url.Values
	body  any
}

// do performs one authenticated PVE API call: it applies the client's auth
// mode (renewing a ticket first if needed), sends the request, maps
// non-2xx responses to typed errors, and — if out is non-nil — decodes the
// envelope's "data" field into out.
func (c *Client) do(ctx context.Context, method, path string, params requestParams, out any) error {
	if c.auth != nil {
		if err := c.auth.prepare(ctx, c); err != nil {
			return err
		}
	}

	u := c.endpointURL(path)
	if len(params.query) > 0 {
		u.RawQuery = params.query.Encode()
	}

	var bodyReader io.Reader
	var contentType string
	if params.body != nil {
		encoded, err := json.Marshal(params.body)
		if err != nil {
			return fmt.Errorf("pve: encoding request body for %s: %w", path, err)
		}
		bodyReader = bytes.NewReader(encoded)
		contentType = "application/json"
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		return fmt.Errorf("pve: building request for %s: %w", path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.auth != nil {
		c.auth.apply(req)
	}

	status, body, err := c.rawDo(req, path)
	if err != nil {
		return err
	}

	if status >= 400 {
		return mapHTTPError(status, body)
	}

	if out != nil {
		var env pveEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return fmt.Errorf("pve: decoding response envelope for %s: %w", path, err)
		}
		if len(env.Data) == 0 || string(env.Data) == "null" {
			return nil
		}
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("pve: decoding response data for %s: %w", path, err)
		}
	}
	return nil
}

// rawDo sends req and returns the raw status code and body, mapping
// transport-level failures (DNS, connection refused, TLS, context
// cancellation) to *ErrPVETransport. It never inspects the PVE envelope —
// callers decide how to interpret status/body. logPath is what gets
// logged (never headers, cookies, or the request/response body, which may
// carry ticket/CSRF/token values — see docs/security.md and T-604).
func (c *Client) rawDo(req *http.Request, logPath string) (status int, body []byte, err error) {
	start := time.Now()
	resp, doErr := c.httpc.Do(req)
	duration := time.Since(start)

	if doErr != nil {
		c.log.Debug("pve request", "method", req.Method, "path", logPath, "duration", duration, "error", doErr)
		return 0, nil, &ErrPVETransport{Err: doErr}
	}
	defer func() { _ = resp.Body.Close() }()

	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if readErr != nil {
		c.log.Debug("pve request", "method", req.Method, "path", logPath, "status", resp.StatusCode, "duration", duration, "error", readErr)
		return 0, nil, &ErrPVETransport{Err: fmt.Errorf("reading response body: %w", readErr)}
	}

	c.log.Debug("pve request", "method", req.Method, "path", logPath, "status", resp.StatusCode, "duration", duration)

	// Record mode (T-2502). Deliberately after the body has been read in
	// full and before any status interpretation: a 403's body is as worth
	// recording as a 200's, since internal/pve's own error mapping is
	// written against it. A failed write fails the request — see
	// recorder's doc comment for why silence is the wrong answer here.
	if c.rec != nil {
		if recErr := c.rec.record(req, resp.StatusCode, data); recErr != nil {
			return 0, nil, fmt.Errorf("pve: recording %s %s: %w", req.Method, logPath, recErr)
		}
	}

	return resp.StatusCode, data, nil
}
