package change

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/host"
)

// rawFileHash is the sha256 hex digest GET /nodes/{node}/interfaces/raw
// returns alongside a node's file content, and that a saved
// iface.raw.replace op's BaseHash is compared against — the raw editor's
// conflict guard (task card: "file hash captured at open, mismatch on save
// -> reload prompt").
func rawFileHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ReadRawInterfaces returns node's current, live /etc/network/interfaces
// content plus its rawFileHash — the raw Monaco editor's "open" call
// (GET /nodes/{node}/interfaces/raw) and the baseline the client stamps
// into every subsequent lint/save round as BaseHash.
func (s *Service) ReadRawInterfaces(ctx context.Context, node string) (content, hash string, err error) {
	if s.nodes == nil {
		return "", "", fmt.Errorf("change: reading raw interfaces for node %s: no node agent configured", node)
	}
	content, err = s.nodes.ReadInterfaces(ctx, node)
	if err != nil {
		return "", "", fmt.Errorf("change: reading /etc/network/interfaces on node %s: %w", node, err)
	}
	return content, rawFileHash(content), nil
}

// validate is the single entry point Create/UpdateDraft/Validate call to
// run the T-202/T-203 pipeline: it first expands any iface.raw.replace op
// in ops into its equivalent per-entity delta ops (against that node's
// live file), feeds the expanded set through ValidateWithSafety, and
// prepends any raw-op-specific findings (parse error, hash conflict, read
// failure) expandRawReplaceOps produced along the way. Non-raw changesets
// pass through expandRawReplaceOps unchanged, so this is a strict
// superset of the pre-T-208 behavior for every other op type.
// T-2601: the cluster's declarative policy set is part of the pipeline's
// inputs (validationInputs), so a policy `deny` blocks here — at validate,
// before any diff or plan is ever computed.
func (s *Service) validate(ctx context.Context, clusterID string, ops []Op) []Finding {
	expanded, rawFindings := s.expandRawReplaceOps(ctx, ops)
	var report PolicyResult
	safety, policyFindings := s.validationInputs(ctx, clusterID, &report)
	findings := ValidateWithSafety(expanded, s.inventorySnapshot(), safety)
	s.recordPolicyStats(ctx, clusterID, report)

	out := make([]Finding, 0, len(rawFindings)+len(policyFindings)+len(findings))
	out = append(out, rawFindings...)
	out = append(out, policyFindings...)
	return append(out, findings...)
}

// validateScoped is validate plus T-1201's cross-cluster scoping class: it
// runs the ordinary pipeline, then appends any codeCrossClusterRef findings
// for ops whose target belongs to a different attached cluster than
// clusterID. The scoping class is deliberately additive and last — it never
// short-circuits or reorders the existing classes, so a single-cluster
// deployment (clusterID == "" and no ClusterMembership seam) sees byte-for-
// byte the same findings validate already produced.
func (s *Service) validateScoped(ctx context.Context, clusterID string, ops []Op) []Finding {
	findings := s.validate(ctx, clusterID, ops)
	// Skip the membership fetch entirely for an unscoped (implicit-default-
	// cluster) changeset — the common single-cluster case — since
	// ValidateClusterScope is a guaranteed no-op there anyway.
	if clusterID == "" {
		return findings
	}
	if scope := ValidateClusterScope(clusterID, ops, s.nodeClusters(ctx)); len(scope) > 0 {
		findings = append(findings, scope...)
	}
	return findings
}

// nodeClusters resolves the node->cluster membership map the cross-cluster
// scoping check needs, or nil when no ClusterMembership seam is wired (the
// non-federated case) or the live read fails — a soft-fail read exactly like
// dhcpAllocations, never blocking validation on a transient hiccup.
func (s *Service) nodeClusters(ctx context.Context) map[string]string {
	if s.membership == nil {
		return nil
	}
	m, err := s.membership.NodeClusters(ctx)
	if err != nil {
		s.log.Debug("change: reading cluster membership for cross-cluster scoping failed, skipping", "error", err)
		return nil
	}
	return m
}

// expandRawReplaceOps walks ops, passing every non-raw op through
// unchanged, and for each iface.raw.replace op: reading that node's live
// file, checking BaseHash against it (a mismatch is the hash-conflict
// guard), and — if that passes — expanding it via expandRawReplace
// (validate_raw.go) into the synthesized ops appended after it. The
// exclusive-op-per-node check (rawReplaceExclusiveFindings) runs first,
// against ops as received (before any expansion adds more entries for the
// same node) — a node that fails it has its raw op left un-expanded, since
// its net effect on that node is ambiguous (see that function's doc
// comment). The raw op itself is also kept in the returned slice
// (schemaValidate's *IfaceRawReplaceParams case still needs to see it);
// referentialValidate/safetyValidate/advisoryValidate simply have no
// switch case for it and contribute nothing extra for that literal entry,
// so keeping it alongside its expansion is harmless.
//
// A read failure or hash conflict short-circuits that op's expansion (no
// synthesized ops for it, but the offending changeset still carries a
// blocking Finding) rather than aborting the whole call — other ops in the
// same changeset (targeting other nodes) are still validated normally.
func (s *Service) expandRawReplaceOps(ctx context.Context, ops []Op) ([]Op, []Finding) {
	findings := rawReplaceExclusiveFindings(ops)
	notExclusive := map[string]bool{}
	for _, f := range findings {
		notExclusive[f.Ref] = true
	}

	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		out = append(out, op)
		p, ok := op.Params.(*IfaceRawReplaceParams)
		if !ok {
			continue
		}
		if notExclusive[refOf(op)] {
			// Already flagged above; expanding it too would mean
			// validating against a changeset whose net effect on this
			// node is genuinely ambiguous (see rawReplaceExclusiveFindings'
			// doc comment) — skip rather than compound the error.
			continue
		}

		node := op.Target.Node
		if s.nodes == nil {
			findings = append(findings, errorf(codeRawReplaceReadFailed, refOf(op),
				"no node agent configured; cannot validate this raw file edit on node %s", node))
			continue
		}
		before, err := s.nodes.ReadInterfaces(ctx, node)
		if err != nil {
			findings = append(findings, errorf(codeRawReplaceReadFailed, refOf(op),
				"reading current /etc/network/interfaces on node %s: %v", node, err))
			continue
		}
		if p.BaseHash != "" && rawFileHash(before) != p.BaseHash {
			findings = append(findings, errorf(codeRawReplaceHashConflict, refOf(op),
				"the /etc/network/interfaces file on node %s changed after this edit was opened — reload the file and reapply your changes before saving", node))
			continue
		}

		deltaOps, deltaFindings := expandRawReplace(op.Target, before, p.Content)
		findings = append(findings, deltaFindings...)
		out = append(out, deltaOps...)
	}
	return out, findings
}

// LintMarker is one interfaces(5) syntax diagnostic, line-precise per the
// task card's AC1 ("syntax errors underline with line-precise messages as
// you type"). Line is 1-based, matching host.ParseError and every text
// editor's convention (Monaco markers are 1-based too).
type LintMarker struct {
	Message string `json:"message"`
	Line    int    `json:"line"`
}

// LintRawInterfaces runs the T-102 parser against content and returns any
// syntax error as a line-precise LintMarker (an empty slice when content
// parses cleanly). It is a pure function of content — no node, no I/O — so
// it backs POST /interfaces/lint directly with no Service dependency;
// exported here (rather than as a package-level function elsewhere) purely
// for discoverability alongside this file's other raw-editor surface.
//
// interfaces(5)'s single-pass parser stops at the first error (T-102's
// design; see internal/host/interfaces_parse.go), so this returns at most
// one marker today — good enough for the editor's "you have a syntax
// error, here's where" loop; a future multi-error recovery parser could
// widen this to a slice of independent markers without changing the
// return type.
func LintRawInterfaces(content string) []LintMarker {
	if _, err := host.ParseInterfaces([]byte(content)); err != nil {
		return []LintMarker{lintMarkerFor(err)}
	}
	return []LintMarker{}
}

// lintMarkerFor extracts a line-precise LintMarker from a host.ParseError,
// falling back to line 1 for any other error shape (defensive only —
// host.ParseInterfaces only ever returns *host.ParseError today).
func lintMarkerFor(err error) LintMarker {
	if perr, ok := asHostParseError(err); ok {
		return LintMarker{Line: perr.Line, Message: perr.Msg}
	}
	return LintMarker{Line: 1, Message: err.Error()}
}

// asHostParseError unwraps err into a *host.ParseError, if it is (or
// wraps) one. Shared by LintRawInterfaces above and
// validate_raw.go's rawParseErrorFinding.
func asHostParseError(err error) (*host.ParseError, bool) {
	var perr *host.ParseError
	ok := errors.As(err, &perr)
	return perr, ok
}
