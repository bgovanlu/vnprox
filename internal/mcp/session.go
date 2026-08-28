// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// Session is a single authenticated MCP connection's view over the Server. It
// carries the token's identity and the scope set derived from the token at
// connect time — exposure is computed from these scopes, never from anything
// the client asserts afterwards.
type Session struct {
	srv       *Server
	scopes    map[string]bool
	TokenID   string
	TokenName string
	actor     string // "mcp:<token-name>" — the audit actor for everything this session does
}

// errUnknownTool is returned by call for a tool the session may not use —
// whether because it doesn't exist or because the token's scopes don't cover
// it. The two cases are deliberately merged so scope membership isn't leaked.
type errUnknownTool struct{}

func (errUnknownTool) Error() string { return "unknown tool" }

// errRevoked is returned by call when the token was revoked between connect and
// this invocation (defence in depth on top of the transport's revocation
// watcher).
var errRevoked = errors.New("mcp: token revoked")

// hasScope reports whether the session's token carries scope.
func (s *Session) hasScope(scope string) bool { return s.scopes[scope] }

// exposesTool reports whether tool name is visible to this session: the tool
// must exist in the allowlist AND the token must hold its RequiredScope.
func (s *Session) exposesTool(name string) bool {
	spec, ok := toolByName(name)
	if !ok {
		return false
	}
	return s.hasScope(spec.RequiredScope)
}

// exposedTools returns the allowlisted tools this session's scopes cover, in
// registration order.
func (s *Session) exposedTools() []ToolSpec {
	var out []ToolSpec
	for _, spec := range toolSpecs {
		if s.hasScope(spec.RequiredScope) {
			out = append(out, spec)
		}
	}
	return out
}

// exposedToolDescriptors renders exposedTools into the wire shape tools/list
// returns.
func (s *Session) exposedToolDescriptors() []toolDescriptor {
	specs := s.exposedTools()
	out := make([]toolDescriptor, 0, len(specs))
	for _, spec := range specs {
		out = append(out, toolDescriptor{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: spec.InputSchema,
		})
	}
	return out
}

// call dispatches a tool invocation: scope check, live-token re-check, audit,
// then the handler. A scope failure returns errUnknownTool (indistinguishable
// from a nonexistent tool); a revoked token returns errRevoked; any other error
// is the tool's own failure, surfaced to the caller as an MCP tool error.
func (s *Session) call(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if !s.exposesTool(name) {
		// Audit even the refusal, so a scoped-out probe is visible.
		s.audit(ctx, "mcp.tool.invoke", "denied", map[string]any{"tool": name, "reason": "not_exposed"})
		return nil, errUnknownTool{}
	}
	if !s.srv.deps.Auth.Live(ctx, s.TokenID) {
		s.audit(ctx, "mcp.tool.invoke", "denied", map[string]any{"tool": name, "reason": "revoked"})
		return nil, errRevoked
	}
	handler := s.srv.handlers[name]
	result, err := handler(ctx, s, args)
	if err != nil {
		s.audit(ctx, "mcp.tool.invoke", "error", map[string]any{"tool": name, "error": err.Error()})
		return nil, err
	}
	s.audit(ctx, "mcp.tool.invoke", "ok", map[string]any{"tool": name})
	return result, nil
}

// audit writes one append-only audit row attributed to this session's
// mcp:<token-name> actor. Best-effort (logged, never failing the call), the
// same stance every other non-critical audit side effect in this codebase
// takes.
func (s *Session) audit(ctx context.Context, action, result string, detail map[string]any) {
	if s.srv.deps.Audit == nil {
		return
	}
	var detailJSON sql.NullString
	if detail != nil {
		detail["tokenId"] = s.TokenID
		if b, err := json.Marshal(detail); err == nil {
			detailJSON = sql.NullString{String: string(b), Valid: true}
		}
	}
	entry := store.AuditEntry{
		At:         s.srv.now().Unix(),
		Username:   s.actor,
		Action:     action,
		Result:     result,
		DetailJSON: detailJSON,
	}
	if _, err := s.srv.deps.Audit.Append(ctx, entry); err != nil {
		s.srv.log.Error("mcp: appending audit entry", "action", action, "actor", s.actor, "error", err)
	}
}

// watch cancels ctx (via the returned context) the moment the token stops being
// live, polled every Server.revoke interval. A long-lived transport derives its
// serve loop's context from this so a mid-session revocation force-closes the
// connection within one tick (AC5). The caller must call the returned cancel
// when the connection ends, to stop the poller.
func (s *Session) watch(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		ticker := time.NewTicker(s.srv.revoke)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.srv.deps.Auth.Live(ctx, s.TokenID) {
					s.audit(ctx, "mcp.session.close", "revoked", map[string]any{"reason": "token_revoked"})
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}
