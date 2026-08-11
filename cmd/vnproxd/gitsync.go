// gitsync.go wires T-2701's git-backed spec sync into the daemon.
//
// Three things happen here and nothing else: the [gitsync] section becomes a
// *gitsync.Service, that service is registered as a supervised actor in the
// run group, and its issues are adapted into the unified findings stream.
//
// Two properties this file exists to keep:
//
//   - **Off by default.** With `[gitsync] enabled = false` (or no section at
//     all) buildGitSync returns a Service that contacts nothing. It is still
//     constructed and still registered, so `GET /gitsync/status` has an
//     honest answer and the actor set does not change shape with config —
//     but it fetches nothing, reads no credential file, and writes nothing.
//   - **A remote that is down never blocks startup.** The service's Run
//     blocks for the daemon's lifetime and returns nil on cancellation; a
//     failing poll degrades to a finding and a retry inside that loop. The
//     only gitsync condition fatal at startup is a remote this daemon cannot
//     even describe (a malformed URL, an unguessable provider, an unreadable
//     trust anchor) — a configuration error, not an outage.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// buildGitSync resolves the [gitsync] section into a Service.
//
// changesets is late-bound through the caller (change.Service is built well
// after the findings engine), so this takes it as an argument and is called
// once both exist.
func buildGitSync(cfg config.GitSyncConfig, changesets gitsync.ChangesetStager, graph *inventory.Graph, audit *store.AuditRepo, logger *slog.Logger) (*gitsync.Service, error) {
	if !cfg.Enabled {
		// Nothing is read — not the token file, not the trust anchors, and
		// certainly not the remote.
		return gitsync.New(gitsync.Config{Logger: logger}), nil
	}

	token, err := readGitSyncToken(cfg.TokenFile)
	if err != nil {
		return nil, err
	}
	source, err := gitsync.NewHTTPSource(gitsync.SourceConfig{
		URL:      cfg.URL,
		Provider: gitsync.Provider(cfg.Provider),
		Token:    token,
	})
	if err != nil {
		return nil, err
	}

	var signers []gitsync.AllowedSigner
	if cfg.RequireSignedCommits {
		// A daemon must never come up quietly enforcing a signature policy
		// it could not load the anchors for — the same stance
		// [changesets] policy_file takes (internal/config's doc comment).
		signers, err = gitsync.LoadAllowedSigners(cfg.AllowedSignersFile)
		if err != nil {
			return nil, err
		}
	}

	return gitsync.New(gitsync.Config{
		Enabled:              true,
		Source:               source,
		Ref:                  cfg.Ref,
		Path:                 cfg.Path,
		PollInterval:         cfg.PollInterval,
		RequireSignedCommits: cfg.RequireSignedCommits,
		AllowedSigners:       signers,
		Changesets:           changesets,
		Inventory:            graph,
		Audit:                audit,
		Logger:               logger,
	}), nil
}

// buildGitSyncProposer resolves the [gitsync] section into T-2702's
// Proposer: the path that turns a staged changeset into a pull request
// against the spec repository.
//
// It is built unconditionally and is INERT unless the operator has set a
// write-scoped credential ([gitsync] push_token_file). That key is separate
// from token_file on purpose: syncing needs only a read, proposing needs a
// write, and a deployment that never asked to propose anything never has a
// write credential read off disk — let alone held in memory.
//
// The Proposer holds one read seam and one write seam, which are different
// types (gitsync.Source and gitsync.Host). Nothing here can widen the sync
// path into a pushing one.
func buildGitSyncProposer(
	cfg config.GitSyncConfig,
	changesets gitsync.ChangesetReader,
	graph *inventory.Graph,
	proposals gitsync.ProposalStore,
	mgmt mgmtPathSource,
	audit *store.AuditRepo,
	logger *slog.Logger,
) (*gitsync.Proposer, error) {
	if !cfg.Enabled || cfg.PushTokenFile == "" {
		// Nothing is read: not the push credential, not the remote.
		return gitsync.NewProposer(gitsync.ProposerConfig{Logger: logger}), nil
	}

	pushToken, err := readGitSyncToken(cfg.PushTokenFile)
	if err != nil {
		return nil, err
	}
	readToken, err := readGitSyncToken(cfg.TokenFile)
	if err != nil {
		return nil, err
	}
	// The base document is read through the ordinary READ source, with the
	// ordinary read credential — proposing does not read through the write
	// host, and the write host does not fetch specs.
	source, err := gitsync.NewHTTPSource(gitsync.SourceConfig{
		URL: cfg.URL, Provider: gitsync.Provider(cfg.Provider), Token: readToken,
	})
	if err != nil {
		return nil, err
	}
	host, err := gitsync.NewHTTPHost(gitsync.HostConfig{
		URL: cfg.URL, Provider: gitsync.Provider(cfg.Provider), Token: pushToken,
	})
	if err != nil {
		return nil, err
	}

	return gitsync.NewProposer(gitsync.ProposerConfig{
		Enabled: true, Source: source, Host: host, Ref: cfg.Ref, Path: cfg.Path,
		Changesets: changesets, Inventory: graph, Proposals: proposals, Audit: audit,
		MgmtPaths: mgmtPathsFrom(mgmt), Logger: logger,
	}), nil
}

// mgmtPathSource is the management-path read the blast radius in a proposal's
// body is evaluated against — *change.Service satisfies it. Nil-safe: a
// deployment without it reports the management path as "not evaluated" in the
// pull-request body rather than reporting a false "not touched".
type mgmtPathSource interface {
	MgmtStatus(ctx context.Context) (change.MgmtStatus, error)
}

func mgmtPathsFrom(mgmt mgmtPathSource) func(context.Context) map[string][]topology.MgmtPath {
	if mgmt == nil {
		return nil
	}
	return func(ctx context.Context) map[string][]topology.MgmtPath {
		status, err := mgmt.MgmtStatus(ctx)
		if err != nil {
			return nil
		}
		return status.Nodes
	}
}

// readGitSyncToken reads the host credential from its own root:root 0600
// file — the same on-disk-secret convention [oidc] client_secret_file and
// [pve] token_file already use, and the reason no credential is ever written
// into vnprox.toml. An empty token_file means an unauthenticated read of a
// public repository, which is a legitimate configuration.
//
// The error deliberately names the path and not the contents.
func readGitSyncToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // operator-configured credential path, same convention as [pve] token_file
	if err != nil {
		return "", fmt.Errorf("gitsync: reading token file %s: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// gitSyncFindingsAdapter converts *gitsync.Service's issues into the unified
// findings shape, late-bound the same way haFindingsAdapter is: the findings
// engine is built before the change engine the sync service needs.
type gitSyncFindingsAdapter struct {
	svc *gitsync.Service
	mu  sync.Mutex
}

func (a *gitSyncFindingsAdapter) set(svc *gitsync.Service) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.svc = svc
}

// GitSyncIssues implements findings.GitSyncProvider. Before the service
// exists (or when gitsync is off) it reports nothing — the same
// degrade-before-ready contract every other adapter in this package has.
func (a *gitSyncFindingsAdapter) GitSyncIssues() []findings.GitSyncIssue {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return nil
	}
	issues := svc.Issues()
	out := make([]findings.GitSyncIssue, 0, len(issues))
	for _, iss := range issues {
		out = append(out, findings.GitSyncIssue{Check: iss.Check, Severity: iss.Severity, Detail: iss.Detail})
	}
	return out
}

// gitSyncStatusAdapter is the API seam (internal/api.GitSyncStatusService),
// late-bound for the same reason.
type gitSyncStatusAdapter struct {
	svc *gitsync.Service
	mu  sync.Mutex
}

func (a *gitSyncStatusAdapter) set(svc *gitsync.Service) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.svc = svc
}

func (a *gitSyncStatusAdapter) Status() gitsync.Status {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return gitsync.Status{}
	}
	return svc.Status()
}
