// SPDX-License-Identifier: Apache-2.0

// applycmd.go implements `vnproxctl apply spec.yaml --plan|--apply`
// (T-1105): the GitOps entry point over T-1101's `POST /spec/import` — a
// Terraform-`plan`-style dry run, or a full create→apply→poll→auto-confirm
// round trip, both over the ordinary changeset lifecycle (never a second
// mutation path).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// remoteApplyPollInterval is how often `--apply` re-fetches the changeset
// while waiting for it to reach `committed`. Kept as its own named constant
// (rather than inlined) so applycmd_test.go can document why the injected
// test clock's `sleep` doesn't actually need to wait this long — it just
// advances the fake clock by this amount per iteration.
const remoteApplyPollInterval = 2 * time.Second

// applyClock is the injectable time source `--apply`'s poll loop uses, the
// same pattern change.Service's own scheduler tests use (T-1103's Clock
// interface) — a real clock in production, a fake one in tests, so a
// deterministic timeout test never needs a real sleep.
type applyClock struct {
	now   func() time.Time
	sleep func(d time.Duration)
}

func productionApplyClock() applyClock {
	return applyClock{now: time.Now, sleep: time.Sleep}
}

// specImportWire mirrors internal/api's specImportResponse: the created
// draft changeset plus notInSpec (docs/api.md's Declarative cluster network
// spec section).
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing (matches internal/api/spec.go's identical precedent).
type specImportWire struct {
	changesetWire
	NotInSpec []string `json:"notInSpec"`
}

// specImportRequestBody is POST /spec/import's body.
type specImportRequestBody struct {
	Content string `json:"content"`
}

func runApply(args []string, stdout, stderr io.Writer) int {
	return runApplyWithClock(args, stdout, stderr, productionApplyClock())
}

func runApplyWithClock(args []string, stdout, stderr io.Writer, clock applyClock) int {
	fs := flag.NewFlagSet("vnproxctl apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	plan := fs.Bool("plan", false, "dry run: POST /spec/import, print the diff, exit ExitPending if non-empty (Terraform-plan style)")
	doApply := fs.Bool("apply", false, "apply the imported spec's changeset and poll it to committed, auto-confirming non-interactively")
	confirmTimeoutSec := fs.Int("confirm-timeout-sec", 120, "commit-confirm window, seconds (--apply only)")
	// Named apply-timeout, not timeout: addRemoteFlags above already
	// registers --timeout as the per-request timeout every remote/apply
	// call uses; this is a distinct, longer-lived bound (the whole
	// create->apply->poll->confirm round trip), so it needs its own flag
	// name rather than colliding with (or overloading the meaning of) the
	// shared one.
	applyTimeout := fs.Duration("apply-timeout", 2*time.Minute, "max time to wait for the changeset to reach committed (--apply only); exceeding it is ExitApplyTimeout, never a hang")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl apply: expected exactly one spec file path")
		return ExitUsage
	}
	if *plan == *doApply {
		_, _ = fmt.Fprintln(stderr, "vnproxctl apply: exactly one of --plan or --apply is required")
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl apply", stderr)
	if !ok {
		return code
	}

	content, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl apply: reading %s: %v\n", fs.Arg(0), err)
		return ExitUsage
	}

	client, code := buildRemoteClient(rf, "vnproxctl apply", stderr)
	if client == nil {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	var imported specImportWire
	httpStatus, apiErr, err := client.doJSON(ctx, "POST", "/spec/import", specImportRequestBody{Content: string(content)}, &imported)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl apply: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl apply: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if *plan {
		return runApplyPlan(client, imported, jsonOut, stdout, stderr, *rf.timeout)
	}
	return runApplyApply(client, imported, jsonOut, stdout, stderr, *rf.timeout, *confirmTimeoutSec, *applyTimeout, clock)
}

// planResultWire is `--plan`'s `-o json` payload: the imported changeset,
// its rendered diff, and whether it counts as "changes pending".
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing.
type planResultWire struct {
	Diff      changesetDiffWire `json:"diff"`
	Changeset changesetWire     `json:"changeset"`
	NotInSpec []string          `json:"notInSpec"`
	Pending   bool              `json:"pending"`
}

func runApplyPlan(client *remoteClient, imported specImportWire, jsonOut bool, stdout, stderr io.Writer, reqTimeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), reqTimeout)
	defer cancel()
	var diff changesetDiffWire
	_, diffErr, err := client.doJSON(ctx, "GET", "/changesets/"+imported.ID+"/diff", nil, &diff)
	if err != nil || diffErr != nil {
		// Non-fatal: the diff render is a convenience over the ops the
		// changeset already carries. A caller that only cares about the
		// exit code (the "changes pending" signal) still gets a correct
		// answer even if this secondary fetch fails.
		_, _ = fmt.Fprintf(stderr, "vnproxctl apply --plan: warning: could not render diff for %s: %v\n", imported.ID, firstNonNil(err, diffErr))
	}

	// The plan is a preview, not meant to leave a persistent artifact: best-
	// effort discard the draft it necessarily created (POST /spec/import has
	// no plan-only mode — see docs/api.md's Spec section), regardless of the
	// outcome below. A failed discard is reported but never changes the exit
	// code — the plan answer itself is already final.
	discardCtx, discardCancel := context.WithTimeout(context.Background(), reqTimeout)
	defer discardCancel()
	if _, discardErr, err := client.doJSON(discardCtx, "DELETE", "/changesets/"+imported.ID, nil, nil); err != nil || discardErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl apply --plan: warning: could not discard preview changeset %s: %v\n", imported.ID, firstNonNil(err, discardErr))
	}

	pending := len(imported.Ops) > 0

	if jsonOut {
		result := planResultWire{Changeset: imported.changesetWire, NotInSpec: imported.NotInSpec, Diff: diff, Pending: pending}
		if err := writeJSONOut(stdout, result); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl apply --plan: %v\n", err)
			return ExitError
		}
	} else {
		printChangesetDiff(stdout, diff)
		if len(imported.NotInSpec) > 0 {
			_, _ = fmt.Fprintln(stdout, "\nNot in spec (present live, reported only — never deleted):")
			for _, ref := range imported.NotInSpec {
				_, _ = fmt.Fprintf(stdout, "  %s\n", ref)
			}
		}
		if pending {
			_, _ = fmt.Fprintf(stdout, "\n%d change(s) pending.\n", len(imported.Ops))
		} else {
			_, _ = fmt.Fprintln(stdout, "\nNo changes: the spec matches live exactly.")
		}
	}

	if pending {
		return ExitPending
	}
	return ExitSuccess
}

func runApplyApply(client *remoteClient, imported specImportWire, jsonOut bool, stdout, stderr io.Writer, reqTimeout time.Duration, confirmTimeoutSec int, applyTimeout time.Duration, clock applyClock) int {
	ctx, cancel := context.WithTimeout(context.Background(), reqTimeout)
	defer cancel()
	var applied changesetWire
	httpStatus, apiErr, err := client.doJSON(ctx, "POST", "/changesets/"+imported.ID+"/apply",
		applyRequestBody{ConfirmTimeoutSec: confirmTimeoutSec}, &applied)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl apply --apply: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl apply --apply: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	final, timedOut, err := pollChangesetToCommitted(client, imported.ID, reqTimeout, applyTimeout, clock)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl apply --apply: %v\n", err)
		return exitForErr(err)
	}

	if jsonOut {
		out := map[string]any{"changeset": final, "timedOut": timedOut}
		if jerr := writeJSONOut(stdout, out); jerr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl apply --apply: %v\n", jerr)
			return ExitError
		}
	} else {
		printChangesetTable(stdout, final)
	}

	switch {
	case timedOut:
		_, _ = fmt.Fprintf(stderr, "vnproxctl apply --apply: changeset %s did not reach committed within %s (currently %s)\n", imported.ID, applyTimeout, final.Status)
		return ExitApplyTimeout
	case final.Status == "committed":
		return ExitSuccess
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl apply --apply: changeset %s ended in status %q, not committed\n", imported.ID, final.Status)
		return ExitError
	}
}

// pollChangesetToCommitted polls GET /changesets/{id} until it reaches
// `committed`, auto-confirming (POST .../confirm) the moment it observes
// `awaiting_confirm` — non-interactive by design (T-1105 card: "auto-
// confirms non-interactively"). Bounded by applyTimeout measured against
// clock.now(), never a real deadline race: a changeset stuck past it returns
// (final, true, nil) rather than hanging, satisfying the card's "never
// hang" requirement even under a fake clock in tests.
func pollChangesetToCommitted(client *remoteClient, id string, reqTimeout, applyTimeout time.Duration, clock applyClock) (final changesetWire, timedOut bool, err error) {
	deadline := clock.now().Add(applyTimeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), reqTimeout)
		var c changesetWire
		_, apiErr, getErr := client.doJSON(ctx, "GET", "/changesets/"+id, nil, &c)
		cancel()
		if getErr != nil {
			return changesetWire{}, false, getErr
		}
		if apiErr != nil {
			return changesetWire{}, false, fmt.Errorf("%s: %s", apiErr.Code, apiErr.Message)
		}
		final = c

		switch c.Status {
		case "committed":
			return final, false, nil
		case "awaiting_confirm":
			confirmCtx, confirmCancel := context.WithTimeout(context.Background(), reqTimeout)
			_, confirmAPIErr, confirmErr := client.doJSON(confirmCtx, "POST", "/changesets/"+id+"/confirm", nil, &final)
			confirmCancel()
			// A confirm race (the changeset moved on between our GET and
			// this POST — e.g. a local timer already committed/rolled it
			// back) is not fatal: loop around and re-GET the real status
			// rather than treating a stale-state 409 as this command's own
			// failure.
			if confirmErr != nil {
				return changesetWire{}, false, confirmErr
			}
			_ = confirmAPIErr // benign on a state-transition race; re-GET below decides the real outcome
		case "rolled_back", "failed":
			return final, false, nil
		}

		if !clock.now().Before(deadline) {
			return final, true, nil
		}
		clock.sleep(remoteApplyPollInterval)
	}
}

// firstNonNil returns whichever of the two errors is non-nil (err) or, if
// err is nil, wraps apiErr as an error — a small helper for the plan
// command's two "best-effort, just report it" call sites above, which each
// have both an (err error, apiErr *apiError) pair to fold into one log line.
func firstNonNil(err error, apiErr *apiError) error {
	if err != nil {
		return err
	}
	if apiErr != nil {
		return fmt.Errorf("%s: %s", apiErr.Code, apiErr.Message)
	}
	return nil
}
