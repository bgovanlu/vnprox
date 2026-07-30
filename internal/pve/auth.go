package pve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// authenticator applies whatever this Client's auth mode requires to an
// outgoing request, performing a login/renewal round trip first if
// needed.
type authenticator interface {
	// prepare is called before every request. It may itself issue HTTP
	// requests through c (e.g. ticket login/renewal).
	prepare(ctx context.Context, c *Client) error
	// apply sets headers/cookies on req once prepare has succeeded.
	apply(req *http.Request)
}

// --- ticket auth -------------------------------------------------------

// ticketAuth implements user-ticket authentication: POST /access/ticket,
// then the PVE convention internal/pvemock's auth.go implements and
// checks — ticket via the "PVEAuthCookie" cookie, CSRF via the
// "CSRFPreventionToken" header on any mutating (non-GET/HEAD) request.
//
// Renewal uses PVE's ticket-as-password shortcut: a previously issued,
// still-valid ticket is accepted as the "password" field of POST
// /access/ticket, so the client does not need to retain the user's
// plaintext password after the first successful ticket-as-password
// renewal — it is dropped from memory at that point (docs/security.md's
// posture: hold secrets no longer than necessary). If PVE rejects the
// ticket as a password (e.g. it already expired), the client falls back
// to the stored plaintext password — only possible before the first
// successful ticket renewal has cleared it; after that, a rejected
// renewal surfaces as *ErrPVEAuth and the caller (internal/auth's renewal
// loop) invalidates the session.
//
// internal/pvemock implements ticket-as-password (its handleTicket accepts
// a valid ticket for the same user), which is what this path is tested
// against; the exact semantics on real PVE (notably: whether a ticket
// close to its 2h expiry is still accepted as a password, and TFA
// interaction) still need hardware validation. OTP is intentionally not
// sent on ticket-as-password renewals (docs/security.md: second factors
// are a first-login concept); it is resent on the plaintext-password
// fallback, matching the pre-existing behavior for realms whose static
// fixture code never rotates — a real TOTP code would be stale there,
// which is another reason the fallback is a fallback.
type ticketAuth struct {
	issuedAt time.Time

	username string
	password string
	realm    string
	otp      string
	ticket   string
	csrf     string

	renewAfter time.Duration
	// sealed marks a T-1805 sealed-ticket client: the ticket was issued
	// elsewhere (a user's live session, sealed into a changeset for its
	// commit-confirm window) and this client must use it exactly as-is —
	// never renewing, never logging in, holding no password to fall back on.
	// When the ticket expires the next call fails with PVE's own 401, which
	// is the intended, visible outcome: the unattended revert path reports
	// "the sealed ticket is no longer valid" rather than the daemon quietly
	// minting itself a fresh credential.
	sealed bool
	mu     sync.Mutex
}

type ticketLoginResponse struct {
	Ticket   string `json:"ticket"`
	CSRF     string `json:"CSRFPreventionToken"`
	Username string `json:"username"`
}

func (a *ticketAuth) prepare(ctx context.Context, c *Client) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.sealed {
		if a.ticket == "" {
			return &ErrPVEAuth{Message: "sealed-ticket client has no ticket"}
		}
		return nil
	}
	if a.ticket != "" && time.Since(a.issuedAt) < a.renewAfter {
		return nil
	}
	return a.login(ctx, c)
}

// credentials returns the current ticket/CSRF pair and the instant the
// ticket was issued. ok is false when no ticket has been obtained yet.
// Caller-facing access goes through Client.RevertCredentials, which is the
// single, documented, deliberately narrow accessor for this material.
func (a *ticketAuth) credentials() (ticket, csrf string, issuedAt time.Time, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ticket == "" {
		return "", "", time.Time{}, false
	}
	return a.ticket, a.csrf, a.issuedAt, true
}

// login performs (or renews) POST /access/ticket: ticket-as-password
// first when a ticket is held, falling back to the stored plaintext
// password (see the ticketAuth doc comment). Caller must hold a.mu.
func (a *ticketAuth) login(ctx context.Context, c *Client) error {
	if a.ticket != "" {
		err := a.loginWith(ctx, c, a.ticket, "" /* no OTP on renewal */)
		if err == nil {
			// Ticket-as-password renewal works against this server: the
			// plaintext password is no longer needed, stop retaining it.
			a.password = ""
			return nil
		}
		var authErr *ErrPVEAuth
		if !errors.As(err, &authErr) || a.password == "" {
			// Transport/server errors are not evidence the ticket was
			// rejected; and with no password left there is no fallback.
			return err
		}
		// The ticket was rejected as a password (expired, or a PVE that
		// doesn't accept the shortcut) — fall through to the plaintext
		// password we still hold.
	}
	if a.password == "" {
		return &ErrPVEAuth{Message: "ticket renewal rejected and no password retained"}
	}
	return a.loginWith(ctx, c, a.password, a.otp)
}

// loginWith performs one POST /access/ticket with the given password
// value (the user's plaintext password, or a previously issued ticket).
// Caller must hold a.mu.
func (a *ticketAuth) loginWith(ctx context.Context, c *Client, password, otp string) error {
	username := a.username
	if a.realm != "" && !strings.Contains(username, "@") {
		username = username + "@" + a.realm
	}

	// Deliberately no separate "realm" form field: hardware validation
	// (T-608) found real PVE rejects the login outright ("authentication
	// failure") when both a "user@realm"-shaped username AND a separate
	// realm field are present in the same POST /access/ticket request.
	// username above always ends up realm-qualified whenever a.realm is
	// set, so a separate field is both redundant and actively broken.
	// pvemock's handleTicket tolerates sending both (a no-op once username
	// already contains "@"), which is why no test caught this.
	form := url.Values{
		"username": {username},
		"password": {password},
	}
	if otp != "" {
		form.Set("otp", otp)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL("/access/ticket").String(), strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("pve: building ticket login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	status, body, err := c.rawDo(req, "/access/ticket")
	if err != nil {
		return err
	}
	if status >= 400 {
		return mapHTTPError(status, body)
	}

	var envelope struct {
		Data ticketLoginResponse `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("pve: decoding ticket login response: %w", err)
	}
	if envelope.Data.Ticket == "" {
		return &ErrPVEAuth{Message: "empty ticket in login response"}
	}

	a.ticket = envelope.Data.Ticket
	a.csrf = envelope.Data.CSRF
	a.issuedAt = time.Now()
	return nil
}

func (a *ticketAuth) apply(req *http.Request) {
	a.mu.Lock()
	ticket, csrf := a.ticket, a.csrf
	a.mu.Unlock()

	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket})
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		req.Header.Set("CSRFPreventionToken", csrf)
	}
}

// RevertCredentials returns this client's live PVE ticket, its CSRF token,
// and the instant the ticket was issued — the credential internal/change
// seals into a changeset for the duration of its commit-confirm window so an
// unattended revert of that changeset's `fw.*`/`sdn.*` ops can act as the
// applying user (T-1805 / roadmap-proven D1).
//
// It is the **only** accessor for a ticket value anywhere in this package's
// exported surface, and it exists for exactly that one caller. Two properties
// are load-bearing:
//
//   - ok is false for any non-ticket client (the daemon's read-only
//     `vnprox@pve!daemon` API-token identity has no ticket to seal, and must
//     never be substituted for one — a revert that acted as vnprox rather
//     than as the user is precisely the design D1 rejected).
//   - It performs the client's ordinary lazy login first, so a session whose
//     first PVE call is the apply itself still yields a real ticket rather
//     than an empty string.
//
// Callers must treat the returned values as credentials: never log them,
// never place them in an API response or audit detail. internal/change seals
// them with the shared AES-256-GCM SessionCipher before they touch the store.
func (c *Client) RevertCredentials(ctx context.Context) (ticket, csrf string, issuedAt time.Time, ok bool) {
	ta, isTicket := c.auth.(*ticketAuth)
	if !isTicket {
		return "", "", time.Time{}, false
	}
	if err := ta.prepare(ctx, c); err != nil {
		return "", "", time.Time{}, false
	}
	return ta.credentials()
}

// --- API token auth ------------------------------------------------------

// tokenAuth implements PVE API-token authentication: a fixed
// "Authorization: PVEAPIToken=user@realm!tokenid=secret" header on every
// request, no cookie, no CSRF, no renewal (docs/security.md's
// vnprox@pve!daemon read-only identity).
//
// internal/pvemock implements this mode (fixture-declared tokens whose
// privileges follow the owning user), so the success path is integration
// tested against the mock (TestAPIToken_FullReadSurfaceAgainstMock); the
// exact header/privilege semantics against real PVE (notably token
// privilege separation, which the mock does not model) still need
// hardware validation.
type tokenAuth struct {
	token string
}

func (a *tokenAuth) prepare(context.Context, *Client) error { return nil }

func (a *tokenAuth) apply(req *http.Request) {
	req.Header.Set("Authorization", "PVEAPIToken="+a.token)
}
