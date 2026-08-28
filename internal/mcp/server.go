// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/change/ifaces"
	"github.com/bgovanlu/vnprox/internal/store"
)

// ChangesetStager is the change-engine seam the MCP surface holds. It exposes
// ONLY draft staging (CreateWithOrigin/CreateWithProvenance), editing an
// already-open draft (UpdateDraft), re-validation (Validate), the read-only
// diff (Diff), and the read-only listing the open-draft cap counts (List). It
// has NO Apply, Confirm, Approve, Rollback, or Discard method — that omission
// is the structural half of T-1701's (and T-2705's) stage-only invariant: no
// MCP code path can reach a live-mutating verb because the type it is handed
// does not have one. *change.Service satisfies this (it has all those methods
// too, but this narrower view is all this package ever receives).
//
// The omission is asserted TWICE, deliberately:
//
//   - at compile time, by stageonly.go's closed-shape assertion — a type that
//     implements exactly these methods and no others is asserted to satisfy
//     this interface, so adding an Apply method here stops the package
//     BUILDING (T-2705 AC2);
//   - at test time, by TestChangesetStagerHasNoMutationVerb, over the
//     interface's own reflected method set (T-1701).
type ChangesetStager interface {
	CreateWithOrigin(ctx context.Context, author, title string, ops []change.Op, origin, originTokenID string) (change.Changeset, error)
	CreateWithProvenance(ctx context.Context, author, title string, ops []change.Op, p change.Provenance) (change.Changeset, error)
	UpdateDraft(ctx context.Context, id, author string, title *string, ops []change.Op) (change.Changeset, error)
	Validate(ctx context.Context, id, author string) (change.Changeset, error)
	Diff(ctx context.Context, id string) (*ifaces.ChangesetDiff, error)
	List(ctx context.Context, status string) ([]change.Changeset, error)
}

// PolicyChecker is T-2601's policy engine as this package consumes it: ONE
// method, which evaluates a rule set over a list of ops and stages nothing at
// all (change.Service.EvaluatePolicySet is pure w.r.t. the store — it writes no
// row, opens no changeset, and is asserted so by that package's own
// TestEvaluatePolicyForChangeset_StagesNothing). Passing the zero PolicySet
// makes it evaluate the cluster's INSTALLED set, which is what every caller
// here does: a staging tool must be judged by the operator's own rules, not by
// rules the caller chose.
//
// Deliberately separate from ChangesetStager: policy evaluation is a read, and
// keeping it off the staging seam means the staging seam's method set stays
// exactly "the verbs that open or edit a draft" — which is what the AC2
// compile-time assertion is asserting about.
type PolicyChecker interface {
	EvaluatePolicySet(ctx context.Context, set change.PolicySet, ops []change.Op) (change.PolicyResult, error)
}

// ToolFunc is the seam for a read tool: it takes the raw JSON arguments the
// client passed and returns a JSON-marshalable result. cmd/vnproxd wires each
// one as a closure over the same live service the corresponding HTTP handler
// uses. A nil ToolFunc means the surface isn't available on this daemon (the
// tool still exists in the registry, but calling it returns a clear
// "not available" tool error rather than panicking).
type ToolFunc func(ctx context.Context, args json.RawMessage) (any, error)

// TokenInfo is the resolved identity of an authenticated bearer token.
type TokenInfo struct {
	ID     string
	Name   string
	Scopes []string
}

// TokenAuthenticator resolves and re-checks bearer tokens. It is the ONLY
// authentication path into an MCP session — there is no cookie/session bridge.
type TokenAuthenticator interface {
	// Authenticate resolves a raw bearer token value to its TokenInfo, or an
	// error for a missing/invalid/revoked token. It must not apply the
	// automation-scope gate itself — that is Server.Authenticate's job, so the
	// gate lives in one place.
	Authenticate(ctx context.Context, raw string) (TokenInfo, error)
	// Live reports whether tokenID is still valid (not revoked). The revocation
	// watcher polls this so a token revoked mid-session force-closes the
	// session within one tick (AC5).
	Live(ctx context.Context, tokenID string) bool
}

// Auditor is the append-only audit seam (docs/security.md: every action is
// recorded). *store.AuditRepo satisfies it directly. Optional: a nil Audit
// simply skips the per-invocation audit row (degraded/test mode).
type Auditor interface {
	Append(ctx context.Context, e store.AuditEntry) (int64, error)
}

// Deps configures a Server. Auth is required. Staging and the read ToolFuncs
// are each optional (nil => that tool reports "not available"); Audit is
// optional. Now/Logger/RevocationInterval default sensibly.
type Deps struct {
	Auth    TokenAuthenticator
	Staging ChangesetStager
	// Policy is T-2601's evaluator (T-2705). When nil, the typed staging
	// tools refuse to stage at all rather than staging unchecked: a
	// deployment that cannot evaluate its policy must not let an AI operator
	// past it. (Nil is a wiring state, not a configuration: cmd/vnproxd
	// always wires the change service here.)
	Policy   PolicyChecker
	Audit    Auditor
	Topology ToolFunc
	Findings ToolFunc
	Flows    ToolFunc
	IPAM     ToolFunc
	Simulate ToolFunc
	Diagnose ToolFunc
	Logger   *slog.Logger
	Now      func() time.Time
	// RevocationInterval is how often a long-lived transport re-checks the
	// token's liveness (AC5's "within one server tick"). Defaults to
	// DefaultRevocationInterval.
	RevocationInterval time.Duration
	// StageRateLimit / StageRateWindow bound how often ONE session may stage
	// (T-2705 AC5). Defaults: DefaultStageRateLimit calls per
	// DefaultStageRateWindow. A non-positive StageRateLimit uses the default;
	// there is deliberately no "unlimited" setting.
	StageRateLimit  int
	StageRateWindow time.Duration
	// MaxOpenMCPDrafts caps how many MCP-staged changesets may be open
	// (draft/validated) cluster-wide at once. Defaults to
	// DefaultMaxOpenMCPDrafts. Exceeding it refuses further staging with a
	// message naming the cap, so the model is told what to do about it
	// (apply or discard one) rather than merely failing.
	MaxOpenMCPDrafts int
}

// DefaultRevocationInterval is the poll cadence for the mid-session
// token-revocation check on long-lived transports.
const DefaultRevocationInterval = time.Second

// Staging budget defaults (T-2705 AC5). They bound the blast radius of a
// looping or runaway AI operator in the two dimensions that matter: how fast
// it can stage, and how much unreviewed work it can leave behind.
const (
	DefaultStageRateLimit   = 12
	DefaultStageRateWindow  = time.Minute
	DefaultMaxOpenMCPDrafts = 10
)

// ErrAuthRequired / ErrAutomationScopeRequired distinguish "no/invalid token"
// from "token lacks the automation scope" so a transport can map them to the
// right status (401 vs 403).
var (
	ErrAuthRequired            = errors.New("mcp: authentication required")
	ErrAutomationScopeRequired = errors.New("mcp: token is missing the automation scope required for MCP access")
)

// Server is a constructed MCP server: the fixed tool registry bound to a set
// of live dependencies. It is safe for concurrent use; a Session is a
// lightweight per-connection view over it.
type Server struct {
	now      func() time.Time
	log      *slog.Logger
	handlers map[string]toolHandler
	limiter  *stageLimiter
	deps     Deps
	revoke   time.Duration
	// stageRate/stageWindow/maxOpenDrafts are the resolved (defaulted) staging
	// budget — see Deps.
	stageRate     int
	stageWindow   time.Duration
	maxOpenDrafts int
}

// toolHandler executes one tool for a session. Read tools wrap a Deps ToolFunc;
// the changesets.* tools are handled internally so origin-labelling and audit
// attribution stay centralized here.
type toolHandler func(ctx context.Context, s *Session, args json.RawMessage) (any, error)

// NewServer constructs a Server. Deps.Auth is required.
func NewServer(deps Deps) (*Server, error) {
	if deps.Auth == nil {
		return nil, fmt.Errorf("mcp: Deps.Auth is required")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}
	revoke := deps.RevocationInterval
	if revoke <= 0 {
		revoke = DefaultRevocationInterval
	}
	stageRate := deps.StageRateLimit
	if stageRate <= 0 {
		stageRate = DefaultStageRateLimit
	}
	stageWindow := deps.StageRateWindow
	if stageWindow <= 0 {
		stageWindow = DefaultStageRateWindow
	}
	maxOpen := deps.MaxOpenMCPDrafts
	if maxOpen <= 0 {
		maxOpen = DefaultMaxOpenMCPDrafts
	}
	s := &Server{
		deps: deps, now: now, log: log, revoke: revoke,
		limiter:   newStageLimiter(),
		stageRate: stageRate, stageWindow: stageWindow, maxOpenDrafts: maxOpen,
	}
	s.handlers = s.buildHandlers()
	// Defence in depth: every registered handler must map to an allowlisted
	// tool name, and every allowlisted tool must have a handler — a mismatch
	// is a wiring bug that would otherwise surface as a silent
	// method-not-found at runtime.
	for name := range s.handlers {
		if _, ok := toolByName(name); !ok {
			return nil, fmt.Errorf("mcp: handler registered for unknown tool %q", name)
		}
	}
	for _, spec := range toolSpecs {
		if _, ok := s.handlers[spec.Name]; !ok {
			return nil, fmt.Errorf("mcp: no handler wired for tool %q", spec.Name)
		}
	}
	return s, nil
}

func (s *Server) buildHandlers() map[string]toolHandler {
	readTool := func(fn ToolFunc, label string) toolHandler {
		return func(ctx context.Context, _ *Session, args json.RawMessage) (any, error) {
			if fn == nil {
				return nil, fmt.Errorf("%s is not available on this daemon", label)
			}
			return fn(ctx, args)
		}
	}
	return map[string]toolHandler{
		ToolTopologyGet:        readTool(s.deps.Topology, "topology read"),
		ToolFindingsList:       readTool(s.deps.Findings, "findings read"),
		ToolFlowsQuery:         readTool(s.deps.Flows, "flows read"),
		ToolIPAMSubnetsList:    readTool(s.deps.IPAM, "IPAM read"),
		ToolSimulatePath:       readTool(s.deps.Simulate, "path simulation"),
		ToolDiagnoseRun:        readTool(s.deps.Diagnose, "diagnosis ladder"),
		ToolChangesetsDiff:     s.handleChangesetDiff,
		ToolChangesetsCreate:   s.handleChangesetCreate,
		ToolChangesetsValidate: s.handleChangesetValidate,
		ToolStageBridge:        stageHandler(ToolStageBridge, buildBridgeCreateOp),
		ToolStageIface:         stageHandler(ToolStageIface, buildIfaceUpdateOp),
		ToolStageFwRule:        stageHandler(ToolStageFwRule, buildFwRuleCreateOp),
		ToolStageIPAM:          stageHandler(ToolStageIPAM, buildIpamAllocOp),
	}
}

// Authenticate resolves raw into a Session, applying the automation-scope gate
// that guards MCP access as a whole. It returns ErrAuthRequired for a
// missing/invalid/revoked token and ErrAutomationScopeRequired for a valid
// token that lacks the automation scope.
func (s *Server) Authenticate(ctx context.Context, raw string) (*Session, error) {
	if raw == "" {
		return nil, ErrAuthRequired
	}
	info, err := s.deps.Auth.Authenticate(ctx, raw)
	if err != nil {
		return nil, ErrAuthRequired
	}
	scopes := make(map[string]bool, len(info.Scopes))
	for _, sc := range info.Scopes {
		scopes[sc] = true
	}
	if !scopes[scopeAutomation] {
		return nil, ErrAutomationScopeRequired
	}
	name := info.Name
	if name == "" {
		name = info.ID
	}
	return &Session{
		srv:       s,
		TokenID:   info.ID,
		TokenName: name,
		actor:     "mcp:" + name,
		scopes:    scopes,
	}, nil
}

// HandleMessage dispatches one inbound JSON-RPC message for session and returns
// the marshaled response bytes, or nil for a notification (which gets no
// reply). A parse error still yields a well-formed JSON-RPC error response with
// a null id. HandleMessage never returns an error itself — every failure is
// encoded into the JSON-RPC envelope.
func (s *Server) HandleMessage(ctx context.Context, session *Session, raw []byte) []byte {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return mustMarshal(newError(nil, codeParseError, "parse error", nil))
	}
	resp := s.dispatch(ctx, session, req)
	if req.Notification() || resp == nil {
		return nil
	}
	return mustMarshal(resp)
}

func (s *Server) dispatch(ctx context.Context, session *Session, req rpcRequest) *rpcResponse {
	if req.JSONRPC != jsonRPCVersion {
		return newError(req.ID, codeInvalidRequest, "unsupported jsonrpc version", nil)
	}
	switch req.Method {
	case "initialize":
		return newResult(req.ID, s.initializeResult(req.Params))
	case "notifications/initialized", "notifications/cancelled":
		return nil // notification, no response
	case "ping":
		return newResult(req.ID, struct{}{})
	case "tools/list":
		return newResult(req.ID, listToolsResult{Tools: session.exposedToolDescriptors()})
	case "tools/call":
		return s.handleToolsCall(ctx, session, req)
	default:
		return newError(req.ID, codeMethodNotFound, "unknown method: "+req.Method, nil)
	}
}

func (s *Server) initializeResult(params json.RawMessage) initializeResult {
	version := protocolVersion
	if len(params) > 0 {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(params, &p); err == nil && p.ProtocolVersion != "" {
			version = p.ProtocolVersion
		}
	}
	return initializeResult{
		ProtocolVersion: version,
		Capabilities:    serverCapabilities{Tools: &toolsCapability{ListChanged: false}},
		ServerInfo:      serverInfo{Name: serverName, Version: serverVersion},
	}
}

// callToolParams is the MCP tools/call request params.
type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (s *Server) handleToolsCall(ctx context.Context, session *Session, req rpcRequest) *rpcResponse {
	var p callToolParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return newError(req.ID, codeInvalidParams, "invalid tools/call params", nil)
		}
	}
	if p.Name == "" {
		return newError(req.ID, codeInvalidParams, "tools/call requires a tool name", nil)
	}

	result, err := session.call(ctx, p.Name, p.Arguments)
	if err != nil {
		var scopeErr errUnknownTool
		if errors.As(err, &scopeErr) {
			// Out-of-scope and genuinely-unknown tools are indistinguishable
			// in the error, so scope membership isn't leaked.
			return newError(req.ID, codeUnknownTool, "unknown tool", nil)
		}
		if errors.Is(err, errRevoked) {
			return newError(req.ID, codeInvalidRequest, "token revoked", nil)
		}
		// A tool-level failure is reported as an MCP tool result with
		// isError=true (the model can see and reason about it), not a
		// protocol error.
		return newResult(req.ID, callToolResult{
			Content: []textContent{{Type: "text", Text: err.Error()}},
			IsError: true,
		})
	}
	return newResult(req.ID, toolResult(result))
}

// toolResult wraps a handler's JSON-marshalable value into MCP's callToolResult
// (a text content block carrying the JSON plus the structured object).
func toolResult(v any) callToolResult {
	text, err := json.Marshal(v)
	if err != nil {
		return callToolResult{Content: []textContent{{Type: "text", Text: "result could not be serialized: " + err.Error()}}, IsError: true}
	}
	return callToolResult{
		StructuredContent: v,
		Content:           []textContent{{Type: "text", Text: string(text)}},
	}
}

// --- changeset staging tool handlers ----------------------------------------

// changesetView is the metadata MCP returns for a staged/validated changeset.
// It deliberately omits the ops themselves (they can carry sealed secrets such
// as a wg preshared key, and an MCP client already knows the ops it submitted),
// surfacing only the identity, provenance, status, and validation findings a
// caller needs to decide what to do next.
type changesetView struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Author        string `json:"author"`
	Status        string `json:"status"`
	Origin        string `json:"origin"`
	OriginTokenID string `json:"originTokenId,omitempty"`
	// OriginTool (T-2705) is the staging tool's own name, so the model sees
	// the same tag the review API shows a human (`originTool` on every
	// changeset response). Empty for a draft this surface did not stage with
	// a typed tool.
	OriginTool string           `json:"originTool,omitempty"`
	Findings   []change.Finding `json:"findings"`
	CreatedAt  int64            `json:"createdAt"`
	UpdatedAt  int64            `json:"updatedAt"`
}

func toChangesetView(c change.Changeset) changesetView {
	findings := c.Findings
	if findings == nil {
		findings = []change.Finding{}
	}
	return changesetView{
		ID: c.ID, Title: c.Title, Author: c.Author, Status: string(c.Status),
		Origin: c.Origin, OriginTokenID: c.OriginTokenID, OriginTool: c.OriginTool,
		Findings:  findings,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

func (s *Server) handleChangesetCreate(ctx context.Context, session *Session, args json.RawMessage) (any, error) {
	if s.deps.Staging == nil {
		return nil, fmt.Errorf("changeset staging is not available on this daemon")
	}
	var req struct {
		Title string      `json:"title"`
		Ops   []change.Op `json:"ops"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid changesets.create arguments: %w", err)
		}
	}
	// Origin is stamped mcp and the changeset is attributed to the token, so
	// the audit trail can never lose the fact that an AI staged it. The author
	// recorded on the changeset (and thus its changeset.create audit row) is
	// the mcp:<token-name> actor, visibly distinct from a human username.
	c, err := s.deps.Staging.CreateWithOrigin(ctx, session.actor, req.Title, req.Ops, change.OriginMCP, session.TokenID)
	if err != nil {
		return nil, err
	}
	return toChangesetView(c), nil
}

func (s *Server) handleChangesetValidate(ctx context.Context, session *Session, args json.RawMessage) (any, error) {
	if s.deps.Staging == nil {
		return nil, fmt.Errorf("changeset staging is not available on this daemon")
	}
	id, err := idArg(args)
	if err != nil {
		return nil, err
	}
	c, err := s.deps.Staging.Validate(ctx, id, session.actor)
	if err != nil {
		return nil, err
	}
	return toChangesetView(c), nil
}

func (s *Server) handleChangesetDiff(ctx context.Context, _ *Session, args json.RawMessage) (any, error) {
	if s.deps.Staging == nil {
		return nil, fmt.Errorf("changeset staging is not available on this daemon")
	}
	id, err := idArg(args)
	if err != nil {
		return nil, err
	}
	diff, err := s.deps.Staging.Diff(ctx, id)
	if err != nil {
		return nil, err
	}
	return diff, nil
}

func idArg(args json.RawMessage) (string, error) {
	var req struct {
		ID string `json:"id"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &req); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if req.ID == "" {
		return "", fmt.Errorf("id is required")
	}
	return req.ID, nil
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// A response we build ourselves should always marshal; if it somehow
		// doesn't, emit a minimal internal-error envelope rather than nothing.
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"internal error"}}`)
	}
	return b
}
