// SPDX-License-Identifier: Apache-2.0

// client.go is a minimal HTTP client over vnproxd's documented /api/v1
// surface (docs/api.md in the main repo), authenticated exclusively with a
// T-1104 bearer token — the same mechanism cmd/vnproxctl/remoteclient.go
// uses. This file intentionally does NOT import anything from the main
// vnprox module: this provider is its own Go module (see this directory's
// go.mod and README.md's "Module boundary" section), so it speaks the wire
// contract in docs/api.md rather than sharing Go types with the daemon.
//
// THE LOAD-BEARING CONSTRAINT: every mutating method on this client stops
// at Create/Validate/Update-draft/Delete-draft. There is no ApplyChangeset,
// ConfirmChangeset, or RollbackChangeset method here, and there never should
// be — a `terraform apply` stages a draft changeset and stops; making it
// live is a human review action inside vnprox (see README.md). This mirrors
// internal/plugin.Stager and internal/mcp.ChangesetStager's compile-time
// stage-only seams in the main module; this client is the same shape,
// enforced by review rather than a compiler (a Go module boundary has no
// "this interface may not grow a method" mechanism to lean on the way an
// in-process interface satisfaction assertion does), which is why
// client_test.go's TestClient_HasNoApplyMethod exists: a reflection check
// over this type's method set, run in CI, so a future PR that adds an
// ApplyChangeset method here fails a test by name instead of silently
// widening the provider's reach.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// apiError mirrors docs/api.md's error envelope:
// `{"error": {"code","message","details"}}`.
type apiError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

func (e *apiError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type apiErrorEnvelope struct {
	Error apiError `json:"error"`
}

// notFoundError is returned by Get* methods when the daemon answers 404, so
// callers (resource Read implementations) can distinguish "gone" from every
// other failure and remove the resource from Terraform state accordingly.
type notFoundError struct{ apiErr *apiError }

func (e *notFoundError) Error() string {
	if e.apiErr != nil {
		return e.apiErr.Error()
	}
	return "not_found"
}

func isNotFound(err error) bool {
	_, ok := err.(*notFoundError) //nolint:errorlint // sentinel-shaped local error, never wrapped
	return ok
}

// client is a thin bearer-token HTTP client over one vnproxd's /api/v1 base
// URL, deliberately narrow: it exposes exactly the routes this provider's
// data sources and resources need (docs/api.md's Changesets, Inventory &
// topology sections), nothing from the Auth/Tokens routes (a human, or the
// harness in acceptance_test.go, mints the bearer token this client is
// configured with — the provider itself never logs in with a username and
// password, per CLAUDE.md's "do not re-litigate decisions" and this card's
// own instruction to reuse vnproxctl's token/session mechanism).
type client struct {
	http    *http.Client
	baseURL string // e.g. "https://host:8007/api/v1", no trailing slash
	token   string
}

func newClient(httpClient *http.Client, baseURL, token string) *client {
	return &client{http: httpClient, baseURL: baseURL, token: token}
}

// doJSON issues method/path (relative to baseURL) with body marshaled as
// the JSON request payload (nil for no body), decodes a 2xx response into
// out (nil to discard it), and returns a *notFoundError for 404, a
// *apiError-wrapping error for any other 4xx/5xx, or a plain error for a
// transport/decode failure.
func (c *client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body for %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("building request %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body for %s %s: %w", method, path, err)
	}

	if resp.StatusCode >= 400 {
		var env apiErrorEnvelope
		if len(data) > 0 {
			_ = json.Unmarshal(data, &env) // best-effort; a non-JSON error body still reports the HTTP status
		}
		if env.Error.Code == "" {
			env.Error.Code = "unknown_error"
			env.Error.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode == http.StatusNotFound {
			return &notFoundError{apiErr: &env.Error}
		}
		return fmt.Errorf("%s %s: %w", method, path, &env.Error)
	}

	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decoding response from %s %s: %w", method, path, err)
		}
	}
	return nil
}

// --- Changesets (docs/api.md "Changesets (the only write path)") ---------
//
// Every method below reaches AT MOST validate. There is deliberately no
// method here that calls POST /changesets/{id}/apply,
// POST /changesets/{id}/confirm, or POST /changesets/{id}/rollback — see
// this file's package doc comment and client_test.go.

// op is the wire shape of one change.Op (internal/change/op.go's
// opEnvelope in the main module) — reimplemented here field-for-field
// rather than imported, because this provider is a separate Go module by
// design (README.md's "Module boundary" section). Target is a Ref triplet
// string ("kind:node:id", docs/api.md's IDs convention); noTargetOps in the
// main module render it as JSON null, but every op this provider stages
// carries a target, so this client always sets it.
type op struct {
	Params json.RawMessage `json:"params"`
	Op     string          `json:"op"`
	ID     string          `json:"id,omitempty"`
	Target string          `json:"target"`
}

// finding mirrors change.Finding's wire shape (docs/api.md: "Validation
// finding shape").
type finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Ref      string `json:"ref,omitempty"`
}

// changeset mirrors the subset of GET/POST /changesets{,/{id}}'s response
// shape (internal/api/changesets.go's response DTO in the main module) this
// provider actually reads. Fields this provider never uses (comments,
// approval, applyStage, unattendedRevert, ...) are deliberately omitted —
// less wire-shape drift surface, not a claim those fields don't exist.
type changeset struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Status          string    `json:"status"`
	Origin          string    `json:"origin"`
	Findings        []finding `json:"findings"`
	Ops             []op      `json:"ops"`
	CreatedAt       int64     `json:"createdAt"`
	UpdatedAt       int64     `json:"updatedAt"`
	TouchesMgmtPath bool      `json:"touchesMgmtPath"`
}

type createChangesetRequest struct {
	Title string `json:"title"`
	Ops   []op   `json:"ops"`
}

// CreateChangeset stages a draft changeset via POST /changesets. This is
// exactly the "Create" half of the stage-only Create/Validate pair — the
// same pair internal/plugin.Stager and internal/mcp.ChangesetStager expose
// in-process, reused here over HTTP.
func (c *client) CreateChangeset(ctx context.Context, title string, ops []op) (changeset, error) {
	var out changeset
	err := c.doJSON(ctx, http.MethodPost, "/changesets", createChangesetRequest{Title: title, Ops: ops}, &out)
	return out, err
}

// ValidateChangeset re-runs the validator pipeline via
// POST /changesets/{id}/validate — the "Validate" half of the stage-only
// pair. It never advances a changeset past "validated".
func (c *client) ValidateChangeset(ctx context.Context, id string) (changeset, error) {
	var out changeset
	err := c.doJSON(ctx, http.MethodPost, "/changesets/"+id+"/validate", nil, &out)
	return out, err
}

// GetChangeset reads one changeset via GET /changesets/{id}. Returns a
// *notFoundError (see isNotFound) when the daemon answers 404 — a resource
// whose staged changeset was discarded or pruned out from under Terraform.
func (c *client) GetChangeset(ctx context.Context, id string) (changeset, error) {
	var out changeset
	err := c.doJSON(ctx, http.MethodGet, "/changesets/"+id, nil, &out)
	return out, err
}

// UpdateChangesetOps replaces the ops on a still-editable (draft/validated)
// changeset via PUT /changesets/{id}, which revalidates server-side. It is
// refused by the daemon once a changeset has left draft/validated (e.g.
// applying/applied) — callers (resource Update implementations) must check
// changeset.Status first and stage a NEW changeset instead when it isn't
// editable (see resource_bridge.go/resource_vlan.go's Update methods and
// README.md's "What happens after a human applies" section).
func (c *client) UpdateChangesetOps(ctx context.Context, id string, ops []op) (changeset, error) {
	var out changeset
	err := c.doJSON(ctx, http.MethodPut, "/changesets/"+id, struct {
		Ops []op `json:"ops"`
	}{Ops: ops}, &out)
	return out, err
}

// DeleteChangeset discards a draft/validated changeset via
// DELETE /changesets/{id}. It is refused once a changeset has left
// draft/validated — see UpdateChangesetOps's doc comment for the same
// caveat, and resource Delete implementations for how callers handle it.
func (c *client) DeleteChangeset(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/changesets/"+id, nil, nil)
}

// --- Inventory & topology (read-only; docs/api.md "Inventory & topology") -

// topologyNode mirrors internal/topology.Node's wire shape (the subset this
// provider's data source exposes).
type topologyNode struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Layer     string `json:"layer"`
	NodeGroup string `json:"nodeGroup"`
	Status    string `json:"status"`
}

// topologyEdge mirrors internal/topology.Edge's wire shape (the subset this
// provider's data source exposes).
type topologyEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type topologyResponse struct {
	Nodes       []topologyNode `json:"nodes"`
	Edges       []topologyEdge `json:"edges"`
	GeneratedAt int64          `json:"generatedAt"`
}

// GetTopology reads the full projected topology via GET /topology
// (netRead). Read-only: no data source or resource in this provider ever
// calls a mutating topology-adjacent route.
func (c *client) GetTopology(ctx context.Context) (topologyResponse, error) {
	var out topologyResponse
	err := c.doJSON(ctx, http.MethodGet, "/topology", nil, &out)
	return out, err
}

// relatedRef mirrors internal/topology.RelatedRef's wire shape.
type relatedRef struct {
	Ref       string `json:"ref"`
	EdgeKind  string `json:"edgeKind"`
	Direction string `json:"direction"`
}

// entityDetail mirrors internal/topology.EntityDetail's wire shape (the
// subset this provider's data source exposes; Provenance/RawSource are
// deliberately omitted — this data source is for reading an entity's
// resolved fields, not its per-field provenance debug view).
type entityDetail struct {
	Ref         string         `json:"ref"`
	Kind        string         `json:"kind"`
	Node        string         `json:"node"`
	Label       string         `json:"label"`
	Fields      map[string]any `json:"fields"`
	Related     []relatedRef   `json:"related"`
	GeneratedAt int64          `json:"generatedAt"`
}

// GetInventory reads one entity's full detail via GET /inventory/{ref}
// (netRead). ref travels as a raw path suffix — docs/api.md's Ref triplet
// scheme allows literal '/' inside the ID (an SDN vnet path, a subnet
// CIDR) and the daemon's wildcard route matches it unencoded (see
// internal/api/topology.go's mountTopologyRoutes doc comment in the main
// module), so this deliberately does not percent-encode ref.
func (c *client) GetInventory(ctx context.Context, ref string) (entityDetail, error) {
	var out entityDetail
	err := c.doJSON(ctx, http.MethodGet, "/inventory/"+ref, nil, &out)
	return out, err
}

// EntityExists reports whether GET /inventory/{ref} currently resolves —
// used by resource Read methods to compute the "live_exists" computed
// attribute (has a human actually applied the staged changeset yet?)
// without treating "not found" as an error.
func (c *client) EntityExists(ctx context.Context, ref string) (bool, error) {
	_, err := c.GetInventory(ctx, ref)
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, err
}
