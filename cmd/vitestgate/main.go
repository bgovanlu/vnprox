// SPDX-License-Identifier: Apache-2.0

// Command vitestgate decides whether a vitest run passed, the way
// cmd/e2egate (T-2505) already decides for the Playwright suite.
//
// T-3708. The e2e suite has real flake infrastructure — a quarantine with
// hard expiries and a 20-run trend, computed from an append-only run-history
// log. The vitest suite (web/, 2,278 tests as of this writing), which gates
// every push via the pre-push `make ci` hook, had none of it: when
// web/src/governance/TenantsPanel.test.tsx timed out on a `findByRole` under
// `make ci`'s concurrent load and refused a push, nothing recorded that the
// test was load-sensitive rather than broken (fixed in 2cd48367 — see
// web/src/test/setup.ts). The next occurrence would have been diagnosed from
// scratch.
//
// vitest is not sharded the way the e2e suite is: one process, one JSON
// report. So there is no shard reconciliation to do here — this command
// wraps that single report in a one-element internal/e2egate.ShardReport
// slice and hands it to exactly the same Evaluate/Trend/quarantine
// machinery cmd/e2egate uses, imported from internal/e2egate rather than
// reimplemented, because none of it is Playwright-specific. The only new
// code (internal/vitestgate) is the vitest report parser.
//
//	vitestgate gate  --report web/test-results/vitest.json --quarantine cmd/vitestgate/quarantine.json
//	vitestgate trend --history var/vitest-runs --last 20
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/e2egate"
	"github.com/bgovanlu/vnprox/internal/vitestgate"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "vitestgate: %v\n", err)
		os.Exit(2)
	}
}

// Exit codes mirror cmd/e2egate exactly, for the same reason: CI has to
// tell a red suite (1) apart from a broken gate (2).
func run(args []string, out *os.File) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vitestgate <gate|trend> [flags]")
	}
	switch args[0] {
	case "gate":
		return gate(args[1:], out)
	case "trend":
		return trend(args[1:], out)
	default:
		return fmt.Errorf("unknown subcommand %q: expected gate or trend", args[0])
	}
}

func gate(args []string, out *os.File) error {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	report := fs.String("report", "web/test-results/vitest.json", "vitest JSON reporter output (single file — vitest is not sharded)")
	root := fs.String("root", "web", "directory the report's absolute file paths are made relative to")
	quarantinePath := fs.String("quarantine", "cmd/vitestgate/quarantine.json", "quarantine file")
	historyDir := fs.String("history", "var/vitest-runs", "run-history directory (append-only)")
	complete := fs.Bool("complete", true, "the report covers the whole suite (enables the stale-quarantine check and records the run for the trend)")
	window := fs.Int("window", e2egate.TrendWindow, "how many recent complete runs the flake trend covers")
	record := fs.Bool("record", true, "append this run to the history log")
	runID := fs.String("run-id", "", "identifier for this run (default: UTC timestamp)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parsing gate flags: %w", err)
	}

	now := time.Now()

	f, err := os.Open(*report) //nolint:gosec // path is operator-supplied tooling input, same as cmd/e2egate.
	if err != nil {
		return fmt.Errorf("opening vitest report %s: %w", *report, err)
	}
	rep, parseErr := vitestgate.ParseReport(f, *root)
	closeErr := f.Close()
	if parseErr != nil {
		return parseErr
	}
	if closeErr != nil {
		return fmt.Errorf("closing vitest report %s: %w", *report, closeErr)
	}

	q, err := e2egate.LoadQuarantine(*quarantinePath)
	if err != nil {
		return err
	}

	reports := []e2egate.ShardReport{rep}
	verdict := e2egate.Evaluate(e2egate.EvaluateInput{
		Reports:    reports,
		Quarantine: q,
		Now:        now,
		Complete:   *complete,
	})

	id := *runID
	if id == "" {
		id = now.UTC().Format("20060102-150405")
	}
	if *record {
		host, hostErr := os.Hostname()
		if hostErr != nil {
			host = "unknown"
		}
		rec := e2egate.NewRunRecord(id, gitCommit(), host, now, *complete, reports)
		if appendErr := e2egate.AppendRun(*historyDir, rec); appendErr != nil {
			return appendErr
		}
	}

	runs, err := e2egate.LoadRuns(*historyDir, *window)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprint(out, vitestgate.Summary(verdict))
	_, _ = fmt.Fprint(out, vitestgate.TrendReport(e2egate.Trend(runs, q), *window))
	if !verdict.OK() {
		os.Exit(1)
	}
	return nil
}

func trend(args []string, out *os.File) error {
	fs := flag.NewFlagSet("trend", flag.ContinueOnError)
	historyDir := fs.String("history", "var/vitest-runs", "run-history directory")
	quarantinePath := fs.String("quarantine", "cmd/vitestgate/quarantine.json", "quarantine file")
	window := fs.Int("last", e2egate.TrendWindow, "how many recent complete runs to cover")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parsing trend flags: %w", err)
	}
	runs, err := e2egate.LoadRuns(*historyDir, *window)
	if err != nil {
		return err
	}
	q, err := e2egate.LoadQuarantine(*quarantinePath)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(out, vitestgate.TrendReport(e2egate.Trend(runs, q), *window))
	return nil
}

// gitCommit records which tree a run's numbers belong to. Duplicated from
// cmd/e2egate rather than shared: it is eight lines of os/exec with no
// Playwright- or vitest-specific meaning, and the two commands are not
// expected to change it in lockstep.
func gitCommit() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmdOut, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(cmdOut))
}
