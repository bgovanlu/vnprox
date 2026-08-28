// SPDX-License-Identifier: Apache-2.0

// plugincmd.go implements `vnproxctl plugin scaffold <name>` (T-3811): it
// stamps out examples/plugin-template's exact, tested source — a minimal,
// compiling findingProducer plugin — into a new directory, renamed to
// <name>. It is pure local file work, in the same daemon-independent family
// as `hub`/`backup`/`doctor`: no daemon, no network, nothing staged or
// applied.
//
// The scaffold's output is not a hand-maintained second copy of the
// template: it is byte-identical to examples/plugin-template/{manifest.go,
// producer.go, producer_test.go, README.md} (embedded via that package's
// doc.go) with two literal token substitutions applied — the lowercase
// package/identity token "plugintemplate" and the display string
// "Plugin Template" — so this command cannot silently drift from a template
// that no longer builds or passes its own test.
//
// Because the template imports "github.com/bgovanlu/vnprox/internal/plugin"
// (a Go "internal" package, importable only from code rooted under this
// module), the scaffolded output can only be built from inside a checkout of
// this repository — see examples/plugin-template/README.md's "Why this
// can't be its own repository (yet)" section, which this command's own
// success message points to.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	plugintemplate "github.com/bgovanlu/vnprox/examples/plugin-template"
)

// templateFiles is the exact set of files examples/plugin-template embeds
// and this command copies, in the order they are written out.
var templateFiles = []string{"manifest.go", "producer.go", "producer_test.go", "README.md"}

// templateToken/templateDisplay are the literal strings substituted in every
// copied file — see plugincmd.go's package doc comment.
const (
	templateToken   = "plugintemplate"
	templateDisplay = "Plugin Template"
)

func runPlugin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printPluginUsage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		printPluginUsage(stdout)
		return ExitSuccess
	case "scaffold":
		return runPluginScaffold(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl plugin: unknown subcommand %q\n\n", args[0])
		printPluginUsage(stderr)
		return ExitUsage
	}
}

func printPluginUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `vnproxctl plugin - scaffold a vnprox SDK plugin

  vnproxctl plugin scaffold <name> [--out <dir>] [--force]
        Write a complete, minimal, compiling findingProducer plugin (the
        same template at examples/plugin-template/) into a new directory,
        renamed to <name>: a plugin.Manifest + plugin.Registration
        (manifest.go), a plugin.FindingProducer implementation
        (producer.go), a test exercising internal/plugin/plugintest's
        conformance harness and installing through the real
        plugin.Registry (producer_test.go), and a README.

        --out <dir>   directory to write into (default: ./<name>)
        --force       overwrite an existing non-empty directory

        The scaffold imports "github.com/bgovanlu/vnprox/internal/plugin", a
        Go internal package importable only from inside this repository — so
        the scaffolded directory must be built from a checkout of vnprox
        (e.g. "go build ./<dir>/..." run from the repo root), not as a
        standalone module. See docs/plugin-development.md's "In-process vs.
        out-of-process" section for how to ship a plugin as an independent
        project instead.

  vnproxctl plugin --help    Show this help
`)
}

func runPluginScaffold(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl plugin scaffold", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "directory to write the scaffold into (default: ./<name>)")
	force := fs.Bool("force", false, "overwrite an existing non-empty directory")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl plugin scaffold: %v\n", ofErr)
		return ExitUsage
	}
	rest := fs.Args()
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "vnproxctl plugin scaffold: expected exactly one argument, <name>\n")
		return ExitUsage
	}
	rawName := rest[0]

	pkgName, err := sanitizePluginToken(rawName)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl plugin scaffold: %v\n", err)
		return ExitUsage
	}
	dir := *out
	if dir == "" {
		dir = "./" + pkgName
	}

	written, err := scaffoldPlugin(dir, pkgName, displayName(rawName), *force)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl plugin scaffold: %v\n", err)
		return ExitError
	}

	if jsonOut {
		if werr := writeJSONOut(stdout, map[string]any{
			"name": pkgName, "manifestId": "com.example." + pkgName,
			"dir": dir, "files": written,
		}); werr != nil {
			return ExitError
		}
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "Wrote plugin scaffold %q to %s\n", pkgName, dir)
	for _, f := range written {
		_, _ = fmt.Fprintf(stdout, "  %s\n", f)
	}
	buildPath := filepath.ToSlash(dir)
	if !strings.HasPrefix(buildPath, "/") && !strings.HasPrefix(buildPath, "./") && !strings.HasPrefix(buildPath, "../") {
		buildPath = "./" + buildPath
	}
	_, _ = fmt.Fprintf(stdout, "\nBuild and test it from inside this vnprox checkout:\n")
	_, _ = fmt.Fprintf(stdout, "  go build %s/...\n", buildPath)
	_, _ = fmt.Fprintf(stdout, "  go test %s/...\n", buildPath)
	_, _ = fmt.Fprintf(stdout, "\nIt imports an internal vnprox package, so it can only build from inside this\n")
	_, _ = fmt.Fprintf(stdout, "repository — see %s/README.md's \"Why this can't be its own repository (yet)\".\n", dir)
	return ExitSuccess
}

// scaffoldPlugin writes the template's files into dir with the two literal
// token substitutions applied, refusing to touch an existing non-empty
// directory unless force is set. It returns the paths written, in order.
func scaffoldPlugin(dir, pkgName, display string, force bool) ([]string, error) {
	if !force {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
			return nil, fmt.Errorf("%s already exists and is not empty (use --force to overwrite)", dir)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}

	written := make([]string, 0, len(templateFiles))
	for _, name := range templateFiles {
		raw, err := plugintemplate.Files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("reading embedded template %s: %w", name, err)
		}
		content := string(raw)
		content = strings.ReplaceAll(content, templateToken, pkgName)
		content = strings.ReplaceAll(content, templateDisplay, display)

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // scaffolded plugin source, not a secret
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}

// sanitizePluginToken turns an arbitrary user-supplied plugin name into a
// valid Go package identifier and manifest-id suffix: lowercase ASCII
// letters and digits only, every other character dropped, and a leading
// digit prefixed with "p" (a bare digit is not a valid Go identifier start).
func sanitizePluginToken(raw string) (string, error) {
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	token := b.String()
	if token == "" {
		return "", fmt.Errorf("plugin name %q has no letters or digits usable as a Go package name", raw)
	}
	if token[0] >= '0' && token[0] <= '9' {
		token = "p" + token
	}
	return token, nil
}

// displayName turns raw into a human-readable title (manifest.go's Name
// field, and README headings that use it): separators become spaces, each
// word's first letter is upper-cased. Purely cosmetic — sanitizePluginToken
// is what the package/identity actually derives from.
func displayName(raw string) string {
	spaced := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' {
			return ' '
		}
		return r
	}, raw)
	words := strings.Fields(spaced)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	if len(words) == 0 {
		return "Plugin"
	}
	return strings.Join(words, " ")
}
