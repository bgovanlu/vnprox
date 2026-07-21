package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

func stubReads() Deps {
	ok := func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	}
	return Deps{
		Topology: ok, Findings: ok, Flows: ok, IPAM: ok, Simulate: ok,
		Diagnose: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]any{"verdict": map[string]any{"confidence": "high"}}, nil
		},
	}
}

func realChange(t *testing.T, now func() time.Time) (*change.Service, *store.AuditRepo) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	audit := store.NewAuditRepo(db)
	svc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      audit,
		Now:        now,
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc, audit
}

// TestExposedToolsByScope is AC2: exposure is derived from the token's scopes,
// never a client assertion, and a {netRead, automation} session never sees
// changesets.create/validate (both netWrite).
func TestExposedToolsByScope(t *testing.T) {
	auth := newFakeAuth()
	deps := stubReads()
	deps.Auth = auth
	srv, err := NewServer(deps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	cases := []struct {
		name   string
		scopes []string
		want   []string
	}{
		{
			name:   "read+automation",
			scopes: []string{"netRead", "automation"},
			want:   []string{"topology.get", "findings.list", "flows.query", "ipam.subnets.list", "simulate.path", "diagnose.run", "changesets.diff"},
		},
		{
			name:   "read+write+automation",
			scopes: []string{"netRead", "netWrite", "automation"},
			want:   []string{"topology.get", "findings.list", "flows.query", "ipam.subnets.list", "simulate.path", "diagnose.run", "changesets.diff", "changesets.create", "changesets.validate"},
		},
		{
			name:   "write+automation-only",
			scopes: []string{"netWrite", "automation"},
			// changesets.diff is a read (netRead), so a netWrite-only token
			// does not see it — only the two write-staging tools.
			want: []string{"changesets.create", "changesets.validate"},
		},
		{
			name:   "automation-only",
			scopes: []string{"automation"},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth.add("raw-"+tc.name, TokenInfo{ID: "id-" + tc.name, Name: tc.name, Scopes: tc.scopes})
			session, aerr := srv.Authenticate(context.Background(), "raw-"+tc.name)
			if aerr != nil {
				t.Fatalf("Authenticate: %v", aerr)
			}
			var got []string
			for _, spec := range session.exposedTools() {
				got = append(got, spec.Name)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("exposed tools = %v, want %v", got, tc.want)
			}
			// changesets.create must never be exposed without netWrite.
			if !contains(tc.scopes, "netWrite") && contains(got, "changesets.create") {
				t.Fatalf("changesets.create exposed to a session without netWrite")
			}
		})
	}
}

// TestAuthenticateRequiresAutomation is AC2's connection gate: a valid token
// with no automation scope cannot open an MCP session at all.
func TestAuthenticateRequiresAutomation(t *testing.T) {
	auth := newFakeAuth()
	auth.add("raw", TokenInfo{ID: "x", Name: "nope", Scopes: []string{"netRead", "netWrite"}})
	deps := stubReads()
	deps.Auth = auth
	srv, _ := NewServer(deps)

	if _, err := srv.Authenticate(context.Background(), "raw"); err != ErrAutomationScopeRequired {
		t.Fatalf("Authenticate without automation = %v, want ErrAutomationScopeRequired", err)
	}
	if _, err := srv.Authenticate(context.Background(), "bogus"); err != ErrAuthRequired {
		t.Fatalf("Authenticate with unknown token = %v, want ErrAuthRequired", err)
	}
}

// TestE2EStageOnly is AC3: a full-scope MCP client runs diagnose.run then
// changesets.create against a real change.Service, gets a draft with
// origin=mcp, and has no tool that could apply it.
func TestE2EStageOnly(t *testing.T) {
	auth := newFakeAuth()
	auth.add("full", TokenInfo{ID: "tok-full", Name: "ci-bot", Scopes: []string{"netRead", "netWrite", "automation"}})
	svc, _ := realChange(t, func() time.Time { return time.Unix(1000, 0) })
	deps := stubReads()
	deps.Auth = auth
	deps.Staging = svc
	srv, err := NewServer(deps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session, _ := srv.Authenticate(context.Background(), "full")
	client, _ := newMockClient(t, srv, session)

	init := client.initialize()
	if init.Capabilities.Tools == nil {
		t.Fatalf("initialize did not advertise tools capability")
	}

	names := client.listToolNames()
	for _, n := range names {
		for _, bad := range forbiddenToolSubstrings {
			if strings.Contains(strings.ToLower(n), bad) {
				t.Fatalf("tools/list offered a mutating tool %q", n)
			}
		}
	}
	if !contains(names, "diagnose.run") || !contains(names, "changesets.create") {
		t.Fatalf("expected diagnose.run and changesets.create in %v", names)
	}

	// diagnose.run
	dres, rerr := client.callTool("diagnose.run", map[string]any{"targetRef": "guest-nic:node1:100/net0"})
	if rerr != nil || dres.IsError {
		t.Fatalf("diagnose.run failed: err=%+v isErr=%v", rerr, dres.IsError)
	}

	// changesets.create → draft, origin mcp
	cres, rerr := client.callTool("changesets.create", map[string]any{"title": "ai-drafted", "ops": []any{}})
	if rerr != nil || cres.IsError {
		t.Fatalf("changesets.create failed: err=%+v isErr=%v content=%+v", rerr, cres.IsError, cres.Content)
	}
	var view changesetView
	remarshal(t, cres.StructuredContent, &view)
	if view.Status != "draft" {
		t.Fatalf("staged changeset status = %q, want draft", view.Status)
	}
	if view.Origin != change.OriginMCP {
		t.Fatalf("staged changeset origin = %q, want %q", view.Origin, change.OriginMCP)
	}
	if view.OriginTokenID != "tok-full" {
		t.Fatalf("staged changeset originTokenId = %q, want tok-full", view.OriginTokenID)
	}

	// The changeset persisted with the mcp origin and stayed a draft (no apply
	// path exists via MCP).
	stored, gerr := svc.Get(context.Background(), view.ID)
	if gerr != nil {
		t.Fatalf("Get staged changeset: %v", gerr)
	}
	if stored.Status != change.StatusDraft || stored.Origin != change.OriginMCP {
		t.Fatalf("stored changeset = status %q origin %q, want draft/mcp", stored.Status, stored.Origin)
	}
}

// TestMCPAuditActorIsDistinct is AC4: MCP tool invocations and the resulting
// changeset's audit trail carry actor mcp:<token-name>, visibly distinct from a
// UI-originated changeset by the same run.
func TestMCPAuditActorIsDistinct(t *testing.T) {
	auth := newFakeAuth()
	auth.add("full", TokenInfo{ID: "tok-full", Name: "ci-bot", Scopes: []string{"netRead", "netWrite", "automation"}})
	svc, audit := realChange(t, func() time.Time { return time.Unix(2000, 0) })
	deps := stubReads()
	deps.Auth = auth
	deps.Staging = svc
	deps.Audit = audit
	srv, _ := NewServer(deps)
	session, _ := srv.Authenticate(context.Background(), "full")

	// A human-originated changeset in the same store, for contrast.
	if _, err := svc.Create(context.Background(), "alice@pve", "human-drafted", nil); err != nil {
		t.Fatalf("human Create: %v", err)
	}

	// An MCP-staged changeset.
	client, _ := newMockClient(t, srv, session)
	client.initialize()
	cres, rerr := client.callTool("changesets.create", map[string]any{"title": "ai-drafted", "ops": []any{}})
	if rerr != nil || cres.IsError {
		t.Fatalf("changesets.create failed: %+v %v", rerr, cres.IsError)
	}

	rows, err := audit.List(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("audit.List: %v", err)
	}
	var sawMCPInvoke, sawMCPCreate, sawHumanCreate bool
	for _, r := range rows {
		switch {
		case r.Action == "mcp.tool.invoke" && r.Username == "mcp:ci-bot":
			sawMCPInvoke = true
		case r.Action == "changeset.create" && r.Username == "mcp:ci-bot":
			sawMCPCreate = true
		case r.Action == "changeset.create" && r.Username == "alice@pve":
			sawHumanCreate = true
		}
	}
	if !sawMCPInvoke {
		t.Errorf("no mcp.tool.invoke audit row with actor mcp:ci-bot")
	}
	if !sawMCPCreate {
		t.Errorf("no changeset.create audit row with actor mcp:ci-bot")
	}
	if !sawHumanCreate {
		t.Errorf("no changeset.create audit row for the human author (distinctness check)")
	}
}

// TestOutOfScopeToolIsUnknown: a read-scoped session calling changesets.create
// gets codeUnknownTool (scope membership is not leaked as a distinct forbidden
// error).
func TestOutOfScopeToolIsUnknown(t *testing.T) {
	auth := newFakeAuth()
	auth.add("read", TokenInfo{ID: "tok-read", Name: "reader", Scopes: []string{"netRead", "automation"}})
	svc, _ := realChange(t, func() time.Time { return time.Unix(3000, 0) })
	deps := stubReads()
	deps.Auth = auth
	deps.Staging = svc
	srv, _ := NewServer(deps)
	session, _ := srv.Authenticate(context.Background(), "read")
	client, _ := newMockClient(t, srv, session)
	client.initialize()

	if names := client.listToolNames(); contains(names, "changesets.create") {
		t.Fatalf("read-only session should not list changesets.create: %v", names)
	}
	_, rerr := client.callTool("changesets.create", map[string]any{"ops": []any{}})
	if rerr == nil || rerr.Code != codeUnknownTool {
		t.Fatalf("out-of-scope changesets.create error = %+v, want codeUnknownTool", rerr)
	}
}

// TestTokenRevocationClosesSession is AC5: revoking the token mid-session
// force-closes the stdio serve loop within one revocation tick.
func TestTokenRevocationClosesSession(t *testing.T) {
	auth := newFakeAuth()
	auth.add("full", TokenInfo{ID: "tok-full", Name: "ci-bot", Scopes: []string{"netRead", "automation"}})
	deps := stubReads()
	deps.Auth = auth
	deps.RevocationInterval = 10 * time.Millisecond
	srv, _ := NewServer(deps)
	session, _ := srv.Authenticate(context.Background(), "full")
	client, _ := newMockClient(t, srv, session)
	client.initialize()

	// Sanity: works before revocation.
	if _, rerr := client.callTool("topology.get", nil); rerr != nil {
		t.Fatalf("pre-revocation call failed: %+v", rerr)
	}

	auth.revoke("tok-full")
	err, done := client.waitServeDone(2 * time.Second)
	if !done {
		t.Fatalf("serve loop did not close within the revocation bound")
	}
	if err != ErrSessionRevoked {
		t.Fatalf("serve loop error = %v, want ErrSessionRevoked", err)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
