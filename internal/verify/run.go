// SPDX-License-Identifier: Apache-2.0

package verify

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// Options selects what a run does.
type Options struct {
	// Logger receives per-check progress. Nil means slog.Default().
	Logger *slog.Logger
	// Version is the vnproxctl build's version, recorded in the report.
	Version string
	// Suite is which suite to run. Ignored when Only is non-empty, so an
	// operator chasing one failure does not have to remember which suite it
	// lives in.
	Suite Suite
	// Only, when non-empty, restricts the run to these check IDs. An
	// unrecognised ID is an error (AC6), never a silently empty run.
	Only []string
}

// Run executes the selected checks and assembles a report.
//
// It returns an error only for conditions that mean the *run* is invalid — a
// malformed registry, an unknown --only ID. A check that fails is a result,
// not an error: the whole point is to produce an artifact describing what was
// observed, including when what was observed is bad.
func Run(ctx context.Context, opts Options, deps Deps) (Report, error) {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
		deps.Now = now
	}
	if deps.Wait == nil {
		deps.Wait = realWait
	}

	registry := Checks()
	if err := ValidateRegistry(registry); err != nil {
		return Report{}, err
	}

	selected, err := selectChecks(registry, opts)
	if err != nil {
		return Report{}, err
	}

	env := collectEnvironment(ctx, opts, deps)

	results := make([]Result, 0, len(selected))
	for _, c := range selected {
		started := now()
		outcome := runOne(ctx, c, deps, log)
		res := Result{
			ID:           c.ID,
			MatrixRow:    c.MatrixRow,
			Area:         c.Area,
			Suite:        c.Suite,
			Precondition: c.Precondition,
			Status:       outcome.Status,
			Detail:       outcome.Detail,
			Evidence:     outcome.Evidence,
			DurationMS:   now().Sub(started).Milliseconds(),
		}
		if outcome.Status == StatusSkip {
			res.SkipReason = outcome.Reason
			if strings.TrimSpace(res.SkipReason) == "" {
				res.SkipReason = outcome.Detail
			}
		}
		log.Info("verify: check complete", "id", c.ID, "status", res.Status, "durationMs", res.DurationMS)
		results = append(results, res)
	}

	report := Report{
		ReportVersion: CurrentReportVersion,
		GeneratedAt:   now().UTC(),
		Environment:   env,
		Results:       results,
		Summary:       Summarize(results),
	}
	if len(opts.Only) > 0 {
		report.Selection = CheckIDs(selected)
	} else {
		report.Suite = opts.Suite
	}
	if err := report.Validate(); err != nil {
		// A malformed report is a bug in a check, not an operator problem.
		// Refusing to hand it back is what keeps Validate's guarantees real
		// rather than advisory.
		return Report{}, fmt.Errorf("verify: assembled report is malformed (this is a bug in a check, not in your cluster): %w", err)
	}
	return report, nil
}

// runOne applies the two gates every check shares — node count and
// destructive consent — before calling it.
//
// Both gates produce a skip naming what is missing, and both live here rather
// than in each check so that a new check cannot forget one. A check that
// needs more consent than the gates give it is still free to skip on its own.
func runOne(ctx context.Context, c Check, deps Deps, log *slog.Logger) Outcome {
	if c.Suite == SuiteDestructive && !deps.Consent.Destructive {
		return skipNoConsent(c.ID + " (" + c.Precondition + ")")
	}
	online := onlineNodes(deps.Nodes)
	if len(online) < c.MinNodes {
		names := nodeNames(online)
		detail := "none"
		if len(names) > 0 {
			detail = strings.Join(names, ", ")
		}
		return Skip(fmt.Sprintf("needs %d online node(s); this cluster has %d (%s). %s",
			c.MinNodes, len(online), detail, c.Precondition))
	}
	log.Debug("verify: running check", "id", c.ID, "suite", c.Suite)
	return c.Run(ctx, deps)
}

// selectChecks resolves Options into the ordered set of checks to run.
func selectChecks(registry []Check, opts Options) ([]Check, error) {
	if len(opts.Only) > 0 {
		byID := make(map[string]Check, len(registry))
		for _, c := range registry {
			byID[c.ID] = c
		}
		var unknown []string
		selected := make([]Check, 0, len(opts.Only))
		for _, id := range opts.Only {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			c, ok := byID[id]
			if !ok {
				unknown = append(unknown, id)
				continue
			}
			selected = append(selected, c)
		}
		if len(unknown) > 0 {
			// AC6: an unknown id is an error naming it, not a run that
			// quietly selects nothing and exits 0 — which is the failure that
			// would let a typo in a CI pipeline look like a passing gate
			// forever.
			return nil, &UnknownCheckError{Unknown: unknown, Known: CheckIDs(registry)}
		}
		if len(selected) == 0 {
			return nil, &UnknownCheckError{Unknown: []string{"(empty --only)"}, Known: CheckIDs(registry)}
		}
		return selected, nil
	}

	if !ValidSuite(opts.Suite) {
		return nil, fmt.Errorf("unknown suite %q: want one of %s", opts.Suite, strings.Join(suiteNames(), ", "))
	}
	selected := make([]Check, 0, len(registry))
	for _, c := range registry {
		if c.Suite == opts.Suite {
			selected = append(selected, c)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("suite %q has no checks", opts.Suite)
	}
	return selected, nil
}

func suiteNames() []string {
	out := make([]string, 0, len(AllSuites))
	for _, s := range AllSuites {
		out = append(out, string(s))
	}
	return out
}

// collectEnvironment gathers what attributes the report to real hardware.
//
// Every field falls back to the literal string "unknown" rather than to an
// empty string. That is not politeness: Report.Validate rejects an empty
// value, so the fallback is what keeps a partial environment from producing a
// malformed report — and "unknown" in an artifact is a fact a reader can act
// on, where a blank is one they will fill in optimistically.
func collectEnvironment(ctx context.Context, opts Options, deps Deps) Environment {
	env := Environment{
		VnproxVersion: fallbackUnknown(opts.Version),
		PVEVersion:    "unknown",
		Kernel:        "unknown",
		PVEEndpoint:   fallbackUnknown(deps.Endpoint.URL),
		Mock:          deps.Endpoint.Mock,
		MockReason:    deps.Endpoint.MockReason,
		Nodes:         nodeNames(deps.Nodes),
	}
	if deps.Cluster != nil {
		if v, err := deps.Cluster.PVEVersion(ctx); err == nil {
			env.PVEVersion = fallbackUnknown(v)
		}
	}
	node := localNode(deps.Nodes)
	if deps.Host != nil {
		if out, err := deps.Host.Run(ctx, node, "uname", "-r"); err == nil {
			env.Kernel = fallbackUnknown(firstLine(out))
		}
		if models := collectNICModels(ctx, deps, node); len(models) > 0 {
			env.NICModels = models
		}
	}
	return env
}

// collectNICModels reads each physical NIC's driver-reported model, so a
// result that turns out to be driver-specific — LACP partner parsing, SR-IOV
// VF behaviour — can be read against the hardware that produced it.
func collectNICModels(ctx context.Context, deps Deps, node string) []string {
	out, err := deps.Host.Run(ctx, node, "sh", "-c",
		"for d in /sys/class/net/*/device/; do n=${d%/device/}; n=${n##*/}; m=$(cat $d/modalias 2>/dev/null); v=$(cat $d/vendor 2>/dev/null); p=$(cat $d/device 2>/dev/null); echo \"$n $v:$p $m\"; done")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var models []string
	for _, line := range nonEmptyLines(out) {
		if seen[line] {
			continue
		}
		seen[line] = true
		models = append(models, line)
	}
	sort.Strings(models)
	return models
}

// realWait is Deps.Wait in production: a sleep that a cancelled run cuts
// short, so ^C during the destructive suite's two-minute failover watch does
// not take two minutes to be noticed.
func realWait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func fallbackUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}
