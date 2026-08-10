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
//     exactly thirteen tools. There is no generic "call any route/method"
//     bridge. A package init() rejects (panics) any tool whose name matches a
//     mutating verb, and TestRegistryIsStageOnlyAllowlist /
//     TestNoMutatingToolByName / TestNoApplyConfirmOrDeleteToolName pin the
//     exact set — the last of those also enumerating the WIRED HANDLER MAP, so
//     a tool smuggled in by wiring rather than by declaration is caught too.
//
//  2. The change-engine seam this package holds (ChangesetStager, server.go)
//     exposes only CreateWithOrigin/CreateWithProvenance/UpdateDraft/Validate/
//     Diff/List — it has no Apply, Confirm, Approve, Rollback, or Discard
//     method at all, so no MCP code path can call one even if a tool tried.
//     T-2705 made this a COMPILE-TIME guarantee (stageonly.go): a placeholder
//     type implementing exactly those verbs and nothing else is asserted to
//     satisfy the interface, so widening it stops the package building, naming
//     the offending method. TestChangesetStagerHasNoMutationVerb additionally
//     asserts it over the interface's own reflected method set (the same
//     interface-surface style T-1702's plugin seam uses).
//
// # Staging (T-2705)
//
// Four typed tools — changesets.stage.bridge/.iface/.fwrule/.ipam — turn a
// small, schema-described request into exactly ONE op in a draft changeset, so
// an AI operator can propose a concrete change instead of handing a human a
// paragraph to type. Every one of them funnels through Server.stage (stage.go):
// rate limit, build the op, POLICY (T-2601's evaluator, before any row exists —
// a denial names the refusing rule's id and description and stages nothing),
// open-draft cap, stage. Nothing else. A policy set that cannot be evaluated
// fails closed.
//
// A human (or T-1103's confirm machinery) therefore remains the sole apply/
// confirm authority: an MCP-staged changeset is an ordinary draft that a person
// still reviews and applies through the authenticated UI/API, exactly like any
// other changeset since T-205 — and it is tagged, unerasably, with the tool and
// the session that produced it (change.OriginMCP + the token id + the tool
// name), all three of which survive to the review API.
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
