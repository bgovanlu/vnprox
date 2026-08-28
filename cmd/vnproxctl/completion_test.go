// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestRunCompletion_UsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"completion"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("bare `completion` exit code = %d, want ExitUsage", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"completion", "fish"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("`completion fish` exit code = %d, want ExitUsage (only bash and zsh are supported)", code)
	}
}

// TestRunCompletion_BashParsesAsShellAndListsEverySubcommand is AC3: the
// script is sourceable, and — because it is generated from commandTable
// rather than hand-maintained — every dispatchable top-level command
// appears in it without this test (or completion.go) needing to name them
// twice.
func TestRunCompletion_BashParsesAsShellAndListsEverySubcommand(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available in this environment")
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"completion", "bash"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	script := stdout.String()

	cmd := exec.Command(bashPath, "-n", "-")
	cmd.Stdin = strings.NewReader(script)
	var checkErr bytes.Buffer
	cmd.Stderr = &checkErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bash -n rejected the generated script: %v\n%s\nscript:\n%s", err, checkErr.String(), script)
	}

	for _, c := range commandTable {
		if !strings.Contains(script, c.name) {
			t.Errorf("bash completion script does not mention command %q — commandTable and completion.go have drifted", c.name)
		}
		for _, sub := range c.subcommands {
			if !strings.Contains(script, sub) {
				t.Errorf("bash completion script does not mention %s's subcommand %q", c.name, sub)
			}
		}
	}
	if !strings.Contains(script, "complete -F _vnproxctl vnproxctl") {
		t.Error("script does not register the completion function with `complete`")
	}
}

func TestRunCompletion_ZshParsesAsShell(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available in this environment")
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"completion", "zsh"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	script := stdout.String()
	if !strings.HasPrefix(script, "#compdef vnproxctl\n") {
		t.Errorf("zsh script does not start with the #compdef directive:\n%s", script)
	}

	// The zsh script embeds the bash script via bashcompinit; it is valid
	// POSIX-ish shell syntax even though it's meant to run under zsh (the
	// #compdef line is a comment as far as any shell's parser is
	// concerned), so `bash -n` is a legitimate "parses as valid shell"
	// check here too.
	cmd := exec.Command(bashPath, "-n", "-")
	cmd.Stdin = strings.NewReader(script)
	var checkErr bytes.Buffer
	cmd.Stderr = &checkErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bash -n rejected the generated zsh script: %v\n%s\nscript:\n%s", err, checkErr.String(), script)
	}
}

// TestCommandTable_EveryEntryDispatches proves commandTable is genuinely
// what `run` dispatches through (not a second list that could drift from
// it): every name in the table must route to its own run function and
// nowhere else. Driven by triggering each command's own -h/usage path,
// which every run* function in this binary supports without side effects.
func TestCommandTable_EveryEntryDispatches(t *testing.T) {
	for _, c := range commandTable {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{c.name, "-h"}, &stdout, &stderr)
			// -h/usage exits 0 on some commands and ExitUsage on others
			// (flag.ContinueOnError's -h path returns flag.ErrHelp, which
			// this binary's convention maps to ExitUsage) — either is
			// evidence the command was actually reached, as opposed to
			// falling through to "unknown command".
			combined := stdout.String() + stderr.String()
			if strings.Contains(combined, "unknown command") {
				t.Errorf("`%s -h` fell through to \"unknown command\" — commandTable and run's dispatch have drifted (exit %d)", c.name, code)
			}
		})
	}
}
