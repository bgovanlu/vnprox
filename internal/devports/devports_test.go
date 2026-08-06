package devports

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// registryPath is the source of truth every check below reads.
const registryPath = "testdata/dev-ports.tsv"

// binderGlobs are the files in which a port literal means "this repository's
// tooling binds this port on the developer's machine".
//
// Deliberately not included: planning/validation/harness/**. Those scripts run
// against a real Proxmox node, where :8006 is PVE's own pveproxy — a remote
// endpoint they connect to, not a local port they bind. Scanning them would
// make the registry claim ownership of a port it does not own. Also excluded:
// internal/**/testdata fixtures and web/src/**, where port numbers are sample
// data (a WireGuard 51820, a NetFlow 2055 in a decoder test) rather than binds.
var binderGlobs = []string{
	"Makefile",
	"packaging/config/*.toml",
	"packaging/test/*.sh",
	"testdata/dev*.toml",
	"web/playwright.config.ts",
	"web/vite.config.ts",
	"web/e2e/*.ts",
}

// portPatterns extract port literals in the shapes this repo's tooling
// actually writes them. Each must have exactly one capture group.
var portPatterns = []*regexp.Regexp{
	// https://127.0.0.1:8007, 0.0.0.0:8007, localhost:3000.
	// The trailing \b matters: without it the placeholder "127.0.0.1:220x" in
	// cluster-ssh.sh's log line reads as port 220.
	regexp.MustCompile(`(?:127\.0\.0\.1|0\.0\.0\.0|localhost):(\d{2,5})\b`),
	// --addr 127.0.0.1:18006  /  --addr :8006
	regexp.MustCompile(`--addr\s+\S*:(\d{2,5})\b`),
	// Playwright webServer `port: 18006`. The leading \b keeps "viewport:" out.
	regexp.MustCompile(`\bport:\s*(\d{2,5})\b`),
	// TOML `netflow_port = 52055`, shell `ANSWER_PORT=8007`.
	regexp.MustCompile(`(?i)\b[a-z_]*port\s*=\s*"?(\d{2,5})\b"?`),
	// Shell default-expansion `${VNPROX_TEST_SERVICE_PORT:-62007}`.
	regexp.MustCompile(`:-(\d{2,5})\}`),
	// Makefile `MOCKPVE_ADDR ?= :8006`.
	regexp.MustCompile(`ADDR\s*\?=\s*\S*:(\d{2,5})\b`),
	// sshd inside a --network=host container: `setup_sshd pve2 2202`, and the
	// matching `Port 2202` in the generated ssh_config.
	regexp.MustCompile(`(?i)\bsetup_sshd\s+\S+\s+(\d{2,5})\b`),
	regexp.MustCompile(`(?m)^\s*Port\s+(\d{2,5})\b`),
	// A deliberate fake listener: `nc -l -p 8007`. cluster-ssh.sh binds this to
	// force install.sh's port-conflict path, on the host network.
	regexp.MustCompile(`\bnc\s+[^\n]*?-p\s+(\d{2,5})\b`),
	// Any URL with an explicit port, including the assertion patterns packaging
	// tests grep for (`URL: https://.*:8008`). Without this the single most
	// collision-prone bind in the repo is invisible to the scan.
	regexp.MustCompile(`https?://[^\s"']*:(\d{2,5})\b`),
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate the module root")
	return ""
}

func loadRegistry(t *testing.T) []Entry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), registryPath))
	if err != nil {
		t.Fatalf("reading %s: %v", registryPath, err)
	}
	entries, err := Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parsing %s: %v", registryPath, err)
	}
	return entries
}

// stripComments removes comment text so a port mentioned only in prose does
// not read as a bind. The `//` rule is ':'-aware on purpose: naively cutting
// at the first `//` would eat the scheme separator in "https://127.0.0.1:8007"
// and silently blind the scan to the very literals it exists to find.
func stripComments(path, line string) string {
	switch filepath.Ext(path) {
	case ".ts":
		for i := 0; i+1 < len(line); i++ {
			if line[i] == '/' && line[i+1] == '/' && (i == 0 || line[i-1] != ':') {
				return line[:i]
			}
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "*") {
			return ""
		}
		return line
	default: // Makefile, .sh, .toml — '#' to end of line
		if i := strings.Index(line, "#"); i >= 0 {
			return line[:i]
		}
		return line
	}
}

// scanBinders returns every port literal found in binderGlobs, mapped to the
// files it was found in.
func scanBinders(t *testing.T) map[int][]string {
	t.Helper()
	root := repoRoot(t)
	found := make(map[int][]string)

	for _, glob := range binderGlobs {
		matches, err := filepath.Glob(filepath.Join(root, glob))
		if err != nil {
			t.Fatalf("glob %q: %v", glob, err)
		}
		if len(matches) == 0 {
			// A glob that matches nothing means a file was moved or renamed and
			// the scan quietly stopped covering it — the exact way this kind of
			// check rots into a no-op.
			t.Errorf("binder glob %q matched no files: the scan is no longer covering it", glob)
			continue
		}
		for _, path := range matches {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				rel = path
			}
			for _, line := range strings.Split(string(raw), "\n") {
				line = stripComments(rel, line)
				for _, re := range portPatterns {
					for _, m := range re.FindAllStringSubmatch(line, -1) {
						port, convErr := strconv.Atoi(m[1])
						if convErr != nil || port < 1 || port > 65535 {
							continue
						}
						if !containsStr(found[port], rel) {
							found[port] = append(found[port], rel)
						}
					}
				}
			}
		}
	}
	return found
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestScanFindsKnownPorts is the control for every scan-based assertion below.
// Without it, a broken glob, a regex that stopped matching, or an
// over-eager comment stripper would turn TestEveryBoundPortIsRegistered into a
// test that passes because it looked at nothing.
func TestScanFindsKnownPorts(t *testing.T) {
	found := scanBinders(t)

	if len(found) < 15 {
		t.Errorf("scan found only %d distinct ports across %d binder globs; expected at least 15 — the scan is probably broken, not the repo", len(found), len(binderGlobs))
	}

	// Sentinels: one port per extraction shape, so a single dead pattern is a
	// failure rather than a silent reduction in coverage.
	sentinels := map[int]string{
		8007:  "vnproxd default — appears as a 127.0.0.1:PORT URL",
		8006:  "pvemock — appears as `--addr` and Makefile `ADDR ?=`",
		18006: "pvemock sim — appears as Playwright `port:`",
		52055: "netflow — appears as TOML `netflow_port =`",
		62007: "upgrade-service — appears only as a shell `:-PORT}` default",
	}
	for port, why := range sentinels {
		if len(found[port]) == 0 {
			t.Errorf("scan did not find port %d (%s): an extraction pattern has stopped working", port, why)
		}
	}
}

// TestEveryBoundPortIsRegistered is the forward check: nothing binds a port
// this repository has not written down. This is what would have caught
// commit 9047685's collision at authoring time rather than at debugging time.
func TestEveryBoundPortIsRegistered(t *testing.T) {
	entries := loadRegistry(t)
	registered := make(map[int]Entry, len(entries))
	for _, e := range entries {
		registered[e.Port] = e
	}

	for port, files := range scanBinders(t) {
		if _, ok := registered[port]; !ok {
			t.Errorf("port %d is bound by %s but has no row in %s.\n"+
				"Add one (see docs/testing/port-registry.md) or use a port that is already yours.",
				port, strings.Join(files, ", "), registryPath)
		}
	}
}

// TestEveryRegisteredPortIsStillBound is the reverse check: a row whose port
// no longer appears in the file it names is stale, and a stale registry is
// worse than none — it reserves ports nothing uses while readers trust it.
//
// It searches the declared binder file for the bare port literal rather than
// re-using the extraction patterns above, so a row stays valid when its file
// writes the port in a shape the patterns do not model (port-conflict.sh pipes
// "8009" into install.sh's stdin, which is a genuine bind and matches nothing).
func TestEveryRegisteredPortIsStillBound(t *testing.T) {
	root := repoRoot(t)
	for _, e := range loadRegistry(t) {
		raw, err := os.ReadFile(filepath.Join(root, e.Binder))
		if err != nil {
			t.Errorf("port %d (%s): declared binder %s is unreadable: %v", e.Port, e.Owner, e.Binder, err)
			continue
		}
		if !strings.Contains(string(raw), strconv.Itoa(e.Port)) {
			t.Errorf("port %d (%s) is registered to %s, but that file no longer mentions the port — the row is stale", e.Port, e.Owner, e.Binder)
		}
	}
}

// TestNoAdjacentPortCollisions catches the near-miss shape specifically: a new
// port one or two away from an existing pair looks free and is, but it sits
// inside a family a future stack will grow into. T-1807-bug-01's first draft
// picked 28017 for exactly this reason and had to be caught by hand.
func TestNoAdjacentPortCollisions(t *testing.T) {
	entries := loadRegistry(t)
	byPort := make(map[int]Entry, len(entries))
	for _, e := range entries {
		byPort[e.Port] = e
	}
	// Every e2e stack is an NNN006/NNN007 pair. A row at NNN006 must have its
	// NNN007 sibling registered too, otherwise the pair is half-claimed and the
	// free half is a trap.
	for _, e := range entries {
		if e.Port%1000 != 6 {
			continue
		}
		sibling := e.Port + 1
		if _, ok := byPort[sibling]; !ok {
			t.Errorf("port %d (%s) is registered but its %d sibling is not: the pair is half-claimed, so %d looks free to the next author while belonging to this stack", e.Port, e.Owner, sibling, sibling)
		}
	}
}

// crossDomainExemption records a port that two independently-authored families
// of tooling both legitimately touch, and why. Anything not listed here is a
// collision waiting to happen.
type crossDomainExemption struct {
	Reason string
	Port   int
}

// exemptions are the only ports a packaging test may bind that belong to the
// e2e/dev-stack domain. Each is a deliberate test subject, not an accident —
// and each one is a documented reason that script cannot run concurrently with
// the e2e suite.
var exemptions = []crossDomainExemption{
	{
		Port: 8007,
		Reason: "The product's real listen port. port-conflict.sh, cluster-ssh.sh, pve-token.sh and " +
			"answers-parity.sh all exercise install.sh's handling of it, and cluster-ssh.sh binds a " +
			"fake nc listener on it on purpose to force the conflict path. Cannot run concurrently " +
			"with `make dev` or the default e2e stack.",
	},
	{
		Port: 8008,
		Reason: "install.sh's documented first fallback when 8007 is busy. cluster-ssh.sh asserts every " +
			"node lands on it, so the assertion fails if anything else holds 8008 — including the e2e " +
			"suite's own k8smock, which owns this port. Cannot run concurrently with the e2e suite.",
	},
}

// TestPackagingTestsDoNotBindE2EPorts is the check the forward scan cannot make.
//
// Commit 9047685's collision was invisible to "is this port registered?" — 61007
// was registered, to vnproxd-physcollapse, and upgrade-service.sh bound it
// anyway. The hazard is not an unknown port; it is two independently-authored
// families of tooling reaching for the same known one. This asserts that
// packaging/test/*.sh binds nothing owned by the e2e/dev-stack domain except
// the ports listed above, each with a reason.
func TestPackagingTestsDoNotBindE2EPorts(t *testing.T) {
	root := repoRoot(t)

	exempt := make(map[int]string, len(exemptions))
	for _, e := range exemptions {
		if len(e.Reason) < 40 {
			t.Errorf("exemption for port %d needs a reason explaining why two domains share it", e.Port)
		}
		exempt[e.Port] = e.Reason
	}

	// Ports owned by the e2e/dev-stack domain.
	e2eOwned := make(map[int]Entry)
	for _, e := range loadRegistry(t) {
		if strings.HasPrefix(e.Binder, "web/") || strings.HasPrefix(e.Binder, "testdata/dev") {
			e2eOwned[e.Port] = e
		}
	}
	if len(e2eOwned) < 10 {
		t.Fatalf("only %d e2e-domain ports found; the domain classification is broken and this test is vacuous", len(e2eOwned))
	}

	scripts, err := filepath.Glob(filepath.Join(root, "packaging/test/*.sh"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("globbing packaging tests: %v (matched %d)", err, len(scripts))
	}

	for _, script := range scripts {
		raw, readErr := os.ReadFile(script)
		if readErr != nil {
			t.Fatalf("reading %s: %v", script, readErr)
		}
		rel, _ := filepath.Rel(root, script)
		for _, line := range strings.Split(string(raw), "\n") {
			line = stripComments(rel, line)
			for _, re := range portPatterns {
				for _, m := range re.FindAllStringSubmatch(line, -1) {
					port, convErr := strconv.Atoi(m[1])
					if convErr != nil {
						continue
					}
					owner, owned := e2eOwned[port]
					if !owned {
						continue
					}
					if _, ok := exempt[port]; ok {
						continue
					}
					t.Errorf("%s binds port %d, which belongs to %q (%s).\n"+
						"This is the shape of commit 9047685's collision: a known port, claimed by another family of tooling.\n"+
						"Either move to a port of your own, or add a crossDomainExemption with a reason.",
						rel, port, owner.Owner, owner.Binder)
				}
			}
		}
	}
}

// TestExemptedPortsHavePreflight makes the exemptions safe rather than merely
// documented: a script that binds a shared port must call ports_require_free,
// so a busy port fails by naming its holder instead of by a downstream
// assertion that looks like a product defect.
func TestExemptedPortsHavePreflight(t *testing.T) {
	root := repoRoot(t)
	scripts, err := filepath.Glob(filepath.Join(root, "packaging/test/*.sh"))
	if err != nil {
		t.Fatalf("globbing packaging tests: %v", err)
	}

	exempt := make(map[int]bool, len(exemptions))
	for _, e := range exemptions {
		exempt[e.Port] = true
	}

	checked := 0
	for _, script := range scripts {
		raw, readErr := os.ReadFile(script)
		if readErr != nil {
			t.Fatalf("reading %s: %v", script, readErr)
		}
		rel, _ := filepath.Rel(root, script)
		body := string(raw)

		binds := false
		for _, line := range strings.Split(body, "\n") {
			line = stripComments(rel, line)
			for _, re := range portPatterns {
				for _, m := range re.FindAllStringSubmatch(line, -1) {
					if port, convErr := strconv.Atoi(m[1]); convErr == nil && exempt[port] {
						binds = true
					}
				}
			}
		}
		if !binds {
			continue
		}
		checked++
		if !strings.Contains(body, "ports_require_free") {
			t.Errorf("%s binds a shared port but never calls ports_require_free.\n"+
				"Source packaging/test/lib/ports.sh and preflight it, so a busy port reports its holder "+
				"instead of failing later as a confusing assertion (T-1807-bug-01).", rel)
		}
	}
	if checked == 0 {
		t.Error("no packaging test was found to bind a shared port; this check is looking at nothing")
	}
}

// TestParseRejectsBadRegistries proves the parser's guards are load-bearing.
// A duplicate port that parsed cleanly would defeat the whole registry.
func TestParseRejectsBadRegistries(t *testing.T) {
	const good = "8007\ttcp\tone\ta.toml\tpurpose\n"

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"duplicate port", good + "8007\ttcp\ttwo\tb.toml\tpurpose\n", "already claimed"},
		{"duplicate owner", good + "9001\ttcp\tone\tb.toml\tpurpose\n", "already used"},
		{"too few fields", "8007\ttcp\tone\ta.toml\n", "want 5 tab-separated"},
		{"non-numeric port", "eighty\ttcp\tone\ta.toml\tpurpose\n", "invalid syntax"},
		{"port out of range", "70000\ttcp\tone\ta.toml\tpurpose\n", "out of range"},
		{"bad proto", "8007\tsctp\tone\ta.toml\tpurpose\n", "must be tcp or udp"},
		{"empty purpose", "8007\ttcp\tone\ta.toml\t\n", "required"},
		{"no entries", "# only a comment\n", "no entries"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.input))
			if err == nil {
				t.Fatalf("Parse(%q) succeeded; want error containing %q", tt.input, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Parse error = %q; want it to contain %q", err, tt.wantErr)
			}
		})
	}

	// Control: the well-formed row every negative case is built from must
	// itself parse, or the cases above would "pass" for the wrong reason.
	entries, err := Parse(strings.NewReader(good))
	if err != nil {
		t.Fatalf("the control row failed to parse, so every negative case above is vacuous: %v", err)
	}
	if len(entries) != 1 || entries[0].Port != 8007 {
		t.Fatalf("control parse = %+v; want a single entry for port 8007", entries)
	}
}

// TestRegistryIsDocumented keeps the shell helper and the human-facing doc
// wired to this file. Either drifting away restores the "every author picks
// their own convention" state the registry replaced.
func TestRegistryIsDocumented(t *testing.T) {
	root := repoRoot(t)
	for _, consumer := range []string{
		"docs/testing/port-registry.md",
		"packaging/test/lib/ports.sh",
	} {
		raw, err := os.ReadFile(filepath.Join(root, consumer))
		if err != nil {
			t.Errorf("reading %s: %v", consumer, err)
			continue
		}
		if !strings.Contains(string(raw), "dev-ports.tsv") {
			t.Errorf("%s no longer references dev-ports.tsv: it has drifted from the source of truth", consumer)
		}
	}
}

// TestRegistryRowsAreSelfDescribing guards the field the next author actually
// needs: a purpose that says why this port, not one that restates the owner.
func TestRegistryRowsAreSelfDescribing(t *testing.T) {
	for _, e := range loadRegistry(t) {
		if len(e.Purpose) < 30 {
			t.Errorf("port %d (%s): purpose %q is too terse to help the next author choose a non-colliding port", e.Port, e.Owner, e.Purpose)
		}
		if !strings.Contains(e.Binder, "/") && e.Binder != "Makefile" {
			t.Errorf("port %d (%s): binder %q should be a repo-relative path", e.Port, e.Owner, e.Binder)
		}
	}
}
