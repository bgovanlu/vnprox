// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"encoding/json"
	"fmt"
)

// ErrPVEAuth indicates PVE rejected the request with 401 — bad credentials
// on ticket login, or an invalid/expired API token. Message carries PVE's
// own response text verbatim where available.
//
// Despite the "Err" prefix (kept to match the task-card-documented name),
// this is a struct type, not a sentinel value: use errors.As, not
// errors.Is.
//
//	var authErr *pve.ErrPVEAuth
//	if errors.As(err, &authErr) { ... authErr.Message ... }
type ErrPVEAuth struct {
	Message string
}

func (e *ErrPVEAuth) Error() string {
	return fmt.Sprintf("pve: authentication failed: %s", e.Message)
}

// ErrPVEDenied indicates PVE rejected an authenticated request with 403 —
// the session/token lacks the PVE privilege the operation requires.
// Message carries PVE's own permission-check text verbatim (e.g.
// "permission check failed (Sys.Modify)") so callers/UI can surface it
// unmodified (docs/api.md error code `pve_denied`).
//
// Use errors.As:
//
//	var denied *pve.ErrPVEDenied
//	if errors.As(err, &denied) { ... denied.Message ... }
type ErrPVEDenied struct {
	Message string
}

func (e *ErrPVEDenied) Error() string {
	return fmt.Sprintf("pve: permission denied: %s", e.Message)
}

// ErrPVEServer indicates PVE responded with a 5xx status — an internal
// failure on the PVE side, not a client-side auth/permission/validation
// problem.
type ErrPVEServer struct {
	Message    string
	StatusCode int
}

func (e *ErrPVEServer) Error() string {
	return fmt.Sprintf("pve: server error (status %d): %s", e.StatusCode, e.Message)
}

// ErrPVERequest indicates PVE responded with some other 4xx status (bad
// request, not found, conflict, ...) that isn't an auth or permission
// failure.
type ErrPVERequest struct {
	Message    string
	StatusCode int
}

func (e *ErrPVERequest) Error() string {
	return fmt.Sprintf("pve: request error (status %d): %s", e.StatusCode, e.Message)
}

// ErrPVETransport indicates the request never got a PVE response at all:
// DNS failure, connection refused, TLS handshake failure, context
// cancellation/timeout, or the response body was unreadable/undecodable.
// Unwrap returns the underlying error so callers can still
// errors.Is(err, context.DeadlineExceeded) etc. through it.
type ErrPVETransport struct {
	Err error
}

func (e *ErrPVETransport) Error() string {
	return fmt.Sprintf("pve: transport error: %v", e.Err)
}

func (e *ErrPVETransport) Unwrap() error {
	return e.Err
}

// ErrPVETaskFailed indicates a task polled to completion with a failed
// exit status (docs/architecture.md §4's apply steps report failure this
// way). ExitStatus carries PVE's raw string (e.g. "failed: ifupdown2
// error").
type ErrPVETaskFailed struct {
	UPID       string
	ExitStatus string
}

func (e *ErrPVETaskFailed) Error() string {
	return fmt.Sprintf("pve: task %s failed: %s", e.UPID, e.ExitStatus)
}

// ErrPVETaskTimeout indicates WaitTask's own timeout (WaitOptions.Timeout,
// or a deadline already on the passed context) elapsed before the task
// reached a terminal status. Unwrap returns the context error
// (context.DeadlineExceeded) that triggered it.
type ErrPVETaskTimeout struct {
	Err  error
	UPID string
}

func (e *ErrPVETaskTimeout) Error() string {
	return fmt.Sprintf("pve: waiting for task %s: %v", e.UPID, e.Err)
}

func (e *ErrPVETaskTimeout) Unwrap() error {
	return e.Err
}

// pveErrorEnvelope mirrors the {"data": ..., "message": "..."} shape PVE
// (and internal/pvemock) return on error responses.
type pveErrorEnvelope struct {
	Message string `json:"message"`
}

// mapHTTPError builds the typed error for a non-2xx PVE response. body may
// be empty or undecodable (e.g. a proxy timeout returning HTML); in that
// case Message falls back to the raw body or the HTTP status text.
func mapHTTPError(statusCode int, body []byte) error {
	msg := extractMessage(body)
	switch statusCode {
	case 401:
		return &ErrPVEAuth{Message: msg}
	case 403:
		return &ErrPVEDenied{Message: msg}
	default:
		if statusCode >= 500 {
			return &ErrPVEServer{StatusCode: statusCode, Message: msg}
		}
		return &ErrPVERequest{StatusCode: statusCode, Message: msg}
	}
}

func extractMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var env pveErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Message != "" {
		return env.Message
	}
	return string(body)
}
