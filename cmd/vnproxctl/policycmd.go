// SPDX-License-Identifier: Apache-2.0

// policycmd.go implements `vnproxctl policy` (T-2601): developing a
// declarative policy rule set against a real changeset without staging
// anything.
//
//	vnproxctl policy lint     --policy=f.yaml            (local, daemon-free)
//	vnproxctl policy examples                            (local, daemon-free)
//	vnproxctl policy test     --policy=f.yaml --changeset=ID
//
// `lint` and `examples` are pure local operations — they parse and validate
// a document, which needs no daemon at all. `test` evaluates against a REAL
// changeset and therefore talks to the daemon over `POST /policies/test`
// (bearer token, like `vnproxctl apply` and the `remote` family): the
// changeset lives in the daemon's store, and the rules' inventory facts are
// resolved against the live snapshot only the daemon has. Evaluating
// locally would answer a different question than the one enforcement will
// ask, which is the opposite of what a "test" command is for.
//
// Nothing in this file stages, edits, or applies anything. `test` is a
// read: the daemon evaluates and answers, and the changeset is untouched.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"

	"github.com/bgovanlu/vnprox/internal/change"
)

// policyTestBody mirrors internal/api's policyTestRequest.
type policyTestBody struct {
	Policy      *policyDocumentWire `json:"policy,omitempty"`
	ChangesetID string              `json:"changesetId,omitempty"`
}

// policyDocumentWire mirrors internal/api's policyPutRequest.
type policyDocumentWire struct {
	Rules   []change.PolicyRule `json:"rules"`
	Version int                 `json:"version,omitempty"`
}

// policyResultWire mirrors change.PolicyResult.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing.
type policyResultWire struct {
	Findings []policyFindingWire    `json:"findings"`
	Rules    []policyRuleResultWire `json:"rules"`
}

type policyFindingWire struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Ref      string `json:"ref,omitempty"`
}

//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing.
type policyRuleResultWire struct {
	MatchedOps   []int    `json:"matchedOps"`
	ViolatingOps []int    `json:"violatingOps"`
	Tags         []string `json:"tags"`
	RuleID       string   `json:"ruleId"`
	Description  string   `json:"description"`
	Severity     string   `json:"severity"`
}

func runPolicy(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl policy: expected a subcommand (test, lint, examples)")
		return ExitUsage
	}
	switch args[0] {
	case "test":
		return runPolicyTest(args[1:], stdout, stderr)
	case "lint":
		return runPolicyLint(args[1:], stdout, stderr)
	case "examples":
		return runPolicyExamples(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl policy: unknown subcommand %q\n", args[0])
		return ExitUsage
	}
}

// runPolicyLint parses and validates a policy file locally. It is the
// fastest possible loop for the "did I write this rule correctly" question,
// and it fails with exactly the message the daemon would refuse to start
// with — file, rule id, and field.
func runPolicyLint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl policy lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	policyPath := fs.String("policy", "", "path to the policy YAML document to validate")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *policyPath == "" {
		_, _ = fmt.Fprintln(stderr, "vnproxctl policy lint: --policy is required")
		return ExitUsage
	}
	set, err := change.LoadPolicyFile(*policyPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl policy lint: %v\n", err)
		return ExitUsage
	}
	_, _ = fmt.Fprintf(stdout, "ok: %s — %d rule(s)\n", *policyPath, len(set.Rules))
	for _, r := range set.Rules {
		_, _ = fmt.Fprintf(stdout, "  %-40s %-5s %s\n", r.ID, r.Severity, r.Description)
	}
	return ExitSuccess
}

// runPolicyExamples prints the shipped example rule set — the document an
// operator copies from. These rules are examples, not defaults: vnprox
// enforces nothing until a policy is installed.
func runPolicyExamples(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl policy examples", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if _, err := stdout.Write(change.ExamplePolicyYAML()); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl policy examples: %v\n", err)
		return ExitError
	}
	return ExitSuccess
}

// runPolicyTest evaluates a policy document against a real changeset,
// server-side, without staging anything. A `deny` violation exits
// ExitPending — the same "cannot proceed automatically, a human must look"
// code a 422 validation_failed already earns — so a CI job can gate on it.
func runPolicyTest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl policy test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	policyPath := fs.String("policy", "", "path to a policy YAML document to evaluate (default: the cluster's installed rule set)")
	changesetID := fs.String("changeset", "", "id of the changeset to evaluate against")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *changesetID == "" {
		_, _ = fmt.Fprintln(stderr, "vnproxctl policy test: --changeset is required")
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl policy test", stderr)
	if !ok {
		return code
	}

	body := policyTestBody{ChangesetID: *changesetID}
	if *policyPath != "" {
		set, err := change.LoadPolicyFile(*policyPath)
		if err != nil {
			// The same message the daemon would refuse to start with —
			// naming the file, the rule, and the field.
			_, _ = fmt.Fprintf(stderr, "vnproxctl policy test: %v\n", err)
			return ExitUsage
		}
		body.Policy = &policyDocumentWire{Version: set.Version, Rules: set.Rules}
	}

	client, code := buildRemoteClient(rf, "vnproxctl policy test", stderr)
	if client == nil {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()

	var result policyResultWire
	httpStatus, apiErr, err := client.doJSON(ctx, "POST", "/policies/test", body, &result)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl policy test: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl policy test: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if jsonOut {
		if werr := writeJSONOut(stdout, result); werr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl policy test: %v\n", werr)
			return ExitError
		}
	} else {
		printPolicyResult(stdout, *changesetID, result)
	}
	return policyTestExitCode(result)
}

// policyTestExitCode maps an evaluation onto an exit code: a `deny`
// violation is ExitPending (a human must look), everything else — including
// `warn` violations, which by definition do not block — is success.
func policyTestExitCode(result policyResultWire) int {
	for _, f := range result.Findings {
		if f.Severity == "error" {
			return ExitPending
		}
	}
	return ExitSuccess
}

func printPolicyResult(stdout io.Writer, changesetID string, result policyResultWire) {
	_, _ = fmt.Fprintf(stdout, "changeset %s\n", changesetID)
	if len(result.Rules) == 0 {
		_, _ = fmt.Fprintln(stdout, "  no policy rules were evaluated (the cluster has no installed policy set)")
		return
	}
	rules := append([]policyRuleResultWire(nil), result.Rules...)
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].RuleID < rules[j].RuleID })
	for _, r := range rules {
		verdict := "pass"
		switch {
		case len(r.ViolatingOps) > 0:
			verdict = "VIOLATED"
		case len(r.MatchedOps) == 0:
			verdict = "no match"
		}
		_, _ = fmt.Fprintf(stdout, "  %-40s %-5s %-9s matched=%d violating=%d\n",
			r.RuleID, r.Severity, verdict, len(r.MatchedOps), len(r.ViolatingOps))
	}
	for _, f := range result.Findings {
		_, _ = fmt.Fprintf(stdout, "  [%s] %s %s\n", f.Severity, f.Ref, f.Message)
	}
}
