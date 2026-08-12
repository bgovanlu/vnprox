// Command e2egate decides whether a sharded Playwright run passed.
//
// T-2505. The e2e suite runs as N independent shards, each its own Playwright
// process with its own daemons and its own exit code, so no single process sees
// the whole run. This one reads every shard's JSON report and applies the three
// rules the exit codes cannot express: unquarantined failures fail the build,
// an expired quarantine fails the build whether or not its test did, and each
// run is appended to a history log the flake trend is computed from.
//
// It lives in cmd/ alongside cmd/pvemock and cmd/k8smock — development and test
// tooling, not shipped in the .deb.
//
//	e2egate gate  --reports web/test-results/shards --quarantine web/e2e/quarantine.json
//	e2egate trend --history var/e2e-runs --last 20
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/e2egate"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "e2egate: %v\n", err)
		os.Exit(2)
	}
}

// Exit codes, because CI has to tell two different things apart: a red suite is
// a result (1), a broken gate is an outage (2). `gate` exits 1 directly when the
// verdict fails; every error returned from here is the second kind.
func run(args []string, out *os.File) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: e2egate <gate|trend> [flags]")
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
	reports := fs.String("reports", "web/test-results/shards", "directory of per-shard Playwright JSON reports")
	quarantinePath := fs.String("quarantine", "web/e2e/quarantine.json", "quarantine file")
	historyDir := fs.String("history", "var/e2e-runs", "run-history directory (append-only)")
	shards := fs.String("shards", "", "comma-separated shard names that must have reported")
	complete := fs.Bool("complete", true, "the reports cover the whole suite (enables the stale-quarantine check and records the run for the trend)")
	window := fs.Int("window", e2egate.TrendWindow, "how many recent complete runs the flake trend covers")
	record := fs.Bool("record", true, "append this run to the history log")
	runID := fs.String("run-id", "", "identifier for this run (default: UTC timestamp)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parsing gate flags: %w", err)
	}

	now := time.Now()
	parsed, err := e2egate.ParseReportDir(*reports)
	if err != nil {
		return err
	}
	q, err := e2egate.LoadQuarantine(*quarantinePath)
	if err != nil {
		return err
	}

	verdict := e2egate.Evaluate(e2egate.EvaluateInput{
		Reports:        parsed,
		Quarantine:     q,
		Now:            now,
		ExpectedShards: splitNonEmpty(*shards),
		Complete:       *complete,
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
		rec := e2egate.NewRunRecord(id, gitCommit(), host, now, *complete, parsed)
		if appendErr := e2egate.AppendRun(*historyDir, rec); appendErr != nil {
			return appendErr
		}
	}

	runs, err := e2egate.LoadRuns(*historyDir, *window)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprint(out, verdict.Summary())
	_, _ = fmt.Fprint(out, e2egate.TrendReport(e2egate.Trend(runs, q), *window))
	if !verdict.OK() {
		os.Exit(1)
	}
	return nil
}

func trend(args []string, out *os.File) error {
	fs := flag.NewFlagSet("trend", flag.ContinueOnError)
	historyDir := fs.String("history", "var/e2e-runs", "run-history directory")
	quarantinePath := fs.String("quarantine", "web/e2e/quarantine.json", "quarantine file")
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
	_, _ = fmt.Fprint(out, e2egate.TrendReport(e2egate.Trend(runs, q), *window))
	return nil
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// gitCommit records which tree a run's numbers belong to. Best effort: a run
// from a tarball with no .git is still a run worth recording.
func gitCommit() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
