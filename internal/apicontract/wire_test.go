// SPDX-License-Identifier: Apache-2.0

package apicontract

import (
	"github.com/bgovanlu/vnprox/internal/change"
)

// changesetResponse mirrors docs/api.md's documented changeset shape (and
// internal/api/changesets.go's own changesetResponse struct field-for-field
// — this package deliberately does not import that unexported type, since
// the whole point of a *contract* suite is asserting on the wire JSON, the
// thing an external caller actually sees, not on an internal Go type).
// Reusing change.Op/change.Finding directly (real, importable library
// types, not hand-duplicated shapes) means a change to either's JSON
// encoding is caught here automatically.
type changesetResponse struct {
	ConfirmDeadline *int64           `json:"confirmDeadline,omitempty"`
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Author          string           `json:"author"`
	Status          string           `json:"status"`
	Ops             []change.Op      `json:"ops"`
	Findings        []change.Finding `json:"findings"`
	CreatedAt       int64            `json:"createdAt"`
	UpdatedAt       int64            `json:"updatedAt"`
	TouchesMgmtPath bool             `json:"touchesMgmtPath"`
}

// specImportResponse is docs/api.md's `POST /spec/import` shape: every
// changesetResponse field plus notInSpec.
type specImportResponse struct {
	NotInSpec []string `json:"notInSpec"`
	changesetResponse
}

// specExportResponse is docs/api.md's `GET /spec` shape.
type specExportResponse struct {
	Content     string `json:"content"`
	SpecVersion int    `json:"specVersion"`
}

// errorResponse is docs/api.md's documented error envelope:
// `{"error": {"code", "message", "details"}}`.
type errorResponse struct {
	Error struct {
		Details map[string]any `json:"details"`
		Code    string         `json:"code"`
		Message string         `json:"message"`
	} `json:"error"`
}
