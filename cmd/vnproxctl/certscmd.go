// SPDX-License-Identifier: Apache-2.0

// certscmd.go implements `vnproxctl certs` (T-2304): the cluster certificate
// inventory on the console.
//
// This reads pmxcfs directly rather than calling the daemon's GET /certs, for
// the same reason `snapshots list` reads the store directly: the moment you
// most need to look at certificates is the moment a certificate problem has
// made the API unreachable. A subcommand that could only answer while
// everything was already working would be answering the wrong question.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/certs"
)

func runCerts(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("certs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		root     = fs.String("root", certs.DefaultRoot, "pmxcfs mount to read certificates from")
		asJSON   = fs.Bool("json", false, "emit the inventory and issues as JSON")
		warnDays = fs.Int("expiry-warn-days", 0, "how far ahead to warn about expiry (default 30)")
	)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	inv, err := certs.Scan(certs.Options{Root: *root, LocalNode: certs.LocalNodeFromRoot(*root)})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl certs: %v\n", err)
		return ExitError
	}

	now := time.Now()
	var warn time.Duration
	if *warnDays > 0 {
		warn = time.Duration(*warnDays) * 24 * time.Hour
	}
	verify := func(leafPath string) error { return certs.VerifyChain(*root, leafPath, now) }
	// Cluster facts are deliberately not fetched here: this command must work
	// with the daemon down, and PVE's cluster status needs an authenticated
	// API call. Without dial addresses the SAN and missing-certificate checks
	// stay silent rather than guess — see certs.ClusterFacts.
	issues := certs.Evaluate(inv, certs.ClusterFacts{}, now, warn, verify)

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(certs.Report{Inventory: inv, Issues: issues}); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl certs: %v\n", err)
			return ExitError
		}
		return certsExitCode(issues)
	}

	printCerts(stdout, inv, issues, now)
	return certsExitCode(issues)
}

// certsExitCode makes the command scriptable: 0 clean, 1 when any error-level
// problem exists. Warnings alone stay 0 — an expiring certificate is not yet a
// failure, and making it one would push operators to stop running the check.
func certsExitCode(issues []certs.Issue) int {
	for _, i := range issues {
		if i.Severity == certs.SeverityError {
			return ExitError
		}
	}
	return ExitSuccess
}

func printCerts(w io.Writer, inv certs.Inventory, issues []certs.Issue, now time.Time) {
	if inv.ClusterCA != nil {
		_, _ = fmt.Fprintf(w, "cluster CA   %s\n", inv.ClusterCA.Subject)
		_, _ = fmt.Fprintf(w, "             expires %s (%s)\n\n",
			inv.ClusterCA.NotAfter.Format("2006-01-02"), humanLeft(inv.ClusterCA, now))
	} else {
		_, _ = fmt.Fprintf(w, "cluster CA   NOT FOUND — peer TLS cannot verify any peer\n\n")
	}

	byNode := map[string][]certs.Certificate{}
	for _, c := range inv.Certificates {
		if c.Kind == certs.KindClusterCA {
			continue
		}
		byNode[c.Node] = append(byNode[c.Node], c)
	}
	nodes := make([]string, 0, len(byNode))
	for n := range byNode {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	for _, node := range nodes {
		label := node
		if label == "" {
			label = "(unattributed)"
		}
		_, _ = fmt.Fprintf(w, "%s\n", label)
		for _, c := range byNode[node] {
			_, _ = fmt.Fprintf(w, "  %-11s %s\n", c.Kind, c.Subject)
			_, _ = fmt.Fprintf(w, "  %-11s expires %s (%s), %s-%d, %s\n", "",
				c.NotAfter.Format("2006-01-02"), humanLeft(&c, now), c.KeyAlgorithm, c.KeyBits, c.SignatureAlgorithm)
			_, _ = fmt.Fprintf(w, "  %-11s names   %s\n", "", sanLine(c))
		}
		_, _ = fmt.Fprintln(w)
	}

	if len(issues) == 0 {
		_, _ = fmt.Fprintln(w, "no certificate problems found")
		return
	}
	_, _ = fmt.Fprintf(w, "%d problem(s):\n", len(issues))
	for _, i := range issues {
		_, _ = fmt.Fprintf(w, "  [%s] %s: %s\n", strings.ToUpper(i.Severity), i.Check, i.Detail)
		_, _ = fmt.Fprintf(w, "        fix: %s\n", i.Remediation)
	}
}

func humanLeft(c *certs.Certificate, now time.Time) string {
	d := c.ExpiresIn(now)
	if d <= 0 {
		return "EXPIRED"
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

func sanLine(c certs.Certificate) string {
	if len(c.SANs) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(c.SANs))
	for _, s := range c.SANs {
		parts = append(parts, s.Value)
	}
	return strings.Join(parts, ", ")
}
