// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxHTTPMessageBytes bounds a single JSON-RPC POST body (same generous ceiling
// as the stdio transport).
const maxHTTPMessageBytes = 8 << 20 // 8 MiB

const bearerPrefix = "Bearer "

// HTTPHandler returns an http.Handler implementing MCP's Streamable HTTP
// transport for this server. Every request authenticates with an
// `Authorization: Bearer <token>` header carrying the automation scope — there
// is no cookie/session path onto this handler, so an SPA session can never
// reach the MCP surface. A POST carries one JSON-RPC message and receives one
// JSON response (or, when the client sends `Accept: text/event-stream`, that
// response framed as a single SSE event). A GET with `Accept: text/event-stream`
// opens the server->client SSE stream, held open until the client disconnects
// or the token is revoked (AC5), whichever comes first.
//
// This handler is mounted raw (it does its own auth) — cmd/vnproxd hangs it off
// the router the same way the metrics exporter's bearer-scheme route is,
// outside the session-cookie/CSRF middleware.
func (s *Server) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := s.authenticateRequest(r)
		if err != nil {
			s.writeAuthError(w, err)
			return
		}
		switch r.Method {
		case http.MethodPost:
			s.serveHTTPPost(w, r, session)
		case http.MethodGet:
			s.serveHTTPStream(w, r, session)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (s *Server) authenticateRequest(r *http.Request) (*Session, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, bearerPrefix) {
		return nil, ErrAuthRequired
	}
	raw := strings.TrimSpace(strings.TrimPrefix(h, bearerPrefix))
	return s.Authenticate(r.Context(), raw)
}

func (s *Server) writeAuthError(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	code := "not_authenticated"
	msg := "MCP access requires a bearer token"
	if errors.Is(err, ErrAutomationScopeRequired) {
		status = http.StatusForbidden
		code = "forbidden"
		msg = "token is missing the automation scope required for MCP access"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func (s *Server) serveHTTPPost(w http.ResponseWriter, r *http.Request, session *Session) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHTTPMessageBytes))
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
		return
	}
	// The revocation watcher applies to this single request's lifetime too.
	ctx, cancel := session.watch(r.Context())
	defer cancel()

	resp := s.HandleMessage(ctx, session, body)
	if resp == nil {
		// A notification: acknowledge with 202 and no body (MCP convention).
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if acceptsEventStream(r) {
		s.writeSSEEvent(w, resp)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(resp)
}

// serveHTTPStream opens the server->client SSE channel. This server pushes no
// unsolicited server-initiated messages (its tools are request/response), so
// the stream carries only keepalive comments; its real job is to demonstrate
// the SSE transport and to close promptly when the token is revoked (AC5).
func (s *Server) serveHTTPStream(w http.ResponseWriter, r *http.Request, session *Session) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := session.watch(r.Context())
	defer cancel()

	ticker := time.NewTicker(s.revoke)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return // client gone or token revoked
		case <-ticker.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) writeSSEEvent(w http.ResponseWriter, resp []byte) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, "event: message\ndata: ")
	_, _ = w.Write(resp)
	_, _ = io.WriteString(w, "\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func acceptsEventStream(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}
