package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/auth/oidcmock"
)

const testClientID = "vnprox-client"

// challengeFor returns the PKCE S256 challenge for a verifier.
func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newTestProvider(t *testing.T, idp *oidcmock.Provider, clientID string) *auth.OIDCProvider {
	t.Helper()
	p, err := auth.NewOIDCProvider(auth.OIDCProviderConfig{
		Issuer:      idp.Issuer(),
		ClientID:    clientID,
		RedirectURL: "https://vnprox.example/oidc/callback",
		HTTPClient:  idp.HTTPClient(),
		Scopes:      []string{"profile", "groups"},
		GroupsClaim: "groups",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	return p
}

func TestOIDCProvider_ExchangeAndVerify(t *testing.T) {
	idp, err := oidcmock.New(testClientID)
	if err != nil {
		t.Fatalf("oidcmock.New: %v", err)
	}
	t.Cleanup(idp.Close)
	p := newTestProvider(t, idp, testClientID)
	ctx := context.Background()

	// AuthCodeURL exercises discovery and carries the PKCE challenge/nonce.
	const verifier = "verifier-abc-123-verifier-abc-123-verifier"
	nonce := "nonce-xyz"
	authURL, err := p.AuthCodeURL(ctx, "state-1", nonce, challengeFor(verifier))
	if err != nil {
		t.Fatalf("AuthCodeURL: %v", err)
	}
	if !strings.Contains(authURL, "code_challenge_method=S256") || !strings.Contains(authURL, "client_id="+testClientID) {
		t.Errorf("authURL missing expected params: %s", authURL)
	}

	code, err := idp.IssueCode(nonce, challengeFor(verifier), oidcmock.User{
		Subject: "u-1", PreferredUsername: "alice", Email: "alice@example",
		Groups: []string{"vnprox-readers", "team-net"},
	})
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}

	tr, err := p.Exchange(ctx, code, verifier)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	claims, err := p.VerifyIDToken(ctx, tr.IDToken, nonce)
	if err != nil {
		t.Fatalf("VerifyIDToken: %v", err)
	}
	if claims.Username() != "alice" {
		t.Errorf("Username() = %q, want alice", claims.Username())
	}
	if len(claims.Groups) != 2 || claims.Groups[0] != "vnprox-readers" {
		t.Errorf("Groups = %v, want [vnprox-readers team-net]", claims.Groups)
	}

	// Refresh (AC4): the rotated refresh token yields a fresh, verifiable ID
	// token. A refreshed token carries no nonce, so verification passes "".
	tr2, err := p.Refresh(ctx, tr.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tr2.IDToken == tr.IDToken {
		t.Errorf("Refresh returned the same id_token")
	}
	if _, err := p.VerifyIDToken(ctx, tr2.IDToken, ""); err != nil {
		t.Fatalf("VerifyIDToken(refreshed): %v", err)
	}
	// The original refresh token is single-use (rotated) — reusing it fails.
	if _, err := p.Refresh(ctx, tr.RefreshToken); err == nil {
		t.Errorf("reusing a rotated refresh token should fail")
	}
}

func TestOIDCProvider_VerifyRejects(t *testing.T) {
	idp, err := oidcmock.New(testClientID)
	if err != nil {
		t.Fatalf("oidcmock.New: %v", err)
	}
	t.Cleanup(idp.Close)
	ctx := context.Background()

	const verifier = "verifier-abc-123-verifier-abc-123-verifier"
	mint := func(t *testing.T, p *auth.OIDCProvider, nonce string) string {
		t.Helper()
		code, err := idp.IssueCode(nonce, challengeFor(verifier), oidcmock.User{Subject: "u-1", Groups: []string{"g"}})
		if err != nil {
			t.Fatalf("IssueCode: %v", err)
		}
		tr, err := p.Exchange(ctx, code, verifier)
		if err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		return tr.IDToken
	}

	t.Run("nonce mismatch", func(t *testing.T) {
		p := newTestProvider(t, idp, testClientID)
		tok := mint(t, p, "real-nonce")
		if _, err := p.VerifyIDToken(ctx, tok, "different-nonce"); !errors.Is(err, auth.ErrOIDCVerify) {
			t.Errorf("err = %v, want ErrOIDCVerify", err)
		}
	})

	t.Run("wrong audience", func(t *testing.T) {
		// A provider configured for a different client id rejects the token's
		// aud claim.
		p := newTestProvider(t, idp, "some-other-client")
		tok := mint(t, newTestProvider(t, idp, testClientID), "n")
		if _, err := p.VerifyIDToken(ctx, tok, "n"); !errors.Is(err, auth.ErrOIDCVerify) {
			t.Errorf("err = %v, want ErrOIDCVerify for bad audience", err)
		}
	})

	t.Run("tampered signature", func(t *testing.T) {
		p := newTestProvider(t, idp, testClientID)
		tok := mint(t, p, "n")
		// Flip a character near the start of the signature segment (the last
		// base64url char carries padding bits that decode identically, so it
		// must not be the one flipped).
		parts := strings.Split(tok, ".")
		sig := []byte(parts[2])
		if sig[0] == 'A' {
			sig[0] = 'B'
		} else {
			sig[0] = 'A'
		}
		tampered := parts[0] + "." + parts[1] + "." + string(sig)
		if _, err := p.VerifyIDToken(ctx, tampered, "n"); !errors.Is(err, auth.ErrOIDCVerify) {
			t.Errorf("err = %v, want ErrOIDCVerify for tampered signature", err)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		// Mint against an IdP whose clock is far in the past so the token is
		// already expired by real-time verification.
		past := time.Now().Add(-24 * time.Hour)
		expiredIDP, err := oidcmock.New(testClientID, oidcmock.WithNow(func() time.Time { return past }), oidcmock.WithTokenTTL(time.Minute))
		if err != nil {
			t.Fatalf("oidcmock.New: %v", err)
		}
		t.Cleanup(expiredIDP.Close)
		p := newTestProvider(t, expiredIDP, testClientID)
		code, err := expiredIDP.IssueCode("n", challengeFor(verifier), oidcmock.User{Subject: "u"})
		if err != nil {
			t.Fatalf("IssueCode: %v", err)
		}
		tr, err := p.Exchange(ctx, code, verifier)
		if err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		if _, err := p.VerifyIDToken(ctx, tr.IDToken, "n"); !errors.Is(err, auth.ErrOIDCVerify) {
			t.Errorf("err = %v, want ErrOIDCVerify for expired token", err)
		}
	})

	t.Run("bad pkce verifier", func(t *testing.T) {
		p := newTestProvider(t, idp, testClientID)
		code, err := idp.IssueCode("n", challengeFor(verifier), oidcmock.User{Subject: "u"})
		if err != nil {
			t.Fatalf("IssueCode: %v", err)
		}
		if _, err := p.Exchange(ctx, code, "wrong-verifier"); err == nil {
			t.Errorf("Exchange with wrong PKCE verifier should fail")
		}
	})
}
