// SPDX-License-Identifier: Apache-2.0

package gitsync_test

// T-2701 AC6: "Credentials never appear in logs, findings, audit entries, or
// `gitsync status` output — one assertion per surface."
//
// The test drives the REAL HTTPSource against a mock host with a REAL token,
// through a successful sync and through a failing one (the error path is
// where a credential leaks in practice — a 401 body that quotes the request
// back, an error string built from a URL with userinfo in it). Then it scans
// each of the four surfaces for the marker.
//
// Two controls run first, because a scan that looks in the wrong place, or a
// pipeline that never carried the credential at all, would make every
// "absent" assertion pass vacuously:
//
//	control 1 — the mock host must have actually been presented with the
//	            token, so the credential really was in flight;
//	control 2 — each surface must contain a known non-secret marker, so we
//	            know the surface was populated and is being read correctly.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/gitsync"
)

const ac6Token = "ghp-VNPROXAC6MARKER-do-not-log-me" //nolint:gosec // a test marker, not a real credential

func TestAC6_CredentialNeverAppearsOnAnySurface(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	doc := divergentSpec(t, g, 1400)

	mock := &gitHostMock{sha: "abcdef0123456789abcdef0123456789abcdef01", content: doc}
	ts := httptest.NewServer(mock)
	defer ts.Close()

	src, err := gitsync.NewHTTPSource(gitsync.SourceConfig{
		URL: ts.URL + "/org/infra", Provider: gitsync.ProviderGitHub,
		Token: ac6Token, Client: ts.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}

	logger, logs := captureLogger()
	audit := &fakeAudit{}
	stager := newFakeStager()
	svc := gitsync.New(gitsync.Config{
		Enabled: true, Source: src, Ref: "main", Path: "network/cluster.yaml",
		PollInterval: 20 * time.Millisecond,
		Changesets:   stager, Inventory: g, Audit: audit, Logger: logger,
		Now: func() time.Time { return time.Unix(1_754_000_000, 0) },
	})

	// A successful cycle: opens a draft, writes an audit row, logs.
	if _, syncErr := svc.Sync(context.Background()); syncErr != nil {
		t.Fatalf("Sync: %v", syncErr)
	}
	// A failing cycle: the host answers 401 with a body quoting the
	// Authorization header back at us. This is the realistic leak.
	mock.setStatus(http.StatusUnauthorized)
	if _, syncErr := svc.Sync(context.Background()); syncErr == nil {
		t.Fatal("the 401 cycle did not fail")
	}
	// Log the failure the way Run does, so the log surface carries it.
	logger.Warn("gitsync: sync cycle failed, will retry", "error", svc.Status().LastError)

	// --- control 1: the credential really was in flight -------------------
	if seen := mock.credentialSeen(); !strings.Contains(seen, ac6Token) {
		t.Fatalf("control failed: the mock host was presented with %q, which does not carry the token — "+
			"the absence assertions below would be vacuous", seen)
	}

	statusJSON, err := json.Marshal(svc.Status())
	if err != nil {
		t.Fatalf("marshaling status: %v", err)
	}
	auditText := auditSurface(t, audit)
	findingsText := findingsSurface(svc)
	logText := logs()

	surfaces := []struct {
		name string
		text string
		// control is a string that MUST be present, proving this surface was
		// populated and is being read from the right place.
		control string
	}{
		{name: "daemon log", text: logText, control: "gitsync"},
		{name: "findings", text: findingsText, control: "gitsync_unreachable"},
		{name: "audit entries", text: auditText, control: "gitsync.changeset.open"},
		{name: "gitsync status output", text: string(statusJSON), control: "lastFetchedSha"},
	}

	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			// --- control 2: this surface is populated and readable --------
			if !strings.Contains(s.text, s.control) {
				t.Fatalf("control failed: the %s surface does not contain %q, so it was never populated — "+
					"a leak assertion against it proves nothing.\nsurface was:\n%s", s.name, s.control, s.text)
			}
			// --- the assertion -------------------------------------------
			if strings.Contains(s.text, ac6Token) {
				t.Errorf("the credential appears in the %s surface:\n%s", s.name, s.text)
			}
			// A partial leak is still a leak: check the distinctive middle
			// of the token too, in case something truncated it.
			if strings.Contains(s.text, "VNPROXAC6MARKER") {
				t.Errorf("part of the credential appears in the %s surface:\n%s", s.name, s.text)
			}
		})
	}
}

func auditSurface(t *testing.T, a *fakeAudit) string {
	t.Helper()
	var sb strings.Builder
	entries := a.all()
	if len(entries) == 0 {
		t.Fatal("no audit entries were written; the audit surface cannot be asserted against")
	}
	for _, e := range entries {
		sb.WriteString(e.Username + " " + e.Action + " " + e.Result + " ")
		sb.WriteString(e.ChangesetID.String + " " + e.DetailJSON.String + "\n")
	}
	return sb.String()
}

func findingsSurface(svc *gitsync.Service) string {
	var sb strings.Builder
	for _, iss := range svc.Issues() {
		sb.WriteString(iss.Check + " " + iss.Severity + " " + iss.Detail + "\n")
	}
	return sb.String()
}
