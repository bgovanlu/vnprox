package pve

import (
	"context"
	"encoding/json"
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
// Real PVE also accepts a previously-issued ticket as the "password" field
// to renew without resending the user's actual password. The mock server
// this client is developed and tested against (internal/pvemock) does not
// implement that shortcut — it only ever checks the fixture user's static
// password — so this client renews by replaying the stored
// username/password/realm through the same login path. That is also
// forward-compatible with real PVE (which accepts genuine credentials at
// any time, not just first login); it just forgoes the minor optimization
// of not needing to hold the plaintext password after first login. OTP is
// intentionally not resent on renewal (docs/security.md: second factors
// are a first-login concept).
type ticketAuth struct {
	issuedAt time.Time

	username string
	password string
	realm    string
	otp      string
	ticket   string
	csrf     string

	renewAfter time.Duration
	mu         sync.Mutex
}

type ticketLoginResponse struct {
	Ticket   string `json:"ticket"`
	CSRF     string `json:"CSRFPreventionToken"`
	Username string `json:"username"`
}

func (a *ticketAuth) prepare(ctx context.Context, c *Client) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.ticket != "" && time.Since(a.issuedAt) < a.renewAfter {
		return nil
	}
	return a.login(ctx, c)
}

// login performs (or renews) POST /access/ticket. Caller must hold a.mu.
func (a *ticketAuth) login(ctx context.Context, c *Client) error {
	username := a.username
	if a.realm != "" && !strings.Contains(username, "@") {
		username = username + "@" + a.realm
	}

	form := url.Values{
		"username": {username},
		"password": {a.password},
	}
	if a.realm != "" {
		form.Set("realm", a.realm)
	}
	if a.otp != "" {
		form.Set("otp", a.otp)
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

// --- API token auth ------------------------------------------------------

// tokenAuth implements PVE API-token authentication: a fixed
// "Authorization: PVEAPIToken=user@realm!tokenid=secret" header on every
// request, no cookie, no CSRF, no renewal (docs/security.md's
// vnprox@pve!daemon read-only identity).
//
// Note: internal/pvemock (T-004) does not implement API-token
// authentication at all — its authenticate() only ever looks for the
// PVEAuthCookie cookie (see internal/pvemock/auth.go's doc comment on
// authenticate). Requests sent in this mode against the mock therefore
// always receive a 401 today; this is a documented gap (see this
// package's test suite and the T-101 completion report), not a bug in
// this client. The header is still built and sent exactly per PVE's
// documented convention so it will work unmodified against real PVE or a
// future mock update.
type tokenAuth struct {
	token string
}

func (a *tokenAuth) prepare(context.Context, *Client) error { return nil }

func (a *tokenAuth) apply(req *http.Request) {
	req.Header.Set("Authorization", "PVEAPIToken="+a.token)
}
