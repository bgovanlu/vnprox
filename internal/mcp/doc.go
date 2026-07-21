// Package mcp implements T-1701's Model Context Protocol (MCP) server: a
// first-class, capability-scoped surface that lets an AI operator (or any
// MCP-speaking automation) reach vnprox's READ surfaces (topology, findings,
// flows, IPAM, path simulation, the T-1307 diagnosis ladder) and STAGE draft
// changesets — and nothing more.
//
// # The central invariant (stage-only, structurally enforced)
//
// No apply/confirm/rollback/discard verb is reachable through this package, by
// any tool, parameter, or combination of tools. This is guaranteed by two
// independent structural facts, not by documentation or convention:
//
//  1. The tool registry (registry.go) is a fixed, enumerable allowlist of
//     exactly nine tools. There is no generic "call any route/method" bridge.
//     A package init() rejects (panics) any tool whose name matches a mutating
//     verb, and TestRegistryIsStageOnlyAllowlist / TestNoMutatingToolByName
//     pin the exact set so a future edit that adds an apply-shaped tool fails
//     loudly in CI.
//
//  2. The change-engine seam this package holds (ChangesetStager, server.go)
//     exposes only CreateWithOrigin/Validate/Diff — it has no Apply, Confirm,
//     Rollback, or Discard method at all, so no MCP code path can call one even
//     if a tool tried. TestChangesetStagerHasNoMutationVerb asserts this over
//     the interface's own method set by reflection (the same interface-surface
//     style T-1702's plugin seam uses).
//
// A human (or T-1103's confirm machinery) therefore remains the sole apply/
// confirm authority: an MCP-staged changeset is an ordinary draft that a person
// still reviews and applies through the authenticated UI/API, exactly like any
// other changeset since T-205.
//
// # Auth & scoping
//
// A session authenticates with a T-1104 bearer token (TokenAuthenticator).
// The token must carry the `automation` scope to open an MCP session at all;
// beyond that, each tool is exposed to the session only if the token's scopes
// cover the tool's RequiredScope (server derives exposure from the token, never
// from a client-asserted capability list). Revoking the token force-closes the
// session within one revocation tick (see Session.watch / ServeStdio).
//
// # AI origin is unerasable in audit
//
// Every changeset staged via MCP is created with change.OriginMCP and the
// authenticating token's id (CreateWithOrigin), and every MCP tool invocation
// writes its own audit row with actor `mcp:<token-name>`, so an operator
// reading GET /audit can always tell an AI-originated action from a
// UI-originated one (TestMCPAuditActorIsDistinct).
package mcp
