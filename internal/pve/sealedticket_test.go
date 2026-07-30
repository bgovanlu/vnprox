package pve_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// T-1805: the sealed-ticket client — a *pve.Client built from an already-issued
// PVE ticket + CSRF token that NEVER renews and NEVER logs in. It is what the
// unattended revert path acts through, so two properties are load-bearing and
// tested here rather than assumed:
//
//  1. it authenticates real mutating calls with the ticket it was handed
//     (a revert is made entirely of mutating calls), and
//  2. it issues no POST /access/ticket of its own — a daemon that could mint a
//     fresh ticket from a sealed one would be able to extend a user's
//     credential lifetime indefinitely, which is precisely the standing
//     privileged credential D1 rejected.

func TestSealedTicketClient_UsesTheGivenTicketAndNeverLogsIn(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureSingleNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	counter := &countingHandler{inner: pvemock.NewServer(f)}
	ts := httptest.NewServer(counter)
	t.Cleanup(ts.Close)
	ctx := context.Background()

	// A user logs in normally; this is the credential that would be sealed.
	live, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New(live): %v", err)
	}
	ticket, csrf, issuedAt, ok := live.RevertCredentials(ctx)
	if !ok || ticket == "" || csrf == "" {
		t.Fatalf("RevertCredentials on a ticket client returned ok=%v ticket=%q", ok, ticket)
	}
	if issuedAt.IsZero() {
		t.Errorf("RevertCredentials returned a zero issue time; the sealed ticket's expiry would be unknowable")
	}
	if expiry := change_TicketExpiry(issuedAt.Unix()); expiry <= issuedAt.Unix() {
		t.Errorf("derived expiry %d must follow the issue time %d", expiry, issuedAt.Unix())
	}
	loginsBefore := counter.count()

	// The revert path's client: the sealed ticket, nothing else.
	sealed, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Ticket: ticket, CSRFToken: csrf})
	if err != nil {
		t.Fatalf("pve.New(sealed): %v", err)
	}

	// A real mutating call — the shape every firewall/SDN revert step takes.
	// pvemock validates the PVEAuthCookie against its own session table and
	// the CSRFPreventionToken header on any non-GET, so success here means the
	// exact credential pair was carried on the wire.
	scope := pve.GuestFirewallScope("pve1", pve.GuestQemu, 100)
	if err = sealed.CreateFirewallRule(ctx, scope, pve.FirewallRule{
		Type: "in", Action: "ACCEPT", Proto: "tcp", Dport: "9999", Comment: "sealed-ticket write", Enabled: true,
	}); err != nil {
		t.Fatalf("mutating call through the sealed-ticket client: %v", err)
	}
	if got := counter.count(); got != loginsBefore {
		t.Errorf("the sealed-ticket client performed %d login(s); it must never log in", got-loginsBefore)
	}
}

func TestSealedTicketClient_RejectsAnInvalidTicketRatherThanReauthenticating(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureSingleNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	counter := &countingHandler{inner: pvemock.NewServer(f)}
	ts := httptest.NewServer(counter)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{
		APIURL: ts.URL, Auth: pve.AuthTicket,
		Ticket: "PVE:root@pam:DEADBEEF::not-a-real-signature", CSRFToken: "not-a-real-csrf",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	scope := pve.GuestFirewallScope("pve1", pve.GuestQemu, 100)
	err = client.CreateFirewallRule(context.Background(), scope, pve.FirewallRule{Type: "in", Action: "ACCEPT", Enabled: true})
	if err == nil {
		t.Fatalf("a bogus sealed ticket was accepted")
	}
	if counter.count() != 0 {
		t.Errorf("the client fell back to a login (%d calls); an expired/invalid sealed ticket must fail visibly instead", counter.count())
	}
}

func TestSealedTicketConfig_Validation(t *testing.T) {
	// cfg is built by a closure rather than stored inline so this table stays a
	// small value type (govet fieldalignment) despite pve.Config being large.
	cases := []struct {
		cfg     func() pve.Config
		name    string
		wantErr string
	}{
		{
			name:    "ticket without csrf",
			cfg:     func() pve.Config { return pve.Config{APIURL: "https://x:8006", Auth: pve.AuthTicket, Ticket: "t"} },
			wantErr: "must be set together",
		},
		{
			name:    "csrf without ticket",
			cfg:     func() pve.Config { return pve.Config{APIURL: "https://x:8006", Auth: pve.AuthTicket, CSRFToken: "c"} },
			wantErr: "must be set together",
		},
		{
			name: "sealed ticket plus a password",
			cfg: func() pve.Config {
				return pve.Config{APIURL: "https://x:8006", Auth: pve.AuthTicket, Ticket: "t", CSRFToken: "c", Password: "p"}
			},
			wantErr: "Password must be empty",
		},
		{
			name: "valid sealed-ticket config",
			cfg: func() pve.Config {
				return pve.Config{APIURL: "https://x:8006", Auth: pve.AuthTicket, Ticket: "t", CSRFToken: "c"}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pve.New(tc.cfg())
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("pve.New = %v, want success", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("pve.New succeeded, want an error containing %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("pve.New = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestAPITokenClient_HasNoRevertCredentials asserts the daemon's read-only
// `vnprox@pve!daemon` identity can never be sealed as a revert credential:
// a revert must act as the user, never as vnprox (docs/security.md's "one
// privileged internal identity ... never for writes").
func TestAPITokenClient_HasNoRevertCredentials(t *testing.T) {
	client, err := pve.New(pve.Config{APIURL: "https://x:8006", Auth: pve.AuthAPIToken, TokenValue: "vnprox@pve!daemon=secret"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	if _, _, _, ok := client.RevertCredentials(context.Background()); ok {
		t.Fatal("an API-token client offered revert credentials; only a user ticket may ever be sealed")
	}
}

// change_TicketExpiry mirrors change.TicketExpiryFrom without importing
// internal/change (which imports internal/pve — the reverse edge would be a
// cycle). It exists only so the issue-time assertion above is meaningful.
func change_TicketExpiry(issuedAtUnix int64) int64 {
	return issuedAtUnix + int64(pve.TicketLifetime.Seconds())
}
