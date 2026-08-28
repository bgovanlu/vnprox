// SPDX-License-Identifier: Apache-2.0

// stage.go is T-2705's typed staging surface: the four `changesets.stage.*`
// tools that turn a small, schema-described request from an AI operator into
// exactly ONE op in a DRAFT changeset.
//
// Everything mutating on this surface funnels through one function,
// Server.stage, so the four tools cannot drift apart on any of the guarantees
// the card is about:
//
//	rate limit  ->  build the op  ->  POLICY  ->  open-draft cap  ->  stage
//
// and nothing else. There is no branch in this file that reaches an apply,
// confirm, approve, or discard verb, and — more to the point — there could not
// be one: the only change-engine value this package holds is ChangesetStager,
// whose method set has no such verb (server.go), and stageonly.go asserts that
// at COMPILE time. A tool that applied could not be written here without first
// changing that interface, at which point the package stops building.
//
// The policy check is the deliberate ordering choice: T-2601's
// EvaluatePolicySet is evaluated BEFORE the draft exists, so a denied op leaves
// no row behind at all — the model gets the refusing rule's id and description
// and can try something else, rather than leaving a poisoned draft for a human
// to clean up.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// stageArgs are the fields every typed staging tool accepts on top of its own
// op-specific ones.
type stageArgs struct {
	TargetRef   string `json:"targetRef"`
	Title       string `json:"title"`
	ChangesetID string `json:"changesetId"`
}

// opBuilder turns one tool's raw JSON arguments into the single op it stages,
// plus a short human summary used as the draft's default title. It is pure: it
// touches no service, so an argument error is reported before anything is
// rate-limited, policy-checked, or written.
type opBuilder func(args json.RawMessage) (change.Op, stageArgs, string, error)

// stageHandler adapts an opBuilder into a toolHandler bound to tool's name (the
// name is what the staged changeset is tagged with — AC4).
func stageHandler(tool string, build opBuilder) toolHandler {
	return func(ctx context.Context, s *Session, args json.RawMessage) (any, error) {
		return s.srv.stage(ctx, s, tool, build, args)
	}
}

// ErrStagingUnavailable / ErrPolicyUnavailable are the two "this daemon cannot
// honour the guarantee" refusals. Both fail CLOSED: a surface that cannot
// policy-check an op does not stage it.
var (
	ErrStagingUnavailable = errors.New("mcp: changeset staging is not available on this daemon")
	ErrPolicyUnavailable  = errors.New("mcp: the policy engine is not available on this daemon, so no op can be policy-checked before staging")
)

// DeniedRule is one policy rule that refused an op, as reported back to the
// caller: the rule's id and its description, which is what makes the refusal
// actionable to a model rather than an opaque failure (AC3).
type DeniedRule struct {
	RuleID      string `json:"ruleId"`
	Description string `json:"description"`
}

// PolicyDeniedError is returned by a staging tool whose op the cluster's
// installed policy denies. NOTHING was staged when this is returned. It carries
// the denying rules structurally (for an in-process caller such as T-2808's
// in-app assistant, via errors.As) and renders them into the message (for an
// MCP client, which only ever sees the text).
type PolicyDeniedError struct {
	Tool  string
	Rules []DeniedRule
	// Findings are the blocking findings' messages, which name the specific
	// assertion that failed within each rule.
	Findings []string
}

func (e *PolicyDeniedError) Error() string {
	var b strings.Builder
	b.WriteString("policy denied this op, so nothing was staged")
	for _, r := range e.Rules {
		fmt.Fprintf(&b, "; rule %q: %s", r.RuleID, r.Description)
	}
	for _, f := range e.Findings {
		b.WriteString("; ")
		b.WriteString(f)
	}
	return b.String()
}

// RateLimitedError reports that this session has staged too often. It names the
// budget so the caller knows how long to wait.
type RateLimitedError struct {
	Limit  int
	Window time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("staging rate limit exceeded: this session may stage at most %d times per %s; nothing was staged", e.Limit, e.Window)
}

// OpenDraftCapError reports that too many MCP-staged changesets are already
// open. AC5: the message names the cap.
type OpenDraftCapError struct {
	Cap  int
	Open int
}

func (e *OpenDraftCapError) Error() string {
	return fmt.Sprintf("open MCP draft cap reached: %d MCP-staged changesets are already open and the cap is %d; apply or discard one before staging another — nothing was staged", e.Open, e.Cap)
}

// stage is the single mutating path of the whole MCP surface.
func (s *Server) stage(ctx context.Context, session *Session, tool string, build opBuilder, rawArgs json.RawMessage) (any, error) {
	if s.deps.Staging == nil {
		return nil, ErrStagingUnavailable
	}
	if s.deps.Policy == nil {
		return nil, ErrPolicyUnavailable
	}
	// 1. Rate limit. Charged before any work so a hot loop is cheap to refuse,
	// and charged on the attempt (not the success) so a session cannot probe
	// policy for free.
	if !s.limiter.allow(session.TokenID, s.now(), s.stageRate, s.stageWindow) {
		return nil, &RateLimitedError{Limit: s.stageRate, Window: s.stageWindow}
	}

	// 2. Build the op from the tool's own arguments. Pure; nothing is written.
	op, common, summary, err := build(rawArgs)
	if err != nil {
		return nil, fmt.Errorf("invalid %s arguments: %w", tool, err)
	}

	// 3. Resolve the destination: an existing open MCP draft, or a new one.
	var target *change.Changeset
	if common.ChangesetID != "" {
		target, err = s.openMCPDraft(ctx, common.ChangesetID)
		if err != nil {
			return nil, err
		}
	}

	// 4. POLICY, before anything is staged (AC3). Evaluated over the ops the
	// draft would END UP with, so a rule about the whole changeset (e.g.
	// changeset.opCount) judges the real result rather than this op alone.
	ops := []change.Op{op}
	if target != nil {
		ops = append(append([]change.Op{}, target.Ops...), op)
	}
	if perr := s.checkPolicy(ctx, tool, ops); perr != nil {
		return nil, perr
	}

	// 5. The open-draft cap — only when this call would OPEN a draft; adding
	// an op to a draft that is already counted cannot make the count worse.
	if target == nil {
		if cerr := s.checkOpenDraftCap(ctx); cerr != nil {
			return nil, cerr
		}
	}

	// 6. Stage. This is the whole of what an AI operator can do to a cluster.
	if target != nil {
		c, uerr := s.deps.Staging.UpdateDraft(ctx, target.ID, session.actor, nil, ops)
		if uerr != nil {
			return nil, fmt.Errorf("appending a %s op to changeset %s: %w", op.Type, target.ID, uerr)
		}
		s.log.Info("mcp: op staged into an existing draft",
			"tool", tool, "changeset_id", c.ID, "op", string(op.Type), "target", op.Target.String(),
			"token_id", session.TokenID, "actor", session.actor)
		return toChangesetView(c), nil
	}

	title := common.Title
	if title == "" {
		title = summary
	}
	c, cerr := s.deps.Staging.CreateWithProvenance(ctx, session.actor, title, ops, change.Provenance{
		Origin:  change.OriginMCP,
		TokenID: session.TokenID,
		Tool:    tool,
	})
	if cerr != nil {
		return nil, fmt.Errorf("staging a %s op as a new draft: %w", op.Type, cerr)
	}
	s.log.Info("mcp: draft staged",
		"tool", tool, "changeset_id", c.ID, "op", string(op.Type), "target", op.Target.String(),
		"token_id", session.TokenID, "actor", session.actor)
	return toChangesetView(c), nil
}

// checkPolicy runs T-2601's evaluator over ops with the cluster's INSTALLED
// rule set (the zero PolicySet selects it) and turns a denial into a
// *PolicyDeniedError naming every refusing rule. It stages nothing: the
// evaluator is a pure read (see PolicyChecker).
func (s *Server) checkPolicy(ctx context.Context, tool string, ops []change.Op) error {
	result, err := s.deps.Policy.EvaluatePolicySet(ctx, change.PolicySet{}, ops)
	if err != nil {
		// Fail closed: an unreadable/unparsable policy set is exactly the
		// situation in which an AI operator must NOT be allowed past.
		return fmt.Errorf("refusing to stage: the cluster's policy set could not be evaluated: %w", err)
	}
	if !result.Denied() {
		return nil
	}
	denied := &PolicyDeniedError{Tool: tool}
	for _, rr := range result.Rules {
		if rr.Severity == change.PolicyDeny && len(rr.ViolatingOps) > 0 {
			denied.Rules = append(denied.Rules, DeniedRule{RuleID: rr.RuleID, Description: rr.Description})
		}
	}
	for _, f := range result.Findings {
		if f.Severity == change.SeverityError {
			denied.Findings = append(denied.Findings, f.Message)
		}
	}
	s.log.Info("mcp: staging refused by policy", "tool", tool, "rules", len(denied.Rules))
	return denied
}

// checkOpenDraftCap refuses to open another MCP draft once maxOpenDrafts of
// them are already open (draft or validated — the two editable states, i.e.
// exactly the changesets still waiting on a human).
func (s *Server) checkOpenDraftCap(ctx context.Context) error {
	open, err := s.openMCPDrafts(ctx)
	if err != nil {
		return err
	}
	if len(open) >= s.maxOpenDrafts {
		return &OpenDraftCapError{Cap: s.maxOpenDrafts, Open: len(open)}
	}
	return nil
}

// openMCPDrafts lists every still-open (editable) MCP-staged changeset.
func (s *Server) openMCPDrafts(ctx context.Context) ([]change.Changeset, error) {
	all, err := s.deps.Staging.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("counting open MCP drafts: %w", err)
	}
	var out []change.Changeset
	for _, c := range all {
		if c.Origin == change.OriginMCP && c.Editable() {
			out = append(out, c)
		}
	}
	return out, nil
}

// openMCPDraft resolves the changesetId a staging call asked to append to. It
// must be an MCP-staged changeset that is still editable — an AI operator may
// extend its own open draft, never edit a human's changeset and never revive
// one that has moved on.
func (s *Server) openMCPDraft(ctx context.Context, id string) (*change.Changeset, error) {
	open, err := s.openMCPDrafts(ctx)
	if err != nil {
		return nil, err
	}
	for i := range open {
		if open[i].ID == id {
			return &open[i], nil
		}
	}
	return nil, fmt.Errorf("changeset %s is not an open MCP-staged draft; omit changesetId to open a new one", id)
}

// --- op builders -------------------------------------------------------------
//
// Each builder decodes its tool's arguments, resolves the target ref, and
// returns the ONE op it stages. They never touch a service and never return
// more than one op: "one tool call, one op" is what makes a staged changeset
// legible to the human who reviews it.

func decodeStageArgs(args json.RawMessage, into any) error {
	if len(args) == 0 {
		return errors.New("arguments are required")
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	// An unknown field is a typo or a hallucinated parameter; saying so is
	// better feedback to a model than silently ignoring what it asked for.
	dec.DisallowUnknownFields()
	return dec.Decode(into)
}

// parseTarget resolves a targetRef argument, rejecting a kind the op cannot
// legally target — a wrong-kind ref is a mistake the model can fix, and saying
// so is worth more than a validation finding on a draft it did not mean to
// create.
func parseTarget(raw string, want ...inventory.Kind) (inventory.Ref, error) {
	if raw == "" {
		return inventory.Ref{}, errors.New("targetRef is required")
	}
	ref, err := inventory.ParseRef(raw)
	if err != nil {
		return inventory.Ref{}, err
	}
	for _, k := range want {
		if ref.Kind == k {
			return ref, nil
		}
	}
	names := make([]string, 0, len(want))
	for _, k := range want {
		names = append(names, string(k))
	}
	return inventory.Ref{}, fmt.Errorf("targetRef %q has kind %q; this tool targets %s", raw, ref.Kind, strings.Join(names, "|"))
}

func buildBridgeCreateOp(args json.RawMessage) (change.Op, stageArgs, string, error) {
	//nolint:govet // fieldalignment: this is a wire-shape decode target; the
	// field order mirrors the tool's documented JSON schema (docs/api.md), which
	// is what a reader checks it against, not the struct's packing.
	var req struct {
		stageArgs
		Addresses []string `json:"addresses"`
		Ports     []string `json:"ports"`
		Gateway   string   `json:"gateway"`
		Comments  string   `json:"comments"`
		MTU       int      `json:"mtu"`
		VlanAware bool     `json:"vlanAware"`
		STP       bool     `json:"stp"`
	}
	if err := decodeStageArgs(args, &req); err != nil {
		return change.Op{}, stageArgs{}, "", err
	}
	ref, err := parseTarget(req.TargetRef, inventory.KindBridge, inventory.KindOVSBridge)
	if err != nil {
		return change.Op{}, stageArgs{}, "", err
	}
	op := change.Op{
		Type:   change.OpBridgeCreate,
		Target: ref,
		Params: &change.BridgeCreateParams{
			Gateway:   req.Gateway,
			Comments:  req.Comments,
			Ports:     req.Ports,
			Addresses: req.Addresses,
			MTU:       req.MTU,
			VlanAware: req.VlanAware,
			STP:       req.STP,
		},
	}
	return op, req.stageArgs, fmt.Sprintf("create bridge %s on %s", ref.ID, ref.Node), nil
}

func buildIfaceUpdateOp(args json.RawMessage) (change.Op, stageArgs, string, error) {
	//nolint:govet // fieldalignment: this is a wire-shape decode target; the
	// field order mirrors the tool's documented JSON schema (docs/api.md), which
	// is what a reader checks it against, not the struct's packing.
	var req struct {
		stageArgs
		MTU           *int      `json:"mtu"`
		Addresses     *[]string `json:"addresses"`
		Gateway       *string   `json:"gateway"`
		Autostart     *bool     `json:"autostart"`
		Comments      *string   `json:"comments"`
		RemoveAddress bool      `json:"removeAddress"`
		RemoveGateway bool      `json:"removeGateway"`
	}
	if err := decodeStageArgs(args, &req); err != nil {
		return change.Op{}, stageArgs{}, "", err
	}
	ref, err := parseTarget(req.TargetRef,
		inventory.KindPhysNic, inventory.KindBond, inventory.KindBridge,
		inventory.KindVlan, inventory.KindOVSBridge, inventory.KindOVSBond)
	if err != nil {
		return change.Op{}, stageArgs{}, "", err
	}
	params := &change.IfaceUpdateParams{
		MTU: req.MTU, Comments: req.Comments, Addresses: req.Addresses,
		Gateway: req.Gateway, Autostart: req.Autostart,
		RemoveAddress: req.RemoveAddress, RemoveGateway: req.RemoveGateway,
	}
	if params.MTU == nil && params.Comments == nil && params.Addresses == nil &&
		params.Gateway == nil && params.Autostart == nil && !params.RemoveAddress && !params.RemoveGateway {
		return change.Op{}, stageArgs{}, "", errors.New("nothing to change: set at least one of mtu/addresses/gateway/autostart/comments/removeAddress/removeGateway")
	}
	op := change.Op{Type: change.OpIfaceUpdate, Target: ref, Params: params}
	return op, req.stageArgs, fmt.Sprintf("edit interface %s on %s", ref.ID, ref.Node), nil
}

func buildFwRuleCreateOp(args json.RawMessage) (change.Op, stageArgs, string, error) {
	//nolint:govet // fieldalignment: this is a wire-shape decode target; the
	// field order mirrors the tool's documented JSON schema (docs/api.md), which
	// is what a reader checks it against, not the struct's packing.
	var req struct {
		stageArgs
		Direction string `json:"direction"`
		Action    string `json:"action"`
		Proto     string `json:"proto"`
		Source    string `json:"source"`
		Dest      string `json:"dest"`
		Sport     string `json:"sport"`
		Dport     string `json:"dport"`
		Iface     string `json:"iface"`
		Macro     string `json:"macro"`
		Log       string `json:"log"`
		Comment   string `json:"comment"`
		Pos       int    `json:"pos"`
		Enabled   *bool  `json:"enabled"`
	}
	if err := decodeStageArgs(args, &req); err != nil {
		return change.Op{}, stageArgs{}, "", err
	}
	ref, err := parseTarget(req.TargetRef, inventory.KindFwRuleset)
	if err != nil {
		return change.Op{}, stageArgs{}, "", err
	}
	if req.Direction == "" || req.Action == "" {
		return change.Op{}, stageArgs{}, "", errors.New("direction and action are required")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	op := change.Op{
		Type:   change.OpFwRuleCreate,
		Target: ref,
		Params: &change.FwRuleCreateParams{
			Direction: req.Direction, Action: req.Action, Proto: req.Proto,
			Source: req.Source, Dest: req.Dest, Sport: req.Sport, Dport: req.Dport,
			Iface: req.Iface, Macro: req.Macro, Log: req.Log, Comment: req.Comment,
			Pos: req.Pos, Enabled: enabled,
		},
	}
	return op, req.stageArgs, fmt.Sprintf("add %s %s firewall rule to %s", req.Direction, req.Action, ref.ID), nil
}

func buildIpamAllocOp(args json.RawMessage) (change.Op, stageArgs, string, error) {
	//nolint:govet // fieldalignment: this is a wire-shape decode target; the
	// field order mirrors the tool's documented JSON schema (docs/api.md), which
	// is what a reader checks it against, not the struct's packing.
	var req struct {
		stageArgs
		CIDR     string `json:"cidr"`
		Hostname string `json:"hostname"`
		MAC      string `json:"mac"`
		Comment  string `json:"comment"`
	}
	if err := decodeStageArgs(args, &req); err != nil {
		return change.Op{}, stageArgs{}, "", err
	}
	ref, err := parseTarget(req.TargetRef, inventory.KindSDNSubnet)
	if err != nil {
		return change.Op{}, stageArgs{}, "", err
	}
	if req.CIDR == "" {
		return change.Op{}, stageArgs{}, "", errors.New("cidr is required")
	}
	op := change.Op{
		Type:   change.OpIpamAllocCreate,
		Target: ref,
		Params: &change.IpamAllocCreateParams{
			CIDR: req.CIDR, Hostname: req.Hostname, MAC: req.MAC, Comment: req.Comment,
		},
	}
	return op, req.stageArgs, fmt.Sprintf("reserve %s in subnet %s", req.CIDR, ref.ID), nil
}

// --- rate limiter ------------------------------------------------------------

// stageLimiter is a per-session sliding-window counter over staging calls. It
// is keyed on the session's TOKEN id rather than on the *Session pointer, so
// reconnecting does not hand a runaway client a fresh budget — a session is its
// credential, not its socket.
type stageLimiter struct {
	hits map[string][]time.Time
	mu   sync.Mutex
}

func newStageLimiter() *stageLimiter {
	return &stageLimiter{hits: map[string][]time.Time{}}
}

// allow records an attempt at now and reports whether it fits within limit
// calls per window. A refused attempt is NOT recorded, so a client that backs
// off recovers exactly one window after its last accepted call rather than
// being held out indefinitely by its own retries.
func (l *stageLimiter) allow(key string, now time.Time, limit int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}
