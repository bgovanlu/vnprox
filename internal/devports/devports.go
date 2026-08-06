// Package devports parses testdata/dev-ports.tsv, the single source of truth
// for every port this repository's development and test tooling binds on a
// developer's machine.
//
// T-1807-bug-02. The package is deliberately tiny: its reason for existing is
// the enforcement test next to it (devports_test.go), which reads this
// registry and refuses a port literal that no row claims. Production code does
// not import it — vnproxd's listen address comes from its config file, not
// from here.
package devports

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Entry is one registry row: a port some piece of repo tooling binds, and the
// file that is the authority for that claim.
type Entry struct {
	// Owner is a short, stable, registry-unique label ("vnproxd-flow").
	Owner string
	// Binder is the repo-relative path of the file that binds the port.
	Binder string
	// Purpose is the one-line justification for this port and not another.
	Purpose string
	// Proto is "tcp" or "udp".
	Proto string
	// Port is the bound port.
	Port int
}

// Parse reads the TSV registry format described at the top of
// testdata/dev-ports.tsv. It rejects duplicate ports and duplicate owners
// rather than letting a later row silently win, because a silently-won
// duplicate is precisely the collision this registry exists to surface.
func Parse(r *strings.Reader) ([]Entry, error) {
	var entries []Entry
	byPort := make(map[int]Entry)
	byOwner := make(map[string]Entry)

	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) != 5 {
			return nil, fmt.Errorf("dev-ports.tsv line %d: want 5 tab-separated fields, got %d: %q", line, len(fields), sc.Text())
		}
		port, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil {
			return nil, fmt.Errorf("dev-ports.tsv line %d: port %q: %w", line, fields[0], err)
		}
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("dev-ports.tsv line %d: port %d out of range", line, port)
		}
		e := Entry{
			Port:    port,
			Proto:   strings.TrimSpace(fields[1]),
			Owner:   strings.TrimSpace(fields[2]),
			Binder:  strings.TrimSpace(fields[3]),
			Purpose: strings.TrimSpace(fields[4]),
		}
		if e.Proto != "tcp" && e.Proto != "udp" {
			return nil, fmt.Errorf("dev-ports.tsv line %d: proto %q must be tcp or udp", line, e.Proto)
		}
		if e.Owner == "" || e.Binder == "" || e.Purpose == "" {
			return nil, fmt.Errorf("dev-ports.tsv line %d: owner, binder and purpose are all required", line)
		}
		if prev, dup := byPort[port]; dup {
			return nil, fmt.Errorf("dev-ports.tsv line %d: port %d already claimed by %q (%s)", line, port, prev.Owner, prev.Binder)
		}
		if prev, dup := byOwner[e.Owner]; dup {
			return nil, fmt.Errorf("dev-ports.tsv line %d: owner %q already used for port %d", line, e.Owner, prev.Port)
		}
		byPort[port] = e
		byOwner[e.Owner] = e
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading dev-ports.tsv: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("dev-ports.tsv contains no entries")
	}
	return entries, nil
}
